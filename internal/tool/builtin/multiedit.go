package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/NB-Agent/ok/internal/diff"
	"github.com/NB-Agent/ok/internal/tool"
)

func init() { tool.RegisterBuiltin(multiEdit{}) }

type multiEdit struct {
	roots   []string
	workDir string
}

type editStep = EditStep

func (multiEdit) Name() string { return "multi_edit" }

func (multiEdit) Description() string {
	return "Apply ordered edits atomically — all in-memory, written only if all succeed. Safer than chaining edit_file. Unified diff included."
}

func (multiEdit) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"edits":{"items":{"properties":{"new_string":{"type":"string"},"old_string":{"type":"string"},"replace_all":{"type":"boolean"}},"required":["old_string","new_string"],"type":"object"},"minItems":1,"type":"array"},"path":{"type":"string"}},"required":["path","edits"],"type":"object"}`)
}

func (multiEdit) ReadOnly() bool { return false }

func (m multiEdit) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Path  string     `json:"path"`
		Edits []editStep `json:"edits"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if len(p.Edits) == 0 {
		return "", fmt.Errorf("edits must not be empty")
	}

	content, mode, err := readEditTarget(m.roots, m.workDir, p.Path)
	if err != nil {
		return "", err
	}

	updated, applied, err := applyEdits(content, p.Edits)
	if err != nil {
		return "", err
	}

	resolved := resolveIn(m.workDir, p.Path)

	// Pre-write Go semantic check: write to temp → go vet → only write on pass.
	if err := precheckGoFile(resolved, updated, m.workDir); err != nil {
		return "", err
	}

	// Save undo snapshot before writing.
	saveUndo(resolved, content, mode)

	if err := os.WriteFile(resolved, []byte(updated), mode); err != nil {
		return "", fmt.Errorf("write %s: %w", resolved, err)
	}

	d := diff.Build(resolved, content, updated, diff.Modify)
	return renderEditResult(resolved, d, len(p.Edits), applied), nil
}
