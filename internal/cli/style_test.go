package cli

import (
	"strings"
	"testing"
)

func TestSgrColorEnabled(t *testing.T) {
	orig := colorEnabled
	defer func() { colorEnabled = orig }()

	colorEnabled = true
	if got := sgr(ansiBold, "hello"); got != ansiBold+"hello"+ansiReset {
		t.Errorf("sgr with color=true = %q, want ANSI-wrapped", got)
	}
	if got := sgr(ansiAccent, "foo"); !strings.HasPrefix(got, ansiAccent) || !strings.HasSuffix(got, ansiReset) {
		t.Errorf("accent wrapping wrong: %q", got)
	}
}

func TestSgrColorDisabled(t *testing.T) {
	orig := colorEnabled
	defer func() { colorEnabled = orig }()

	colorEnabled = false
	if got := sgr(ansiBold, "hello"); got != "hello" {
		t.Errorf("sgr with color=false = %q, want plain text", got)
	}
}

func TestBold(t *testing.T) {
	orig := colorEnabled
	defer func() { colorEnabled = orig }()
	colorEnabled = true
	if got := bold("x"); got != ansiBold+"x"+ansiReset {
		t.Errorf("bold = %q", got)
	}
	colorEnabled = false
	if got := bold("x"); got != "x" {
		t.Errorf("bold (no color) = %q", got)
	}
}

func TestDim(t *testing.T) {
	orig := colorEnabled
	defer func() { colorEnabled = orig }()
	colorEnabled = true
	if got := dim("x"); got != ansiDim+"x"+ansiReset {
		t.Errorf("dim = %q", got)
	}
}

func TestAccent(t *testing.T) {
	orig := colorEnabled
	defer func() { colorEnabled = orig }()
	colorEnabled = true
	if got := accent("x"); got != ansiAccent+"x"+ansiReset {
		t.Errorf("accent = %q", got)
	}
}

func TestDetectColor(t *testing.T) {
	t.Run("NO_COLOR disables", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		t.Setenv("TERM", "xterm-256color")
		if detectColor() {
			t.Error("NO_COLOR should disable color")
		}
	})
	t.Run("dumb TERM disables", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		t.Setenv("TERM", "dumb")
		if detectColor() {
			t.Error("dumb TERM should disable color")
		}
	})
	t.Run("empty permits", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		t.Setenv("TERM", "")
		got := detectColor()
		t.Logf("detectColor = %v (false is expected in test env)", got)
	})
}
