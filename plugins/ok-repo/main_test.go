package main

import (
	"testing"

	"github.com/NB-Agent/ok/internal/plugin"
)

func TestInfo(t *testing.T) {
	var s server
	name, ver := s.Info()
	if name != "ok-repo" {
		t.Errorf("name = %q, want ok-repo", name)
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
	_, err := s.Call(nil, "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestCallList(t *testing.T) {
	var s server
	args := plugin.MustJSON(map[string]string{"action": "list"})
	res, err := s.Call(nil, "repo", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == "" {
		t.Error("expected non-empty response")
	}
}

func TestCallAdd(t *testing.T) {
	var s server
	args := plugin.MustJSON(map[string]string{
		"action": "add",
		"name":   "test-repo",
		"path":   t.TempDir(),
	})
	res, err := s.Call(nil, "repo", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(res, "test-repo") {
		t.Errorf("response missing repo name: %q", res)
	}
}

func TestCallUnknownAction(t *testing.T) {
	var s server
	args := plugin.MustJSON(map[string]string{"action": "invalid"})
	_, err := s.Call(nil, "repo", args)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
