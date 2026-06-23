package main

import (
	"testing"

	"github.com/NB-Agent/ok/internal/plugin"
)

func TestInfo(t *testing.T) {
	var s server
	name, ver := s.Info()
	if name != "ok-translate" {
		t.Errorf("name = %q, want ok-translate", name)
	}
	if ver != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0", ver)
	}
}

func TestTools(t *testing.T) {
	var s server
	tools := s.Tools()
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
	if tools[0].Name != "translate" {
		t.Errorf("tool[0].name = %q, want translate", tools[0].Name)
	}
}

func TestCallUnknownTool(t *testing.T) {
	var s server
	_, err := s.Call(nil, "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestCallMissingRequired(t *testing.T) {
	var s server
	_, err := s.Call(nil, "translate", plugin.MustJSON(map[string]any{}))
	if err == nil {
		t.Fatal("expected error for missing text/target")
	}
}

func TestCallDispatch(t *testing.T) {
	var s server
	args := plugin.MustJSON(map[string]string{"text": "Hello", "target": "zh"})
	result, err := s.Call(nil, "translate", args)
	if err != nil {
		t.Logf("translate call failed (expected without API key): %v", err)
	}
	_ = result
}
