package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// RenderTOML renders the config as annotated TOML in the `ok setup` house style:
// comments preserved, system_prompt as a multi-line string, helpful hints. The
// output round-trips back through Load (see render_test.go).
func RenderTOML(c *Config) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var b strings.Builder

	b.WriteString("# OK configuration.\n")
	b.WriteString("# Resolution order: flag > ./ok.toml > ~/.config/ok/config.toml > built-in defaults.\n")
	b.WriteString("# Secrets come from the environment via api_key_env; never put keys here.\n\n")

	fmt.Fprintf(&b, "default_model = %q\n", c.DefaultModel)
	if c.Language != "" {
		fmt.Fprintf(&b, "language      = %q   # ui language; empty = auto-detect from $LANG / $OK_LANG\n", c.Language)
	} else {
		b.WriteString("# language      = \"zh\"   # ui language; empty = auto-detect from $LANG / $OK_LANG\n")
	}
	b.WriteString("\n")

	b.WriteString("[agent]\n")
	b.WriteString("system_prompt = \"\"\"\n")
	b.WriteString(c.Agent.SystemPrompt)
	b.WriteString("\"\"\"\n")
	if c.Agent.SystemPromptFile != "" {
		fmt.Fprintf(&b, "system_prompt_file = %q\n", c.Agent.SystemPromptFile)
	} else {
		b.WriteString("# system_prompt_file = \"prompts/system.md\"   # overrides system_prompt when set\n")
	}
	fmt.Fprintf(&b, "max_steps   = %d\n", c.Agent.MaxSteps)
	fmt.Fprintf(&b, "temperature = %s\n", formatFloat(c.Agent.Temperature))
	if c.Agent.PlannerModel != "" {
		fmt.Fprintf(&b, "planner_model = %q   # low-frequency planner (two-model collaboration)\n", c.Agent.PlannerModel)
	} else {
		b.WriteString("# planner_model = \"mimo\"   # optional: enable two-model collaboration\n")
	}
	if c.Agent.MaxConcurrentTasks > 0 {
		fmt.Fprintf(&b, "max_concurrent_tasks = %d   # sub-agent slots; 0 = unlimited, default 5\n", c.Agent.MaxConcurrentTasks)
	} else {
		b.WriteString("# max_concurrent_tasks = 5   # sub-agent slots\n")
	}
	// system_prompt_parts: optional prompt fragment files appended after the base.
	if len(c.Agent.SystemPromptParts) > 0 {
		for _, p := range c.Agent.SystemPromptParts {
			fmt.Fprintf(&b, "system_prompt_parts = %q\n", p)
		}
	} else {
		b.WriteString("# system_prompt_parts = [\"prompts/rules.md\", \"prompts/tools.md\"]   # extra prompt files\n")
	}
	if c.Agent.CompileCmd != "" {
		fmt.Fprintf(&b, "compile_cmd  = %q   # DST: compile check command\n", c.Agent.CompileCmd)
	} else {
		b.WriteString("# compile_cmd  = \"cargo check\"   # DST: compile check command (default: go build ./...)\n")
	}
	if c.Agent.TestCmd != "" {
		fmt.Fprintf(&b, "test_cmd     = %q   # DST: test check command\n", c.Agent.TestCmd)
	} else {
		b.WriteString("# test_cmd     = \"cargo test\"   # DST: test check command (default: go test ./...)\n")
	}
	b.WriteString("\n")

	for _, p := range c.Providers {
		b.WriteString("[[providers]]\n")
		fmt.Fprintf(&b, "name        = %q\n", p.Name)
		fmt.Fprintf(&b, "kind        = %q\n", p.Kind)
		fmt.Fprintf(&b, "base_url    = %q\n", p.BaseURL)
		if len(p.Models) > 0 {
			// Vendor model list: write `models` array and optionally `default`.
			fmt.Fprintf(&b, "models = %s   # all models available from this provider\n",
				renderStringArray(p.Models))
			if p.Default != "" {
				fmt.Fprintf(&b, "default = %q   # default model when multiple are listed\n", p.Default)
			}
		} else {
			fmt.Fprintf(&b, "model       = %q\n", p.Model)
		}
		fmt.Fprintf(&b, "api_key_env = %q\n", p.APIKeyEnv)
		if p.BalanceURL != "" {
			fmt.Fprintf(&b, "balance_url = %q   # optional; wallet-balance endpoint shown in the status bar\n", p.BalanceURL)
		}
		if p.ContextWindow > 0 {
			fmt.Fprintf(&b, "context_window = %d   # tokens; compaction triggers near this limit\n", p.ContextWindow)
		}
		if p.Price != nil {
			fmt.Fprintf(&b, "price       = { cache_hit = %v, input = %v, output = %v, currency = %q }   # per 1M tokens\n",
				p.Price.CacheHit, p.Price.Input, p.Price.Output, p.Price.Symbol())
		}
		b.WriteString("\n")
	}

	b.WriteString("[tools]\n")
	if len(c.Tools.Enabled) == 0 {
		b.WriteString("enabled = []   # empty = all built-in tools\n\n")
	} else {
		b.WriteString("enabled = [")
		for i, t := range c.Tools.Enabled {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q", t)
		}
		b.WriteString("]\n\n")
	}

	b.WriteString("[mode]\n")
	b.WriteString("# Interaction style: \"plan\" (read-only, writers blocked), \"normal\" (prompt\n")
	b.WriteString("# before writers), or \"yolo\" (writers allowed without prompting).\n")
	style := c.modeStyle()
	fmt.Fprintf(&b, "default = %q\n", style)
	b.WriteString(renderRuleList("deny", c.modeDeny(), `["bash(rm -rf*)", "bash(git push*)"]   # blocked in every mode`))
	b.WriteString("\n")

	b.WriteString("[sandbox]\n")
	b.WriteString("# Confine tool blast radius. File-writers (write_file/edit_file/multi_edit)\n")
	b.WriteString("# may only write under workspace_root (empty = current dir) + allow_write.\n")
	b.WriteString("# bash = \"enforce\" (default) jails each command in an OS sandbox (macOS now;\n")
	b.WriteString("# graceful fallback elsewhere); \"off\" disables it. network allows egress.\n")
	if c.Sandbox.WorkspaceRoot != "" {
		fmt.Fprintf(&b, "workspace_root = %q\n", c.Sandbox.WorkspaceRoot)
	} else {
		b.WriteString("# workspace_root = \"\"            # default: current working directory\n")
	}
	if len(c.Sandbox.AllowWrite) > 0 {
		fmt.Fprintf(&b, "allow_write = %s\n", renderStringArray(c.Sandbox.AllowWrite))
	} else {
		b.WriteString("# allow_write = [\"/tmp\"]          # extra dirs writers may also modify\n")
	}
	if len(c.Sandbox.AllowRead) > 0 {
		fmt.Fprintf(&b, "allow_read = %s\n", renderStringArray(c.Sandbox.AllowRead))
	} else {
		b.WriteString("# allow_read  = []                 # extra dirs readers may access (defaults to allow_write)\n")
	}
	fmt.Fprintf(&b, "bash    = %q\n", c.bashMode())
	fmt.Fprintf(&b, "network = %v\n", c.Sandbox.Network)
	on := c.Sandbox.OnUnavailable
	if on == "" {
		on = "warn"
	}
	fmt.Fprintf(&b, "on_unavailable = %q  # warn|block; block refuses to run when sandbox is unavailable\n", on)
	b.WriteString("\n")

	b.WriteString("[skills]\n")
	b.WriteString("# Custom skill roots — each a directory of SKILL.md / <name>.md playbooks.\n")
	b.WriteString("# Scanned between project roots (.ok/.agents/.claude) and home-dir roots.\n")
	b.WriteString("# ~ and relative paths and ${VAR} expansion are supported.\n")
	if len(c.Skills.Paths) > 0 {
		fmt.Fprintf(&b, "paths = %s\n", renderStringArray(c.Skills.Paths))
	} else {
		b.WriteString("# paths = [\"~/.ok/skills\"]\n")
	}
	b.WriteString("\n")

	b.WriteString("[codegraph]\n")
	b.WriteString("# Built-in CodeGraph code-intelligence server (tree-sitter + SQLite).\n")
	b.WriteString("# enabled = false drops the codegraph_* tools and falls back to grep/glob.\n")
	fmt.Fprintf(&b, "enabled = %v\n", c.Codegraph.Enabled)
	if c.Codegraph.Path != "" {
		fmt.Fprintf(&b, "path = %q   # override binary resolution\n", c.Codegraph.Path)
	} else {
		b.WriteString("# path = \"\"   # empty = bundle next to ok, then codegraph on PATH\n")
	}
	b.WriteString("\n")

	b.WriteString("# [team]\n")
	b.WriteString("# Multi-model agent team: orchestrator delegates sub-tasks to specialists.\n")
	b.WriteString("# orchestrator = \"deepseek-pro\"\n")
	b.WriteString("# [[team.specialists]]\n")
	b.WriteString("# name        = \"coder\"\n")
	b.WriteString("# model       = \"deepseek-flash\"\n")
	b.WriteString("# description = \"Writes and edits code\"\n")
	b.WriteString("# prompt      = \"You are a coding specialist.\"\n")
	b.WriteString("# tools       = [\"read_file\", \"write_file\", \"edit_file\", \"bash\", \"grep\"]\n")
	b.WriteString("\n")
	b.WriteString("# [reasoner]\n")
	b.WriteString("# Top-level multi-agent Reasoner — decomposes goals into task DAGs.\n")
	b.WriteString("# decompose_model = \"deepseek-pro\"   # LLM for DAG decomposition\n")
	b.WriteString("# max_concurrent  = 3                 # max parallel task execution\n")
	b.WriteString("\n")
	b.WriteString("# [router]\n")
	b.WriteString("# Model routing by task complexity — cheap model for simple tasks,\n")
	b.WriteString("# expensive model for complex refactoring. Default model for normal.\n")
	b.WriteString("# enabled        = false\n")
	b.WriteString("# cheap_model    = \"deepseek-flash\"\n")
	b.WriteString("# expensive_model = \"deepseek-pro\"\n")
	b.WriteString("\n")

	// ECP — Evolution Control Protocol for cross-instance knowledge federation.
	b.WriteString("# [ecp]\n")
	b.WriteString("# Cross-instance knowledge federation — share learned skills across agents.\n")
	b.WriteString("# enabled       = false   # set true to join the federation\n")
	b.WriteString("# shared_secret = \"${ECP_SECRET}\"   # shared HMAC secret for peer auth\n")
	b.WriteString("# peers         = []                 # peer URLs to sync with\n")
	b.WriteString("# sync_interval = \"1h\"               # how often to sync (e.g. \"30m\", \"2h\")\n")
	b.WriteString("\n")

	if c.PluginQuiet {
		b.WriteString("plugin_quiet = true   # silence \"auto-discovered N plugin(s)\" startup notice\n")
	} else {
		b.WriteString("# plugin_quiet = true   # silence \"auto-discovered N plugin(s)\" startup notice\n")
	}
	b.WriteString("\n")

	b.WriteString("# External MCP servers. type: \"stdio\" (default, a subprocess) | \"http\" | \"sse\".\n")
	b.WriteString("# ${VAR} / ${VAR:-default} are expanded from the environment in command/args/env/url/headers.\n")
	if len(c.Plugins) == 0 {
		b.WriteString("# [[plugins]]\n")
		b.WriteString("# name    = \"example\"\n")
		b.WriteString("# command = \"ok-plugin-example\"\n")
		b.WriteString("# [[plugins]]                                  # a remote server over Streamable HTTP\n")
		b.WriteString("# name    = \"stripe\"\n")
		b.WriteString("# type    = \"http\"\n")
		b.WriteString("# url     = \"https://mcp.stripe.com\"\n")
		b.WriteString("# headers = { Authorization = \"Bearer ${STRIPE_KEY}\" }\n")
	} else {
		for _, pl := range c.Plugins {
			b.WriteString("\n[[plugins]]\n")
			fmt.Fprintf(&b, "name    = %q\n", pl.Name)
			if pl.Type != "" {
				fmt.Fprintf(&b, "type    = %q\n", pl.Type)
			}
			if pl.Command != "" {
				fmt.Fprintf(&b, "command = %q\n", pl.Command)
			}
			if len(pl.Args) > 0 {
				fmt.Fprintf(&b, "args    = %s\n", renderStringArray(pl.Args))
			}
			if pl.URL != "" {
				fmt.Fprintf(&b, "url     = %q\n", pl.URL)
			}
			if len(pl.Headers) > 0 {
				fmt.Fprintf(&b, "headers = %s\n", renderStringMap(pl.Headers))
			}
			if len(pl.Env) > 0 {
				fmt.Fprintf(&b, "env     = %s\n", renderStringMap(pl.Env))
			}
		}
	}

	return b.String()
}

// renderStringArray renders a []string as a TOML inline array.
func renderStringArray(ss []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, s := range ss {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", s)
	}
	b.WriteByte(']')
	return b.String()
}

// renderStringMap renders a map[string]string as a TOML inline table with keys
// in sorted order so output is deterministic (round-trips cleanly).
func renderStringMap(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{ ")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s = %q", k, m[k])
	}
	b.WriteString(" }")
	return b.String()
}

// renderRuleList emits a permission rule list. A populated list renders as an
// active TOML array; an empty one renders as a commented example so `ok setup`
// scaffolds discoverable guidance without imposing surprising rules.
func renderRuleList(key string, rules []string, example string) string {
	if len(rules) == 0 {
		return fmt.Sprintf("# %s = %s\n", key, example)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s = [", key)
	for i, r := range rules {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", r)
	}
	b.WriteString("]\n")
	return b.String()
}

// formatFloat ensures a float renders with a decimal point so TOML types it as a
// float, not an integer (e.g. 0 -> "0.0").
func formatFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}
