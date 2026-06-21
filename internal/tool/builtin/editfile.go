package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/NB-Agent/ok/internal/diff"
	"github.com/NB-Agent/ok/internal/tool"
)

func init() { tool.RegisterBuiltin(editFile{}) }

// editFile replaces an exact string in a file. roots confines the target to the
// workspace when non-empty (see writeFile); workDir, when non-empty, is the
// directory a relative path resolves against (see resolveIn).
type editFile struct {
	roots   []string
	workDir string
}

func (editFile) Name() string { return "edit_file" }

func (editFile) Description() string {
	return "Replace exact string in a file. old_string must be unique; add context to disambiguate. Unified diff included."
}

func (editFile) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"new_string":{"type":"string"},"old_string":{"type":"string"},"path":{"type":"string"}},"required":["path","old_string","new_string"],"type":"object"}`)
}

func (editFile) ReadOnly() bool { return false }

func (e editFile) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if p.OldString == "" {
		return "", fmt.Errorf("old_string is required")
	}

	content, mode, err := readEditTarget(e.roots, e.workDir, p.Path)
	if err != nil {
		return "", err
	}

	updated, _, err := applyEdits(content, []EditStep{{OldString: p.OldString, NewString: p.NewString}})
	if err != nil {
		return "", err
	}

	resolved := resolveIn(e.workDir, p.Path)

	// Pre-write Go semantic check: write to temp → go vet → only write on pass.
	if err := precheckGoFile(resolved, updated, e.workDir); err != nil {
		return "", err
	}

	// Save undo snapshot before writing.
	saveUndo(resolved, content, mode)

	if err := os.WriteFile(resolved, []byte(updated), mode); err != nil {
		return "", fmt.Errorf("write %s: %w", resolved, err)
	}

	// Build unified diff for the model to verify.
	d := diff.Build(resolved, content, updated, diff.Modify)
	return renderEditResult(resolved, d, 1, 1), nil
}

// renderEditResult formats an edit result with a unified diff preview.
func renderEditResult(path string, d diff.Change, steps, total int) string {
	var b strings.Builder
	b.WriteString("# Edit ")
	b.WriteString(path)
	b.WriteString("\n\n")
	if d.Binary {
		b.WriteString("✅ Binary file changed.\n")
	} else if d.Added == 0 && d.Removed == 0 {
		b.WriteString("✅ No changes (content identical).\n")
	} else {
		b.WriteString(fmt.Sprintf("✅ %d edit(s) / %d replacement(s) — +%d / −%d lines\n\n", steps, total, d.Added, d.Removed))
		b.WriteString("```diff\n")
		b.WriteString(d.Diff)
		b.WriteString("\n```\n")
	}
	b.WriteString(fmt.Sprintf("\n💡 Undo with `undo` (stack depth: %d)\n", undoSize()))
	return b.String()
}
