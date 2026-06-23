package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/tool"
)

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

// ─── Legacy compactBounds tests ───────────────────────────────────────────

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
		{"no-system", []provider.Message{
			u, as, u, as, u, as, u, as, u, as, u, as, u, as, u, as,
		}, 2, 0, 14, true},
		{"with-system", []provider.Message{
			sys, u, as, u, as, u, as, u, as, u, as, u, as, u, as, u, as,
		}, 3, 1, 14, true},
		{"align-off-tool", []provider.Message{
			sys, u, as, u, as, ac, to, as, u, as, u, as, u, as, u, ac, to,
		}, 1, 1, 15, true},
		{"too-short", []provider.Message{sys, u, as}, 8, 1, 0, false},
		{"below-min-compact", []provider.Message{sys, u, as, u, as, u, as, u}, 2, 1, 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			head, start, ok := compactBounds(tc.msgs, tc.keep, minCompactMessagesOld)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if head != tc.wantHead {
				t.Errorf("head = %d, want %d", head, tc.wantHead)
			}
			if ok && start != tc.wantStr {
				t.Errorf("start = %d, want %d", start, tc.wantStr)
			}
			if ok && tc.msgs[start].Role == provider.RoleTool {
				t.Errorf("recent tail begins with orphan tool message at %d", start)
			}
		})
	}
}

// ─── planCompaction tests ─────────────────────────────────────────────────

func TestPlanCompaction(t *testing.T) {
	sys := provider.Message{Role: provider.RoleSystem}
	u := provider.Message{Role: provider.RoleUser, Content: "hello"}
	as := provider.Message{Role: provider.RoleAssistant, Content: "ok"}
	ac := provider.Message{Role: provider.RoleAssistant, Content: "call", ToolCalls: []provider.ToolCall{{ID: "1", Name: "f"}}}
	to := provider.Message{Role: provider.RoleTool, ToolCallID: "1", Name: "f", Content: "result"}

	cases := []struct {
		name              string
		msgs              []provider.Message
		keep, ctxWin      int
		wantHead, wantStr int
		wantOK            bool
	}{
		{"no-system", []provider.Message{
			u, as, u, as, u, as, u, as, u, as, u, as, u, as, u, as,
		}, 2, 0, 1, 14, true},
		{"with-system", []provider.Message{
			sys, u, as, u, as, u, as, u, as, u, as, u, as, u, as, u, as,
		}, 3, 0, 2, 14, true},
		{"align-off-tool", []provider.Message{
			sys, u, as, u, as, ac, to, as, u, as, u, as, u, as, u, ac, to,
		}, 1, 0, 2, 15, true},
		{"too-short", []provider.Message{sys, u, as}, 8, 0, 2, 0, false},
		{"below-min-compact", []provider.Message{sys, u, as, u, as, u, as, u}, 2, 0, 2, 6, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := New(&fakeProvider{reply: "ok"}, tool.NewRegistry(),
				NewSession(""), Options{ContextWindow: tc.ctxWin, RecentKeep: tc.keep}, event.Discard)
			a.session.Messages = tc.msgs
			head, start, ok := a.planCompaction(tc.msgs, minCompactMessages)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if head != tc.wantHead {
				t.Errorf("head = %d, want %d", head, tc.wantHead)
			}
			if ok && start != tc.wantStr {
				t.Errorf("start = %d, want %d", start, tc.wantStr)
			}
			if ok && tc.msgs[start].Role == provider.RoleTool {
				t.Errorf("recent tail begins with orphan tool message at %d", start)
			}
		})
	}
}

// ─── compactWith test ─────────────────────────────────────────────────────

