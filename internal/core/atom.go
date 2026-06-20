// Package core defines DST-Lite's core types: ProofChain (a tamper-evident hash chain).
// Deleted DST v2 types (Atom, AtomSet, PCVA stage interfaces, etc.) are gone —
// the current implementation only does L0 compile/test checks, without a
// standalone LLM verification loop.
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

// ProofEntry is a single entry in the verification chain.
type ProofEntry struct {
	Index       int    `json:"index"`
	AtomID      string `json:"atom_id"`
	Proposition string `json:"proposition"`
	Evidence    string `json:"evidence"`
	ParentID    string `json:"parent_id,omitempty"`
	Path        string `json:"path,omitempty"`
	PrevHash    string `json:"prev_hash"`
	Hash        string `json:"hash"`
}

// ProofChain is an append-only hash-chain verification log.
// Each entry is SHA-256 chained to its predecessor, forming a tamper-evident arrow of time.
//
// GenesisHash anchors the chain to a prior state after pruning: when entries are
// pruned to bound memory, the hash of the last pruned entry is saved here so
// external verifiers who stored that hash can still validate the surviving chain.
// An empty GenesisHash means no pruning has occurred.
type ProofChain struct {
	Entries     []ProofEntry `json:"entries"`
	GenesisHash string       `json:"genesis_hash,omitempty"` // hash of last pruned entry, or "" if never pruned
	mu          sync.Mutex   `json:"-"`
}

// NewProofChain creates an empty proof chain.
func NewProofChain() *ProofChain {
	return &ProofChain{
		Entries: make([]ProofEntry, 0),
	}
}

// Len returns the number of entries in the proof chain, thread-safe.
func (pc *ProofChain) Len() int {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return len(pc.Entries)
}

// Append records a proof entry without a task-tree path.
func (pc *ProofChain) Append(atomID, proposition, evidence string) ProofEntry {
	return pc.AppendWithPath(atomID, proposition, evidence, "", "")
}

// AppendWithPath records a verified proposition with its task-tree path.
// atomID must be non-empty; an empty atomID returns a zero-value ProofEntry.
// Entries capped at 2000 — oldest 500 pruned on overflow.
func (pc *ProofChain) AppendWithPath(atomID, proposition, evidence, parentID, path string) ProofEntry {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if atomID == "" {
		return ProofEntry{} // reject: atomID is the primary key for dedup/summary
	}

	// Gradual pruning: every 100 new entries, remove the 25 oldest.
	// This keeps the chain size smooth (~1500-1600 entries) instead of
	// spiking to 2000 then dropping 25% all at once. Burst drops lose
	// too much history at once and make the agent's [Verified state] blink.
	const (
		maxEntries    = 1600
		pruneInterval = 100 // check every N entries
		pruneCount    = 25  // remove this many oldest when over limit
	)
	if len(pc.Entries) >= maxEntries && len(pc.Entries)%pruneInterval == 0 {
		// Save the hash of the last entry BEFORE pruning as the genesis anchor.
		// This preserves external verifiability: any verifier who stored this
		// hash can still validate the surviving chain — they only need to
		// start from the genesis hash instead of an empty string.
		if len(pc.Entries) > 0 {
			pc.GenesisHash = pc.Entries[pruneCount-1].Hash
		}
		pc.Entries = pc.Entries[pruneCount:]
		// Re-hash the surviving chain. The first entry's PrevHash is set to
		// "" (not GenesisHash) because the genesis hash anchors the pruned
		// portion, not the surviving chain's first entry. External verifiers
		// compare GenesisHash against the hash they stored for the last
		// pruned entry; the chain itself starts fresh from index 0.
		for i := range pc.Entries {
			pc.Entries[i].Index = i
			prev := ""
			if i > 0 {
				prev = pc.Entries[i-1].Hash
			}
			pc.Entries[i].PrevHash = prev
			h := sha256.New()
			fmt.Fprintf(h, "%d%s%s%s%s", pc.Entries[i].Index, pc.Entries[i].AtomID,
				pc.Entries[i].Proposition, pc.Entries[i].Evidence, prev)
			pc.Entries[i].Hash = hex.EncodeToString(h.Sum(nil))
		}
	}
	prevHash := ""
	if len(pc.Entries) > 0 {
		prevHash = pc.Entries[len(pc.Entries)-1].Hash
	}
	entry := ProofEntry{
		Index:       len(pc.Entries),
		AtomID:      atomID,
		Proposition: proposition,
		Evidence:    evidence,
		ParentID:    parentID,
		Path:        path,
		PrevHash:    prevHash,
	}
	h := sha256.New()
	fmt.Fprintf(h, "%d%s%s%s%s", entry.Index, entry.AtomID,
		entry.Proposition, entry.Evidence, entry.PrevHash)
	entry.Hash = hex.EncodeToString(h.Sum(nil))

	pc.Entries = append(pc.Entries, entry)
	return entry
}

