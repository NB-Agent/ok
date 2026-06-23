package evolution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		s    string
		max  int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "he..."},
		{"", 5, ""},
		{"abc", 3, "abc"},
		{"abcdef", 6, "abcdef"},
	}
	for _, tc := range tests {
		got := truncate(tc.s, tc.max)
		if got != tc.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.s, tc.max, got, tc.want)
		}
	}
}

func TestFindPatterns_NoMatch(t *testing.T) {
	recent := []string{"hello world", "foo bar", "baz qux"}
	patterns := findPatterns(recent)
	if len(patterns) != 0 {
		t.Errorf("expected no patterns, got %v", patterns)
	}
}

func TestFindPatterns_RepeatedTool(t *testing.T) {
	recent := []string{
		"Running bash command: ls",
		"Running bash command: pwd",
		"Running bash command: echo",
		"Using git to clone",
		"Using git to commit",
		"Using git to push",
	}
	patterns := findPatterns(recent)
	if len(patterns) == 0 {
		t.Fatal("expected patterns")
	}
	foundBash, foundGit := false, false
	for _, p := range patterns {
		if strings.Contains(p, "bash") {
			foundBash = true
		}
		if strings.Contains(p, "git") {
			foundGit = true
		}
	}
	if !foundBash {
		t.Errorf("expected bash pattern, got %v", patterns)
	}
	if !foundGit {
		t.Errorf("expected git pattern, got %v", patterns)
	}
}

func TestSaveEpisodicMemory(t *testing.T) {
	dir := t.TempDir()
	e := New(nil, nil, dir)
	e.turnCount = 1
	e.saveEpisodicMemory("test input", "test output")

	path := filepath.Join(dir, "episodic", "turn-1.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read episodic: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "test input") {
		t.Errorf("missing input in episodic: %s", content)
	}
	if !strings.Contains(content, "test output") {
		t.Errorf("missing output in episodic: %s", content)
	}
}

