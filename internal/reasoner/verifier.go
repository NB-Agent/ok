package reasoner

import (
	"regexp"
	"strings"
)

// yesPatterns match affirmative conclusions in task output.
var yesPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?im)^(?:✅|✓)\s*(.*)`),                        // ✅ or ✓ prefix
	regexp.MustCompile(`(?im)\b(?:RESULT|VERDICT|CONCLUSION)\s*:\s*YES\b`),
	regexp.MustCompile(`(?im)\b(?:PASS|SUCCESS|OK)\b.*\b(?:completed|succeeded|verified|confirmed)\b`),
	regexp.MustCompile(`(?im)\btask\s+\S+\s+(?:completed|succeeded|passed)\b`),
}

// noPatterns match negative conclusions in task output.
var noPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?im)^(?:❌|✗|✘)\s*(.*)`),                     // ❌ or ✗ prefix
	regexp.MustCompile(`(?im)\b(?:RESULT|VERDICT|CONCLUSION)\s*:\s*NO\b`),
	regexp.MustCompile(`(?im)\b(?:FAIL|ERROR|FAILED)\b.*\b(?:task|check|verification|test)\b`),
	regexp.MustCompile(`(?im)\b(?:not found|not unique|does not exist|no match|cannot|unable)\b`),
}

// evidencePattern extracts file:line references like "path/file.go:42" or
// "path/file.go:42: message".
var evidencePattern = regexp.MustCompile(`([\w\-./\\]+\.\w+):(\d+)(?::\s*(.*))?`)

// ParseVerdict examines the raw output of a sub-agent task and extracts:
//   - VerdictYes if the output signals success
//   - VerdictNo if the output signals failure
//   - VerdictUncertain if neither can be determined
//
// It also extracts file:line evidence references found in the output.
// The method parameter records which extraction approach was used (may be
// empty if not known).
func ParseVerdict(output string) (Verdict, []Evidence) {
	if output == "" {
		return VerdictUncertain, nil
	}

	evidence := extractEvidence(output)

	// Check YES patterns first.
	for _, p := range yesPatterns {
		if p.MatchString(output) {
			return VerdictYes, evidence
		}
	}

	// Check NO patterns.
	for _, p := range noPatterns {
		if p.MatchString(output) {
			return VerdictNo, evidence
		}
	}

	// Heuristic: if we have at least one file:line evidence, lean YES
	// (the task produced concrete proof). But only if there's no explicit
	// failure indicator.
	if len(evidence) > 0 && !strings.Contains(strings.ToLower(output), "error") &&
		!strings.Contains(strings.ToLower(output), "fail") {
		return VerdictYes, evidence
	}

	return VerdictUncertain, evidence
}

// extractEvidence scans output for file:line references.
func extractEvidence(output string) []Evidence {
	matches := evidencePattern.FindAllStringSubmatch(output, 20)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	var out []Evidence
	for _, m := range matches {
		key := m[1] + ":" + m[2]
		if seen[key] {
			continue
		}
		seen[key] = true
		line := 0
		// ParseInt not needed — regex guarantees digits.
		for _, c := range m[2] {
			line = line*10 + int(c-'0')
		}
		text := ""
		if len(m) > 3 {
			text = strings.TrimSpace(m[3])
		}
		out = append(out, Evidence{File: m[1], Line: line, Text: text})
	}
	return out
}
