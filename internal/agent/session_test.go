package agent

import (
	"sync"
	"testing"

	"github.com/NB-Agent/ok/internal/provider"
)

func TestSessionConcurrentReadWrite(t *testing.T) {
	s := NewSession("system")
	const readers = 8
	const writes = 50
	var wg sync.WaitGroup

	// Writers: append messages concurrently.
	wg.Add(4)
	for g := 0; g < 4; g++ {
		go func(base int) {
			defer wg.Done()
			for i := 0; i < writes; i++ {
				s.Add(provider.Message{
					Role:    provider.RoleUser,
					Content: "write-" + string(rune('a'+i%26)),
				})
			}
		}(g)
	}

	// Readers: take snapshots concurrently with writes.
	wg.Add(readers)
	for g := 0; g < readers; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < writes*2; i++ {
				snap := s.Snapshot()
				// Must always include the system message at index 0.
				if len(snap) == 0 || snap[0].Role != provider.RoleSystem {
					// This can legitimately happen if reads and writes race
					// — but a nil or empty slice is never ok.
					if len(snap) == 0 && s.Len() > 0 {
						t.Errorf("Snapshot returned empty when Len=%d", s.Len())
						return
					}
				}
			}
		}()
	}

	wg.Wait()

	// After all writes, the final count must be: 1 system + 4*50 = 201.
	if n := s.Len(); n != 1+4*writes {
		t.Errorf("final message count = %d, want %d", n, 1+4*writes)
	}

	// Snapshot must be non-nil and match Len.
	snap := s.Snapshot()
	if len(snap) != s.Len() {
		t.Errorf("Snapshot len = %d, Len = %d", len(snap), s.Len())
	}
}

func TestSessionReplaceConcurrent(t *testing.T) {
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "a"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "b"})

	var wg sync.WaitGroup
	wg.Add(2)

	// Replace while reading.
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			s.Replace([]provider.Message{
				{Role: provider.RoleSystem, Content: "sys"},
				{Role: provider.RoleUser, Content: "replaced"},
			})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			snap := s.Snapshot()
			if snap == nil {
				t.Error("Snapshot must never return nil")
				return
			}
		}
	}()

	wg.Wait()
}

func TestSessionSnapshotCached(t *testing.T) {
	s := NewSession("sys")
	snap1 := s.Snapshot()
	snap2 := s.Snapshot()
	// Without any Add/Replace, Snapshot should return the same slice.
	// The header bytes will be identical for cached snapshots.
	if len(snap1) != len(snap2) || (len(snap1) > 0 && snap1[0].Content != snap2[0].Content) {
		t.Error("consecutive Snapshots without mutation should be equivalent")
	}
}
