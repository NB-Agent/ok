package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NB-Agent/ok/internal/tool"
)

// mockTool implements tool.Tool for testing.
type mockTool struct {
	name        string
	description string
	schema      json.RawMessage
	readOnly    bool
}

func (m mockTool) Name() string            { return m.name }
func (m mockTool) Description() string     { return m.description }
func (m mockTool) Schema() json.RawMessage { return m.schema }
func (m mockTool) ReadOnly() bool          { return m.readOnly }
func (m mockTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	return "mock result for " + m.name, nil
}

func TestRegistryAdapter_ListTools(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(mockTool{name: "echo", description: "echoes input", schema: json.RawMessage(`{"type":"object"}`), readOnly: true})
	reg.Add(mockTool{name: "bash", description: "run shell", schema: json.RawMessage(`{"type":"object"}`), readOnly: false})

	adapter := NewRegistryAdapter(reg)
	tools := adapter.ListTools()
	if len(tools) != 2 {
		t.Fatalf("ListTools() returned %d tools, want 2", len(tools))
	}
	names := map[string]bool{}
	for _, ti := range tools {
		names[ti.Name] = true
		if ti.Name == "" {
			t.Error("tool has empty name")
		}
		if ti.Description == "" {
			t.Errorf("tool %q has empty description", ti.Name)
		}
		if len(ti.InputSchema) == 0 {
			t.Errorf("tool %q has empty schema", ti.Name)
		}
	}
	if !names["echo"] {
		t.Error("missing 'echo' tool")
	}
	if !names["bash"] {
		t.Error("missing 'bash' tool")
	}
}

func TestRegistryAdapter_CallTool(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(mockTool{name: "echo", description: "echoes", schema: json.RawMessage(`{}`), readOnly: true})

	adapter := NewRegistryAdapter(reg)
	result, readOnly, err := adapter.CallTool(context.Background(), "echo", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CallTool() unexpected error: %v", err)
	}
	if result == "" {
		t.Error("CallTool() returned empty result")
	}
	if !readOnly {
		t.Error("CallTool() readOnly = false for a read-only tool")
	}
	if !strings.Contains(result, "mock result") {
		t.Errorf("CallTool() result = %q, want 'mock result' substring", result)
	}
}

func TestRegistryAdapter_CallTool_Unknown(t *testing.T) {
	reg := tool.NewRegistry()
	adapter := NewRegistryAdapter(reg)
	_, _, err := adapter.CallTool(context.Background(), "nonexistent", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("CallTool() expected error for unknown tool, got nil")
	}
}

func TestNewRegistryAdapter(t *testing.T) {
	reg := tool.NewRegistry()
	adapter := NewRegistryAdapter(reg)
	if adapter == nil {
		t.Fatal("NewRegistryAdapter() returned nil")
	}
	tools := adapter.ListTools()
	if len(tools) != 0 {
		t.Fatalf("empty registry ListTools() = %d tools, want 0", len(tools))
	}
}

func TestServer_New(t *testing.T) {
	reg := tool.NewRegistry()
	adapter := NewRegistryAdapter(reg)
	s := New(strings.NewReader(""), &strings.Builder{}, adapter)
	if s == nil {
		t.Fatal("New() returned nil")
	}
}

func TestServer_Run_EmptyInput(t *testing.T) {
	reg := tool.NewRegistry()
	adapter := NewRegistryAdapter(reg)
	var buf strings.Builder
	s := New(strings.NewReader(""), &buf, adapter)
	ctx := context.Background()
	if err := s.Run(ctx); err != nil {
		t.Fatalf("Run() on empty input returned error: %v", err)
	}
}
