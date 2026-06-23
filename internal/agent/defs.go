// Package agent includes support for user-defined sub-agent profiles stored
// as Markdown files under .ok/agents/. Each file defines a reusable, named
// sub-agent with an optional model override, tool whitelist, and custom system
// prompt. The loader is called from boot; the resulting tools are registered
// alongside built-in tools so the LLM can invoke them by name.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/jobs"
	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/tool"
)

// AgentDef is a user-defined sub-agent profile loaded from .ok/agents/*.md.
type AgentDef struct {
	Name              string   // derived from filename (no .md)
	Description       string   // first H1 heading or "Sub-agent: <name>"
	Model             string   // optional model override (empty = inherit parent)
	Tools             []string // tool whitelist (empty = all parent tools)
	PermissionMode    string   // "plan" | "normal" | "yolo" | "" (inherit)
	AllowedMCPServers []string // MCP servers this agent can access; empty = all
	SystemPrompt      string   // raw Markdown body
	FilePath          string   // source file path
}

// LoadAgentDefs scans .ok/agents/ (under projectRoot) for .md files and parses
// each one into an AgentDef. Non-existent directory is not an error; permission
// or I/O errors are logged to stderr so the operator can diagnose.
func LoadAgentDefs(projectRoot string) []AgentDef {
	dir := filepath.Join(projectRoot, ".ok", "agents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "agent: cannot read defs dir %s: %v\n", dir, err)
		}
		return nil
	}
	var defs []AgentDef
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agent: cannot read def %s: %v\n", path, err)
			continue
		}
		def := parseAgentDef(e.Name(), string(b), path)
		defs = append(defs, def)
	}
	return defs
}

// parseAgentDef extracts frontmatter and body from a Markdown agent definition.
// Supported format:
//
//	---
//	model: deepseek-pro
//	tools: [bash, read_file, write_file]
//	description: Short description
//	---
//	# Agent Name
//	System prompt body here...
func parseAgentDef(filename, content, path string) AgentDef {
	name := strings.TrimSuffix(filename, ".md")
	def := AgentDef{
		Name:     name,
		FilePath: path,
	}

	// Parse optional YAML-like frontmatter.
	if strings.HasPrefix(content, "---\n") {
		end := strings.Index(content[4:], "\n---\n")
		if end >= 0 {
			fm := content[4 : end+4]
			body := content[end+9:] // skip "\n---\n"
			parseFrontmatter(fm, &def)
			content = body
		}
	}

	// Use first H1 as description fallback.
	if def.Description == "" {
		for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "# ") {
				def.Description = strings.TrimPrefix(trimmed, "# ")
				break
			}
		}
	}
	if def.Description == "" {
		def.Description = "Sub-agent: " + name
	}

	def.SystemPrompt = strings.TrimSpace(content)
	return def
}

// parseFrontmatter extracts key-value pairs from simple key: value lines.
// Supports: model, description, tools (as JSON array).
func parseFrontmatter(fm string, def *AgentDef) {
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "model":
			def.Model = strings.Trim(val, "\"'")
		case "description":
			def.Description = strings.Trim(val, "\"'")
		case "tools":
			var list []string
			if err := json.NewDecoder(strings.NewReader(val)).Decode(&list); err == nil {
				def.Tools = list
			}
		case "permission_mode", "permission-mode":
			def.PermissionMode = strings.Trim(val, "\"'")
		case "mcp_servers", "mcp-servers":
			var list []string
			if err := json.NewDecoder(strings.NewReader(val)).Decode(&list); err == nil {
				def.AllowedMCPServers = list
			}
		default: // unknown key — ignore
		}
	}
}

// Describe returns a one-line tool description for registration.
func (d AgentDef) Describe() string {
	desc := d.Description
	if d.Model != "" {
		desc += fmt.Sprintf(" (model: %s)", d.Model)
	}
	if len(d.Tools) > 0 {
		desc += fmt.Sprintf(" [tools: %s]", strings.Join(d.Tools, ", "))
	}
	return desc
}

// MakeAgentTool returns a tool.Tool that executes this agent definition via
// RunSubAgent, using the given provider, registry, pricing, gate, hooks, jobs,
// and options. The caller supplies a function to resolve a model name to a
// provider (for model overrides) and a function to filter the registry.
func (d AgentDef) MakeAgentTool(
	prov provider.Provider,
	parentReg *tool.Registry,
	pricing *provider.Pricing,
	gate Gate,
	hooks ToolHooks,
	jm *jobs.Manager,
	opts Options,
	resolveModel func(string) (provider.Provider, *provider.Pricing, int, error),
) tool.Tool {
	return &agentDefTool{
		def:          d,
		prov:         prov,
		parentReg:    parentReg,
		pricing:      pricing,
		gate:         gate,
		hooks:        hooks,
		jm:           jm,
		opts:         opts,
		resolveModel: resolveModel,
	}
}

type agentDefTool struct {
	def          AgentDef
	prov         provider.Provider
	parentReg    *tool.Registry
	pricing      *provider.Pricing
	gate         Gate
	hooks        ToolHooks
	jm           *jobs.Manager
	opts         Options
	resolveModel func(string) (provider.Provider, *provider.Pricing, int, error)
}

func (t *agentDefTool) Name() string        { return t.def.Name }
func (t *agentDefTool) ReadOnly() bool      { return false }
func (t *agentDefTool) Description() string { return t.def.Describe() }

func (t *agentDefTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string","description":"The task for this sub-agent"}},"required":["prompt"]}`)
}

func (t *agentDefTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}

	prov, pricing, ctxWin := t.prov, t.pricing, t.opts.ContextWindow
	if t.def.Model != "" {
		if p2, pr2, cw2, err := t.resolveModel(t.def.Model); err == nil {
			prov, pricing, ctxWin = p2, pr2, cw2
		} else {
			log.Warn("model override failed, falling back to parent", "agent", t.def.Name, "model", t.def.Model, "err", err)
		}
	}

	subReg := FilterRegistry(t.parentReg, t.def.Tools,
		"review", "security_review",
		"run_skill", "install_skill", "task",
		// Execution-side tools must be excluded for user-defined agents too.
		"auto-heal", "bash", "deploy", "desktop", "computer-use", "tool-groups",
		"edit_file", "kill_shell", "make-tool",
		"multi_edit", "write_file", "complete_step", "todo_write")

	// Apply permission mode override
	effectiveGate := t.gate
	switch t.def.PermissionMode {
	case "plan":
		effectiveGate = &planModeGate{}
	case "yolo":
		effectiveGate = nil
	default: // unknown — ignore
	}

	sysPrompt := t.def.SystemPrompt

	return RunSubAgent(ctx, prov, subReg, sysPrompt, p.Prompt, Options{
		MaxSteps:      t.opts.MaxSteps / 2,
		Temperature:   t.opts.Temperature,
		Pricing:       pricing,
		Gate:          effectiveGate,
		Hooks:         t.hooks,
		Jobs:          t.jm,
		ContextWindow: ctxWin,
		ArchiveDir:    t.opts.ArchiveDir,
	}, NestedSink(ctx, event.Discard))
}

// planModeGate blocks all non-read-only tool calls.
type planModeGate struct{}

func (g *planModeGate) Check(ctx context.Context, toolName string, args json.RawMessage, readOnly bool) (bool, string, error) {
	if readOnly {
		return true, "", nil
	}
	return false, "blocked: sub-agent is in read-only plan mode", nil
}