// VerifyChain validates every entry's hash and prevHash link within the
// surviving chain. When GenesisHash is set (indicating prior pruning), the
// caller should also verify that GenesisHash matches the hash they stored for
// the pruned segment — VerifyChain only validates the surviving portion.
func (pc *ProofChain) VerifyChain() error {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	for i, entry := range pc.Entries {
		h := sha256.New()
		prevHash := ""
		if i > 0 {
			prevHash = pc.Entries[i-1].Hash
		}
		fmt.Fprintf(h, "%d%s%s%s%s", entry.Index, entry.AtomID,
			entry.Proposition, entry.Evidence, prevHash)
		expected := hex.EncodeToString(h.Sum(nil))
		if entry.Hash != expected {
			return fmt.Errorf("entry %d (%s): hash mismatch", i, entry.AtomID)
		}
		if i > 0 && entry.PrevHash != pc.Entries[i-1].Hash {
			return fmt.Errorf("entry %d (%s): prevHash broken link (got %s, want %s)",
				i, entry.AtomID, entry.PrevHash, pc.Entries[i-1].Hash)
		}
	}
	return nil
}

// VerifyExternal validates the surviving chain's internal integrity AND
// checks that GenesisHash matches the provided expectedGenesis (from a
// previously exported audit snapshot). Pass "" for expectedGenesis if
// you only need internal chain validation.
func (pc *ProofChain) VerifyExternal(expectedGenesis string) error {
	if err := pc.VerifyChain(); err != nil {
		return err
	}
	if expectedGenesis != "" && pc.GenesisHash != expectedGenesis {
		return fmt.Errorf("genesis hash mismatch: chain has %s, expected %s (the pruned prefix may have been tampered with)",
			pc.GenesisHash, expectedGenesis)
	}
	return nil
}

// MarshalJSON serializes the proof chain.
func (pc *ProofChain) MarshalJSON() ([]byte, error) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	type alias ProofChain
	return json.Marshal((*alias)(pc))
}

// UnmarshalJSON deserializes the proof chain.
func (pc *ProofChain) UnmarshalJSON(data []byte) error {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	type alias ProofChain
	a := (*alias)(pc)
	return json.Unmarshal(data, a)
}

