package agent

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/NB-Agent/ok/internal/provider"
)

func firstLine(s string) string {
	if i := strings.IndexByte(s, 10); i >= 0 {
		return s[:i]
	}
	return s
}

// truncateToolOutput head+tails s when it exceeds maxToolOutputBytes. Slices are
// aligned to rune boundaries by nudging INWARD (never outward), so the result
// is never longer than the original and always valid UTF-8.
func truncateToolOutput(s string) (string, string) {
	if len(s) <= maxToolOutputBytes {
		return s, ""
	}
	keep := maxToolOutputBytes / 2
	// Head: take up to 'keep' bytes, then back up to the last rune start.
	headEnd := keep
	for headEnd > 0 && !utf8.RuneStart(s[headEnd]) {
		headEnd--
	}
	head := s[:headEnd]
	// Tail: take up to 'keep' bytes from the end, then advance to the first rune start.
	tailStart := len(s) - keep
	for tailStart < len(s) && !utf8.RuneStart(s[tailStart]) {
		tailStart++
	}
	tail := s[tailStart:]
	// Check that we actually made progress — the overhead of the truncation
	// message could make the output longer. In practice this only happens with
	// very large multibyte runes at the boundary. Just return the original with
	// a notice in that case.
	overhead := len("\n\n…[truncated ...]…\n\n")
	overheadMax := overhead + 120 // approx max byte count display
	if headEnd <= 0 && tailStart >= len(s) {
		// Can't find any rune boundaries; return unchanged with notice
		return s + fmt.Sprintf("\n\n…[tool output is %d bytes and contains no valid UTF-8 boundaries]…", len(s)),
			fmt.Sprintf("tool output %d bytes exceeds limit", len(s))
	}
	omitted := len(s) - len(head) - len(tail)
	if omitted < overheadMax {
		// Not enough to justify truncation — return full output with a warning marker
		return s + fmt.Sprintf("\n\n…[tool output exceeds %d bytes]…", maxToolOutputBytes),
			fmt.Sprintf("tool output truncated: %d of %d bytes elided", omitted, len(s))
	}
	notice := fmt.Sprintf("tool output truncated: %d of %d bytes elided", omitted, len(s))
	body := head + fmt.Sprintf("\n\n…[truncated %d of %d bytes — rerun with narrower args to see the middle]…\n\n", omitted, len(s)) + tail
	return body, notice
}

// finishReasonMessage maps an abnormal finish_reason to a one-line warning,
// returning ok=false for the normal terminations ("stop", "tool_calls") and a
// nil usage. The sink renders the message; the "! " prefix is presentation.
func finishReasonMessage(u *provider.Usage) (string, bool) {
	if u == nil {
		return "", false
	}
	switch u.FinishReason {
	case "length":
		return "response truncated: hit max output tokens", true
	case "content_filter":
		return "response blocked by content filter", true
	case "repetition_truncation":
		return "response truncated: model repetition detected", true
	default:
		return "", false
	}
}
