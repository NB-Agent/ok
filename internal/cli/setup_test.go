package cli

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAsk(t *testing.T) {
	t.Run("returns input", func(t *testing.T) {
		in := bufio.NewScanner(strings.NewReader("hello\n"))
		var buf bytes.Buffer
		got := ask(in, &buf, "Prompt", "")
		if got != "hello" {
			t.Errorf("got %q, want hello", got)
		}
		if !strings.Contains(buf.String(), "Prompt") {
			t.Errorf("output doesn't contain 'Prompt': %q", buf.String())
		}
	})

	t.Run("returns default on empty", func(t *testing.T) {
		in := bufio.NewScanner(strings.NewReader("\n"))
		var buf bytes.Buffer
		got := ask(in, &buf, "Prompt", "default")
		if got != "default" {
			t.Errorf("got %q, want default", got)
		}
		if !strings.Contains(buf.String(), "[default]") {
			t.Error("should show default in prompt")
		}
	})

	t.Run("returns default on EOF", func(t *testing.T) {
		in := bufio.NewScanner(strings.NewReader(""))
		var buf bytes.Buffer
		got := ask(in, &buf, "Prompt", "fallback")
		if got != "fallback" {
			t.Errorf("got %q, want fallback", got)
		}
	})

	t.Run("trims whitespace", func(t *testing.T) {
		in := bufio.NewScanner(strings.NewReader("  spaced  \n"))
		var buf bytes.Buffer
		got := ask(in, &buf, "Prompt", "")
		if got != "spaced" {
			t.Errorf("got %q, want spaced", got)
		}
	})
}

func TestFamilyOf(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"deepseek-flash", "deepseek"},
		{"deepseek-pro", "deepseek"},
		{"mimo-pro", "mimo"},
		{"mimo-flash", "mimo"},
		{"custom-provider", "custom-provider"},
	}
	for _, tt := range tests {
		f := familyOf(tt.name)
		if f.key != tt.key {
			t.Errorf("familyOf(%q).key = %q, want %q", tt.name, f.key, tt.key)
		}
	}
}

func TestAppendEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	// New key.
	if err := appendEnv(path, []string{"NEW_KEY=val"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "NEW_KEY=val") {
		t.Errorf("should contain NEW_KEY: %q", b)
	}

	// Replace existing.
	if err := appendEnv(path, []string{"NEW_KEY=updated"}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	content := string(b)
	if strings.Count(content, "NEW_KEY=") != 1 {
		t.Errorf("should have exactly one NEW_KEY line: %q", content)
	}
	if !strings.Contains(content, "NEW_KEY=updated") {
		t.Errorf("should contain updated value: %q", content)
	}

	// Add a second key alongside the first.
	if err := appendEnv(path, []string{"OTHER_KEY=other"}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if !strings.Contains(string(b), "NEW_KEY=updated") || !strings.Contains(string(b), "OTHER_KEY=other") {
		t.Errorf("both keys should exist: %q", b)
	}
}

func TestAppendEnvExportPrefix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	// Write an export-style line.
	os.WriteFile(path, []byte("export FOO=old\nBAR=keep\n"), 0644)
	if err := appendEnv(path, []string{"FOO=new"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	content := string(b)
	if strings.Contains(content, "FOO=old") {
		t.Errorf("export FOO=old should be replaced: %q", content)
	}
	if !strings.Contains(content, "FOO=new") {
		t.Errorf("should contain FOO=new: %q", content)
	}
	if !strings.Contains(content, "BAR=keep") {
		t.Errorf("BAR=keep should be preserved: %q", content)
	}
}
