package main

import (
	"testing"

	"github.com/NB-Agent/ok/internal/plugin"
)

func TestInfo(t *testing.T) {
	var s server
	name, ver := s.Info()
	if name != "ok-workflow" {
		t.Errorf("name = %q, want ok-workflow", name)
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

func TestCallDefine(t *testing.T) {
	var s server
	args := plugin.MustJSON(map[string]string{
		"action": "define",
		"name":   "test-wf",
		"steps":  "step1,step2",
	})
	res, err := s.Call(nil, "workflow", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(res, "test-wf") {
		t.Errorf("response missing workflow name: %q", res)
	}
}

func TestCallRun(t *testing.T) {
	var s server
	args := plugin.MustJSON(map[string]string{
		"action": "run",
		"name":   "test-wf",
	})
	res, err := s.Call(nil, "workflow", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(res, "Running workflow: test-wf") {
		t.Errorf("unexpected response: %q", res)
	}
}

func TestCallStatus(t *testing.T) {
	var s server
	args := plugin.MustJSON(map[string]string{
		"action": "status",
		"name":   "test-wf",
	})
	res, err := s.Call(nil, "workflow", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(res, "idle") {
		t.Errorf("response missing status: %q", res)
	}
}

func TestCallList(t *testing.T) {
	var s server
	args := plugin.MustJSON(map[string]string{"action": "list"})
	res, err := s.Call(nil, "workflow", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(res, "Defined workflows") {
		t.Errorf("unexpected response: %q", res)
	}
}

func TestCallUnknownTool(t *testing.T) {
	var s server
	_, err := s.Call(nil, "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestCallUnknownAction(t *testing.T) {
	var s server
	args := plugin.MustJSON(map[string]string{"action": "invalid"})
	_, err := s.Call(nil, "workflow", args)
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
