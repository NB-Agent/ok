package main

import (
	"encoding/json"
	"testing"
)

func TestHandle_Initialize(t *testing.T) {
	s := &mcpServer{name: "ok-utils", version: "1.0.0"}
	resp := s.handle(jsonRPC{ID: intPtr(1), Method: "initialize"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	if resp.ID == nil || *resp.ID != 1 {
		t.Fatal("ID not preserved")
	}
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.ProtocolVersion != "2024-11-05" {
		t.Errorf("protocol version = %q, want 2024-11-05", result.ProtocolVersion)
	}
	if result.ServerInfo.Name != "ok-utils" || result.ServerInfo.Version != "1.0.0" {
		t.Errorf("server info = %+v", result.ServerInfo)
	}
}

func TestHandle_ToolsList(t *testing.T) {
	s := &mcpServer{name: "ok-utils", version: "1.0.0"}
	resp := s.handle(jsonRPC{ID: intPtr(1), Method: "tools/list"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	var result struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	expected := []string{"schedule", "undo", "plan", "todo_write", "complete_step",
		"auto_heal", "self_scan", "capabilities", "covenant", "style_check", "go_profile", "vuln_check"}
	if len(result.Tools) != len(expected) {
		t.Fatalf("got %d tools, want %d", len(result.Tools), len(expected))
	}
	for i, exp := range expected {
		if result.Tools[i]["name"] != exp {
			t.Errorf("tool[%d].name = %q, want %q", i, result.Tools[i]["name"], exp)
		}
	}
}

func TestExecute_Plan(t *testing.T) {
	s := &mcpServer{name: "ok-utils", version: "1.0.0"}
	args := mustJSON(map[string]any{"goal": "build a webserver", "steps": "1. init 2. routes 3. test"})
	resp := s.handle(jsonRPC{ID: intPtr(1), Method: "tools/call", Params: mustJSON(map[string]any{
		"name": "plan", "arguments": args,
	})})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp.Result, &r); err != nil {
		t.Fatalf("unmarshal content: %v", err)
	}
	if len(r.Content) != 1 || r.Content[0].Type != "text" {
		t.Fatalf("unexpected content: %+v", r.Content)
	}
	if !contains(r.Content[0].Text, "build a webserver") {
		t.Errorf("response missing goal: %q", r.Content[0].Text)
	}
}

func TestExecute_PlanNoGoal(t *testing.T) {
	s := &mcpServer{name: "ok-utils", version: "1.0.0"}
	resp := s.handle(jsonRPC{ID: intPtr(1), Method: "tools/call", Params: mustJSON(map[string]any{
		"name": "plan", "arguments": mustJSON(map[string]any{}),
	})})
	if resp.Error == nil {
		t.Fatal("expected error for empty goal")
	}
	if resp.Error.Code != -32000 {
		t.Errorf("error code = %d, want -32000", resp.Error.Code)
	}
}

func TestExecute_Undo(t *testing.T) {
	s := &mcpServer{name: "ok-utils", version: "1.0.0"}
	resp := s.handle(jsonRPC{ID: intPtr(1), Method: "tools/call", Params: mustJSON(map[string]any{
		"name": "undo", "arguments": mustJSON(map[string]any{"n": 2}),
	})})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	text := extractText(t, resp.Result)
	if !contains(text, "Undid 2 step(s)") {
		t.Errorf("unexpected text: %q", text)
	}
}

func TestExecute_TodoWrite(t *testing.T) {
	s := &mcpServer{name: "ok-utils", version: "1.0.0"}
	resp := s.handle(jsonRPC{ID: intPtr(1), Method: "tools/call", Params: mustJSON(map[string]any{
		"name": "todo_write", "arguments": mustJSON(map[string]any{
			"todos": []map[string]any{{"id": "1", "name": "test"}},
		}),
	})})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	text := extractText(t, resp.Result)
	if !contains(text, "Todos updated") {
		t.Errorf("unexpected text: %q", text)
	}
}

func TestExecute_CompleteStep(t *testing.T) {
	s := &mcpServer{name: "ok-utils", version: "1.0.0"}
	resp := s.handle(jsonRPC{ID: intPtr(1), Method: "tools/call", Params: mustJSON(map[string]any{
		"name": "complete_step", "arguments": mustJSON(map[string]any{"step": "test", "result": "ok"}),
	})})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	text := extractText(t, resp.Result)
	if !contains(text, "test") || !contains(text, "ok") {
		t.Errorf("unexpected text: %q", text)
	}
}

func TestExecute_Capabilities(t *testing.T) {
	s := &mcpServer{name: "ok-utils", version: "1.0.0"}
	resp := s.handle(jsonRPC{ID: intPtr(1), Method: "tools/call", Params: mustJSON(map[string]any{
		"name": "capabilities", "arguments": mustJSON(map[string]any{}),
	})})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	text := extractText(t, resp.Result)
	if !contains(text, "schedule") || !contains(text, "undo") {
		t.Errorf("capabilities missing tools: %q", text)
	}
}

func TestExecute_Covenant(t *testing.T) {
	s := &mcpServer{name: "ok-utils", version: "1.0.0"}
	resp := s.handle(jsonRPC{ID: intPtr(1), Method: "tools/call", Params: mustJSON(map[string]any{
		"name": "covenant", "arguments": mustJSON(map[string]any{}),
	})})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	text := extractText(t, resp.Result)
	if !contains(text, "Immutable Core") {
		t.Errorf("covenant missing core message: %q", text)
	}
}

func TestExecute_UnknownTool(t *testing.T) {
	s := &mcpServer{name: "ok-utils", version: "1.0.0"}
	resp := s.handle(jsonRPC{ID: intPtr(1), Method: "tools/call", Params: mustJSON(map[string]any{
		"name": "nonexistent", "arguments": mustJSON(map[string]any{}),
	})})
	if resp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestHandle_UnknownMethod(t *testing.T) {
	s := &mcpServer{name: "ok-utils", version: "1.0.0"}
	resp := s.handle(jsonRPC{ID: intPtr(1), Method: "unknown"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
}

// ─── helpers ───

func intPtr(n int) *int { return &n }

func extractText(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("extractText unmarshal: %v", err)
	}
	if len(r.Content) == 0 {
		return ""
	}
	return r.Content[0].Text
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