func TestCompactReplacesHistory(t *testing.T) {
	prov := &fakeProvider{reply: "- goal: do X\n- changed file Y"}
	sess := &Session{Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "task"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "1", Name: "read_file", Arguments: "{}"}}},
		{Role: provider.RoleTool, ToolCallID: "1", Name: "read_file", Content: strings.Repeat("file contents ", 100)},
		{Role: provider.RoleAssistant, Content: strings.Repeat("did a step ", 100)},
		{Role: provider.RoleUser, Content: "more work"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("another step ", 100)},
		{Role: provider.RoleUser, Content: "and more"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("still going ", 100)},
		{Role: provider.RoleUser, Content: "next"},
		{Role: provider.RoleAssistant, Content: "ok"},
	}}
	dir := t.TempDir()
	a := New(prov, tool.NewRegistry(), sess, Options{RecentKeep: 2, ArchiveDir: dir}, event.Discard)

	if err := a.compactWith(context.Background(), "test", "", true); err != nil {
		t.Fatalf("compact: %v", err)
	}

	// system + first_user (pinned) + summary + last 2 verbatim = 5
	if got := len(sess.Messages); got != 5 {
		t.Fatalf("len = %d, want 5: %+v", got, sess.Messages)
	}
	if sess.Messages[0].Role != provider.RoleSystem {
		t.Errorf("message 0 = %s, want system", sess.Messages[0].Role)
	}
	if sess.Messages[1].Role != provider.RoleUser || sess.Messages[1].Content != "task" {
		t.Errorf("first user turn not pinned: %+v", sess.Messages[1])
	}
	summary := sess.Messages[2]
	if summary.Role != provider.RoleUser || !strings.Contains(summary.Content, "Summary of earlier") || !strings.Contains(summary.Content, "do X") {
		t.Errorf("summary message = %+v", summary)
	}
	if sess.Messages[3].Content != "next" || sess.Messages[4].Content != "ok" {
		t.Errorf("recent tail not preserved: %+v", sess.Messages[3:])
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("archive dir: entries=%d", len(entries))
	}
	data, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	_ = data
	if !strings.HasSuffix(entries[0].Name(), ".jsonl") {
		t.Errorf("archive name = %q, want .jsonl", entries[0].Name())
	}
}

// ─── maybeCompact tests ───────────────────────────────────────────────────

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

	// Below 90%: untouched
	sess := newSess()
	a := New(&fakeProvider{reply: "s"}, tool.NewRegistry(), sess,
		Options{ContextWindow: 1000, RecentKeep: 2, ArchiveDir: t.TempDir()}, event.Discard)
	a.maybeCompact(context.Background(), &provider.Usage{PromptTokens: 500})
	if len(sess.Messages) != 11 {
		t.Errorf("below threshold should not compact, len = %d", len(sess.Messages))
	}

	// No context window: compaction disabled
	sess = newSess()
	a = New(&fakeProvider{reply: "s"}, tool.NewRegistry(), sess,
		Options{RecentKeep: 2, ArchiveDir: t.TempDir()}, event.Discard)
	a.maybeCompact(context.Background(), &provider.Usage{PromptTokens: 1 << 30})
	if len(sess.Messages) != 11 {
		t.Errorf("no window should disable compaction, len = %d", len(sess.Messages))
	}
}

func TestCompactStuck(t *testing.T) {
	prov := &fakeProvider{reply: "s"}
	sess := &Session{Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "task"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("step1 ", 200)},
		{Role: provider.RoleUser, Content: "more"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("step2 ", 200)},
		{Role: provider.RoleUser, Content: "more2"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("step3 ", 200)},
		{Role: provider.RoleUser, Content: "more3"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("step4 ", 200)},
	}}
	a := New(prov, tool.NewRegistry(), sess, Options{ContextWindow: 500, RecentKeep: 2, ArchiveDir: t.TempDir()}, event.Discard)

	// First compact with force=true
	if err := a.compactWith(context.Background(), "test", "", true); err != nil {
		t.Fatalf("compact: %v", err)
	}
	a.compactedLastMu.Lock()
	a.consecutiveCompacts = 1
	a.compactedLastMu.Unlock()

	// Second compact
	sess = a.session
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "more"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: strings.Repeat("step5 ", 200)})
	if err := a.compactWith(context.Background(), "test", "", true); err != nil {
		t.Fatalf("compact2: %v", err)
	}
}

func TestSoftCompactRatio(t *testing.T) {
	prov := &fakeProvider{reply: "s"}
	sess := &Session{Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "task"},
		{Role: provider.RoleAssistant, Content: "step1"},
	}}
	a := New(prov, tool.NewRegistry(), sess, Options{ContextWindow: 1000, RecentKeep: 2}, event.Discard)

	// At 60%: should NOT compact (soft band)
	a.maybeCompact(context.Background(), &provider.Usage{PromptTokens: 600})
	if len(sess.Messages) != 3 {
		t.Error("should not compact in soft band")
	}
	if !a.softCompactNoticed {
		t.Error("softCompactNoticed should be set")
	}
}

// ─── truncation tests ─────────────────────────────────────────────────────

func TestTruncateToolResultShort(t *testing.T) {
	in := "short result"
	if truncateToolResult(in) != in {
		t.Errorf("short result should be unchanged")
	}
}

