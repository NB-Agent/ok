package agent

import (
	"sync"

	"github.com/NB-Agent/ok/internal/provider"
)

// Session holds the conversation history for one task.
// Uses sync.RWMutex so concurrent reads (Snapshot) don't serialize.
// A generation counter avoids copying the message slice on repeated
// Snapshot calls within the same mutation epoch — the cached snapshot
// is returned until the next Add or Replace.
type Session struct {
	mu       sync.RWMutex
	Messages []provider.Message

	gen        uint64
	cachedSnap []provider.Message // invalidated when gen != cachedGen (below)
	cachedGen  uint64

	saveMu sync.Mutex // serialises concurrent Save calls to prevent race on Rename
}

// NewSession initializes a session with an optional system prompt.
func NewSession(system string) *Session {
	s := &Session{}
	if system != "" {
		s.Messages = append(s.Messages, provider.Message{Role: provider.RoleSystem, Content: system})
	}
	return s
}

// Add appends a message. Safe for concurrent use.
func (s *Session) Add(m provider.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Messages = append(s.Messages, m)
	s.gen++
}

// Snapshot returns the current message slice for safe iteration. The
// returned slice shares a backing array with the session — callers MUST
// NOT mutate any element or append to the slice. A generation counter
// ensures stale cached snapshots are refreshed after Add or Replace.
func (s *Session) Snapshot() []provider.Message {
	// Fast path: cache hit under read lock — the common case where
	// the session hasn't changed since the last snapshot.
	s.mu.RLock()
	if s.cachedSnap != nil && s.gen == s.cachedGen {
		msgs := s.cachedSnap
		s.mu.RUnlock()
		return msgs
	}
	s.mu.RUnlock()

	// Cache miss: take the write lock to update the cached snapshot.
	// Double-check under write lock so two racing misses don't both
	// write to cachedSnap/cachedGen without synchronisation.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cachedSnap != nil && s.gen == s.cachedGen {
		return s.cachedSnap
	}
	s.cachedSnap = append([]provider.Message{}, s.Messages...)
	s.cachedGen = s.gen
	return s.cachedSnap
}

// Gen returns the current generation counter. It increments on every Add,
// Replace, and ReplaceIfUnchanged call. Used for detecting concurrent changes.
func (s *Session) Gen() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.gen
}

// ReplaceIfUnchanged atomically swaps the message history only if gen still
// equals expectedGen. Used by compaction to detect concurrent Add calls that
// would otherwise be lost. Returns false when gen has changed (caller should
// retry or skip).
func (s *Session) ReplaceIfUnchanged(msgs []provider.Message, expectedGen uint64) bool {
	if msgs == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen != expectedGen {
		return false // concurrent Add happened — don't overwrite
	}
	s.Messages = msgs
	s.gen++
	return true
}

// Replace atomically swaps the message history. Used by compaction.
// A nil input is rejected to prevent accidental data loss.
func (s *Session) Replace(msgs []provider.Message) {
	if msgs == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Messages = msgs
	s.gen++
}

// Len returns the number of messages. Safe for concurrent use.
func (s *Session) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.Messages)
}

// SkeletonizeAt removed — history is now append-only. File re-reads accumulate
// and are cleaned up by compaction.
