package kernel

import (
	"context"
	"encoding/json"
	"testing"
)

func TestBaseline_NewKernelNotPanic(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("&Kernel{} panicked: %v", r)
		}
	}()
	_ = &Kernel{}
}

func TestBaseline_Schema(t *testing.T) {
	t.Parallel()
	k := &Kernel{}
	schemas := k.Schema()
	if len(schemas) != 9 {
		t.Fatalf("Schema() returned %d tools, want 9 (5 syscalls + 4 civilization primitives)", len(schemas))
	}
	names := make(map[string]bool)
	for _, s := range schemas {
		names[s.Name] = true
		if s.Description == "" {
			t.Errorf("Schema entry %q has empty Description", s.Name)
		}
		if len(s.Schema) == 0 {
			t.Errorf("Schema entry %q has empty Schema", s.Name)
		}
	}
	expected := []string{"bash", "read_file", "write_file", "edit_file", "grep", "identity", "recall", "trust", "learn"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("Schema() missing tool %q", name)
		}
	}
}

func TestBaseline_SyscallNames(t *testing.T) {
	t.Parallel()
	k := &Kernel{}
	names := k.SyscallNames()
	if len(names) != 5 {
		t.Fatalf("SyscallNames() returned %d names, want 5", len(names))
	}
	expected := []string{"bash", "read_file", "write_file", "edit_file", "grep"}
	for i, name := range expected {
		if names[i] != name {
			t.Errorf("SyscallNames()[%d] = %q, want %q", i, names[i], name)
		}
	}
}

