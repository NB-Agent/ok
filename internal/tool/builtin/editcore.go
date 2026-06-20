package builtin

import (
	"fmt"
	"os"
	"strings"
)

// editStep is one edit in a multi_edit operation. Exported so preview.go can
// reuse the same apply logic.
type EditStep struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

// applyEdits applies a list of edits against content in order, in memory.
// Returns the result, the total number of replacements made, and the first
// error (which leaves the file untouched — the caller never writes on failure).
func applyEdits(content string, edits []EditStep) (string, int, error) {
	applied := 0
	for i, step := range edits {
		if step.OldString == "" {
			return content, applied, fmt.Errorf("edit %d: old_string is required", i+1)
		}
		if step.ReplaceAll {
			count := strings.Count(content, step.OldString)
			if count == 0 {
				return content, applied, fmt.Errorf("edit %d: old_string not found", i+1)
			}
			content = strings.ReplaceAll(content, step.OldString, step.NewString)
			applied += count
			continue
		}
		switch strings.Count(content, step.OldString) {
		case 0:
			return content, applied, fmt.Errorf("edit %d: old_string not found", i+1)
		case 1:
			content = strings.Replace(content, step.OldString, step.NewString, 1)
			applied++
		default:
			return content, applied, fmt.Errorf("edit %d: old_string is not unique; add more surrounding context or set replace_all", i+1)
		}
	}
	return content, applied, nil
}

// readEditTarget resolves, confines, reads the target file and returns its
// content and mode. Shared by editFile, multiEdit, and preview.
func readEditTarget(roots []string, workDir, path string) (content string, mode os.FileMode, err error) {
	path = resolveIn(workDir, path)
	if err := confine(roots, path); err != nil {
		return "", 0, err
	}
	resolved, err := realPath(path)
	if err != nil {
		return "", 0, fmt.Errorf("resolve %s: %w", path, err)
	}
	b, err := os.ReadFile(resolved)
	if err != nil {
		return "", 0, fmt.Errorf("read %s: %w", resolved, err)
	}
	mode = os.FileMode(0o644)
	if info, err := os.Stat(resolved); err == nil {
		mode = info.Mode()
	}
	return string(b), mode, nil
}
