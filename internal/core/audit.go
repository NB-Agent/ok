package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// AuditRecord is one entry in the audit trail — a tamper-evident log of every
// tool execution, chained via SHA-256 hashes.
type AuditRecord struct {
	Index     int       `json:"index"`
	Timestamp time.Time `json:"timestamp"`
	Tool      string    `json:"tool"`
	Args      string    `json:"args,omitempty"`
	Result    string    `json:"result,omitempty"`
	Allowed   bool      `json:"allowed"`
	PrevHash  string    `json:"prev_hash"`
	Hash      string    `json:"hash"`
}

// AuditChain is an append-only hash-chained audit log. Each entry's Hash is
// SHA-256(Index|Tool|Args|Result|Allowed|PrevHash), chained to the previous
// entry's Hash, forming a tamper-evident ProofChain of tool executions.
type AuditChain struct {
	mu      sync.Mutex    `json:"-"`
	Entries []AuditRecord `json:"entries"`
}

// NewAuditChain creates an empty audit chain.
func NewAuditChain() *AuditChain {
	return &AuditChain{
		Entries: make([]AuditRecord, 0),
	}
}

// Append records one tool execution in the audit chain. It computes the hash
// link automatically: Index = len(Entries), PrevHash = previous entry's Hash
// (empty string for the first entry).
func (ac *AuditChain) Append(tool, args, result string, allowed bool) AuditRecord {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	index := len(ac.Entries)
	prevHash := ""
	if index > 0 {
		prevHash = ac.Entries[index-1].Hash
	}

	record := AuditRecord{
		Index:     index,
		Timestamp: time.Now().UTC(),
		Tool:      tool,
		Args:      args,
		Result:    result,
		Allowed:   allowed,
		PrevHash:  prevHash,
	}

	// Hash = sha256(fmt.Sprintf("%d|%s|%s|%s|%v|%s", index, tool, args, result, allowed, prevHash))
	h := sha256.New()
	fmt.Fprintf(h, "%d|%s|%s|%s|%v|%s",
		record.Index, record.Tool, record.Args, record.Result, record.Allowed, record.PrevHash)
	record.Hash = hex.EncodeToString(h.Sum(nil))

	ac.Entries = append(ac.Entries, record)
	return record
}

// Len returns the number of entries in the chain.
func (ac *AuditChain) Len() int {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	return len(ac.Entries)
}

// Recent returns the most recent n entries. If n <= 0 or n > len(Entries),
// it returns all entries.
func (ac *AuditChain) Recent(n int) []AuditRecord {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	if n <= 0 || n > len(ac.Entries) {
		n = len(ac.Entries)
	}
	if n == 0 {
		return nil
	}
	start := len(ac.Entries) - n
	result := make([]AuditRecord, n)
	copy(result, ac.Entries[start:])
	return result
}

// All returns a copy of every entry in the chain.
func (ac *AuditChain) All() []AuditRecord {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	result := make([]AuditRecord, len(ac.Entries))
	copy(result, ac.Entries)
	return result
}

// VerifyChain validates every entry's hash and prevHash link, returning
// a descriptive error on the first mismatch.
func (ac *AuditChain) VerifyChain() error {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	for i, entry := range ac.Entries {
		prevHash := ""
		if i > 0 {
			prevHash = ac.Entries[i-1].Hash
		}
		h := sha256.New()
		fmt.Fprintf(h, "%d|%s|%s|%s|%v|%s",
			entry.Index, entry.Tool, entry.Args, entry.Result, entry.Allowed, prevHash)
		expected := hex.EncodeToString(h.Sum(nil))

		if entry.Hash != expected {
			return fmt.Errorf("entry %d (%s): hash mismatch", i, entry.Tool)
		}
		if i > 0 && entry.PrevHash != ac.Entries[i-1].Hash {
			return fmt.Errorf("entry %d (%s): prevHash broken link", i, entry.Tool)
		}
	}
	return nil
}

// MarshalJSON serializes the audit chain.
func (ac *AuditChain) MarshalJSON() ([]byte, error) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	type alias AuditChain
	return json.Marshal((*alias)(ac))
}

// UnmarshalJSON deserializes the audit chain.
func (ac *AuditChain) UnmarshalJSON(data []byte) error {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	type alias AuditChain
	a := (*alias)(ac)
	return json.Unmarshal(data, a)
}

// Save writes the audit chain to a file.
func (ac *AuditChain) Save(path string) error {
	data, err := ac.MarshalJSON()
	if err != nil {
		return fmt.Errorf("marshal audit chain: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}
