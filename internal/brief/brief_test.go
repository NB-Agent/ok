package brief

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenerate(t *testing.T) {
	dir := t.TempDir()

	// Create a minimal Go project structure
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n\ngo 1.21\n\nrequire (\n\tgithub.com/foo/bar v1.0.0\n\tgithub.com/baz/qux v2.0.0\n)\n"), 0644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0644)
	os.WriteFile(filepath.Join(dir, "Makefile"), []byte("build:\n\tgo build\n\ntest:\n\tgo test\n"), 0644)
	os.MkdirAll(filepath.Join(dir, "internal", "foo"), 0755)
	os.WriteFile(filepath.Join(dir, "internal", "foo", "foo.go"), []byte("package foo\n"), 0644)
	os.MkdirAll(filepath.Join(dir, "cmd", "app"), 0755)
	os.WriteFile(filepath.Join(dir, "cmd", "app", "main.go"), []byte("package main\n"), 0644)

	if err := Generate(dir); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("read brief: %v", err)
	}

	content := string(data)
	checks := []string{
		"# Project Brief",
		"**Project**: example.com/test",
		"**Language**: Go",
		"**Build**: go mod, make",
		"`internal/`",
		"`cmd/`",
		"`README.md`",
		"`go.mod`",
		"`Makefile`",
		"`main.go`",
		"github.com/foo/bar",
		"github.com/baz/qux",
	}

	for _, c := range checks {
		if !contains(content, c) {
			t.Errorf("brief missing %q\n\nFull content:\n%s", c, content)
		}
	}

	// Verify it's reasonably small (<2000 bytes ≈ ~500 tokens)
	if len(content) > 2000 {
		t.Errorf("brief too large: %d bytes", len(content))
	}
	t.Logf("Brief (%d bytes):\n%s", len(content), content)
}

func TestEmptyProject(t *testing.T) {
	dir := t.TempDir()
	// Just an empty directory
	if err := Generate(dir); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Should still produce a valid brief
	_, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("read brief: %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
