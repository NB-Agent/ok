package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemPromptMatchesFile(t *testing.T) {
	// The default system prompt in config.go and prompts/system.md must
	// both contain the tool-groups section. This test catches drift.
	if !strings.Contains(DefaultSystemPrompt, "Tool groups") {
		t.Error("DefaultSystemPrompt missing Tool groups section")
	}
	if !strings.Contains(DefaultSystemPrompt, "tool-groups") {
		t.Error("DefaultSystemPrompt missing tool-groups reference")
	}
	if !strings.Contains(DefaultSystemPrompt, "Base instructions end") {
		t.Error("DefaultSystemPrompt missing end-of-instructions marker")
	}
}

func TestSystemPromptFileExists(t *testing.T) {
	// Verify the system.md file exists and has content.
	candidates := []string{
		"prompts/system.md",
		"../prompts/system.md",
		filepath.Join("..", "..", "prompts", "system.md"),
	}
	var found string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			found = c
			break
		}
	}
	if found == "" {
		t.Skip("system.md not found from test working directory")
	}
	data, err := os.ReadFile(found)
	if err != nil {
		t.Fatalf("read system.md: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Tool groups") || !strings.Contains(content, "Base instructions end") {
		t.Error("prompts/system.md missing critical sections")
	}
}
