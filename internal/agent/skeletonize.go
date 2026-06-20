// Package agent — tool result skeletonizer for context compression.
//
// When the same file is read multiple times in a session (by read_file or
// grep), older copies are skeletonized — the implementation body is replaced
// with a structural summary while signatures, imports, and type declarations
// are preserved. The model still knows what the file exports but stops paying
// tokens for stale copies of the implementation.
//
// Only the most recent read of each file is kept full; every earlier read is
// skeletonized in-place in the session message slice.
package agent

import (
	"strings"
	"unicode"
)

// skeletonizeThreshold is the minimum number of lines before we consider
// skeletonizing. Very short results (one-liners, small grep hits) are left
// intact — the overhead of a skeleton marker would exceed the savings.
const skeletonizeThreshold = 10

// Keep at most this many lines from the top of a skeletonized file read.
const skeletonKeepHead = 5

// Keep at most this many lines from the bottom of a skeletonized file read.
const skeletonKeepTail = 3

// skeletonize replaces the body of a read_file or grep result with a
// structural summary, preserving the file identity, key signatures, and
// type declarations. Returns the content unchanged if the tool is not a
// read tool or the content is too short to benefit from skeletonization.
func skeletonize(toolName string, content string) string {
	if !isReadTool(toolName) {
		return content
	}
	lines := strings.Split(content, "\n")
	if len(lines) < skeletonizeThreshold {
		return content
	}
	switch toolName {
	case "read_file":
		return skeletonizeFileRead(lines)
	case "grep":
		return skeletonizeGrepResult(lines)
	}
	return content
}

// skeletonizeFileRead produces a skeleton of a read_file result.
// It keeps the head (path/first lines), extracts key structural lines
// (function signatures, type declarations, imports), and drops the body.
func skeletonizeFileRead(lines []string) string {
	if len(lines) <= skeletonKeepHead+skeletonKeepTail {
		// Too small — skeleton marker would cost more than savings.
		return strings.Join(lines, "\n")
	}

	var b strings.Builder

	// Head: keep first N lines (usually contains the file path and header).
	for i := 0; i < skeletonKeepHead && i < len(lines); i++ {
		b.WriteString(lines[i])
		b.WriteByte('\n')
	}

	// Structural lines: keep signatures, type declarations, imports.
	structLines := extractStructuralLines(lines[skeletonKeepHead : len(lines)-skeletonKeepTail])
	if len(structLines) > 0 {
		b.WriteString("\n")
		for _, l := range structLines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}

	// Skeleton marker.
	savings := estimateSavings(lines, structLines)
	b.WriteString("\n[... skeletonized — ")
	b.WriteString(savings)
	b.WriteString(" ...]\n")

	// Tail: keep last few lines (often closing braces, EOF markers).
	if skeletonKeepTail > 0 {
		tailStart := len(lines) - skeletonKeepTail
		if tailStart < skeletonKeepHead {
			tailStart = skeletonKeepHead
		}
		b.WriteByte('\n')
		for i := tailStart; i < len(lines); i++ {
			b.WriteString(lines[i])
			b.WriteByte('\n')
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

// skeletonizeGrepResult produces a skeleton of a grep result.
// grep results are typically many small lines; we collapse them into a
// summary with unique file paths and match counts.
func skeletonizeGrepResult(lines []string) string {
	if len(lines) <= skeletonKeepHead {
		return strings.Join(lines, "\n")
	}

	// Count matches per file.
	fileCounts := make(map[string]int)
	var fileOrder []string
	for _, line := range lines {
		path := extractGrepPath(line)
		if path != "" {
			if _, ok := fileCounts[path]; !ok {
				fileOrder = append(fileOrder, path)
			}
			fileCounts[path]++
		}
	}

	var b strings.Builder
	b.WriteString("[grep result skeletonized — ")
	b.WriteString(formatInt(len(lines)))
	b.WriteString(" matches across ")
	b.WriteString(formatInt(len(fileCounts)))
	b.WriteString(" files")

	if len(fileOrder) > 0 {
		b.WriteString(": ")
		for i, f := range fileOrder {
			if i > 0 {
				b.WriteString(", ")
			}
			if i >= 10 {
				b.WriteString("...")
				break
			}
			b.WriteString(f)
			b.WriteString(" (")
			b.WriteString(formatInt(fileCounts[f]))
			b.WriteString(")")
		}
	}
	b.WriteString("]")
	return b.String()
}

// extractStructuralLines returns lines from the middle of a file that look
// like structural declarations: function/method signatures, type declarations,
// import statements, and package declarations.
func extractStructuralLines(lines []string) []string {
	var out []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if isStructuralLine(trimmed) {
			out = append(out, l)
		}
	}
	// Cap at 30 structural lines to prevent the skeleton from being too large.
	if len(out) > 30 {
		out = out[:30]
	}
	return out
}

// isStructuralLine reports whether a line looks like a structural declaration
// worth preserving in a skeleton. We detect common Go patterns; for non-Go
// languages the heuristic is weaker but still catches obvious declarations.
func isStructuralLine(line string) bool {
	if line == "" {
		return false
	}

	// Skip comments and blank lines.
	if strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "/*") || strings.HasPrefix(line, "*") {
		return false
	}

	// Package declaration.
	if strings.HasPrefix(line, "package ") {
		return true
	}

	// Import statements.
	if strings.HasPrefix(line, "import ") || strings.HasPrefix(line, "import (") || line == "import (" || line == ")" {
		return true
	}

	// Go function/method declarations: func Xxx(  or  func (r *T) Xxx(
	if strings.HasPrefix(line, "func ") || strings.HasPrefix(line, "func (") {
		return true
	}

	// Type declarations.
	if strings.HasPrefix(line, "type ") {
		return true
	}

	// Interface/struct opening.
	if strings.HasPrefix(line, "type ") && strings.Contains(line, "interface") {
		return true
	}
	if strings.HasPrefix(line, "type ") && strings.Contains(line, "struct") {
		return true
	}

	// Const/var blocks.
	if strings.HasPrefix(line, "const ") || strings.HasPrefix(line, "var ") ||
		line == "const (" || line == "var (" {
		return true
	}

	// Exported identifiers at the top level (heuristic: starts with uppercase,
	// not indented, looks like a declaration).
	if len(line) > 0 && unicode.IsUpper(rune(line[0])) && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
		// Could be a top-level var/const.
		return true
	}

	return false
}

// estimateSavings returns a human-readable estimate of the tokens saved.
func estimateSavings(allLines, kept []string) string {
	keptCount := len(kept) + skeletonKeepHead + skeletonKeepTail
	if keptCount >= len(allLines) {
		return "minimal savings"
	}
	pct := 100 - (keptCount * 100 / len(allLines))
	return formatInt(pct) + "% of content dropped"
}

// formatInt is a simple integer formatter without importing fmt.
func formatInt(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
