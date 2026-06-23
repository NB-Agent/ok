package builtin

import (
	"strings"
	"testing"
)

func TestChromeExtractTextEmpty(t *testing.T) {
	got := chromeExtractText("")
	if got != "" {
		t.Errorf("chromeExtractText('') = %q, want ''", got)
	}
}

func TestChromeExtractTextStripsTags(t *testing.T) {
	got := chromeExtractText("<html><body><p>Hello world</p></body></html>")
	got = strings.TrimSpace(got)
	if got != "Hello world" {
		t.Errorf("chromeExtractText = %q, want 'Hello world'", got)
	}
}

func TestChromeExtractTextTruncates(t *testing.T) {
	long := "<p>" + string(make([]byte, 12000)) + "</p>"
	got := chromeExtractText(long)
	if len(got) > 10100 {
		t.Errorf("chromeExtractText should truncate to ~10000 chars, got %d", len(got))
	}
}

func TestChromeFindNonExistent(t *testing.T) {
	got := chromeFind()
	if got != "" {
		t.Skip("Chrome found at", got)
	}
}

func TestFirstKB(t *testing.T) {
	tests := []struct {
		input []byte
		want  int // expected output length
	}{
		{[]byte("hello"), 5},
		{[]byte("hello world"), 11},
		{make([]byte, 2048), 1024 + 3}, // truncated + "..."
	}
	for _, tt := range tests {
		got := firstKB(tt.input)
		if len(got) != tt.want {
			t.Errorf("firstKB(%d bytes) length = %d, want %d", len(tt.input), len(got), tt.want)
		}
	}
}
