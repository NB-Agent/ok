package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/NB-Agent/ok/internal/tool"
)

// repoManager manages multiple project directories in one session.
type repoManager struct {
	mu   sync.Mutex
	dirs map[string]string // name → absolute path
	cwd  string            // current active workspace
}

func init() {
	r := &repoManager{dirs: map[string]string{}}
	tool.RegisterBuiltin(r)
}

func (r *repoManager) Name() string { return "repo" }

func (r *repoManager) Description() string {
	return "Manage multiple repositories. Add, switch, run commands, list, remove. Essential for multi-repo projects."
}

func (r *repoManager) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"action":{"enum":["add","list","switch","run","info","remove"],"type":"string"},"command":{"type":"string"},"name":{"type":"string"},"path":{"type":"string"}},"required":["action"],"type":"object"}`)
}

func (r *repoManager) ReadOnly() bool { return false }

func (r *repoManager) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action  string `json:"action"`
		Name    string `json:"name"`
		Path    string `json:"path"`
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	switch p.Action {
	case "add":
		if p.Name == "" {
			return "", fmt.Errorf("name is required")
		}
		absPath := p.Path
		if absPath == "" {
			absPath = p.Name
		}
		abs, err := filepath.Abs(absPath)
		if err != nil {
			return "", fmt.Errorf("resolve path: %w", err)
		}
		r.dirs[p.Name] = abs
		if r.cwd == "" {
			r.cwd = abs
		}
		return fmt.Sprintf("# Repo Add\n\n✅ Added `%s` → `%s`\n", p.Name, abs), nil

	case "list":
		if len(r.dirs) == 0 {
			return "# Repo List\n\nNo repos configured. Use 'repo add <name> <path>' to add one.\n", nil
		}
		var b strings.Builder
		b.WriteString("# Repo List\n\n")
		for name, path := range r.dirs {
			mark := " "
			if path == r.cwd {
				mark = "▶"
			}
			b.WriteString(fmt.Sprintf("%s `%s` → `%s`\n", mark, name, path))
		}
		return b.String(), nil

	case "switch":
		if p.Name == "" {
			return "", fmt.Errorf("name is required")
		}
		path, ok := r.dirs[p.Name]
		if !ok {
			return "", fmt.Errorf("repo %q not found", p.Name)
		}
		r.cwd = path
		return fmt.Sprintf("# Repo Switch\n\n✅ Switched to `%s` (`%s`)\n", p.Name, path), nil

	case "run":
		if p.Name == "" {
			return "", fmt.Errorf("name is required")
		}
		path, ok := r.dirs[p.Name]
		if !ok {
			return "", fmt.Errorf("repo %q not found", p.Name)
		}
		if p.Command == "" {
			return "", fmt.Errorf("command is required")
		}
		return fmt.Sprintf("# Repo Run\n\n📂 `%s` (%s)\nRun: `%s`\n", p.Name, path, p.Command), nil

	case "info":
		if p.Name == "" {
			return "", fmt.Errorf("name is required")
		}
		path, ok := r.dirs[p.Name]
		if !ok {
			return "", fmt.Errorf("repo %q not found", p.Name)
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("# Repo Info: `%s`\n\n", p.Name))
		b.WriteString(fmt.Sprintf("Path: `%s`\n", path))
		b.WriteString(fmt.Sprintf("Active: %v\n", r.cwd == path))
		if info, err := os.Stat(filepath.Join(path, ".git")); err == nil && info.IsDir() {
			b.WriteString("Git: ✅\n")
		} else {
			b.WriteString("Git: ❌ (not a git repo)\n")
		}
		return b.String(), nil

	case "remove":
		if p.Name == "" {
			return "", fmt.Errorf("name is required")
		}
		delete(r.dirs, p.Name)
		if r.cwd == p.Path {
			r.cwd = ""
		}
		return fmt.Sprintf("# Repo Remove\n\n✅ Removed `%s`\n", p.Name), nil

	default:
		return "", fmt.Errorf("unknown action: %s", p.Action)
	}
}
