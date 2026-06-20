package main

import (
	"context"
	"encoding/json"
	"testing"
)

func intPtr(n int) *int { return &n }

func TestGit_Initialize(t *testing.T) {
	s := &mcpServer{name: "ok-git", version: "1.0.0"}
	resp := s.handle(context.Background(), jsonRPC{ID: intPtr(1), Method: "initialize"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	if resp.ID == nil || *resp.ID != 1 {
		t.Fatal("ID not preserved")
	}
}

func TestGit_ToolsList(t *testing.T) {
	s := &mcpServer{name: "ok-git", version: "1.0.0"}
	resp := s.handle(context.Background(), jsonRPC{ID: intPtr(1), Method: "tools/list"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	var result struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	expected := []string{"git_status", "git_diff", "git_log", "git_commit", "git_branch"}
	if len(result.Tools) != len(expected) {
		t.Fatalf("got %d tools, want %d", len(result.Tools), len(expected))
	}
	for i, exp := range expected {
		if result.Tools[i]["name"] != exp {
			t.Errorf("tool[%d].name = %q, want %q", i, result.Tools[i]["name"], exp)
		}
	}
}

func TestGit_CommitNoMessage(t *testing.T) {
	s := &mcpServer{name: "ok-git", version: "1.0.0"}
	resp := s.handle(context.Background(), jsonRPC{ID: intPtr(1), Method: "tools/call", Params: mustJSON(map[string]any{
		"name": "git_commit", "arguments": mustJSON(map[string]any{}),
	})})
	if resp.Error == nil {
		t.Fatal("expected error for empty commit message")
	}
}

func TestGit_UnknownTool(t *testing.T) {
	s := &mcpServer{name: "ok-git", version: "1.0.0"}
	resp := s.handle(context.Background(), jsonRPC{ID: intPtr(1), Method: "tools/call", Params: mustJSON(map[string]any{
		"name": "nonexistent", "arguments": mustJSON(map[string]any{}),
	})})
	if resp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestGit_UnknownMethod(t *testing.T) {
	s := &mcpServer{name: "ok-git", version: "1.0.0"}
	resp := s.handle(context.Background(), jsonRPC{ID: intPtr(1), Method: "unknown"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
}