func TestBaseline_IsSyscall(t *testing.T) {
	t.Parallel()
	k := &Kernel{}
	for _, name := range []string{"bash", "read_file", "write_file", "edit_file", "grep"} {
		if !k.IsSyscall(name) {
			t.Errorf("IsSyscall(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "unknown", "Bash", "ls", "cat"} {
		if k.IsSyscall(name) {
			t.Errorf("IsSyscall(%q) = true, want false", name)
		}
	}
}

// ─── User types ─────────────────────────────────────────────────────────────

func TestUserIsZero(t *testing.T) {
	tests := []struct {
		name   string
		user   User
		isZero bool
	}{
		{"zero value", User{}, true},
		{"empty ID", User{Label: "Alice"}, true},
		{"non-zero", User{ID: "u1", Label: "Alice", Roles: []string{"admin"}}, false},
		{"only ID", User{ID: "u1"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.user.IsZero()
			if got != tc.isZero {
				t.Errorf("User%+v.IsZero() = %v, want %v", tc.user, got, tc.isZero)
			}
		})
	}
}

func TestUserJSONRoundTrip(t *testing.T) {
	u := User{
		ID:        "u-1",
		Label:     "Test User",
		Roles:     []string{"admin", "developer"},
		Locale:    "zh-CN",
		ModelPref: "deepseek",
		Meta:      map[string]string{"org": "ok"},
	}
	data, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("json.Marshal(User): %v", err)
	}
	var u2 User
	if err := json.Unmarshal(data, &u2); err != nil {
		t.Fatalf("json.Unmarshal(User): %v", err)
	}
	if u2.ID != u.ID || u2.Label != u.Label || len(u2.Roles) != len(u.Roles) {
		t.Errorf("round-trip mismatch: got %+v, want %+v", u2, u)
	}
}

// ─── Value type construction ────────────────────────────────────────────────

func TestRunOptions_Defaults(t *testing.T) {
	o := RunOptions{}
	if o.TimeoutSec != 0 || o.Network {
		t.Errorf("unexpected non-zero defaults: %+v", o)
	}
}

func TestRunResult_Values(t *testing.T) {
	r := RunResult{Output: "hello", ExitCode: 0, Error: ""}
	if r.Output != "hello" || r.ExitCode != 0 || r.Error != "" {
		t.Errorf("RunResult field mismatch: %+v", r)
	}
	r2 := RunResult{Output: "", ExitCode: 137, Error: "timeout"}
	if r2.ExitCode != 137 || r2.Error != "timeout" {
		t.Errorf("RunResult error case: %+v", r2)
	}
}

func TestPluginIsolation_Values(t *testing.T) {
	p := PluginIsolation{Mode: "seccomp", Network: false}
	if p.Mode != "seccomp" || p.Network {
		t.Errorf("PluginIsolation field mismatch: %+v", p)
	}
}

func TestMessage_Values(t *testing.T) {
	m := Message{Role: "user", Content: "hello"}
	if m.Role != "user" || m.Content != "hello" {
		t.Errorf("Message field mismatch: %+v", m)
	}
	if m.Name != "" {
		t.Errorf("Message.Name expected empty: %q", m.Name)
	}
}

func TestRequest_Values(t *testing.T) {
	r := Request{
		Messages:    []Message{{Role: "user", Content: "hi"}},
		Temperature: 0.7,
	}
	if len(r.Messages) != 1 || r.Temperature != 0.7 {
		t.Errorf("Request field mismatch: %+v", r)
	}
}

func TestChunkTypes(t *testing.T) {
	if ChunkText != 0 || ChunkReasoning != 1 || ChunkToolCall != 2 {
		t.Errorf("ChunkType values shifted: %d %d %d", ChunkText, ChunkReasoning, ChunkToolCall)
	}
	if ChunkToolResult != 3 || ChunkUsage != 4 || ChunkError != 5 {
		t.Errorf("ChunkType values shifted: %d %d %d", ChunkToolResult, ChunkUsage, ChunkError)
	}
}

func TestChunk_Values(t *testing.T) {
	c := Chunk{Type: ChunkText, Text: "hello"}
	if c.Type != ChunkText || c.Text != "hello" {
		t.Errorf("Chunk field mismatch: %+v", c)
	}
}

func TestToolSchema_Valid(t *testing.T) {
	s := ToolSchema{
		Name:        "test_tool",
		Description: "A test tool",
		Schema:      json.RawMessage(`{"type":"object"}`),
	}
	if s.Name != "test_tool" || s.Description == "" {
		t.Errorf("ToolSchema field mismatch: %+v", s)
	}
	if !json.Valid(s.Schema) {
		t.Errorf("ToolSchema.Schema is not valid JSON: %s", string(s.Schema))
	}
}

func TestEventKind_Values(t *testing.T) {
	if EventTurnStarted != 0 || EventText != 2 || EventToolCall != 3 {
		t.Errorf("EventKind values shifted: %d %d %d", EventTurnStarted, EventText, EventToolCall)
	}
}

func TestEvent_Values(t *testing.T) {
	e := Event{Kind: EventText, Text: "hello"}
	if e.Kind != EventText || e.Text != "hello" {
		t.Errorf("Event field mismatch: %+v", e)
	}
	e2 := Event{Kind: EventToolCall, Tool: &ToolEvent{ID: "t1", Name: "bash", ReadOnly: false}}
	if e2.Tool == nil || e2.Tool.ID != "t1" || e2.Tool.Name != "bash" {
		t.Errorf("Event.Tool field mismatch: %+v", e2.Tool)
	}
}

func TestToolEvent_Values(t *testing.T) {
	te := ToolEvent{ID: "t1", Name: "bash", Input: `{"command":"ls"}`}
	if te.ID != "t1" || te.Name != "bash" {
		t.Errorf("ToolEvent field mismatch: %+v", te)
	}
}

func TestFileContent_Values(t *testing.T) {
	fc := FileContent{Path: "/test/file.go", Lines: 10, Size: 200}
	if fc.Path != "/test/file.go" || fc.Lines != 10 || fc.Size != 200 {
		t.Errorf("FileContent field mismatch: %+v", fc)
	}
	if fc.Truncated || fc.Binary || fc.Error != "" {
		t.Errorf("FileContent unexpected defaults: %+v", fc)
	}
}

func TestMatch_Values(t *testing.T) {
	m := Match{File: "main.go", Line: 42, Text: "func main()"}
	if m.File != "main.go" || m.Line != 42 || m.Text != "func main()" {
		t.Errorf("Match field mismatch: %+v", m)
	}
}

func TestBashOut_Values(t *testing.T) {
	b := BashOut{Output: "ok", ExitCode: 0}
	if b.Output != "ok" || b.ExitCode != 0 {
		t.Errorf("BashOut field mismatch: %+v", b)
	}
	b2 := BashOut{Output: "error", ExitCode: 127, JobID: "j1"}
	if b2.ExitCode != 127 || b2.JobID != "j1" {
		t.Errorf("BashOut error case: %+v", b2)
	}
}

// ─── Civilization primitives value types ────────────────────────────────────

func TestFact_Values(t *testing.T) {
	f := Fact{
		ID:    "f-1",
		Scope: "project",
		Key:   "test-key",
		Value: "test-value",
	}
	if f.ID != "f-1" || f.Scope != "project" || f.Key != "test-key" || f.Value != "test-value" {
		t.Errorf("Fact field mismatch: %+v", f)
	}
}

func TestFact_JSONRoundTrip(t *testing.T) {
	f := Fact{
		ID:    "f-1",
		Scope: "project",
		Key:   "test-key",
		Value: "test-value",
		Tags:  map[string]string{"env": "test"},
	}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("json.Marshal(Fact): %v", err)
	}
	var f2 Fact
	if err := json.Unmarshal(data, &f2); err != nil {
		t.Fatalf("json.Unmarshal(Fact): %v", err)
	}
	if f2.ID != f.ID || f2.Key != f.Key || f2.Value != f.Value {
		t.Errorf("round-trip mismatch: got %+v, want %+v", f2, f)
	}
}