func TestSaveSkillCandidate(t *testing.T) {
	dir := t.TempDir()
	e := New(nil, nil, dir)
	e.saveSkillCandidate([]string{"repeated-tool:bash:3"})

	entries, err := os.ReadDir(filepath.Join(dir, "candidates"))
	if err != nil {
		t.Fatalf("read candidates: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one candidate")
	}
	data, err := os.ReadFile(filepath.Join(dir, "candidates", entries[0].Name()))
	if err != nil {
		t.Fatalf("read candidate: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "repeated-tool") {
		t.Errorf("missing pattern in candidate: %s", content)
	}
	if !strings.Contains(content, "pending-review") {
		t.Errorf("missing status in candidate: %s", content)
	}
}

func TestOnTurnComplete_NilMem(t *testing.T) {
	e := New(nil, nil, t.TempDir())
	e.OnTurnComplete(nil, "input", "output")
}

func TestDetectAndGenerate_NoEpisodic(t *testing.T) {
	e := New(nil, nil, t.TempDir())
	e.detectAndGenerate(nil)
}

func TestNewEngine(t *testing.T) {
	e := New(nil, nil, "")
	if e == nil {
		t.Fatal("New returned nil")
	}
}

// ─── Sequence pattern tests ──────────────────────────────────────────────

func TestOrderedTools(t *testing.T) {
	tests := []struct {
		entry string
		want  []string
	}{
		{"Running bash command: ls", []string{"bash"}},
		{"Using grep to search then read_file to open", []string{"grep", "read_file"}},
		{"No tools here", nil},
	}
	for _, tc := range tests {
		got := orderedTools(tc.entry)
		if len(got) != len(tc.want) {
			t.Errorf("orderedTools(%q) = %v, want %v", tc.entry, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("orderedTools(%q)[%d] = %q, want %q", tc.entry, i, got[i], tc.want[i])
			}
		}
	}
}

func TestDetectSequencePatterns_NoMatch(t *testing.T) {
	recent := []string{"hello world", "foo bar"}
	patterns := detectSequencePatterns(recent)
	if len(patterns) != 0 {
		t.Errorf("expected no patterns, got %v", patterns)
	}
}

func TestDetectSequencePatterns_SingleEntry(t *testing.T) {
	recent := []string{"running bash to grep"}
	patterns := detectSequencePatterns(recent)
	if len(patterns) != 0 {
		t.Errorf("expected no patterns from single entry, got %v", patterns)
	}
}

func TestDetectSequencePatterns_RepeatedSequence(t *testing.T) {
	recent := []string{
		"Running bash command then grep the output",
		"Using bash and grep together",
		"Again bash with grep to search",
	}
	patterns := detectSequencePatterns(recent)
	found := false
	for _, p := range patterns {
		if strings.Contains(p, "bash") && strings.Contains(p, "grep") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected bash→grep sequence pattern, got %v", patterns)
	}
}

func TestGenerateSkillBody_WithSequence(t *testing.T) {
	body := generateSkillBody("auto-search", []string{"sequence:bash\u2192grep\u2192read_file:2",
		"repeated-tool:bash:3"})
	if !strings.Contains(body, "bash") {
		t.Errorf("missing bash in body: %s", body)
	}
	if !strings.Contains(body, "grep") {
		t.Errorf("missing grep in body: %s", body)
	}
	if !strings.Contains(body, "read_file") {
		t.Errorf("missing read_file in body: %s", body)
	}
	if !strings.Contains(body, "Detected Workflow Sequences") {
		t.Errorf("missing sequence section in body: %s", body)
	}
}

func TestReadCandidate_SequencePattern(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "candidate-seq.md")
	content := `---
status: pending-review
---

repeated-tool:bash:3
sequence:bash→grep:2
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	e := New(nil, nil, dir)
	cand := e.readCandidate(path)
	if cand == nil {
		t.Fatal("expected candidate")
	}
	if cand.Status != "pending-review" {
		t.Errorf("expected pending-review, got %q", cand.Status)
	}
	if len(cand.Patterns) != 2 {
		t.Errorf("expected 2 patterns, got %v", cand.Patterns)
	}
}

// ─── P1 tests ───────────────────────────────────────────────────────────

func TestValidatePattern_NoEpisodic(t *testing.T) {
	e := New(nil, nil, t.TempDir())
	if e.validatePattern([]string{"repeated-tool:bash:3"}) {
		t.Error("expected false when no episodic memory")
	}
}

func TestPatternToSkillName(t *testing.T) {
	name := patternToSkillName([]string{"repeated-tool:bash:3", "repeated-tool:git:2"})
	if !strings.HasPrefix(name, "auto-") {
		t.Errorf("expected auto- prefix, got %q", name)
	}
	if !strings.Contains(name, "bash") {
		t.Errorf("expected bash in name, got %q", name)
	}
}

func TestGenerateSkillBody(t *testing.T) {
	body := generateSkillBody("auto-bash", []string{"repeated-tool:bash:3"})
	if !strings.Contains(body, "auto-bash") {
		t.Errorf("missing skill name in body: %s", body)
	}
	if !strings.Contains(body, "bash") {
		t.Errorf("missing tool reference in body: %s", body)
	}
	if !strings.Contains(body, "---") {
		t.Errorf("missing frontmatter in body: %s", body)
	}
}

func TestValidateAndInstall_NilStore(t *testing.T) {
	e := New(nil, nil, t.TempDir())
	i, m, s := e.validateAndInstall()
	if i != 0 || m != 0 || s != 0 {
		t.Errorf("expected all zero with nil store, got %d/%d/%d", i, m, s)
	}
}

func TestReadCandidate_Invalid(t *testing.T) {
	e := New(nil, nil, t.TempDir())
	if e.readCandidate("/nonexistent") != nil {
		t.Error("expected nil for nonexistent file")
	}
}

func TestPatternToSkillName_Empty(t *testing.T) {
	if name := patternToSkillName(nil); name != "auto-skill" {
		t.Errorf("expected auto-skill, got %q", name)
	}
}

func TestExtractToolNames(t *testing.T) {
	names := extractToolNames([]string{"repeated-tool:bash:3", "repeated-tool:git:2"})
	if len(names) != 2 {
		t.Errorf("expected 2 names, got %v", names)
	}
	if names[0] != "bash" {
		t.Errorf("expected bash, got %q", names[0])
	}
}

func TestExtractToolNames_Sequence(t *testing.T) {
	names := extractToolNames([]string{"sequence:bash\u2192grep\u2192read_file:2"})
	if len(names) != 3 {
		t.Errorf("expected 3 names, got %v", names)
	}
	if names[0] != "bash" || names[1] != "grep" || names[2] != "read_file" {
		t.Errorf("expected [bash grep read_file], got %v", names)
	}
}

// ─── P2 tests ───────────────────────────────────────────────────────────

func TestForget_EmptyDir(t *testing.T) {
	e := New(nil, nil, t.TempDir())
	d, s := e.forget()
	if d != 0 || s != 0 {
		t.Errorf("expected 0/0, got %d/%d", d, s)
	}
}

func TestAgeEpisodic_Empty(t *testing.T) {
	e := New(nil, nil, t.TempDir())
	deleted := e.ageEpisodic(time.Now())
	if deleted != 0 {
		t.Errorf("expected 0 deleted, got %d", deleted)
	}
}

func TestAgeCandidates_Empty(t *testing.T) {
	e := New(nil, nil, t.TempDir())
	staled := e.ageCandidates(time.Now())
	if staled != 0 {
		t.Errorf("expected 0 staled, got %d", staled)
	}
}

func TestOnTurnComplete_FullCycle(t *testing.T) {
	dir := t.TempDir()
	e := New(nil, nil, dir)
	// 30 turns should trigger all phases
	for i := 0; i < 30; i++ {
		e.OnTurnComplete(nil, "input", "output")
	}
	// Should not panic
}

func TestSkillFileExists_NoFile(t *testing.T) {
	if skillFileExists("nonexistent-skill-xyz") {
		t.Error("expected false for nonexistent skill")
	}
}