// Save writes the proof chain to a file.
func (pc *ProofChain) Save(path string) error {
	data, err := pc.MarshalJSON()
	if err != nil {
		return fmt.Errorf("marshal proof chain: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadProofChain reads a proof chain from a file.
func LoadProofChain(path string) (*ProofChain, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read proof chain: %w", err)
	}
	var pc ProofChain
	if err := pc.UnmarshalJSON(data); err != nil {
		return nil, fmt.Errorf("unmarshal proof chain: %w", err)
	}
	return &pc, nil
}

// scanWindow is the number of most recent entries ProofSummary and
// TreeSummary scan when the chain is large. Older entries are still
// in the chain (for tamper-evident audit) but skipped for per-turn
// agent memory — if a verification result was superseded, the dedup
// picks up the newer one from the window anyway.
//
// LIMITATION: in long sessions (>200 tool calls), the model does not
// see the earliest verification results. The full chain is preserved
// for audit (VerifyChain still checks all entries), but the agent's
// per-turn context only reflects the last `scanWindow` entries.
// Increase this value if your sessions routinely exceed 200 writes.
const scanWindow = 200

// ProofSummary returns a compact, deduplicated list of verified items.
// Failures sort first for attention. MaxItems caps output; 0 = unlimited.
// When the chain exceeds scanWindow entries, only the most recent
// scanWindow entries are scanned — older results are still in the
// chain for audit but skipped for per-turn memory.
func (pc *ProofChain) ProofSummary(maxItems int) string {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if len(pc.Entries) == 0 {
		return ""
	}

	type kept struct {
		idx     int
		success bool
	}
	start := 0
	if len(pc.Entries) > scanWindow {
		start = len(pc.Entries) - scanWindow
	}
	dedup := make(map[string]kept)
	for i := start; i < len(pc.Entries); i++ {
		e := pc.Entries[i]
		success := !strings.HasPrefix(strings.TrimSpace(e.Evidence), "FAIL")
		prev, exists := dedup[e.Proposition]
		if !exists || i > prev.idx {
			dedup[e.Proposition] = kept{idx: i, success: success}
		}
	}

	type item struct {
		prop    string
		success bool
	}
	items := make([]item, 0, len(dedup))
	for prop, k := range dedup {
		items = append(items, item{prop: prop, success: k.success})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].success != items[j].success {
			return !items[i].success
		}
		return items[i].prop < items[j].prop
	})

	if maxItems <= 0 || maxItems > len(items) {
		maxItems = len(items)
	}
	if maxItems == 0 {
		return ""
	}
	items = items[:maxItems]

	var b strings.Builder
	b.WriteString("[Verified state]\n")
	for _, it := range items {
		if it.success {
			b.WriteString("✅ ")
		} else {
			b.WriteString("❌ ")
		}
		b.WriteString(it.prop)
		b.WriteString("\n")
	}
	return b.String()
}

// TreeSummary returns a tree-shaped view of recent verification results.
// Entries with a Path are indented by depth; MaxItems caps output.
// Same scanWindow optimization as ProofSummary.
func (pc *ProofChain) TreeSummary(maxItems int) string {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if len(pc.Entries) == 0 {
		return ""
	}

	type node struct {
		path     string
		success  bool
		attempts int
		idx      int
	}
	start := 0
	if len(pc.Entries) > scanWindow {
		start = len(pc.Entries) - scanWindow
	}
	dedup := make(map[string]node)
	for i := start; i < len(pc.Entries); i++ {
		e := pc.Entries[i]
		key := e.Path
		if key == "" {
			key = e.Proposition
		}
		success := !strings.HasPrefix(strings.TrimSpace(e.Evidence), "FAIL")
		prev, exists := dedup[key]
		if !exists {
			dedup[key] = node{path: e.Path, success: success, attempts: 1, idx: i}
		} else {
			// Update: newer entry for same key replaces older one.
			dedup[key] = node{path: e.Path, success: success, attempts: prev.attempts + 1, idx: i}
		}
	}

	type item struct {
		path string
		succ bool
		atts int
	}
	items := make([]item, 0, len(dedup))
	for _, n := range dedup {
		items = append(items, item{path: n.path, succ: n.success, atts: n.attempts})
	}
	sort.Slice(items, func(i, j int) bool {
		di := strings.Count(items[i].path, "→")
		dj := strings.Count(items[j].path, "→")
		if di != dj {
			return di < dj
		}
		if items[i].succ != items[j].succ {
			return !items[i].succ
		}
		return items[i].path < items[j].path
	})

	if maxItems <= 0 || maxItems > len(items) {
		maxItems = len(items)
	}
	if maxItems == 0 {
		return ""
	}
	items = items[:maxItems]

	var b strings.Builder
	b.WriteString("[Task Tree]\n")
	for _, it := range items {
		indent := strings.Count(it.path, "→")
		prefix := strings.Repeat("  ", indent)
		tag := ""
		if it.atts > 1 {
			tag = fmt.Sprintf(" (%d attempts)", it.atts)
		}
		if it.succ {
			b.WriteString(fmt.Sprintf("%s✅ %s%s\n", prefix, leafName(it.path), tag))
		} else {
			b.WriteString(fmt.Sprintf("%s❌ %s%s\n", prefix, leafName(it.path), tag))
		}
	}
	return b.String()
}

// leafName returns the last segment of a path like "X→A→A1".
func leafName(path string) string {
	if path == "" {
		return "(root)"
	}
	parts := strings.Split(path, "→")
	last := strings.TrimSpace(parts[len(parts)-1])
	if last == "" && len(parts) > 1 {
		last = strings.TrimSpace(parts[len(parts)-2])
	}
	return last
}
