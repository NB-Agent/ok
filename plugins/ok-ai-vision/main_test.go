package main

import (
	"testing"

	"github.com/NB-Agent/ok/internal/plugin"
)

func TestInfo(t *testing.T) {
	var s server
	name, ver := s.Info()
	if name != "ok-ai-vision" {
		t.Errorf("name = %q, want ok-ai-vision", name)
	}
	if ver != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0", ver)
	}
}

func TestTools(t *testing.T) {
	var s server
	tools := s.Tools()
	if len(tools) == 0 {
		t.Fatal("expected at least one tool")
	}
	for _, tool := range tools {
		if tool.Name == "" {
			t.Error("tool with empty name found")
		}
	}
}

func TestCallUnknownTool(t *testing.T) {
	var s server
	_, err := s.Call(nil, "nonexistent", plugin.MustJSON(map[string]any{}))
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}
