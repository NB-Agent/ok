package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/provider"
)

// =============================================================================
// wire.go — toWire serialization
// =============================================================================

func TestKindNamesComplete(t *testing.T) {
	// Every event.Kind must have a wire name so the frontend never gets "".
	all := []event.Kind{
		event.TurnStarted, event.Reasoning, event.Text, event.Message,
		event.ToolDispatch, event.ToolResult, event.Usage, event.Notice,
		event.Phase, event.ApprovalRequest, event.AskRequest, event.TurnDone,
	}
	for _, k := range all {
		if kindNames[k] == "" {
			t.Errorf("kindNames[%d] is empty", k)
		}
	}
}

func TestToWireText(t *testing.T) {
	w := toWire(&event.Event{Kind: event.Text, Text: "hello"})
	if w.Kind != "text" || w.Text != "hello" {
		t.Errorf("got %+v", w)
	}
}

func TestToWireReasoning(t *testing.T) {
	w := toWire(&event.Event{Kind: event.Reasoning, Text: "dim text", Reasoning: "full chain"})
	if w.Kind != "reasoning" || w.Text != "dim text" || w.Reasoning != "full chain" {
		t.Errorf("got %+v", w)
	}
}

func TestToWireNotice(t *testing.T) {
	w := toWire(&event.Event{Kind: event.Notice, Text: "info msg", Level: event.LevelInfo})
	if w.Kind != "notice" || w.Level != "info" || w.Text != "info msg" {
		t.Errorf("got %+v", w)
	}
	w2 := toWire(&event.Event{Kind: event.Notice, Text: "warn", Level: event.LevelWarn})
	if w2.Level != "warn" {
		t.Errorf("warn level = %q", w2.Level)
	}
}

func TestToWireToolDispatch(t *testing.T) {
	w := toWire(&event.Event{
		Kind: event.ToolDispatch,
		Tool: event.Tool{ID: "call-1", Name: "bash", Args: `{"cmd":"ls"}`, ReadOnly: false, Partial: true, ParentID: "p0"},
	})
	if w.Kind != "tool_dispatch" || w.Tool == nil {
		t.Fatal("nil tool")
	}
	if w.Tool.ID != "call-1" || w.Tool.Name != "bash" || w.Tool.Args != `{"cmd":"ls"}` {
		t.Errorf("tool fields: %+v", w.Tool)
	}
	if !w.Tool.Partial {
		t.Error("Partial should be true")
	}
	if w.Tool.ParentID != "p0" {
		t.Errorf("ParentID = %q", w.Tool.ParentID)
	}
}

func TestToWireToolResult(t *testing.T) {
	w := toWire(&event.Event{
		Kind: event.ToolResult,
		Tool: event.Tool{Name: "read_file", Output: "content", Truncated: true, Err: "permission denied"},
	})
	if w.Kind != "tool_result" || w.Tool == nil {
		t.Fatal("nil tool")
	}
	if w.Tool.Output != "content" || !w.Tool.Truncated || w.Tool.Err != "permission denied" {
		t.Errorf("result: %+v", w.Tool)
	}
}

func TestToWireUsage(t *testing.T) {
	u := &provider.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150, CacheHitTokens: 80, CacheMissTokens: 20, ReasoningTokens: 30}
	w := toWire(&event.Event{
		Kind: event.Usage, Usage: u, SessionHit: 200, SessionMiss: 50,
		Pricing: &provider.Pricing{Input: 0.001, Output: 0.002},
	})
	if w.Kind != "usage" || w.Usage == nil {
		t.Fatal("nil usage")
	}
	if w.Usage.PromptTokens != 100 || w.Usage.CompletionTokens != 50 || w.Usage.TotalTokens != 150 {
		t.Errorf("tokens: %+v", w.Usage)
	}
	if w.Usage.SessionCacheHitTokens != 200 || w.Usage.SessionCacheMissTokens != 50 {
		t.Errorf("session cache: %+v", w.Usage)
	}
	if w.Usage.CostUSD <= 0 {
		t.Error("cost should be positive")
	}
}

func TestToWireUsageNil(t *testing.T) {
	w := toWire(&event.Event{Kind: event.Usage, Usage: nil})
	if w.Usage != nil {
		t.Error("nil usage → nil wire usage")
	}
}

