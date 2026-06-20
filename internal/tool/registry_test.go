package tool

import (
	"context"
	"encoding/json"
	"testing"
)

// stubTool is a minimal Tool for registry tests.
type stubTool struct{ name string }

func (s stubTool) Name() string                                             { return s.name }
func (s stubTool) Description() string                                      { return s.name + " desc" }
func (s stubTool) Schema() json.RawMessage                                  { return json.RawMessage(`{"type":"object"}`) }
func (s stubTool) Execute(context.Context, json.RawMessage) (string, error) { return "", nil }
func (s stubTool) ReadOnly() bool                                           { return true }

// TestRegistryRemovePrefix proves an MCP server's namespaced tools are dropped as
// a group on disconnect, leaving built-ins and other servers' tools — and their
// insertion order — intact.
func TestRegistryRemovePrefix(t *testing.T) {
	r := NewRegistry()
	r.Add(stubTool{"bash"})
	r.Add(stubTool{"mcp__fs__read"})
	r.Add(stubTool{"mcp__fs__write"})
	r.Add(stubTool{"mcp__stripe__charge"})

	if got := r.RemovePrefix("mcp__fs__"); got != 2 {
		t.Fatalf("RemovePrefix returned %d, want 2", got)
	}
	if r.Len() != 2 {
		t.Fatalf("registry has %d tools after removal, want 2", r.Len())
	}
	if _, ok := r.Get("mcp__fs__read"); ok {
		t.Errorf("mcp__fs__read should be gone")
	}
	if _, ok := r.Get("mcp__stripe__charge"); !ok {
		t.Errorf("another server's tool should survive")
	}
	want := []string{"bash", "mcp__stripe__charge"}
	got := r.Names()
	if len(got) != len(want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names = %v, want %v (order preserved)", got, want)
		}
	}

	// Removing a prefix that matches nothing is a no-op.
	if got := r.RemovePrefix("mcp__nope__"); got != 0 {
		t.Errorf("RemovePrefix on absent prefix returned %d, want 0", got)
	}
}
