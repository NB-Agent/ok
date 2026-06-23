package main

import (
	"testing"

	"github.com/NB-Agent/ok/internal/plugin"
)

func TestInfo(t *testing.T) {
	var s server
	n, v := s.Info()
	if n != "ok-git" {
		t.Errorf("name=%q", n)
	}
	if v != "1.0.0" {
		t.Errorf("ver=%q", v)
	}
}

func TestTools(t *testing.T) {
	var s server
	tools := s.Tools()
	exp := []string{"git_status", "git_diff", "git_log", "git_commit", "git_branch"}
	if len(tools) != len(exp) {
		t.Fatalf("%d tools, want %d", len(tools), len(exp))
	}
	for i, e := range exp {
		if tools[i].Name != e {
			t.Errorf("tool[%d]=%q want %q", i, tools[i].Name, e)
		}
	}
}

func TestCallUnknownTool(t *testing.T) {
	var s server
	_, err := s.Call(nil, "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCommitNoMsg(t *testing.T) {
	var s server
	_, err := s.Call(nil, "git_commit", plugin.MustJSON(map[string]any{}))
	if err == nil {
		t.Fatal("expected error")
	}
}
