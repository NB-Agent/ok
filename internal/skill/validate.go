// Package skill — security validation for skill bodies.
//
// Every skill body passes through these checks at load time (hand-written
// skills) and at auto-generation time (evolution skills). Layer 0 checks
// structural completeness; Layer 1 scans for dangerous shell patterns.
//
// Unlike evolution.Engine's validation pipeline, these checks run before
// any skill enters the store — malicious skills are rejected at the gate.
package skill

import (
	"fmt"
	"regexp"
	"strings"
)

// ─── Layer 0: Structural validation ──────────────────────────────────────

// ValidateStructure checks a skill body for structural completeness.
// Returns nil when the skill is well-formed enough to be installed.
func ValidateStructure(body string) error {
	if body == "" {
		return fmt.Errorf("skill body is empty")
	}
	if !strings.HasPrefix(strings.TrimSpace(body), "---") {
		return fmt.Errorf("missing frontmatter (--- block)")
	}
	const minBodyLen = 80
	if len(body) < minBodyLen {
		return fmt.Errorf("skill body too short (%d chars, minimum %d)", len(body), minBodyLen)
	}
	return nil
}

// ─── Layer 1: Safety pattern scanning ────────────────────────────────────

// dangerousPatterns are regex patterns that indicate a skill may be harmful.
// These are conservative — matching any of these causes validation failure.
var dangerousPatterns = []struct {
	pattern *regexp.Regexp
	reason  string
}{
	{regexp.MustCompile(`rm\s+-rf\s+/`), "recursive root filesystem deletion"},
	{regexp.MustCompile(`curl\s+.*\|\s*(ba)?sh`), "pipe-download-to-shell (arbitrary code execution)"},
	{regexp.MustCompile(`wget\s+.*-O\s*-\s*\|\s*(ba)?sh`), "pipe-download-to-shell via wget"},
	{regexp.MustCompile(`>\s*/dev/sda`), "direct block device write"},
	{regexp.MustCompile(`mkfs\.`), "filesystem formatting"},
	{regexp.MustCompile(`dd\s+if=`), "raw disk copy"},
	{regexp.MustCompile(`:\(\)\s*\{\s*:\|:&\s*\};:`), "fork bomb"},
	{regexp.MustCompile(`chmod\s+777\s+/`), "world-writable root path"},
	{regexp.MustCompile(`git\s+push\s+--force.*origin.*main`), "force-push to main branch"},
	{regexp.MustCompile(`eval\s+\$`), "dynamic eval of shell variable"},
}

// ValidateSafety scans the skill body for dangerous shell patterns.
// Returns nil when the skill appears safe.
func ValidateSafety(body string) error {
	for _, dp := range dangerousPatterns {
		if dp.pattern.MatchString(body) {
			return fmt.Errorf("dangerous pattern detected: %s", dp.reason)
		}
	}
	return nil
}

// ValidateReferences checks that the skill body only references known
// tool names. Unknown tool references may indicate hallucination.
func ValidateReferences(body string, knownTools []string) error {
	re := regexp.MustCompile("`([a-z][a-z0-9_-]*)`")
	matches := re.FindAllStringSubmatch(body, -1)

	known := make(map[string]bool)
	for _, t := range knownTools {
		known[t] = true
	}

	var unknown []string
	for _, m := range matches {
		tool := m[1]
		if !LooksLikeToolName(tool) {
			continue
		}
		if !known[tool] {
			unknown = append(unknown, tool)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("unknown tool references: %s", strings.Join(unknown, ", "))
	}
	return nil
}

func LooksLikeToolName(s string) bool {
	if strings.Contains(s, "/") || strings.Contains(s, "\\") ||
		strings.Contains(s, " ") || strings.Contains(s, ".") {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return len(s) >= 2
}

// KnownTools returns the canonical set of tool names a skill may reference.
func KnownTools() []string {
	return []string{
		"bash", "read_file", "write_file", "edit_file", "grep",
		"glob", "ls", "web_fetch", "git", "task",
		"ok-verify", "run_skill", "remember", "recall",
		"ask", "tool-groups", "learn", "identity", "trust",
		"multi_edit", "code-explorer", "review", "security_review",
		"deploy", "desktop", "browser", "database", "debug",
	}
}
