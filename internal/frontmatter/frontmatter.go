// Package frontmatter parses YAML-like frontmatter ("---" ... "---") from
// Markdown text. It is dependency-free and shared by command, memory, and skill
// packages — keeping a single implementation rather than three copies.
package frontmatter

import "strings"

// Parse separates an optional leading "---"-fenced block of simple
// "key: value" lines from the body. Returns the parsed keys (lowercased,
// flattened from any depth) and the remaining body. With no opening/closing
// fence the whole input is the body; an unclosed opening fence is treated as
// body (conservatively: it's likely not frontmatter).
//
// For large documents Parse avoids splitting the entire input: it scans for
// the closing fence with strings.Index, splits only the frontmatter region,
// and returns the body as a direct substring.
func Parse(s string) (map[string]string, string) {
	fm := map[string]string{}

	if !strings.HasPrefix(s, "---") {
		return fm, s
	}
	rest := s[3:]
	nl := 0
	if len(rest) > 0 && rest[0] == '\r' {
		nl = 1
		if len(rest) > 1 && rest[1] == '\n' {
			nl = 2
		}
	}
	if nl == 0 && (len(rest) == 0 || rest[0] != '\n') {
		return fm, s // "---" or "---foo" — not a fence
	}
	afterNL := rest[nl:]

	// Scan for a closing "---" on its own line. Must be preceded by \n and
	// followed by \n, \r\n, or end-of-string.
	for {
		idx := strings.Index(afterNL, "\n---")
		if idx < 0 {
			return fm, s // never closed
		}
		end := idx + 4 // past "\n---"
		if end == len(afterNL) {
			// "---" at end of file — valid closing fence, empty body.
			return parseFM(afterNL[:idx]), ""
		}
		// Check what follows "---": must be \n or \r\n.
		if afterNL[end] == '\r' {
			end++
		}
		if end < len(afterNL) && afterNL[end] == '\n' {
			end++ // consume the newline after "---"
			return parseFM(afterNL[:idx]), afterNL[end:]
		}
		// "---" followed by non-newline (e.g. "---foo") — keep scanning.
		afterNL = afterNL[end:]
	}
}

// parseFM extracts "key: value" pairs from a frontmatter region (no leading/trailing fences).
func parseFM(fmRegion string) map[string]string {
	fm := map[string]string{}
	for _, l := range strings.Split(fmRegion, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(k))
		val := strings.Trim(strings.TrimSpace(v), `"'`)
		if val != "" {
			fm[key] = val // last write wins
		}
	}
	return fm
}

// Normalize strips a leading UTF-8 BOM and replaces CRLF with LF, returning
// clean UTF-8 content suitable for parsing. Use on raw []byte before Parse.
func Normalize(raw []byte) string {
	return strings.TrimPrefix(
		strings.ReplaceAll(string(raw), "\r\n", "\n"),
		"\uFEFF",
	)
}
