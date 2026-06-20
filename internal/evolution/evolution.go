// Package evolution automatically extracts experiences from agent turns and
// generates skill candidates. This is OK's self-evolution engine.
//
// Architecture:
//
//	OnTurnComplete hook (called from agent.Run after each turn)
//	  → saveEpisodicMemory() — save turn summary as memory
//	  → detectPatterns()     — scan recent memory for repeatable patterns
//	  → saveSkillCandidate()  — when pattern repeats, propose skill
//
// Efficiency: episodic memories are buffered in memory and flushed to disk
// only every 3 turns, reducing I/O by ~66%.
package evolution

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NB-Agent/ok/internal/memory"
	"github.com/NB-Agent/ok/internal/skill"
)

// Engine is the self-evolution engine. Zero value is safe (no-op).
type Engine struct {
	mem       *memory.Set
	skillStor *skill.Store
	dir       string // memory directory for episodic storage

	mu        sync.Mutex
	turnCount int
	lastCheck int
	memBuffer []episodicEntry // in-memory buffer, flushed to disk every 3 turns
}

type episodicEntry struct {
	input  string
	output string
}

// New creates an evolution engine.
func New(mem *memory.Set, skStore *skill.Store, dir string) *Engine {
	return &Engine{
		mem:       mem,
		skillStor: skStore,
		dir:       dir,
	}
}

// OnTurnComplete is called by the agent after each completed turn.
// Episodic data is buffered in memory and flushed to disk every 3 turns.
func (e *Engine) OnTurnComplete(ctx context.Context, input, output string) {
	if e.mem == nil {
		return
	}
	e.mu.Lock()
	e.turnCount++
	tc := e.turnCount

	// Buffer this episode in memory
	e.memBuffer = append(e.memBuffer, episodicEntry{input, output})

	// Flush to disk every 3 turns
	if tc%3 == 0 && len(e.memBuffer) > 0 {
		e.flushBuffer()
	}
	e.mu.Unlock()

	// P0: Pattern detection — every 3 turns (from in-memory buffer)
	if tc%3 == 0 && tc > e.lastCheck {
		e.mu.Lock()
		e.lastCheck = tc
		buf := make([]string, len(e.memBuffer))
		for i, entry := range e.memBuffer {
			buf[i] = entry.input + "\n" + entry.output
		}
		e.mu.Unlock()
		e.detectAndGenerate(buf)
	}

	// P1: Validate and install candidates — every 6 turns
	if tc%6 == 0 {
		e.validateAndInstall()
	}

	// P2: Forgetting — every 10 turns
	if tc%forgetIntervalTurns == 0 {
		e.forget()
	}
}

// flushBuffer writes buffered episodic memories to disk.
func (e *Engine) flushBuffer() {
	if e.dir == "" {
		return
	}
	for _, entry := range e.memBuffer {
		e.saveEpisodicMemory(entry.input, entry.output)
	}
	e.memBuffer = nil
}

func (e *Engine) saveEpisodicMemory(input, output string) {
	ts := time.Now().UTC().Format(time.RFC3339)
	in := truncate(input, 200)
	out := truncate(output, 500)

	body := fmt.Sprintf(`---
key: episodic-%d
type: episodic
created: %s
source: auto-evolution
---

## Input
%s

## Output
%s
`, e.turnCount, ts, in, out)

	dir := filepath.Join(e.dir, "episodic")
	// Best-effort: OnTurnComplete is a hook callback with no error return path.
	// Failures are logged; the agent continues with slightly degraded evolution.
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("evolution: mkdir episodic: %v (evolution data for turn %d lost)", err, e.turnCount)
		return
	}
	path := filepath.Join(dir, fmt.Sprintf("turn-%d.md", e.turnCount))
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		log.Printf("evolution: write episodic: %v", err)
	}
}

// detectAndGenerate scans recent turns for patterns across 4 layers:
//
//	Layer 0 — individual tool frequency (findPatterns)
//	Layer 1 — 2/3-step tool sequences (detectSequencePatterns)
//	Layer 2 — named workflow signatures (detectWorkflows)
//	Layer 3 — cross-turn fingerprint patterns (detectFingerprintPatterns)
//
// Layers 0/1 are the original keyword-based detectors. Layers 2/3 are the
// semantic upgrade (Gap 1) — zero-LLM but richer signal.
func (e *Engine) detectAndGenerate(recent []string) {
	patterns := findPatterns(recent)
	patterns = append(patterns, detectSequencePatterns(recent)...)
	patterns = append(patterns, semanticPatterns(recent)...)
	if len(patterns) > 0 {
		e.saveSkillCandidate(patterns)
	}
}

// trackedTools is the set of tools the pattern detector scans for.
var trackedTools = []string{"bash", "grep", "read_file", "write_file",
	"edit_file", "web_fetch", "git", "database", "docker"}

func findPatterns(recent []string) []string {
	toolCount := make(map[string]int)
	for _, entry := range recent {
		lower := strings.ToLower(entry)
		for _, tool := range trackedTools {
			if strings.Contains(lower, tool) {
				toolCount[tool]++
			}
		}
	}
	var patterns []string
	for tool, count := range toolCount {
		if count >= 3 {
			patterns = append(patterns, fmt.Sprintf("repeated-tool:%s:%d", tool, count))
		}
	}
	return patterns
}

// orderedTools returns the tools mentioned in entry in detection order.
func orderedTools(entry string) []string {
	lower := strings.ToLower(entry)
	var seen []string
	for _, tool := range trackedTools {
		if strings.Contains(lower, tool) {
			seen = append(seen, tool)
		}
	}
	return seen
}

// detectSequencePatterns looks for tool-usage sequences that repeat across
// multiple turns (e.g. bash→grep→read_file). A 2-step or 3-step sequence
// qualifies when the same ordered sub-sequence appears >= 2 times.
func detectSequencePatterns(recent []string) []string {
	type seqKey string
	seqCount := make(map[seqKey]int)

	for _, entry := range recent {
		tools := orderedTools(entry)
		if len(tools) < 2 {
			continue
		}
		for i := 0; i+1 < len(tools); i++ {
			key := seqKey(tools[i] + "\u2192" + tools[i+1])
			seqCount[key]++
			if i+2 < len(tools) {
				key3 := seqKey(tools[i] + "\u2192" + tools[i+1] + "\u2192" + tools[i+2])
				seqCount[key3]++
			}
		}
	}

	var patterns []string
	for seq, count := range seqCount {
		if count >= 2 {
			patterns = append(patterns, fmt.Sprintf("sequence:%s:%d", seq, count))
		}
	}
	return patterns
}

func (e *Engine) saveSkillCandidate(patterns []string) {
	candidatesDir := filepath.Join(e.dir, "candidates")
	if err := os.MkdirAll(candidatesDir, 0755); err != nil {
		log.Printf("evolution: mkdir candidates: %v", err)
		return
	}
	ts := time.Now().Unix()
	body := fmt.Sprintf(`---
key: candidate-%d
type: skill-candidate
created: %s
status: pending-review
---

## Detected Patterns

%s

## Suggested Skill

A reusable skill could be created to streamline this repeated operation.
Review and either:
1. Run install_skill to create it
2. Delete this file if not useful
`, ts, time.Now().UTC().Format(time.RFC3339), strings.Join(patterns, "\n"))

	path := filepath.Join(candidatesDir, fmt.Sprintf("candidate-%d.md", ts))
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		log.Printf("evolution: write candidate: %v", err)
	}
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-3]) + "..."
}
