// Package evolution — P2: Active forgetting via usage-based scoring.
//
// Replaces the pure time-decay model with a weighted scoring system.
// Candidates are scored on two axes using file metadata and content heuristics:
//
//	recency  — how recently was the candidate modified?  (weight 0.5)
//	activity — how many times has the pattern recurred?   (weight 0.5)
//
// Items scoring below the retention threshold are candidates for forgetting.
package evolution

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// retentionThreshold is the minimum score to keep an item.
const retentionThreshold = 0.25

// scoreAndForget applies usage-based scoring to all candidates, replacing
// pure time-decay. Uses file modification time as recency proxy and pattern
// recurrence count as activity signal.
func (e *Engine) scoreAndForget(now time.Time) int {
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
		path := filepath.Join(candidatesDir, ent.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		if !strings.Contains(content, "status: pending-review") {
			continue
		}

		score := candidateScore(info.ModTime(), content)

		if score < retentionThreshold {
			reason := "low score"
			if now.Sub(info.ModTime()) > candidateStaleAge {
				reason = "stale"
			}
			newContent := strings.Replace(content, "status: pending-review", "status: stale ("+reason+")", 1)
			if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
				log.Printf("evolution: score-forget: write: %v", err)
			} else {
				staled++
			}
		}
	}

	if staled > 0 {
		log.Printf("evolution: score-forget: marked %d low-value candidates stale", staled)
	}
	return staled
}

// candidateScore computes a retention score [0.0, 1.0] for a candidate file.
// Higher = more worth keeping.
func candidateScore(modTime time.Time, content string) float64 {
	// Recency: 1.0 if modified today, decays to 0 over 30 days.
	daysSince := time.Since(modTime).Hours() / 24
	recency := 1.0
	if daysSince > 0 {
		recency = 1.0 - (daysSince / 30)
	}
	if recency < 0 {
		recency = 0
	}

	// Activity: count pattern occurrences as a proxy for recurrence.
	patternCount := strings.Count(content, "repeated-tool:") +
		strings.Count(content, "sequence:")

	activity := float64(patternCount) / 10
	if activity > 1.0 {
		activity = 1.0
	}

	return recency*0.5 + activity*0.5
}
