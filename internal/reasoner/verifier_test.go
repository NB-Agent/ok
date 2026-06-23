package reasoner

import (
	"testing"
)

func TestParseVerdict_Yes(t *testing.T) {
	tests := []string{
		"✅ Task completed successfully. Found the bug in main.go:42.",
		"RESULT: YES — all checks passed. See file.go:15 for details.",
		"VERDICT: YES\n\nEvidence: src/app.go:100: the function is called correctly.",
		"Task verify-imports completed successfully. No issues found.",
		"PASS: all 5 checks succeeded. Confirmed at pkg/handler.go:200.",
	}
	for _, input := range tests {
		v, ev := ParseVerdict(input)
		if v != VerdictYes {
			t.Errorf("expected YES, got %s for: %s", v, input)
		}
		// Evidence extraction is best-effort; at minimum we should not panic.
		_ = ev
	}
}

func TestParseVerdict_No(t *testing.T) {
	tests := []string{
		"❌ Task failed: old_string not found in editfile.go:30.",
		"RESULT: NO — the import is missing. Check config.go:15.",
		"VERDICT: NO\n\nError: file not found at path/to/missing.go:1",
		"FAIL: task check-deps failed. unable to resolve dependency.",
		"not found: the symbol does not exist in the codebase.",
	}
	for _, input := range tests {
		v, ev := ParseVerdict(input)
		if v != VerdictNo {
			t.Errorf("expected NO, got %s for: %s", v, input)
		}
		_ = ev
	}
}

func TestParseVerdict_Uncertain(t *testing.T) {
	tests := []string{
		"",
		"Processing... still working on it.",
		"The analysis is ambiguous. More investigation needed.",
	}
	for _, input := range tests {
		v, _ := ParseVerdict(input)
		if v != VerdictUncertain {
			t.Errorf("expected UNCERTAIN, got %s for: %q", v, input)
		}
	}
}

func TestParseVerdict_EvidenceExtraction(t *testing.T) {
	input := "✅ Found at internal/agent/reasoner.go:150 and also cmd/ok/main.go:42."
	_, evidence := ParseVerdict(input)
	if len(evidence) != 2 {
		t.Errorf("expected 2 evidence items, got %d", len(evidence))
	}
	if evidence[0].File != "internal/agent/reasoner.go" || evidence[0].Line != 150 {
		t.Errorf("bad first evidence: %s:%d", evidence[0].File, evidence[0].Line)
	}
	if evidence[1].File != "cmd/ok/main.go" || evidence[1].Line != 42 {
		t.Errorf("bad second evidence: %s:%d", evidence[1].File, evidence[1].Line)
	}
}

func TestParseVerdict_DeduplicatesEvidence(t *testing.T) {
	input := "Error at main.go:10: something wrong\nAlso main.go:10: same line\nAnd main.go:10 again"
	_, evidence := ParseVerdict(input)
	if len(evidence) != 1 {
		t.Errorf("expected 1 deduplicated evidence, got %d", len(evidence))
	}
}
