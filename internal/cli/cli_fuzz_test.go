package cli

import (
	"testing"
)

func FuzzCompactArgs(f *testing.F) {
	f.Add(`{"path":"x.go"}`)
	f.Add("")
	f.Add(string(make([]byte, 1000)))

	f.Fuzz(func(t *testing.T, s string) {
		// Must never panic or exceed boundaries.
		result := compactArgs(s)
		// Result must never be longer than 120 + 3 = 123 chars.
		if len([]rune(result)) > 123 {
			t.Errorf("compactArgs produced %d-runes result for %d-runes input", len([]rune(result)), len([]rune(s)))
		}
		_ = result
	})
}
