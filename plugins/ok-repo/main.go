// @ok/repo — MCP plugin: Multi-repository management. (migrated to plugin.StdioServer)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/NB-Agent/ok/internal/plugin"
)

// --- harness-backed server ---

type server struct{}

var repos = map[string]string{} // name → abs path

func (server) Info() (string, string) { return "ok-repo", "1.0.0" }

func (server) Tools() []plugin.ToolDef {
	return []plugin.ToolDef{{
		Name:        "repo",
		Description: "Manage multiple repositories",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":  plugin.StrEnum("add", "list", "switch", "run", "info", "remove"),
				"name":    plugin.StrProp(),
				"path":    plugin.StrProp(),
				"command": plugin.StrProp(),
			},
			"required": []string{"action"},
		},
	}}
}

func (server) Call(_ context.Context, name string, args json.RawMessage) (string, error) {
	if name != "repo" {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	var p struct {
		Action  string `json:"action"`
		Name    string `json:"name"`
		Path    string `json:"path"`
		Command string `json:"command"`
	}
	json.Unmarshal(args, &p)

	switch p.Action {
	case "add":
		if p.Name == "" {
			return "", fmt.Errorf("name is required")
		}
		absPath := p.Path
		if absPath == "" {
			absPath, _ = os.Getwd()
		}
		absPath, _ = filepath.Abs(absPath)
		repos[p.Name] = absPath
		return fmt.Sprintf("added repo %q → %s", p.Name, absPath), nil

	case "list":
		if len(repos) == 0 {
			return "No repos registered.\n", nil
		}
		var b strings.Builder
		b.WriteString("Registered repos:\n")
		for name, path := range repos {
			b.WriteString(fmt.Sprintf("  %s → %s\n", name, path))
		}
		return b.String(), nil

	case "switch":
		path, ok := repos[p.Name]
		if !ok {
			return "", fmt.Errorf("repo %q not found", p.Name)
		}
		if err := os.Chdir(path); err != nil {
			return "", err
		}
		return fmt.Sprintf("switched to %q (%s)", p.Name, path), nil

	case "run":
		path, ok := repos[p.Name]
		if !ok {
			return "", fmt.Errorf("repo %q not found", p.Name)
		}
		if p.Command == "" {
			return "", fmt.Errorf("command is required")
		}
		if !isSafeCmdName(p.Command) {
			return "", fmt.Errorf("command not allowed: %s", p.Command)
		}
		return runCmdDir(path, p.Command)

	case "info":
		path, ok := repos[p.Name]
		if !ok {
			return "", fmt.Errorf("repo %q not found", p.Name)
		}
		return runCmdDir(path, "git", "log", "--oneline", "-5")

	case "remove":
		if _, ok := repos[p.Name]; !ok {
			return "", fmt.Errorf("repo %q not found", p.Name)
		}
		delete(repos, p.Name)
		return fmt.Sprintf("removed repo %q", p.Name), nil

	default:
		return "", fmt.Errorf("unknown action: %s", p.Action)
	}
}

func runCmdDir(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func main() { plugin.RunStdio(server{}) }

// isSafeCmdName validates that name is a simple command (no path separators,
// no shell metacharacters, no leading dash). Only letters, digits, hyphens,
// and dots are allowed for the base executable name.
func isSafeCmdName(name string) bool {
	if name == "" || name[0] == '-' || name[0] == '.' {
		return false
	}
	for _, r := range name {
		if r == '/' || r == '\\' || r == ':' || r == ' ' {
			return false
		}
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}
