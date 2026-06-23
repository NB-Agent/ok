package builtin

import (
	"fmt"
	"os"
	"regexp"
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
			// Exact match failed — try fuzzy with whitespace tolerance.
			match, count, fuzzyErr := tryFuzzyMatch(content, step.OldString)
			if fuzzyErr != nil {
				return content, applied, fmt.Errorf("edit %d: %w", i+1, fuzzyErr)
			}
			if count == 1 {
				content = strings.Replace(content, match, step.NewString, 1)
				applied++
				continue
			}
			return content, applied, fmt.Errorf("edit %d: old_string is not unique even with fuzzy matching; add more surrounding context or set replace_all", i+1)
		case 1:
			content = strings.Replace(content, step.OldString, step.NewString, 1)
			applied++
		default:
			return content, applied, fmt.Errorf("edit %d: old_string is not unique; add more surrounding context or set replace_all", i+1)
		}
	}
	return content, applied, nil
}

// tryFuzzyMatch attempts to find needle in haystack when exact match fails.
// It builds a whitespace-tolerant regex from the needle: each line is
// trimmed, regex-escaped, and spaces are replaced with \s+ to tolerate
// tab/space mismatches and indentation drift. Returns the actual matched
// text (from the original content), the match count, or an error.
func tryFuzzyMatch(content, needle string) (string, int, error) {
	lines := strings.Split(needle, "\n")
	var patterns []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		escaped := regexp.QuoteMeta(trimmed)
		flexible := strings.ReplaceAll(escaped, " ", `\s+`)
		patterns = append(patterns, flexible)
	}
	if len(patterns) == 0 {
		return "", 0, fmt.Errorf("old_string not found (needle empty after trimming)")
	}
	reStr := "(?m)" + strings.Join(patterns, `\s*\n\s*`)
	re, err := regexp.Compile(reStr)
	if err != nil {
		return "", 0, fmt.Errorf("old_string not found (fuzzy regex failed: %w)", err)
	}
	matches := re.FindAllString(content, -1)
	if len(matches) == 0 {
		return "", 0, fmt.Errorf("old_string not found (tried exact and fuzzy whitespace matching)")
	}
	return matches[0], len(matches), nil
}

// readEditTarget resolves, confines, reads the target file and returns its
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