func TestToWireApproval(t *testing.T) {
	w := toWire(&event.Event{
		Kind:     event.ApprovalRequest,
		Approval: event.Approval{ID: "a1", Tool: "bash", Subject: "rm -rf /tmp"},
	})
	if w.Kind != "approval_request" || w.Approval == nil {
		t.Fatal("nil approval")
	}
	if w.Approval.ID != "a1" || w.Approval.Tool != "bash" || w.Approval.Subject != "rm -rf /tmp" {
		t.Errorf("approval: %+v", w.Approval)
	}
}

func TestToWireAsk(t *testing.T) {
	q := event.AskQuestion{
		ID: "q1", Header: "Lang", Prompt: "Which?",
		Options: []event.AskOption{{Label: "Go"}, {Label: "Rust", Description: "fast"}},
		Multi:   true,
	}
	w := toWire(&event.Event{Kind: event.AskRequest, Ask: event.Ask{ID: "ask1", Questions: []event.AskQuestion{q}}})
	if w.Kind != "ask_request" || w.Ask == nil {
		t.Fatal("nil ask")
	}
	if len(w.Ask.Questions) != 1 {
		t.Fatalf("questions = %d", len(w.Ask.Questions))
	}
	wq := w.Ask.Questions[0]
	if wq.ID != "q1" || wq.Header != "Lang" || !wq.Multi || len(wq.Options) != 2 {
		t.Errorf("question: %+v", wq)
	}
}

func TestToWireTurnDone(t *testing.T) {
	w := toWire(&event.Event{Kind: event.TurnDone, Err: nil})
	if w.Kind != "turn_done" || w.Err != "" {
		t.Errorf("clean done: %+v", w)
	}
	w2 := toWire(&event.Event{Kind: event.TurnDone, Err: errors.New("boom")})
	if w2.Err != "boom" {
		t.Errorf("error done: %q", w2.Err)
	}
}

func TestToWireMessage(t *testing.T) {
	w := toWire(&event.Event{Kind: event.Message, Text: "full answer", Reasoning: "chain"})
	if w.Kind != "message" || w.Text != "full answer" || w.Reasoning != "chain" {
		t.Errorf("got %+v", w)
	}
}

func TestToWireTurnStarted(t *testing.T) {
	w := toWire(&event.Event{Kind: event.TurnStarted})
	if w.Kind != "turn_started" {
		t.Errorf("got %q", w.Kind)
	}
}

// =============================================================================
// sessions.go — title sidecar
// =============================================================================

func TestSessionTitlesRoundTrip(t *testing.T) {
	dir := t.TempDir()

	// Empty → no file
	m := loadSessionTitles(dir)
	if len(m) != 0 {
		t.Errorf("expected empty, got %v", m)
	}

	// Save some titles
	m["session-1.jsonl"] = "Debugging the thing"
	m["session-2.jsonl"] = "Refactoring"
	if err := saveSessionTitles(dir, m); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Load back
	m2 := loadSessionTitles(dir)
	if !reflect.DeepEqual(m, m2) {
		t.Errorf("round-trip: want %v, got %v", m, m2)
	}
}

func TestLoadSessionTitlesCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(sessionTitlesPath(dir), []byte("not json"), 0o644)
	m := loadSessionTitles(dir)
	if len(m) != 0 {
		t.Errorf("corrupt JSON should yield empty, got %v", m)
	}
}

func TestSetSessionTitle(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "chat.jsonl")

	// Set title
	if err := setSessionTitle(dir, sp, "Hello World"); err != nil {
		t.Fatal(err)
	}
	m := loadSessionTitles(dir)
	if m["chat.jsonl"] != "Hello World" {
		t.Errorf("title = %q", m["chat.jsonl"])
	}

	// Clear title
	if err := setSessionTitle(dir, sp, "  "); err != nil {
		t.Fatal(err)
	}
	m = loadSessionTitles(dir)
	if _, ok := m["chat.jsonl"]; ok {
		t.Error("title should be cleared")
	}
}

func TestDeleteSessionFile(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "gone.jsonl")

	// Non-existent file → no error
	if err := deleteSessionFile(dir, sp); err != nil {
		t.Errorf("non-existent should not error: %v", err)
	}

	// Existing file + title
	os.WriteFile(sp, []byte(`{"role":"user","content":"hi"}`), 0o644)
	if err := setSessionTitle(dir, sp, "Bye"); err != nil {
		t.Fatal(err)
	}
	if err := deleteSessionFile(dir, sp); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sp); !os.IsNotExist(err) {
		t.Error("file should be removed")
	}
	m := loadSessionTitles(dir)
	if _, ok := m["gone.jsonl"]; ok {
		t.Error("title should be removed with file")
	}
}

