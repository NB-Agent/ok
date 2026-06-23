package main

import (
	"testing"

	"github.com/NB-Agent/ok/internal/plugin"
)

func TestInfo(t *testing.T) {
	var s server
	n, v := s.Info()
	if n != "ok-utils" { t.Errorf("name=%q want ok-utils", n) }
	if v != "1.0.0" { t.Errorf("version=%q want 1.0.0", v) }
}

func TestTools(t *testing.T) {
	var s server
	tools := s.Tools()
	exp := []string{"schedule","undo","plan","todo_write","complete_step","auto_heal","self_scan","capabilities","covenant","style_check","go_profile","vuln_check"}
	if len(tools) != len(exp) { t.Fatalf("%d tools, want %d", len(tools), len(exp)) }
	for i, e := range exp {
		if tools[i].Name != e { t.Errorf("tool[%d]=%q want %q", i, tools[i].Name, e) }
	}
}

func TestCallPlan(t *testing.T) {
	var s server
	r, err := s.Call(nil, "plan", plugin.MustJSON(map[string]any{"goal":"build a webserver","steps":"1. init 2. routes 3. test"}))
	if err != nil { t.Fatalf("unexpected: %v", err) }
	if !contains(r, "build a webserver") { t.Errorf("missing goal: %s", r) }
}

func TestCallPlanNoGoal(t *testing.T) {
	var s server
	_, err := s.Call(nil, "plan", plugin.MustJSON(map[string]any{}))
	if err == nil { t.Fatal("expected error") }
}

func TestCallUndo(t *testing.T) {
	var s server
	r, _ := s.Call(nil, "undo", plugin.MustJSON(map[string]any{"n":2}))
	if !contains(r, "Undid 2") { t.Errorf("unexpected: %s", r) }
}

func TestCallTodoWrite(t *testing.T) {
	var s server
	r, _ := s.Call(nil, "todo_write", plugin.MustJSON(map[string]any{"todos":[]map[string]any{{"content":"test","status":"pending"}}}))
	if !contains(r, "Todos updated") { t.Errorf("unexpected: %s", r) }
}

func TestCallCompleteStep(t *testing.T) {
	var s server
	r, _ := s.Call(nil, "complete_step", plugin.MustJSON(map[string]any{"step":"test","result":"ok"}))
	if !contains(r, "test") || !contains(r, "ok") { t.Errorf("unexpected: %s", r) }
}

func TestCallCapabilities(t *testing.T) {
	var s server
	r, _ := s.Call(nil, "capabilities", plugin.MustJSON(map[string]any{}))
	if !contains(r, "schedule") { t.Errorf("missing schedule: %s", r) }
}

func TestCallUnknownTool(t *testing.T) {
	var s server
	_, err := s.Call(nil, "nonexistent", nil)
	if err == nil { t.Fatal("expected error") }
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub { return true }
	}
	return false
}