func TestProofEntry_Values(t *testing.T) {
	pe := ProofEntry{
		AtomID:      "a-1",
		Proposition: "file exists",
		Evidence:    "ls output",
	}
	if pe.AtomID != "a-1" || pe.Proposition != "file exists" || pe.Evidence != "ls output" {
		t.Errorf("ProofEntry field mismatch: %+v", pe)
	}
}

func TestTrustSummary_Values(t *testing.T) {
	ts := TrustSummary{EntryCount: 5, LastAction: "verify", Healthy: true}
	if ts.EntryCount != 5 || !ts.Healthy {
		t.Errorf("TrustSummary field mismatch: %+v", ts)
	}
}

func TestTaskRecord_Values(t *testing.T) {
	tr := TaskRecord{
		Goal:    "build a web app",
		Turns:   3,
		Success: true,
	}
	if tr.Goal != "build a web app" || tr.Turns != 3 || !tr.Success {
		t.Errorf("TaskRecord field mismatch: %+v", tr)
	}
}

func TestToolCallRec_Values(t *testing.T) {
	tc := ToolCallRec{Name: "bash", Args: `{"command":"ls"}`, Order: 1}
	if tc.Name != "bash" || tc.Order != 1 {
		t.Errorf("ToolCallRec field mismatch: %+v", tc)
	}
}

func TestPattern_Values(t *testing.T) {
	p := Pattern{
		ID:          "p-1",
		Description: "use bash for listing",
		Frequency:   3,
		Confidence:  0.8,
	}
	if p.ID != "p-1" || p.Frequency != 3 || p.Confidence != 0.8 {
		t.Errorf("Pattern field mismatch: %+v", p)
	}
}

func TestSkill_Values(t *testing.T) {
	s := Skill{Name: "auto-bash", Version: 1}
	if s.Name != "auto-bash" || s.Version != 1 {
		t.Errorf("Skill field mismatch: %+v", s)
	}
}

func TestLearnStats_Values(t *testing.T) {
	ls := LearnStats{TotalSkills: 5, SuccessRate: 0.9, AvgConfidence: 0.75}
	if ls.TotalSkills != 5 || ls.SuccessRate != 0.9 || ls.AvgConfidence != 0.75 {
		t.Errorf("LearnStats field mismatch: %+v", ls)
	}
}

// ─── Identity tool ──────────────────────────────────────────────────────────

// mockIdentity implements kernel.Identity for testing.
type mockIdentity struct {
	whoami    User
	setUserFn func(ctx context.Context, id string) error
	listUsers []User
	listErr   error
}

func (m *mockIdentity) Whoami(_ context.Context) User               { return m.whoami }
func (m *mockIdentity) SetUser(_ context.Context, id string) error  { return m.setUserFn(nil, id) }
func (m *mockIdentity) ListUsers(_ context.Context) ([]User, error) { return m.listUsers, m.listErr }

func TestIdentityTool_Whoami(t *testing.T) {
	u := User{ID: "u-1", Label: "Test", Locale: "en-US"}
	tool := NewIdentityTool(&mockIdentity{whoami: u})
	result, err := tool.Execute(nil, json.RawMessage(`{"action":"whoami"}`))
	if err != nil {
		t.Fatalf("whoami failed: %v", err)
	}
	if result == "" {
		t.Error("whoami returned empty result")
	}
}