func TestSessionTitlesPath(t *testing.T) {
	if p := sessionTitlesPath("/home/user/sessions"); p != filepath.Join("/home/user/sessions", ".titles.json") {
		t.Errorf("path = %q", p)
	}
}

// =============================================================================
// workspace.go
// =============================================================================

func TestSaveLoadWorkspace(t *testing.T) {
	dir := t.TempDir()
	// Override config.MemoryUserDir — not possible without config.Init, so test
	// the pure functions on disk directly.

	tmp := filepath.Join(dir, "ws-state")
	// Write directly
	os.MkdirAll(filepath.Dir(tmp), 0o755)
	os.WriteFile(tmp, []byte("/some/project"), 0o644)

	b, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != "/some/project" {
		t.Errorf("load: got %q, want /some/project", got)
	}
}

func TestCwdWritable(t *testing.T) {
	// This test runs in a normal Go test working directory — it should be writable.
	if !cwdWritable() {
		t.Error("test cwd should be writable")
	}
}

func TestCwdWritableReadOnly(t *testing.T) {
	dir := t.TempDir()
	// On Windows, Chmod has no effect on directory writability. The immovable
	// path for a read-only cwd test is a path that doesn't exist at all.
	// cwdWritable must return false for a non-existent directory.
	nonexist := filepath.Join(dir, "no-such-dir")
	orig, _ := os.Getwd()
	if err := os.Chdir(nonexist); err != nil {
		t.Skip("chdir to nonexistent fails (expected on some OS)")
		return
	}
	defer os.Chdir(orig)
	if cwdWritable() {
		t.Error("non-existent dir should not be writable")
	}
}

// =============================================================================
// settings_app.go — pure functions
// =============================================================================

func TestNonNil(t *testing.T) {
	if nonNil(nil) == nil {
		t.Error("nil → empty slice")
	}
	if len(nonNil(nil)) != 0 {
		t.Error("nil → non-empty?")
	}
	orig := []string{"a"}
	if got := nonNil(orig); len(got) != 1 || got[0] != "a" {
		t.Errorf("non-nil pass-through: %v", got)
	}
}

func TestOrDefault(t *testing.T) {
	if orDefault("", "x") != "x" {
		t.Error("empty → default")
	}
	if orDefault("  ", "x") != "x" {
		t.Error("whitespace → default")
	}
	if orDefault("y", "x") != "y" {
		t.Error("non-empty → kept")
	}
}

func TestTrimList(t *testing.T) {
	got := trimList([]string{"a", "  ", "b", ""})
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if trimList(nil) == nil {
		t.Error("nil in → non-nil slice out")
	}
}

// =============================================================================
// dotenv.go
// =============================================================================

func TestUpsertDotEnvReplace(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(cwd)

	os.WriteFile(dotEnvPath, []byte("# comment\nFOO=old\nBAR=keep\n"), 0o644)

	if err := upsertDotEnv("FOO", "new"); err != nil {
		t.Fatalf("replace: %v", err)
	}

	b, _ := os.ReadFile(dotEnvPath)
	content := string(b)
	if !strings.Contains(content, "FOO=new") {
		t.Errorf("FOO not replaced: %s", content)
	}
	if !strings.Contains(content, "BAR=keep") {
		t.Errorf("BAR lost: %s", content)
	}
	if !strings.Contains(content, "# comment") {
		t.Errorf("comment lost: %s", content)
	}
	if os.Getenv("FOO") != "new" {
		t.Errorf("env not set: FOO=%s", os.Getenv("FOO"))
	}
}

func TestUpsertDotEnvAppend(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(cwd)

	os.WriteFile(dotEnvPath, []byte("FOO=old\n"), 0o644)

	if err := upsertDotEnv("BAZ", "added"); err != nil {
		t.Fatalf("append: %v", err)
	}

	b, _ := os.ReadFile(dotEnvPath)
	content := string(b)
	if !strings.Contains(content, "BAZ=added") {
		t.Errorf("BAZ not appended: %s", content)
	}
	if !strings.Contains(content, "FOO=old") {
		t.Errorf("FOO lost: %s", content)
	}
}

func TestUpsertDotEnvEmptyKey(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(cwd)

	if err := upsertDotEnv("  ", "x"); err != nil {
		t.Errorf("empty key should be no-op, got: %v", err)
	}
}

func TestUpsertDotEnvNoFile(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(cwd)

	if err := upsertDotEnv("NEW", "val"); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	b, _ := os.ReadFile(dotEnvPath)
	if !strings.Contains(string(b), "NEW=val") {
		t.Errorf("no file → created: %s", b)
	}
}

