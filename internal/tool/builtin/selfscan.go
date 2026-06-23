package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NB-Agent/ok/internal/tool"
	"github.com/NB-Agent/ok/internal/winhide"
)

func init() { tool.RegisterBuiltin(selfScan{}) }

// selfScan analyzes the agent's own state — skills, project health, git status —
// and returns a structured self-assessment. It's read-only and safe to call at
// any time, giving the agent self-awareness for better decision-making.
type selfScan struct{}

func (selfScan) Name() string { return "self-scan" }

func (selfScan) Description() string {
	return "Analyze agent state — skills, project health, git status — and return a self-assessment."
}

func (selfScan) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"focus":{"enum":["quick","full"],"type":"string"}},"type":"object"}`)
}

func (selfScan) ReadOnly() bool { return true }

func (selfScan) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Focus string `json:"focus"`
	}
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	full := p.Focus == "full"

	var b strings.Builder
	b.WriteString("# Self-Scan Report\n\n")

	// 1. Skills inventory
	skillsDir := filepath.Join(".", ".ok", "skills")
	if entries, err := os.ReadDir(skillsDir); err == nil {
		b.WriteString("## Skills\n")
		b.WriteString(fmt.Sprintf("- Total skills: %d\n", len(entries)))
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				name := strings.TrimSuffix(e.Name(), ".md")
				b.WriteString(fmt.Sprintf("  - %s\n", name))
			}
		}
	} else {
		b.WriteString("## Skills (not found — .ok/skills/ does not exist)\n")
	}

	// 2. Git status
	b.WriteString("\n## Git Status\n")
	gitLog := runCmd(ctx, "git", "log", "--oneline", "-5")
	if gitLog != "" {
		b.WriteString(fmt.Sprintf("Recent commits:\n%s\n", gitLog))
	} else {
		b.WriteString("(no git history or not a git repo)\n")
	}

	gitDiff := runCmd(ctx, "git", "diff", "--stat")
	if gitDiff != "" {
		b.WriteString(fmt.Sprintf("Uncommitted changes:\n%s\n", gitDiff))
	} else {
		b.WriteString("(no uncommitted changes)\n")
	}

	// 3. Project structure
	b.WriteString("\n## Project Structure\n")
	dirs := runCmd(ctx, "cmd", "/c", "dir /b /ad 2>nul")
	if dirs == "" {
		dirs = runCmd(ctx, "ls", "-d", "*/")
	}
	if dirs != "" {
		lines := strings.Split(strings.TrimSpace(dirs), "\n")
		var internalDirs []string
		for _, d := range lines {
			d = strings.TrimSpace(d)
			if strings.Contains(d, "internal") || strings.Contains(d, "cmd") || strings.Contains(d, "desktop") {
				internalDirs = append(internalDirs, d)
			}
		}
		if len(internalDirs) > 0 {
			b.WriteString(fmt.Sprintf("Key dirs: %s\n", strings.Join(internalDirs, ", ")))
		}
	}

	// 4. Full mode: build + test health
	if full {
		b.WriteString("\n## Build Health\n")
		if out := runCmd(ctx, "go", "build", "./..."); out == "" {
			b.WriteString("✅ Build: OK\n")
		} else {
			b.WriteString(fmt.Sprintf("❌ Build: %s\n", truncateStr(out, 300)))
		}

		b.WriteString("\n## Test Health\n")
		testOut := runCmd(ctx, "go", "test", "./internal/...")
		if strings.Contains(testOut, "FAIL") {
			// Extract just the FAIL lines
			var fails []string
			for _, line := range strings.Split(testOut, "\n") {
				if strings.Contains(line, "FAIL") {
					fails = append(fails, strings.TrimSpace(line))
				}
			}
			if len(fails) > 0 {
				b.WriteString(fmt.Sprintf("❌ Failing: %s\n", strings.Join(fails, "; ")))
			} else {
				b.WriteString("⚠️  Tests completed with FAIL status\n")
			}
		} else if testOut == "" {
			b.WriteString("✅ Tests: OK\n")
		} else {
			// Extract the ok/FAIL summary lines
			var summary []string
			for _, line := range strings.Split(testOut, "\n") {
				if strings.HasPrefix(line, "ok") || strings.HasPrefix(line, "FAIL") || strings.HasPrefix(line, "?") {
					summary = append(summary, strings.TrimSpace(line))
				}
			}
			if len(summary) > 10 {
				b.WriteString(fmt.Sprintf("✅ Tests: OK (%d packages)\n", len(summary)))
			} else if len(summary) > 0 {
				b.WriteString(fmt.Sprintf("✅ Tests: OK\n%s\n", strings.Join(summary, "\n")))
			}
		}
	}

	b.WriteString("\n## Recommendations\n")
	b.WriteString("- Run `self-scan focus:full` for complete build+test health\n")

	return b.String(), nil
}

func runCmd(ctx context.Context, name string, args ...string) string {
	cmd := winhide.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
