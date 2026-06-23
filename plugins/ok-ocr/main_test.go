package main

import (
	"testing"

	"github.com/NB-Agent/ok/internal/plugin"
)

func TestInfo(t *testing.T) {
	var s server
	name, ver := s.Info()
	if name != "ok-ocr" {
		t.Errorf("name = %q, want ok-ocr", name)
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
	if tools[0].Name != "ocr" {
		t.Errorf("tool[0].name = %q, want ocr", tools[0].Name)
	}
}

func TestCallUnknownTool(t *testing.T) {
	var s server
	_, err := s.Call(nil, "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestCallMissingPath(t *testing.T) {
	var s server
	_, err := s.Call(nil, "ocr", plugin.MustJSON(map[string]any{}))
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestCallDispatch(t *testing.T) {
	var s server
	args := plugin.MustJSON(map[string]string{"path": "nonexistent.png"})
	result, err := s.Call(nil, "ocr", args)
	if err != nil {
		t.Logf("ocr call failed (expected without tesseract/API key): %v", err)
	}
	_ = result
}