func TestTruncateToolResultLong(t *testing.T) {
	in := strings.Repeat("abcdefghij", 200)
	got := truncateToolResult(in)
	if !strings.Contains(got, "truncated") {
		t.Errorf("long result should be truncated")
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

func TestSummarizeToolArgs(t *testing.T) {
	if got := summarizeToolArgs(""); got != "(no arguments)" {
		t.Errorf("empty = %q", got)
	}
	if got := summarizeToolArgs("not json"); !strings.Contains(got, "bytes") {
		t.Errorf("non-json = %q", got)
	}
	if got := summarizeToolArgs(`{"path":"x","old":"a","new":"b"}`); !strings.Contains(got, "3 keys") {
		t.Errorf("json = %q", got)
	}
}

// ─── foldEconomics tests ──────────────────────────────────────────────────

func TestFoldEconomics(t *testing.T) {
	if foldEconomics(nil) {
		t.Error("nil should be false")
	}
	small := []provider.Message{{Role: provider.RoleAssistant, Content: "ok"}}
	if foldEconomics(small) {
		t.Error("small region should be false")
	}
	large := []provider.Message{{Role: provider.RoleAssistant, Content: strings.Repeat("x", 2000)}}
	if !foldEconomics(large) {
		t.Error("large region should be true")
	}
}

// ─── prefix / summary tests ───────────────────────────────────────────────

func TestPinnedPrefixLen(t *testing.T) {
	a := New(&fakeProvider{reply: "ok"}, tool.NewRegistry(),
		NewSession(""), Options{ContextWindow: 128000, RecentKeep: 8}, event.Discard)

	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	if got := a.pinnedPrefixLen(msgs); got != 1 {
		t.Errorf("system only = %d, want 1", got)
	}

	msgs = []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "hello"},
	}
	if got := a.pinnedPrefixLen(msgs); got != 2 {
		t.Errorf("sys+user = %d, want 2", got)
	}

	msgs = []provider.Message{{Role: provider.RoleUser, Content: "hello"}}
	if got := a.pinnedPrefixLen(msgs); got != 1 {
		t.Errorf("user only = %d, want 1", got)
	}
}

func TestIsCompactionSummary(t *testing.T) {
	if isCompactionSummary(provider.Message{Role: provider.RoleAssistant}) {
		t.Error("assistant is not a summary")
	}
	if !isCompactionSummary(provider.Message{Role: provider.RoleUser, Content: summaryTagOpen + "\nblah\n" + summaryTagClose}) {
		t.Error("tagged user should be a summary")
	}
	if isCompactionSummary(provider.Message{Role: provider.RoleUser, Content: "normal message"}) {
		t.Error("normal user should not be a summary")
	}
}

func TestPartitionFold(t *testing.T) {
	a := New(&fakeProvider{reply: "ok"}, tool.NewRegistry(),
		NewSession(""), Options{ContextWindow: 128000, RecentKeep: 8}, event.Discard)
	region := []provider.Message{
		{Role: provider.RoleUser, Content: "small"},
		{Role: provider.RoleAssistant, Content: "step"},
		{Role: provider.RoleUser, Content: strings.Repeat("x", 10000)},
		{Role: provider.RoleAssistant, Content: "another"},
	}
	kept, fold := a.partitionFold(region)
	if len(kept) != 0 {
		t.Errorf("kept = %d, want 0", len(kept))
	}
	if len(fold) != 4 {
		t.Errorf("fold = %d, want 4", len(fold))
	}
}

func TestPruneStaleToolResults(t *testing.T) {
	a := New(&fakeProvider{reply: "ok"}, tool.NewRegistry(),
		&Session{Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: "sys"},
			{Role: provider.RoleUser, Content: "task"},
			{Role: provider.RoleAssistant, Content: "checking"},
			{Role: provider.RoleTool, Name: "grep", ToolCallID: "1", Content: strings.Repeat("x", 2000)},
			{Role: provider.RoleAssistant, Content: "more"},
			{Role: provider.RoleTool, Name: "read_file", ToolCallID: "2", Content: "tiny"},
			{Role: provider.RoleAssistant, Content: "done"},
			{Role: provider.RoleUser, Content: "next"},
			{Role: provider.RoleAssistant, Content: "ok"},
		}},
		Options{ContextWindow: 1000, RecentKeep: 2}, event.Discard,
	)

	st, err := a.PruneStaleToolResults()
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if st.Results != 1 {
		t.Errorf("pruned = %d, want 1", st.Results)
	}
	if !strings.Contains(a.session.Messages[3].Content, prunedMarker) {
		t.Errorf("grep result not pruned: %.80s", a.session.Messages[3].Content)
	}
	if a.session.Messages[5].Content != "tiny" {
		t.Errorf("tiny read_file should not be pruned: %s", a.session.Messages[5].Content)
	}
}
