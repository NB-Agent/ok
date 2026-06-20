package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NB-Agent/ok/internal/diff"
	"github.com/NB-Agent/ok/internal/tool"
)

func init() { tool.RegisterBuiltin(writeFile{}) }

// writeFile writes a file. roots, when non-empty, confines the target to the
// workspace (see confine); the zero value registered at init is unconfined and
// is overridden per run by ConfineWriters. workDir, when non-empty, is the
// directory a relative path resolves against (see resolveIn).
type writeFile struct {
	roots   []string
	workDir string
}

func (writeFile) Name() string { return "write_file" }

func (writeFile) Description() string {
	return "Write content to a file at the given path (overwriting existing content). Creates parent directories as needed. A unified diff is included when overwriting."
}

func (writeFile) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"content":{"type":"string"},"path":{"type":"string"}},"required":["path","content"],"type":"object"}`)
}

func (writeFile) ReadOnly() bool { return false }

func (w writeFile) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	p.Path = resolveIn(w.workDir, p.Path)
	if err := confine(w.roots, p.Path); err != nil {
		return "", err
	}
	resolved, err := realPath(p.Path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", p.Path, err)
	}
	if dir := filepath.Dir(resolved); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	// Snapshot old content for undo + diff.
	var oldContent string
	mode := os.FileMode(0o644)
	existed := false
	if info, err := os.Stat(resolved); err == nil {
		mode = info.Mode()
		existed = true
		if b, err := os.ReadFile(resolved); err == nil {
			oldContent = string(b)
		}
	}

	saveUndo(resolved, oldContent, mode)

	// Pre-write Go semantic check: write to temp → go vet → only write
	// on pass. This prevents the DST rollback cycle for type/import errors.
	if err := precheckGoFile(resolved, p.Content, w.workDir); err != nil {
		return "", err
	}

	// Guard against OOM from tool-feedback loops — a model that echoes its own
	// output through write_file can grow without bound. 100 MiB is far larger
	// than any legitimate source file and protects even 32-bit systems.
	const maxWriteBytes = 100 << 20 // 100 MiB
	if len(p.Content) > maxWriteBytes {
		return "", fmt.Errorf("content too large: %d bytes (max %d)", len(p.Content), maxWriteBytes)
	}
	if err := os.WriteFile(resolved, []byte(p.Content), mode); err != nil {
		return "", fmt.Errorf("write %s: %w", resolved, err)
	}

	// Build unified diff.
	kind := diff.Create
	if existed {
		kind = diff.Modify
	}
	d := diff.Build(resolved, oldContent, p.Content, kind)

	var b strings.Builder
	b.WriteString("# Write ")
	b.WriteString(resolved)
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("✅ %d bytes written", len(p.Content)))
	if existed {
		b.WriteString(fmt.Sprintf(" (overwritten) — +%d / −%d lines", d.Added, d.Removed))
	}
	b.WriteString("\n")
	if !d.Binary && d.Diff != "" {
		b.WriteString("\n```diff\n")
		b.WriteString(d.Diff)
		b.WriteString("\n```\n")
	}
	b.WriteString(fmt.Sprintf("\n💡 Undo with `undo` (stack depth: %d)\n", undoSize()))
	return b.String(), nil
}
