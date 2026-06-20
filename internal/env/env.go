// Package env provides environment awareness for both humans (ok doctor) and
// the AI agent (system-prompt injection). The Context function returns a concise,
// agent-readable summary of the runtime environment that is folded into the
// cache-stable system-prompt prefix so every LLM turn sees it at zero per-turn
// cost. The Summary function mirrors it in human-facing form.
package env

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/NB-Agent/ok/internal/config"
	"github.com/NB-Agent/ok/internal/sandbox"
)

// Context returns a concise, agent-readable environment report injected into
// the system prompt. The agent uses it to adapt behaviour: which tools work,
// whether it can prompt the user, what OS it's on, etc.
// Pass an optional pre-loaded Config to avoid re-parsing.
func Context(cfg ...*config.Config) string {
	var b strings.Builder
	b.WriteString("# Environment\n\n")
	writeKV(&b, "OS", runtime.GOOS+"/"+runtime.GOARCH)
	writeKV(&b, "Sandbox", sandboxStatus())
	writeKV(&b, "Terminal", terminalMode())

	// Use the provided config if given; otherwise load.
	var c *config.Config
	if len(cfg) > 0 && cfg[0] != nil {
		c = cfg[0]
	} else if loaded, err := config.Load(); err == nil {
		c = loaded
	} else {
		c = config.Default()
	}
	if c != nil {
		writeKV(&b, "Default model", c.DefaultModel)
		if c.Agent.PlannerModel != "" {
			writeKV(&b, "Planner model", c.Agent.PlannerModel)
		}
		providers := make([]string, 0, len(c.Providers))
		for _, p := range c.Providers {
			providers = append(providers, p.Name)
		}
		if len(providers) > 0 {
			writeKV(&b, "Providers", strings.Join(providers, ", "))
		}

		// Path-level permission rules (allow/ask/deny with glob patterns).
		allow, ask, deny := c.ModeAllow(), c.ModeAsk(), c.ModeDeny()
		if len(allow) > 0 || len(ask) > 0 || len(deny) > 0 {
			b.WriteString("- Path rules: configured\n")
			if len(allow) > 0 {
				fmt.Fprintf(&b, "  allow (%d): %s\n", len(allow), strings.Join(allow, ", "))
			}
			if len(ask) > 0 {
				fmt.Fprintf(&b, "  ask (%d): %s\n", len(ask), strings.Join(ask, ", "))
			}
			if len(deny) > 0 {
				fmt.Fprintf(&b, "  deny (%d): %s\n", len(deny), strings.Join(deny, ", "))
			}
		}
	}

	// Sandbox implications
	if !sandbox.Available() {
		b.WriteString("\n")
		b.WriteString("IMPORTANT: Sandbox is not available on this platform. ")
		b.WriteString("Bash commands run unconfined — the agent must exercise extra ")
		b.WriteString("caution with destructive operations (rm, disk write, network).\n")
	}

	// Repository structure summary for quick orientation.
	repo := repoMap(".")
	if repo != "" {
		b.WriteString("\n" + repo)
	}

	return b.String()
}

func sandboxStatus() string {
	if sandbox.Available() {
		return "available — commands run confined"
	}
	return "unavailable — commands run unconfined"
}

func terminalMode() string {
	if isatty() {
		return "interactive — user can answer prompts"
	}
	return "non-interactive — no user prompts; decide autonomously"
}

func writeKV(b *strings.Builder, key, value string) {
	fmt.Fprintf(b, "- %s: %s\n", key, value)
}

// isatty detects whether stdin+stdout are real terminals.
func isatty() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// repoMap returns a concise structural overview of the project root,
// listing top-level directories and key config files the agent may need.
func repoMap(root string) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	var dirs, files []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") && e.Name() != ".github" && e.Name() != ".ok" {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, e.Name()+"/")
		} else {
			// Only include recognizable config/source files.
			switch e.Name() {
			case "go.mod", "go.sum", "Makefile", "README.md", "CHANGELOG.md",
				"ok.toml", "ok.example.toml", ".golangci.yml", ".editorconfig",
				"build.bat", "LICENSE", "SECURITY.md":
				files = append(files, e.Name())
			}
		}
	}
	if len(dirs) == 0 && len(files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Repository structure\n\n")
	if len(dirs) > 0 {
		fmt.Fprintf(&b, "Directories: %s\n", strings.Join(dirs, " "))
	}
	if len(files) > 0 {
		fmt.Fprintf(&b, "Key files: %s\n", strings.Join(files, " "))
	}
	return b.String()
}
