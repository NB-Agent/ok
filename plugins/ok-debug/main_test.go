package main

import (
	"testing"

	"github.com/NB-Agent/ok/internal/plugin"
)

func TestInfo(t *testing.T) {
	var s server
	name, ver := s.Info()
	if name != "ok-debug" {
		t.Errorf("name = %q, want ok-debug", name)
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

func TestCallDispatch(t *testing.T) {
	var s server
	args := plugin.MustJSON(map[string]string{"action": "continue"})
	_, err := s.Call(nil, "debug", args)
	if err != nil {
		t.Logf("dlv call failed (expected if dlv not installed): %v", err)
	}
}
