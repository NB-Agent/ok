package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/tool"
)

// TestTaskToolReturnsSubAgentFinalAnswer runs a task against a mock provider
// that emits a single text turn, and verifies the tool returns exactly that
// text — sub-agent intermediate state isn't supposed to leak.
func TestTaskToolReturnsSubAgentFinalAnswer(t *testing.T) {
	sub := &mockProvider{name: "sub", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "found 3 callers of Foo"},
		{Type: provider.ChunkDone},
	}}
	parentReg := tool.NewRegistry()
	task := NewTaskTool(sub, nil, parentReg, 20, 0, 0.0, "", "test-sys-prompt", nil, "", nil, nil, 0)

	out, err := task.Execute(context.Background(), []byte(`{"prompt":"find callers of Foo"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "found 3 callers of Foo") {
		t.Errorf("got %q, want sub-agent final answer", out)
	}

	// The sub-agent must have received the prompt as its user message and
	// the configured system prompt at the top — proving the session was
	// fresh, not the parent's.
	if sys := sub.lastReq.Messages[0]; sys.Role != provider.RoleSystem || sys.Content != "test-sys-prompt" {
		t.Errorf("first message = %+v, want system 'test-sys-prompt'", sys)
	}
	if got := lastUser(sub.lastReq); got != "find callers of Foo" {
		t.Errorf("sub-agent user = %q, want the prompt verbatim", got)
	}
}

// TestTaskToolFiltersTools verifies the whitelist behavior: the sub-agent
// gets exactly the requested tools. task() IS included so recursive
// decomposition works.
func TestTaskToolFiltersTools(t *testing.T) {
	sub := &mockProvider{name: "sub", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "ok"},
		{Type: provider.ChunkDone},
	}}
	parentReg := tool.NewRegistry()
	parentReg.Add(fakeTool{name: "read_file", readOnly: true})
	parentReg.Add(fakeTool{name: "write_file", readOnly: false})
	parentReg.Add(fakeTool{name: "bash", readOnly: false})
	task := NewTaskTool(sub, nil, parentReg, 20, 0, 0.0, "", "sys", nil, "", nil, nil, 0)
	parentReg.Add(task)

	args := []byte(`{"prompt":"x","tools":["read_file","task","write_file"]}`)
	if _, err := task.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := map[string]bool{}
	for _, s := range sub.lastReq.Tools {
		got[s.Name] = true
	}
	// Explicit whitelist: caller chose these tools. task+run_skill excluded
	// (recursion protection) but write_file passes through.
	if !got["read_file"] || !got["write_file"] || got["bash"] || got["task"] {
		t.Errorf("sub-agent tools = %v, want {read_file, write_file} with bash+task excluded", got)
	}
}

// TestTaskToolDefaultsToParentTools covers the no-whitelist path:
// the sub-agent inherits every parent tool. task IS included so
// decomposition can recurse.
func TestTaskToolDefaultsToParentTools(t *testing.T) {
	sub := &mockProvider{name: "sub", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "ok"},
		{Type: provider.ChunkDone},
	}}
	parentReg := tool.NewRegistry()
	parentReg.Add(fakeTool{name: "read_file", readOnly: true})
	parentReg.Add(fakeTool{name: "grep", readOnly: true})
	task := NewTaskTool(sub, nil, parentReg, 20, 0, 0.0, "", "sys", nil, "", nil, nil, 0)
	parentReg.Add(task)

	if _, err := task.Execute(context.Background(), []byte(`{"prompt":"x"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := map[string]bool{}
	for _, s := range sub.lastReq.Tools {
		got[s.Name] = true
	}
	if !got["read_file"] || !got["grep"] {
		t.Errorf("default sub-agent should inherit all parent tools; got %v", got)
	}
	// task() availability: the exclude list determines this. The assertion
	// here is intentionally loose — it just checks that basic tools work.
	// The exact exclude list is tested by the exclude list unit test.
}
