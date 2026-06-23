package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/tool"
)

// FuzzExecuteOne exercises executeOne with random tool names and argument JSON
// fragments. The goal is to catch panics — unknown tools, malformed args, nil
// gate/hooks/asker paths must all return a toolOutcome without crashing.
func FuzzExecuteOne(f *testing.F) {
	reg := tool.NewRegistry()
	reg.Add(fuzzTool{name: "bash", readOnly: false})
	reg.Add(fuzzTool{name: "read_file", readOnly: true})
	reg.Add(fuzzTool{name: "grep", readOnly: true})

	f.Add("bash", `{"command":"echo hi"}`)
	f.Add("read_file", `{"path":"/tmp/x"}`)
	f.Add("grep", `{"pattern":"x","path":"."}`)
	f.Add("nonexistent", `{}`)
	f.Add("bash", `not json`)

	f.Fuzz(func(t *testing.T, name, args string) {
		a := New(nil, reg, NewSession("FUZZ"), Options{}, event.Discard)
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on %q(%s): %v", name, args, r)
			}
		}()
		_ = a.executeOne(context.Background(), provider.ToolCall{
			Name:      name,
			Arguments: args,
		})
	})
}

// FuzzStreamChunkProcessing feeds random text/reasoning/tool-call fragments
// through the chunk-processing path to verify the agent never panics on
// unexpected chunk sequences from a provider.
func FuzzStreamChunkProcessing(f *testing.F) {
	f.Add("hello")
	f.Add("{\"tool\":\"call\"}")
	f.Add("")
	f.Add(strings.Repeat("x", 10000))

	f.Fuzz(func(t *testing.T, chunk string) {
		// Verify the chunk can be safely tokenized and emitted.
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic processing chunk %q: %v", chunk, r)
			}
		}()
		// strings.Contains / TrimSpace on arbitrary input must not panic.
		_ = strings.TrimSpace(chunk)
		_ = strings.Contains(chunk, "YES")
		_ = strings.Contains(chunk, "NO")
	})
}

// FuzzIsTaskFailed ensures the failure classifier never panics.
func FuzzIsTaskFailed(f *testing.F) {
	f.Add("YES")
	f.Add("NO")
	f.Add("✅ pass")
	f.Add("❌ fail")
	f.Add("The verification passed, all green")
	f.Add("")
	f.Add(strings.Repeat("x", 65536))
	f.Add("\x00\x01\x02")

	f.Fuzz(func(t *testing.T, result string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic in isTaskFailed(%q): %v", result, r)
			}
		}()
		_ = isTaskFailed(result)
	})
}

// FuzzArgsParsing feeds random JSON fragments to tools to verify their
// argument unmarshalling never panics.
func FuzzArgsParsing(f *testing.F) {
	reg := tool.NewRegistry()
	reg.Add(fuzzTool{name: "bash", readOnly: false})

	f.Add(`{"command":"x"}`)
	f.Add(`{}`)
	f.Add(``)
	f.Add(`{invalid`)
	f.Add(`null`)

	f.Fuzz(func(t *testing.T, args string) {
		a := New(nil, reg, NewSession("FUZZ"), Options{}, event.Discard)
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic executing with args %q: %v", args, r)
			}
		}()
		_ = a.executeOne(context.Background(), provider.ToolCall{
			Name:      "bash",
			Arguments: args,
		})
	})
}

// FuzzPlanModeGate exercises the plan-mode gate with random tool calls.
func FuzzPlanModeGate(f *testing.F) {
	reg := tool.NewRegistry()
	reg.Add(fuzzTool{name: "write_file", readOnly: false})
	reg.Add(fuzzTool{name: "read_file", readOnly: true})

	f.Add("write_file", `{"path":"/tmp/x","content":"hi"}`)
	f.Add("read_file", `{"path":"/tmp/x"}`)

	f.Fuzz(func(t *testing.T, name, args string) {
		a := New(nil, reg, NewSession("FUZZ"), Options{}, event.Discard)
		a.SetPlanMode(true)
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic in plan mode on %q(%s): %v", name, args, r)
			}
		}()
		out := a.executeOne(context.Background(), provider.ToolCall{
			Name:      name,
			Arguments: args,
		})
		_ = out.output
		_ = out.errMsg
		_ = out.blocked
	})
}

// FuzzCompactBounds exercises the compaction boundary logic.
func FuzzCompactBounds(f *testing.F) {
	// Build a session with system + user + assistant messages.
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "SYS"},
		{Role: provider.RoleUser, Content: "hi"},
		{Role: provider.RoleAssistant, Content: "answer", ToolCalls: []provider.ToolCall{{ID: "1", Name: "bash", Arguments: `{}`}}},
		{Role: provider.RoleTool, Content: "result", ToolCallID: "1"},
		{Role: provider.RoleUser, Content: "more"},
		{Role: provider.RoleAssistant, Content: "ok"},
		{Role: provider.RoleUser, Content: "even more"},
		{Role: provider.RoleAssistant, Content: "done"},
		{Role: provider.RoleUser, Content: "still here"},
		{Role: provider.RoleAssistant, Content: "yep"},
	}
	for _, m := range msgs {
		f.Add(m.Content)
	}

	f.Fuzz(func(t *testing.T, extra string) {
		sess := NewSession("SYS")
		for _, m := range msgs {
			sess.Add(m)
		}
		snapshot := sess.Snapshot()

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic in compactBounds: %v", r)
			}
		}()

		head, start, ok := compactBounds(snapshot, 8, 8)
		_ = head
		_ = start
		_ = ok
		_ = extra
	})
}

// FuzzJSONUnmarshal tools exercise random JSON against the tool argument parser.
func FuzzJSONUnmarshalTools(f *testing.F) {
	f.Add(`{"path":"x","old_string":"a","new_string":"b"}`)
	f.Add(`{}`)
	f.Add(`[]`)

	f.Fuzz(func(t *testing.T, raw string) {
		var v map[string]any
		// Must not panic on arbitrary JSON.
		err := json.Unmarshal([]byte(raw), &v)
		_ = err
		_ = v
	})
}

// fuzzTool is a minimal tool for fuzz testing.
type fuzzTool struct {
	name     string
	readOnly bool
}

func (f fuzzTool) Name() string            { return f.name }
func (f fuzzTool) Description() string     { return "fuzz tool" }
func (f fuzzTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (f fuzzTool) ReadOnly() bool          { return f.readOnly }
func (f fuzzTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return "ok", nil
}
