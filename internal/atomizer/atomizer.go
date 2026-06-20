// Package atomizer provides deterministic helpers for extracting structured
// content from LLM output. After DST-Lite, atom extraction and verification
// inference are not needed — only Markdown list parsing is kept for the UI.
package atomizer

import "strings"

// ListItemContent returns the task text of a markdown list line ("- x", "* x",
// "1. x", "2) x"), or "" if the line isn't a list item. Light inline-markdown
// stripping keeps the checklist readable. Used by controller (plan parsing).
func ListItemContent(line string) string {
	s := strings.TrimSpace(line)
	if s == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(s, "- "), strings.HasPrefix(s, "* "), strings.HasPrefix(s, "+ "):
		s = s[2:]
	default:
		i := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == 0 || i+1 >= len(s) || (s[i] != '.' && s[i] != ')') || s[i+1] != ' ' {
			return ""
		}
		s = s[i+2:]
	}
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[ ] ")
	s = strings.TrimPrefix(s, "[x] ")
	s = strings.TrimPrefix(s, "[X] ")
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "`", "")
	return strings.TrimSpace(s)
}
