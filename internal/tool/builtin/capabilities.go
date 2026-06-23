package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/NB-Agent/ok/internal/tool"
)

func init() { tool.RegisterBuiltin(capabilities{}) }

// capabilities returns a structured map of every tool the agent has access to,
// grouped by capability domain. Opt-in tools that require external setup are
// marked so the model knows what's available now vs. ready once configured.
type capabilities struct{}

func (capabilities) Name() string { return "capabilities" }

func (capabilities) Description() string {
	return "List discoverable capabilities grouped by domain. Includes tools requiring external setup (marked)."
}

func (capabilities) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{},"type":"object"}`)
}

func (capabilities) ReadOnly() bool { return true }

func (capabilities) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	var b strings.Builder
	b.WriteString("# Agent Capabilities\n\n")

	type e struct {
		name   string
		desc   string
		config string
	}
	type c struct {
		name    string
		entries []e
	}

	all := []c{
		{
			"🔍 Code Search & Understanding", []e{
				{"semantic-search", "Find code by meaning (vector search via local Ollama)", "Ollama + nomic-embed-text"},
				{"symbol-find", "Find symbol definitions, references, interface implementations", ""},
				{"code-search", "Natural-language code search (skill)", ""},
				{"trace-flow", "Deep call-chain tracing from entry to exit (skill)", ""},
				{"grep", "Regex text search across the codebase", ""},
				{"glob", "File pattern matching", ""},
			},
		},
		{
			"🔧 Code Generation & Editing", []e{
				{"edit_file", "Precise string replacement in files", ""},
				{"write_file", "Create or overwrite files", ""},
				{"multi_edit", "Atomic multi-step edits on a single file", ""},
				{"scan-style", "Scan project style before writing code (skill)", ""},
				{"style-check", "Validate formatting, vet, naming, imports", ""},
			},
		},
		{
			"🐛 Debug & Diagnostics", []e{
				{"go-debug", "Build failure diagnosis with root-cause analysis (skill)", ""},
				{"test-analyzer", "Test failure analysis with got/want diffing (skill)", ""},
				{"auto-fix", "Diagnose → fix → verify → retry closed loop (skill)", ""},
				{"auto-heal", "Build+vet+test diagnosis with fix instructions", ""},
				{"go-profile", "CPU/memory/heap profiling via pprof", ""},
				{"git-investigate", "Git blame archaeology — trace line history (skill)", ""},
			},
		},
		{
			"🛡️ Quality & Security", []e{
				{"health-check", "Full-dimensional project health scan (skill)", ""},
				{"self-scan", "Agent self-state snapshot (skills, git, build)", ""},
				{"arch-review", "Package dependency graph, coupling, god-package detection (skill)", ""},
				{"dep-audit", "Dependency audit — unused, outdated, indirect (skill)", ""},
				{"error-audit", "Error handling audit — discarded errors, unhandled errs (skill)", ""},
				{"deadcode", "Unused function/variable/type detection (skill)", ""},
				{"vuln-check", "Known vulnerability scan via govulncheck", "govulncheck"},
			},
		},
		{
			"🧠 Learning & Memory", []e{
				{"covenant", "Display the agent's immutable core covenant — principles compiled into the binary that cannot be overridden", ""},
				{"self-critique", "Metacognitive review of own output (skill)", ""},
				{"self-evolve", "Self-evolution gap analysis — audit capabilities, identify missing tools/skills, propose improvements (skill)", ""},
				{"save-experience", "Extract reusable knowledge from tasks (skill)", ""},
				{"remember", "Persist durable facts to per-project memory store", ""},
			},
		},
		{
			"📦 Build & Deploy", []e{
				{"bash", "Shell command execution with timeout and sandbox", ""},
				{"deploy", "SSH deployment, health checks, container build/push", "deploy.toml + SSH"},
				{"make-tool", "Create new built-in tools at runtime (self-evolution)", ""},
			},
		},
		{
			"🌐 External & Media", []e{
				{"web_fetch", "Fetch and extract text from URLs", ""},
				{"image-read", "Read image metadata (visual analysis with multimodal model)", "multimodal model"},
			},
		},
	}

	available := 0
	configurable := 0

	for _, cat := range all {
		fmt.Fprintf(&b, "## %s\n\n", cat.name)
		for _, ent := range cat.entries {
			ready := isConfigured(ent.name)
			switch {
			case ent.config == "" || ready:
				fmt.Fprintf(&b, "- **%s** ✅ — %s\n", ent.name, ent.desc)
				available++
			default:
				fmt.Fprintf(&b, "- **%s** ⚠️ — %s *(needs: %s)*\n", ent.name, ent.desc, ent.config)
				configurable++
			}
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "- **%d** capabilities available now\n", available)
	if configurable > 0 {
		fmt.Fprintf(&b, "- **%d** capabilities available once configured\n", configurable)
	}
	fmt.Fprintf(&b, "- **%d** total\n\n", available+configurable)

	if configurable > 0 {
		b.WriteString("💡 Capabilities marked ⚠️ are opt-in by design. ")
		b.WriteString("They require explicit configuration before use — ")
		b.WriteString("no connection or request is ever made without your setup.\n")
	}

	return b.String(), nil
}

func isConfigured(name string) bool {
	switch name {
	case "semantic-search":
		_, err := os.Stat(".ok/semantic-index.json")
		return err == nil
	case "deploy":
		_, err := os.Stat("deploy.toml")
		return err == nil
	case "vuln-check":
		for _, dir := range strings.Split(os.Getenv("PATH"), string(os.PathListSeparator)) {
			if _, err := os.Stat(dir + "/govulncheck"); err == nil {
				return true
			}
			if _, err := os.Stat(dir + "/govulncheck.exe"); err == nil {
				return true
			}
		}
		return false
	case "image-read":
		return true
	default:
		return true
	}
}
