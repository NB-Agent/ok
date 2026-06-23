package main

import (
	"testing"

	"github.com/NB-Agent/ok/internal/plugin"
)

func TestInfo(t *testing.T) {
	var s server
	name, ver := s.Info()
	if name != "ok-voice" { t.Errorf("name = %q, want ok-voice", name) }
	if ver != "1.0.0" { t.Errorf("version = %q, want 1.0.0", ver) }
}

func TestTools(t *testing.T) {
	var s server
	tools := s.Tools()
	if len(tools) == 0 { t.Fatal("expected at least one tool") }
	for _, tool := range tools {
		if tool.Name == "" { t.Error("tool with empty name") }
	}
}

func TestCallUnknownTool(t *testing.T) {
	var s server
	_, err := s.Call(nil, "nonexistent", nil)
	if err == nil { t.Fatal("expected error") }
}

func TestCallVoice(t *testing.T) {
	var s server
	args := plugin.MustJSON(map[string]string{"action": "speak", "text": "hello"})
	_, err := s.Call(nil, "voice", args)
	// speak may fail if no speech engine, but should not panic
	if err != nil { t.Logf("speak failed (expected if no engine): %v", err) }
}
