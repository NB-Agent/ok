package builtin

import (
	"strings"
	"testing"
)

func TestPrecheckGoFile_NonGoFile(t *testing.T) {
	err := precheckGoFile("readme.md", "# hello", "")
	if err != nil {
		t.Errorf("expected nil for non-.go file, got %v", err)
	}
}

func TestPrecheckGoFile_SyntaxError(t *testing.T) {
	err := precheckGoFile("test.go", `package main
func main() {`, "")
	if err == nil {
		t.Fatal("expected syntax error")
	}
	if !strings.Contains(err.Error(), "syntax error") {
		t.Errorf("expected syntax error message, got %v", err)
	}
}

func TestPrecheckGoFile_ValidSyntax(t *testing.T) {
	err := precheckGoFile("test.go", "package main\nfunc main() {}", "")
	if err != nil {
		t.Errorf("expected pass for valid Go, got %v", err)
	}
}

func TestPrecheckGoFile_ValidPackage(t *testing.T) {
	err := precheckGoFile("test.go", "package main", "")
	if err != nil {
		t.Errorf("expected pass for package decl, got %v", err)
	}
}
