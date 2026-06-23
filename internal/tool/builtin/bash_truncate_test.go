package builtin

import (
	"strings"
	"testing"
)

func TestTruncateBashShort(t *testing.T) {
	in := "hello\nworld"
	got := truncateBash(in)
	if got != in {
		t.Errorf("short output should be unchanged, got %q", got)
	}
}

func TestTruncateBashExact(t *testing.T) {
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = "line"
	}
	in := strings.Join(lines, "\n")
	got := truncateBash(in)
	if got != in {
		t.Errorf("exactly 60 lines should be unchanged, got %d", len(strings.Split(got, "\n")))
	}
}

func TestTruncateBashLong(t *testing.T) {
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = "line"
	}
	in := strings.Join(lines, "\n")
	got := truncateBash(in)

	if !strings.Contains(got, "truncated") {
		t.Errorf("long output should contain truncation notice")
	}
	if !strings.Contains(got, "line") {
		t.Errorf("truncated output should contain content")
	}

	// Head should be first 20 lines
	gotLines := strings.Split(got, "\n")
	if !strings.HasPrefix(got, "line") {
		t.Errorf("should start with content, got %q", gotLines[0])
	}
}

func TestTruncateBashPreservesTail(t *testing.T) {
	// Build output where the last 5 lines are failures
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("ok  pkg\n")
	}
	b.WriteString("--- FAIL: TestFoo (0.01s)\n")
	b.WriteString("    foo_test.go:42: expected X, got Y\n")
	b.WriteString("FAIL\n")
	b.WriteString("FAIL\tpkg/foo\t0.123s\n")
	b.WriteString("FAIL\n")

	got := truncateBash(b.String())
	if !strings.Contains(got, "--- FAIL: TestFoo") {
		t.Errorf("should preserve tail failures, got:\n%s", got)
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("should show truncation notice")
	}
	if !strings.Contains(got, "ok  pkg") {
		t.Errorf("should preserve head")
	}
}

func TestTruncateBashTrailingNewline(t *testing.T) {
	// 200 lines, last one empty (trailing \n)
	lines := make([]string, 201)
	for i := 0; i < 200; i++ {
		lines[i] = "line"
	}
	lines[200] = ""
	in := strings.Join(lines, "\n")
	got := truncateBash(in)

	if !strings.Contains(got, "truncated") {
		t.Errorf("should truncate despite trailing newline, got %d lines", len(strings.Split(got, "\n")))
	}
}
