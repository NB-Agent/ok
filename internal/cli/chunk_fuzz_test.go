package cli

import (
	"strings"
	"testing"
)

func FuzzChunkLines(f *testing.F) {
	f.Add("a\nb\nc\nd\ne", 2)
	f.Add("single", 1)
	f.Add("", 5)
	f.Add(strings.Repeat("x\n", 100), 10)

	f.Fuzz(func(t *testing.T, s string, n int) {
		if n <= 0 {
			return
		}
		result := chunkLines(s, n)
		// Reassembled result must equal original.
		joined := strings.Join(result, "\n")
		if joined != s {
			t.Errorf("chunkLines round-trip mismatch: input=%q n=%d got=%q joined=%q", s, n, result, joined)
		}
	})
}
