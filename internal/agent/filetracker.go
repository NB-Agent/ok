// Package agent — file-read tracker for tool-aware context compression.
//
// The fileTracker records which messages in the session carry the content of
// which files (from read_file and grep results). When the same file appears in
// a new tool result, the previous occurrence is skeletonized in-place —
// keeping signatures and structural hints while dropping the body — so the
// model still knows the file exists and what it exports, but stops paying
// tokens for stale copies of the implementation.
//
// This implements the core insight from TokenTamer
// (github.com/borhen68/TokenTamer): track tool_use → file mappings and
// skeletonize background files while keeping the most recent read intact.
//
// Only read_file and grep results are tracked; writes (write_file, edit_file,
// multi_edit) are never skeletonized because they represent the authoritative
// state after a mutation. bash results are also skipped — their content is
// too varied to map to a single file.
package agent

import (
	"strings"
)

// fileRef records one occurrence of a file path in the session.
type fileRef struct {
	msgIndex int    // position in the session's message slice
	toolName string // "read_file" or "grep"
}

// fileTracker maps file paths to their occurrences in the session.
// The zero value is ready to use.
type fileTracker struct {
	files map[string][]fileRef // path → ordered occurrences (oldest first)
}

// track inspects a tool-result message and records its file path references.
// It returns the list of paths referenced in this message so the caller can
// skeletonize older occurrences.
func (ft *fileTracker) track(msgIndex int, toolName string, content string) []string {
	if !isReadTool(toolName) {
		return nil
	}
	paths := extractPaths(toolName, content)
	if len(paths) == 0 {
		return nil
	}
	if ft.files == nil {
		ft.files = make(map[string][]fileRef)
	}
	var prior []string
	for _, p := range paths {
		if existing, ok := ft.files[p]; ok && len(existing) > 0 {
			prior = append(prior, p)
		}
		ft.files[p] = append(ft.files[p], fileRef{msgIndex: msgIndex, toolName: toolName})
	}
	return prior
}

// priorRefs returns the message indices of previous reads for the given paths.
func (ft *fileTracker) priorRefs(paths []string) []int {
	var indices []int
	for _, p := range paths {
		refs := ft.files[p]
		// All but the last (most recent) are candidates for skeletonization.
		for i := 0; i < len(refs)-1; i++ {
			indices = append(indices, refs[i].msgIndex)
		}
	}
	return indices
}

// isReadTool reports whether the tool produces file content that should be tracked.
func isReadTool(name string) bool {
	return name == "read_file" || name == "grep"
}

// extractPaths extracts file paths from a tool result based on the tool name.
// read_file results carry the path in the tool call arguments; grep results
// prefix each match line with "path:line:text".
func extractPaths(toolName string, content string) []string {
	switch toolName {
	case "read_file":
		return extractReadFilePath(content)
	case "grep":
		return extractGrepPaths(content)
	}
	return nil
}

// extractReadFilePath parses the path from a read_file result.
// The result format from OK's read_file tool is:
//
//	<content of the file, possibly with line numbers>
//
// We can't reliably get the path from the result alone because OK doesn't
// include it in the tool result text. Instead we rely on the tool call
// arguments — the caller (agent_run.go) passes the tool name; the path is
// extracted from tool call args before calling track.
//
// As a fallback, we look for common path patterns in the first line.
func extractReadFilePath(content string) []string {
	// read_file tool in OK prepends path info. Look for path-like patterns.
	// The tool result format varies by frontend. We use a heuristic: if the
	// content starts with a file path pattern, extract it.
	if len(content) == 0 {
		return nil
	}
	// The most common case: /absolute/path or relative/path on the first line.
	firstLine := content
	if nl := strings.IndexByte(content, '\n'); nl >= 0 {
		firstLine = content[:nl]
	}
	firstLine = strings.TrimSpace(firstLine)
	if looksLikePath(firstLine) {
		return []string{firstLine}
	}
	return nil
}

// extractGrepPaths extracts file paths from grep output lines.
// grep results are formatted as "path:line:text" — we collect unique paths.
func extractGrepPaths(content string) []string {
	seen := make(map[string]bool)
	var paths []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Find the first colon that separates path from line number.
		// Paths can contain colons on Windows (C:\foo\bar.go:10:text).
		// We handle this by looking for the pattern: <path>:<digits>:
		path := extractGrepPath(line)
		if path != "" && !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	return paths
}

// extractGrepPath extracts the file path from a single grep output line.
func extractGrepPath(line string) string {
	// Pattern: path:line:text  or  path:line:col:text
	// Windows paths like C:\foo\bar.go:10:text need special handling.
	// Strategy: find the last occurrence of ":\d" (colon then digit) — the
	// path is everything before that colon.
	for i := len(line) - 2; i >= 0; i-- {
		if line[i] == ':' && i+1 < len(line) && line[i+1] >= '0' && line[i+1] <= '9' {
			return line[:i]
		}
	}
	return ""
}

// looksLikePath returns true if s looks like a filesystem path.
func looksLikePath(s string) bool {
	if s == "" {
		return false
	}
	// Absolute paths
	if strings.HasPrefix(s, "/") {
		return true
	}
	// Windows absolute paths (C:\..., \\...)
	if len(s) >= 3 && s[1] == ':' && (s[2] == '\\' || s[2] == '/') {
		return true
	}
	if strings.HasPrefix(s, "\\\\") {
		return true
	}
	// Relative paths with extensions
	if strings.Contains(s, ".") && (strings.Contains(s, "/") || strings.Contains(s, "\\")) {
		return true
	}
	return false
}