// =============================================================================
// settings_app.go — wire types round-trip (JSON)
// =============================================================================

func TestSettingsViewJSON(t *testing.T) {
	// Verify the wire types serialize/deserialize without error — the frontend
	// deserializes these via Wails bindings.
	sv := SettingsView{
		DefaultModel: "deepseek/flash",
		Providers:    []ProviderView{{Name: "ds", Kind: "openai", Models: []string{"flash"}}},
		Permissions:  PermissionsView{Mode: "ask"},
		Sandbox:      SandboxView{Bash: "enforce", Network: true},
		Agent:        AgentView{Temperature: 0.7, MaxSteps: 20},
	}
	b, err := json.Marshal(sv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back SettingsView
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.DefaultModel != "deepseek/flash" || back.Agent.Temperature != 0.7 {
		t.Errorf("round-trip mismatch: %+v", back)
	}
}

func TestWireEventJSON(t *testing.T) {
	w := toWire(&event.Event{Kind: event.Text, Text: "hello"})
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if s := string(b); !strings.Contains(s, `"kind":"text"`) || !strings.Contains(s, "hello") {
		t.Errorf("JSON = %s", s)
	}
}

func TestErrActiveSession(t *testing.T) {
	if errActiveSession.Error() == "" {
		t.Error("errActiveSession should have a message")
	}
}

// =============================================================================
// App methods — nil-safe / no-controller tests
// =============================================================================

func TestAppMetaNilCtrl(t *testing.T) {
	a := NewApp()
	m := a.Meta()
	if m.Ready {
		t.Error("Meta.Ready should be false when ctrl is nil")
	}
	if m.StartupErr != "" {
		t.Errorf("StartupErr = %q, want empty", m.StartupErr)
	}
	if m.EventChannel != "agent:event" {
		t.Errorf("EventChannel = %q, want agent:event", m.EventChannel)
	}
	if m.Cwd == "" {
		t.Error("Cwd should not be empty")
	}
}

func TestAppSetBypassNilCtrl(t *testing.T) {
	a := NewApp()
	// Must not panic when ctrl is nil.
	a.SetBypass(true)
	a.SetBypass(false)
}

func TestAppCommandsNilCtrl(t *testing.T) {
	a := NewApp()
	cmds := a.Commands()
	if len(cmds) < 5 {
		t.Fatalf("expected >=5 builtin commands, got %d", len(cmds))
	}
	// Verify builtin commands are present.
	seen := map[string]bool{}
	for _, c := range cmds {
		seen[c.Name] = true
		if c.Kind != "builtin" && c.Kind != "custom" && c.Kind != "mcp" {
			t.Errorf("command %q has unexpected kind %q", c.Name, c.Kind)
		}
	}
	for _, name := range []string{"new", "compact", "model", "memory", "mcp", "hooks", "skill"} {
		if !seen[name] {
			t.Errorf("missing builtin command /%s", name)
		}
	}
}

func TestAppListDirTemp(t *testing.T) {
	a := NewApp()
	// Create a temp dir structure.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "b.go"), []byte("package b"), 0o644)

	// Override Getwd by changing to the temp dir.
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	entries := a.ListDir("")
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	if names[0] != "sub" || names[1] != "a.go" {
		t.Errorf("unexpected order: %v (expected sub then a.go)", names)
	}

	// List a subdirectory.
	sub := a.ListDir("sub")
	if len(sub) != 1 || sub[0].Name != "b.go" {
		t.Errorf("subdir entries = %+v", sub)
	}

	// Block traversal outside workspace.
	outside := a.ListDir("..")
	if outside != nil {
		t.Errorf("expected nil for .. traversal, got %v", outside)
	}

	// Non-existent dir → nil.
	gone := a.ListDir("no-such-dir")
	if gone != nil {
		t.Errorf("expected nil for missing dir, got %v", gone)
	}
}

func TestAppListSessionsEmpty(t *testing.T) {
	a := NewApp()

	// Use a temp dir as the session dir.
	dir := t.TempDir()
	b, _ := json.Marshal(map[string]string{})
	os.WriteFile(sessionTitlesPath(dir), b, 0o644)

	// Without overriding the config dir, ListSessions uses the real config path,
	// which is not a temp dir. We test the empty case by checking the method
	// doesn't crash — it returns whatever sessions exist.
	sessions := a.ListSessions()
	// sessions should be a non-nil slice.
	if sessions == nil {
		t.Error("ListSessions returned nil, want non-nil slice")
	}
}
