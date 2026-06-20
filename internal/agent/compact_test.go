package agent

import (
	"context"
	"github.com/NB-Agent/ok/internal/event"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/tool"
)

// fakeProvider returns a fixed reply and records the messages it was asked to
// complete, so tests can drive summarization without a network call.
type fakeProvider struct {
	reply string
	got   []provider.Message
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	f.got = req.Messages
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: f.reply}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

func TestCompactBounds(t *testing.T) {
	sys := provider.Message{Role: provider.RoleSystem}
	u := provider.Message{Role: provider.RoleUser}
	as := provider.Message{Role: provider.RoleAssistant}
	ac := provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "1", Name: "f"}}}
	to := provider.Message{Role: provider.RoleTool, ToolCallID: "1", Name: "f"}

	cases := []struct {
		name              string
		msgs              []provider.Message
		keep              int
		wantHead, wantStr int
		wantOK            bool
	}{
		// minCompactMessages=8: the region between head and start must be >= 8
		// for compaction to fire. Build scenarios with enough messages.
		{"no-system", []provider.Message{
			u, as, u, as, u, as, u, as, u, as, u, as, u, as, u, as,
		}, 2, 0, 14, true},
		{"with-system", []provider.Message{
			sys, u, as, u, as, u, as, u, as, u, as, u, as, u, as, u, as,
		}, 3, 1, 14, true},
		// Recent tail of 1 lands on an orphan tool result; the boundary must move
		// back onto its assistant so the tail starts with the tool_calls.
		{"align-off-tool", []provider.Message{
			sys, u, as, u, as, ac, to, as, u, as, u, as, u, as, u, ac, to,
		}, 1, 1, 15, true},
		{"too-short", []provider.Message{sys, u, as}, 8, 1, 0, false},
		{"below-min-compact", []provider.Message{sys, u, as, u, as, u, as, u}, 2, 1, 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			head, start, ok := compactBounds(tc.msgs, tc.keep, minCompactMessages)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if head != tc.wantHead {
				t.Errorf("head = %d, want %d", head, tc.wantHead)
			}
			if ok && start != tc.wantStr {
				t.Errorf("start = %d, want %d", start, tc.wantStr)
			}
			// The aligned tail must never begin with an orphan tool result.
			if ok && tc.msgs[start].Role == provider.RoleTool {
				t.Errorf("recent tail begins with orphan tool message at %d", start)
			}
		})
	}
}

func TestCompactReplacesHistory(t *testing.T) {
	prov := &fakeProvider{reply: "- goal: do X\n- changed file Y"}
	sess := &Session{Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "task"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "1", Name: "read_file", Arguments: "{}"}}},
		{Role: provider.RoleTool, ToolCallID: "1", Name: "read_file", Content: "file contents"},
		{Role: provider.RoleAssistant, Content: "did a step"},
		{Role: provider.RoleUser, Content: "more"},
		{Role: provider.RoleAssistant, Content: "another"},
		{Role: provider.RoleUser, Content: "and more"},
		{Role: provider.RoleAssistant, Content: "still going"},
		{Role: provider.RoleUser, Content: "next"},
		{Role: provider.RoleAssistant, Content: "ok"},
	}}
	dir := t.TempDir()
	a := New(prov, tool.NewRegistry(), sess, Options{RecentKeep: 2, ArchiveDir: dir}, event.Discard)

	if err := a.compact(context.Background()); err != nil {
		t.Fatalf("compact: %v", err)
	}

	// system + summary + last 2 verbatim.
	if got := len(sess.Messages); got != 4 {
		t.Fatalf("len = %d, want 4: %+v", got, sess.Messages)
	}
	if sess.Messages[0].Role != provider.RoleSystem {
		t.Errorf("message 0 = %s, want system", sess.Messages[0].Role)
	}
	summary := sess.Messages[1]
	if summary.Role != provider.RoleUser || !strings.Contains(summary.Content, "Summary of earlier") || !strings.Contains(summary.Content, "do X") {
		t.Errorf("summary message = %+v", summary)
	}
	if sess.Messages[2].Content != "next" || sess.Messages[3].Content != "ok" {
		t.Errorf("recent tail not preserved: %+v", sess.Messages[2:])
	}

	// The 4 dropped originals were archived, one JSON object per line.
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("archive dir: entries=%d err=%v", len(entries), err)
	}
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if lines := strings.Count(strings.TrimSpace(string(data)), "\n") + 1; lines != 8 {
		t.Errorf("archived %d lines, want 8:\n%s", lines, data)
	}
	if !strings.HasSuffix(entries[0].Name(), ".jsonl") {
		t.Errorf("archive name = %q, want .jsonl", entries[0].Name())
	}
}

