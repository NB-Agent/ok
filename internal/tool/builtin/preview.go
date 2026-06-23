package builtin

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/NB-Agent/ok/internal/diff"
)

// preview.go gives the file-writing built-ins the optional tool.Previewer
// capability: compute the change a call would make, reading the current file
// but never writing. A front-end (e.g. a desktop approval card) calls Preview
// before the permission gate runs Execute.
//
// Each Preview mirrors its Execute's transformation exactly — same arg parsing,
// same uniqueness / not-found rules — so the previewed NewText equals what
// Execute would persist. That equality is asserted by TestPreviewMatchesExecute
// in preview_test.go, which runs Execute against a temp file and compares; if
// an Execute body ever drifts, that test fails rather than the preview lying.

// Preview computes the change write_file would make. A path that does not yet
// exist is a Create; an existing one is a Modify.
func (w writeFile) Preview(args json.RawMessage) (diff.Change, error) {
	var p struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return diff.Change{}, fmt.Errorf("invalid args: %w", err)
	}
	if p.Path == "" {
		return diff.Change{}, fmt.Errorf("path is required")
	}
	p.Path = resolveIn(w.workDir, p.Path)

	old, kind := "", diff.Create
	if b, err := os.ReadFile(p.Path); err == nil {
		old, kind = string(b), diff.Modify
	} else if !os.IsNotExist(err) {
		return diff.Change{}, fmt.Errorf("read %s: %w", p.Path, err)
	}
	return diff.Build(p.Path, old, p.Content, kind), nil
}

// Preview computes the change edit_file would make. It enforces the same
// "old_string must occur exactly once" rule as Execute, returning that error
// when it doesn't — so a preview never shows a change the call couldn't make.
func (e editFile) Preview(args json.RawMessage) (diff.Change, error) {
	var p struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return diff.Change{}, fmt.Errorf("invalid args: %w", err)
	}
	if p.Path == "" {
		return diff.Change{}, fmt.Errorf("path is required")
	}
	if p.OldString == "" {
		return diff.Change{}, fmt.Errorf("old_string is required")
	}

	content, _, err := readEditTarget(e.roots, e.workDir, p.Path)
	if err != nil {
		return diff.Change{}, err
	}

	updated, _, err := applyEdits(content, []EditStep{{OldString: p.OldString, NewString: p.NewString, ReplaceAll: p.ReplaceAll}})
	if err != nil {
		return diff.Change{}, err
	}
	return diff.Build(resolveIn(e.workDir, p.Path), content, updated, diff.Modify), nil
}

// Preview computes the change multi_edit would make by replaying every edit
// against an in-memory buffer — exactly as Execute does — and diffing the
// result against the original. Any edit error surfaces here too, so a preview
// of an invalid batch fails the same way the call would.
func (m multiEdit) Preview(args json.RawMessage) (diff.Change, error) {
	var p struct {
		Path  string     `json:"path"`
		Edits []editStep `json:"edits"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return diff.Change{}, fmt.Errorf("invalid args: %w", err)
	}
	if p.Path == "" {
		return diff.Change{}, fmt.Errorf("path is required")
	}
	if len(p.Edits) == 0 {
		return diff.Change{}, fmt.Errorf("edits must not be empty")
	}

	content, _, err := readEditTarget(m.roots, m.workDir, p.Path)
	if err != nil {
		return diff.Change{}, err
	}
	original := content

	updated, _, err := applyEdits(content, p.Edits)
	if err != nil {
		return diff.Change{}, err
	}
	resolved := resolveIn(m.workDir, p.Path)
	return diff.Build(resolved, original, updated, diff.Modify), nil
}
