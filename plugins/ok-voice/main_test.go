package main

import (
	"encoding/json"
	"testing"
)

func TestInitialize(t *testing.T) {
	s := &mcpServer{name: "ok-voice", version: "1.0.0"}
	resp := s.handle(jsonRPC{ID: intPtr(1), Method: "initialize"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	if resp.ID == nil || *resp.ID != 1 {
		t.Fatal("ID not preserved")
	}
}

func TestToolsList(t *testing.T) {
	s := &mcpServer{name: "ok-voice", version: "1.0.0"}
	resp := s.handle(jsonRPC{ID: intPtr(1), Method: "tools/list"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	var result struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Tools) == 0 {
		t.Fatal("expected at least one tool")
	}
	for _, tool := range result.Tools {
		if tool["name"] == "" {
			t.Error("tool with empty name found")
		}
	}
}

func TestUnknownTool(t *testing.T) {
	s := &mcpServer{name: "ok-voice", version: "1.0.0"}
	resp := s.handle(jsonRPC{ID: intPtr(1), Method: "tools/call", Params: mustJSON(map[string]any{
		"name": "nonexistent", "arguments": mustJSON(map[string]any{}),
	})})
	if resp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestUnknownMethod(t *testing.T) {
	s := &mcpServer{name: "ok-voice", version: "1.0.0"}
	resp := s.handle(jsonRPC{ID: intPtr(1), Method: "unknown"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
}

func intPtr(n int) *int { return &n }