func TestMaybeCompactThreshold(t *testing.T) {
	newSess := func() *Session {
		return &Session{Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: "sys"},
			{Role: provider.RoleUser, Content: "a"},
			{Role: provider.RoleAssistant, Content: "b"},
			{Role: provider.RoleUser, Content: "c"},
			{Role: provider.RoleAssistant, Content: "d"},
			{Role: provider.RoleUser, Content: "e"},
			{Role: provider.RoleAssistant, Content: "f"},
			{Role: provider.RoleUser, Content: "g"},
			{Role: provider.RoleAssistant, Content: "h"},
			{Role: provider.RoleUser, Content: "i"},
			{Role: provider.RoleAssistant, Content: "j"},
		}}
	}

	// Below 80% of the window: untouched.
	sess := newSess()
	a := New(&fakeProvider{reply: "s"}, tool.NewRegistry(), sess, Options{ContextWindow: 100, RecentKeep: 2, ArchiveDir: t.TempDir()}, event.Discard)
	a.maybeCompact(context.Background(), &provider.Usage{PromptTokens: 50})
	if len(sess.Messages) != 11 {
		t.Errorf("below threshold should not compact, len = %d", len(sess.Messages))
	}

	// At/above 80%: compacts.
	sess = newSess()
	a = New(&fakeProvider{reply: "s"}, tool.NewRegistry(), sess, Options{ContextWindow: 100, RecentKeep: 2, ArchiveDir: t.TempDir()}, event.Discard)
	a.maybeCompact(context.Background(), &provider.Usage{PromptTokens: 90})
	if len(sess.Messages) >= 11 {
		t.Errorf("above threshold should compact, len = %d", len(sess.Messages))
	}

	// No context window: compaction disabled.
	sess = newSess()
	a = New(&fakeProvider{reply: "s"}, tool.NewRegistry(), sess, Options{RecentKeep: 2, ArchiveDir: t.TempDir()}, event.Discard)
	a.maybeCompact(context.Background(), &provider.Usage{PromptTokens: 1 << 30})
	if len(sess.Messages) != 11 {
		t.Errorf("no window should disable compaction, len = %d", len(sess.Messages))
	}
}

func TestTruncateToolResultShort(t *testing.T) {
	in := "short result"
	got := truncateToolResult(in)
	if got != in {
		t.Errorf("short result should be unchanged, got %q", got)
	}
}

func TestTruncateToolResultLong(t *testing.T) {
	in := strings.Repeat("abcdefghij", 200)
	got := truncateToolResult(in)
	if !strings.Contains(got, "truncated") {
		t.Errorf("long result should be truncated")
	}
	if len(got) >= len(in) {
		t.Errorf("truncated result should be shorter: %d >= %d", len(got), len(in))
	}
}

func TestTruncateToolResultPreservesTailErrors(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("ok  pkg\n")
	}
	b.WriteString("--- FAIL: TestFoo (0.01s)\n")
	b.WriteString("    foo_test.go:42: expected X, got Y\n")
	b.WriteString("FAIL\n")
	got := truncateToolResult(b.String())
	if !strings.Contains(got, "--- FAIL: TestFoo") {
		t.Errorf("should preserve tail error")
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("should show truncation marker")
	}
}

func TestRenderTranscriptTruncatesToolResults(t *testing.T) {
	long := strings.Repeat("x", 3000)
	msgs := []provider.Message{
		{Role: provider.RoleTool, Name: "grep", Content: long},
		{Role: provider.RoleTool, Name: "read_file", Content: "tiny"},
	}
	got := renderTranscript(msgs)
	if !strings.Contains(got, "truncated") {
		t.Errorf("long tool result should be truncated in transcript")
	}
	if !strings.Contains(got, "tiny") {
		t.Errorf("short tool result should be preserved")
	}
}
