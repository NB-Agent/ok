// Package evolution — P2: Automated forgetting mechanism.
//
// Low-value memories decay over time. The forgetter periodically:
//  1. Ages episodic memories: deletes entries older than maxAge
//  2. Ages candidates: marks old pending-review candidates as "stale"
//  3. Limits total episodic count: keeps only the most recent N entries
package evolution

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// maxEpisodicAge is how long an episodic memory survives before deletion.
	maxEpisodicAge = 7 * 24 * time.Hour // 7 days

	// maxEpisodicEntries caps total episodic files.
	maxEpisodicEntries = 100

	// candidateStaleAge is how long a pending-review candidate waits before
	// being auto-rejected.
	candidateStaleAge = 14 * 24 * time.Hour // 14 days

	// forgetIntervalTurns is how often forget() runs.
	forgetIntervalTurns = 10
)

// forget ages out old episodic memories and low-value candidates using
// usage-based scoring (recency × activity) rather than pure time decay.
// Returns (episodicDeleted, candidatesStaled).
func (e *Engine) forget() (episodicDeleted int, candidatesStaled int) {
	if e.dir == "" {
		return 0, 0
	}

	now := time.Now()

	// 1. Age episodic memories (time-based, since episodic volume control needs it)
	episodicDeleted = e.ageEpisodic(now)

	// 2. Score-and-forget candidates (usage-based, replaces pure time decay)
	candidatesStaled = e.scoreAndForget(now)

	return
}

// ageEpisodic deletes episodic entries older than maxAge and limits total count.
func (e *Engine) ageEpisodic(now time.Time) int {
	episodicDir := filepath.Join(e.dir, "episodic")
	entries, err := os.ReadDir(episodicDir)
	if err != nil {
		return 0
	}

	// Collect files with mod time
	type entry struct {
		name    string
		modTime time.Time
	}
	var files []entry
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".md") {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		files = append(files, entry{name: ent.Name(), modTime: info.ModTime()})
	}

	if len(files) == 0 {
		return 0
	}

	// Delete files older than maxEpisodicAge
	deleted := 0
	var kept []entry
	for _, f := range files {
		if now.Sub(f.modTime) > maxEpisodicAge {
			path := filepath.Join(episodicDir, f.name)
			if err := os.Remove(path); err == nil {
				deleted++
			}
			continue
		}
		kept = append(kept, f)
	}

	// If still over limit, delete the oldest
	if len(kept) > maxEpisodicEntries {
		sort.Slice(kept, func(i, j int) bool {
			return kept[i].modTime.Before(kept[j].modTime)
		})
		toDelete := len(kept) - maxEpisodicEntries
		for i := 0; i < toDelete; i++ {
			path := filepath.Join(episodicDir, kept[i].name)
			if err := os.Remove(path); err == nil {
				deleted++
			}
		}
	}

	if deleted > 0 {
		log.Printf("evolution: forget: deleted %d aged episodic memories", deleted)
	}
	return deleted
}

// ageCandidates marks old pending-review candidates as "stale".
func (e *Engine) ageCandidates(now time.Time) int {
	candidatesDir := filepath.Join(e.dir, "candidates")
	entries, err := os.ReadDir(candidatesDir)
	if err != nil {
		return 0
	}

	staled := 0
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".md") {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > candidateStaleAge {
			path := filepath.Join(candidatesDir, ent.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			content := string(data)
			// Only stale pending-review candidates, not already-processed ones
			if strings.Contains(content, "status: pending-review") {
				newContent := strings.Replace(content, "status: pending-review", "status: stale", 1)
				// Best-effort write: if this fails, the stale status is lost for
				// this turn but will be re-attempted on the next forget cycle.
				if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
					log.Printf("evolution: forget: write stale: %v", err)
				}
				staled++
			}
		}
	}

	if staled > 0 {
		log.Printf("evolution: forget: marked %d stale candidates", staled)
	}
	return staled
}
