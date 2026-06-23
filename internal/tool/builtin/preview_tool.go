package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/NB-Agent/ok/internal/diff"
	"github.com/NB-Agent/ok/internal/tool"
)

func init() { tool.RegisterBuiltin(previewEdit{}) }

// previewEdit lets the LLM dry-run an edit_file or multi_edit call before
// committing it. It applies the same matching rules (exact then fuzzy,
// uniqueness check, replace_all) against the current file content but
// never writes to disk or the undo stack. Returns the unified diff that
// would be applied, or the same error the real edit would encounter.
type previewEdit struct {
	roots   []string
	workDir string
}

func (previewEdit) Name() string { return "preview_edit" }

func (previewEdit) Description() string {
	return "Dry-run an edit — preview the unified diff without writing to disk. Same args as edit_file."
}

func (previewEdit) Schema() json.RawMessage {
	// Same schema as edit_file (+ replace_all).
	return json.RawMessage(`{"properties":{"new_string":{"type":"string"},"old_string":{"type":"string"},"path":{"type":"string"},"replace_all":{"type":"boolean"}},"required":["path","old_string","new_string"],"type":"object"}`)
}

func (previewEdit) ReadOnly() bool { return true }

func (p previewEdit) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if params.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if params.OldString == "" {
		return "", fmt.Errorf("old_string is required")
	}

	content, _, err := readEditTarget(p.roots, p.workDir, params.Path)
	if err != nil {
		return "", err
	}

	updated, applied, err := applyEdits(content, []EditStep{{
		OldString:  params.OldString,
		NewString:  params.NewString,
		ReplaceAll: params.ReplaceAll,
	}})
	if err != nil {
		return "", fmt.Errorf("preview: %w", err)
	}

	resolved := resolveIn(p.workDir, params.Path)
	d := diff.Build(resolved, content, updated, diff.Modify)
	return renderEditResult(resolved, d, 1, applied), nil
}
