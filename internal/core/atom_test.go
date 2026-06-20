package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProofChainAppendAndVerify(t *testing.T) {
	pc := NewProofChain()
	if len(pc.Entries) != 0 {
		t.Error("new chain should be empty")
	}

	e1 := pc.Append("a1", "prop 1", "evidence 1")
	if e1.Index != 0 {
		t.Errorf("first index = %d, want 0", e1.Index)
	}
	if e1.PrevHash != "" {
		t.Error("first entry should have empty PrevHash")
	}
	if e1.Hash == "" {
		t.Error("hash should not be empty")
	}

	e2 := pc.AppendWithPath("a2", "prop 2", "evidence 2", "a1", "X→A")
	if e2.Index != 1 {
		t.Errorf("second index = %d, want 1", e2.Index)
	}
	if e2.PrevHash != e1.Hash {
		t.Error("PrevHash should match previous entry's Hash")
	}
	if e2.Path != "X→A" {
		t.Errorf("path = %q, want X→A", e2.Path)
	}

	if err := pc.VerifyChain(); err != nil {
		t.Errorf("chain should verify after valid appends: %v", err)
	}

	pc.Entries[0].Hash = "tampered"
	if pc.VerifyChain() == nil {
		t.Error("chain should fail after tampering")
	}
}

func TestProofChainSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proof.json")

	pc := NewProofChain()
	pc.Append("a1", "prop 1", "evidence 1")
	pc.Append("a2", "prop 2", "ok")

	if err := pc.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadProofChain(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.Entries) != 2 {
		t.Fatalf("loaded entries = %d, want 2", len(loaded.Entries))
	}
	if err := loaded.VerifyChain(); err != nil {
		t.Errorf("loaded chain should verify: %v", err)
	}
}

func TestLoadProofChainMissing(t *testing.T) {
	_, err := LoadProofChain(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestProofSummary(t *testing.T) {
	pc := NewProofChain()
	pc.Append("a1", "file exists", "OK")
	pc.Append("a2", "test passes", "FAIL: some error")

	summary := pc.ProofSummary(0)
	if !strings.Contains(summary, "✅") {
		t.Error("summary should contain ✅")
	}
	if !strings.Contains(summary, "❌") {
		t.Error("summary should contain ❌")
	}

	short := pc.ProofSummary(1)
	lines := strings.Split(strings.TrimSpace(short), "\n")
	if len(lines) > 2 { // header + 1 item
		t.Errorf("truncated summary too long (%d lines):\n%s", len(lines), short)
	}
}

func TestProofSummaryEmpty(t *testing.T) {
	pc := NewProofChain()
	if s := pc.ProofSummary(10); s != "" {
		t.Errorf("empty chain summary should be empty, got %q", s)
	}
}

func TestTreeSummary(t *testing.T) {
	pc := NewProofChain()
	pc.AppendWithPath("a1", "root task", "OK", "", "X")
	pc.AppendWithPath("a2", "sub task 1", "OK", "a1", "X→A")
	pc.AppendWithPath("a3", "sub task 2", "FAIL: bug", "a1", "X→B")

	ts := pc.TreeSummary(0)
	if !strings.Contains(ts, "✅") {
		t.Error("tree summary should contain ✅ for success")
	}
	if !strings.Contains(ts, "❌") {
		t.Error("tree summary should contain ❌ for failure")
	}
	if !strings.Contains(ts, "  ✅") || !strings.Contains(ts, "  ❌") {
		t.Errorf("tree summary should contain indented sub-tasks:\n%s", ts)
	}
}

func TestLeafName(t *testing.T) {
	cases := map[string]string{
		"X→A→B": "B",
		"X→A":   "A",
		"X":     "X",
		"":      "(root)",
		"X→A→":  "A",
	}
	for path, want := range cases {
		if got := leafName(path); got != want {
			t.Errorf("leafName(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestMarshalUnmarshalProofChain(t *testing.T) {
	pc := NewProofChain()
	pc.AppendWithPath("a1", "prop", "ev", "parent", "X→A")

	data, err := pc.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored ProofChain
	if err := restored.UnmarshalJSON(data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(restored.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(restored.Entries))
	}
	if restored.Entries[0].Path != "X→A" {
		t.Errorf("path = %q", restored.Entries[0].Path)
	}
}

func TestProofChainConcurrent(t *testing.T) {
	pc := NewProofChain()
	done := make(chan struct{})
	const n = 100

	go func() {
		for i := 0; i < n; i++ {
			pc.Append("a", "prop", "ev")
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < n; i++ {
			pc.ProofSummary(10)
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < n; i++ {
			pc.TreeSummary(10)
		}
		done <- struct{}{}
	}()

	for i := 0; i < 3; i++ {
		<-done
	}
	if err := pc.VerifyChain(); err != nil {
		t.Errorf("chain should verify after concurrent appends: %v", err)
	}
}

func TestSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proof.json")

	pc := NewProofChain()
	pc.Append("a", "p", "e")
	if err := pc.Save(path); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o600 != 0o600 {
		t.Errorf("file perms = %o, owner should have rw", fi.Mode().Perm())
	}
}

func TestAppendWithPathRejectsEmptyAtomID(t *testing.T) {
	pc := NewProofChain()
	e := pc.AppendWithPath("", "prop", "evid", "", "")
	if e.AtomID != "" || e.Hash != "" {
		t.Error("AppendWithPath with empty atomID must return zero-value ProofEntry")
	}
	if len(pc.Entries) != 0 {
		t.Errorf("chain should be empty after rejected append, got %d entries", len(pc.Entries))
	}
}

func TestProofChainOverflowPrune(t *testing.T) {
	pc := NewProofChain()
	// Append 2500 entries. Progressive pruning: 1600 max, prune 25 every 100.
	// After 2500 appends, count settles around ~1600.
	for i := 0; i < 2500; i++ {
		id := fmt.Sprintf("a%d", i)
		pc.Append(id, "prop", "ev")
	}
	got := len(pc.Entries)
	if got < 1500 || got > 1700 {
		t.Errorf("after 2500 appends, expected ~1600 entries, got %d", got)
	}
	// First surviving entry must have empty PrevHash (predecessor pruned).
	if pc.Entries[0].PrevHash != "" {
		t.Errorf("first surviving entry PrevHash must be empty after rehash, got %q", pc.Entries[0].PrevHash)
	}
	// The last entry should be the most recent.
	if pc.Entries[len(pc.Entries)-1].AtomID != "a2499" {
		t.Errorf("last entry = %q, want a2499", pc.Entries[len(pc.Entries)-1].AtomID)
	}
	// Chain must still verify.
	if err := pc.VerifyChain(); err != nil {
		t.Errorf("chain should verify after overflow prune: %v", err)
	}
}