func TestIdentityTool_Whoami_Anonymous(t *testing.T) {
	tool := NewIdentityTool(&mockIdentity{whoami: User{}})
	result, err := tool.Execute(nil, json.RawMessage(`{"action":"whoami"}`))
	if err != nil {
		t.Fatalf("whoami failed: %v", err)
	}
	if result == "" {
		t.Error("whoami returned empty for anonymous")
	}
}

func TestIdentityTool_SetUser(t *testing.T) {
	tool := NewIdentityTool(&mockIdentity{
		whoami: User{ID: "u-2", Label: "Switched"},
		setUserFn: func(_ context.Context, id string) error {
			if id != "u-2" {
				t.Errorf("SetUser id = %q, want %q", id, "u-2")
			}
			return nil
		},
	})
	result, err := tool.Execute(nil, json.RawMessage(`{"action":"set_user","user_id":"u-2"}`))
	if err != nil {
		t.Fatalf("set_user failed: %v", err)
	}
	if result == "" {
		t.Error("set_user returned empty result")
	}
}

func TestIdentityTool_ListUsers(t *testing.T) {
	tool := NewIdentityTool(&mockIdentity{
		listUsers: []User{{ID: "u-1", Label: "Alice"}, {ID: "u-2", Label: "Bob"}},
	})
	result, err := tool.Execute(nil, json.RawMessage(`{"action":"list_users"}`))
	if err != nil {
		t.Fatalf("list_users failed: %v", err)
	}
	if result == "" {
		t.Error("list_users returned empty result")
	}
}

func TestIdentityTool_InvalidAction(t *testing.T) {
	tool := NewIdentityTool(&mockIdentity{})
	_, err := tool.Execute(nil, json.RawMessage(`{"action":"invalid"}`))
	if err == nil {
		t.Error("expected error for invalid action")
	}
}

func TestIdentityTool_InvalidArgs(t *testing.T) {
	tool := NewIdentityTool(&mockIdentity{})
	_, err := tool.Execute(nil, json.RawMessage(`not json`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestIdentityTool_ReadOnly(t *testing.T) {
	tool := NewIdentityTool(&mockIdentity{})
	if !tool.ReadOnly() {
		t.Error("identity tool should be read-only")
	}
}

func TestIdentityTool_Properties(t *testing.T) {
	tool := NewIdentityTool(&mockIdentity{})
	if tool.Name() != "identity" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "identity")
	}
	if tool.Description() == "" {
		t.Error("Description() is empty")
	}
	if len(tool.Schema()) == 0 {
		t.Error("Schema() is empty")
	}
}

// ─── Recall tool ────────────────────────────────────────────────────────────

type mockRecall struct {
	saveFn   func(ctx context.Context, fact Fact) error
	searchFn func(ctx context.Context, query string, limit int) ([]Fact, error)
	forgetFn func(ctx context.Context, query string) (int, error)
}

func (m *mockRecall) Save(ctx context.Context, fact Fact) error { return m.saveFn(ctx, fact) }
func (m *mockRecall) Search(ctx context.Context, q string, l int) ([]Fact, error) {
	return m.searchFn(ctx, q, l)
}
func (m *mockRecall) Forget(ctx context.Context, q string) (int, error) { return m.forgetFn(ctx, q) }

func TestRecallTool_Properties(t *testing.T) {
	tool := NewRecallTool(&mockRecall{})
	if tool.Name() != "recall" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "recall")
	}
	if tool.Description() == "" {
		t.Error("Description() is empty")
	}
	if len(tool.Schema()) == 0 {
		t.Error("Schema() is empty")
	}
}

func TestRecallTool_InvalidArgs(t *testing.T) {
	tool := NewRecallTool(&mockRecall{})
	_, err := tool.Execute(context.Background(), json.RawMessage(`bad`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestRecallTool_EmptyAction(t *testing.T) {
	tool := NewRecallTool(&mockRecall{})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error for empty action")
	}
}

// ─── Trust tool ─────────────────────────────────────────────────────────────

type mockTrust struct {
	recordFn  func(ctx context.Context, entry ProofEntry) error
	verifyFn  func(ctx context.Context, atomID string) error
	exportFn  func(ctx context.Context) ([]ProofEntry, error)
	summaryFn func(ctx context.Context) TrustSummary
}

func (m *mockTrust) Record(ctx context.Context, e ProofEntry) error   { return m.recordFn(ctx, e) }
func (m *mockTrust) Verify(ctx context.Context, id string) error      { return m.verifyFn(ctx, id) }
func (m *mockTrust) Export(ctx context.Context) ([]ProofEntry, error) { return m.exportFn(ctx) }
func (m *mockTrust) Summary(ctx context.Context) TrustSummary         { return m.summaryFn(ctx) }

func TestTrustTool_Properties(t *testing.T) {
	tool := NewTrustTool(&mockTrust{})
	if tool.Name() != "trust" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "trust")
	}
	if tool.Description() == "" {
		t.Error("Description() is empty")
	}
	if len(tool.Schema()) == 0 {
		t.Error("Schema() is empty")
	}
}

func TestTrustTool_InvalidArgs(t *testing.T) {
	tool := NewTrustTool(&mockTrust{})
	_, err := tool.Execute(context.Background(), json.RawMessage(`bad`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// ─── Learn tool ─────────────────────────────────────────────────────────────

type mockLearn struct {
	extractFn  func(ctx context.Context, task TaskRecord) ([]Pattern, error)
	generateFn func(ctx context.Context, patterns []Pattern) (Skill, error)
	validateFn func(ctx context.Context, skill Skill) error
	publishFn  func(ctx context.Context, skill Skill) error
	statsFn    func(ctx context.Context) LearnStats
}

func (m *mockLearn) Extract(ctx context.Context, t TaskRecord) ([]Pattern, error) {
	return m.extractFn(ctx, t)
}
func (m *mockLearn) Generate(ctx context.Context, p []Pattern) (Skill, error) {
	return m.generateFn(ctx, p)
}
func (m *mockLearn) Validate(ctx context.Context, s Skill) error { return m.validateFn(ctx, s) }
func (m *mockLearn) Publish(ctx context.Context, s Skill) error  { return m.publishFn(ctx, s) }
func (m *mockLearn) Stats(ctx context.Context) LearnStats        { return m.statsFn(ctx) }

func TestLearnTool_Properties(t *testing.T) {
	tool := NewLearnTool(&mockLearn{})
	if tool.Name() != "learn" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "learn")
	}
	if tool.Description() == "" {
		t.Error("Description() is empty")
	}
	if len(tool.Schema()) == 0 {
		t.Error("Schema() is empty")
	}
}

func TestLearnTool_Stats(t *testing.T) {
	tool := NewLearnTool(&mockLearn{
		statsFn: func(_ context.Context) LearnStats {
			return LearnStats{TotalSkills: 3, SuccessRate: 1.0}
		},
	})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"stats"}`))
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}
	if result == "" {
		t.Error("stats returned empty result")
	}
}

func TestLearnTool_Generate(t *testing.T) {
	tool := NewLearnTool(&mockLearn{
		generateFn: func(_ context.Context, patterns []Pattern) (Skill, error) {
			return Skill{Name: "auto-bash", Version: 1}, nil
		},
	})
	result, err := tool.Execute(context.Background(),
		json.RawMessage(`{"action":"generate","skill_name":"auto-bash"}`))
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}
	if result == "" {
		t.Error("generate returned empty result")
	}
}

func TestLearnTool_GenerateMissingName(t *testing.T) {
	tool := NewLearnTool(&mockLearn{})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"generate"}`))
	if err == nil {
		t.Error("expected error for missing skill_name")
	}
}

func TestLearnTool_InvalidAction(t *testing.T) {
	tool := NewLearnTool(&mockLearn{})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"bogus"}`))
	if err == nil {
		t.Error("expected error for invalid action")
	}
}

func TestLearnTool_InvalidArgs(t *testing.T) {
	tool := NewLearnTool(&mockLearn{})
	_, err := tool.Execute(context.Background(), json.RawMessage(`bad`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// ─── Format helper ──────────────────────────────────────────────────────────

func TestFormatUser_Values(t *testing.T) {
	tests := []struct {
		user User
	}{
		{User{ID: "u-1", Label: "Test"}},
		{User{}},
	}
	for _, tc := range tests {
		got := formatUser(tc.user)
		if got == "" {
			t.Errorf("formatUser(%+v) is empty", tc.user)
		}
	}
}
