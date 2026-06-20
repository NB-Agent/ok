// Package evolution — Gap 1: Semantic pattern recognition.
//
// Replaces the pure keyword-frequency approach (findPatterns) with a layered
// detection system:
//
//   Layer 0 — Workflow signatures (zero-LLM, always available):
//     Pre-defined workflow patterns matched against tool sequences + context
//     hints. Catches "TDD workflow", "search-then-edit", "audit-before-deploy"
//     etc. without any LLM call.
//
//   Layer 1 — Semantic analysis (LLM-driven, via Learn tool path):
//     When a provider is available, the episodic corpus is analyzed by the
//     LLM to extract abstract patterns ("user prefers incremental commits",
//     "security review always precedes deployment"). This is the Learn
//     interface's Extract path.
//
//   Layer 2 — Cross-turn narrative patterns (zero-LLM):
//     Aggregates tool frequency + sequence + temporal proximity into a
//     "workflow fingerprint" that captures more signal than isolated counts.
package evolution

import (
	"sort"
	"strings"
)

// ─── Workflow signatures ──────────────────────────────────────────────────

// WorkflowSignature describes a named, repeatable workflow pattern.
// Signatures are matched against tool call sequences across episodic entries.
// This is Layer 0 — zero LLM cost, always active.
type WorkflowSignature struct {
	Name        string   // e.g. "tdd", "search-then-edit"
	Description string   // human-readable explanation
	Tools       []string // ordered tools that must appear in sequence
	MinHits     int      // minimum occurrences to trigger (default 2)
}

// builtinSignatures encodes common developer workflows as pre-defined
// tool sequence patterns. These are matched post-order (any sub-sequence
// within an episodic entry counts).
var builtinSignatures = []WorkflowSignature{
	{
		Name:        "tdd-workflow",
		Description: "Write code, run tests, fix failures, re-run tests",
		Tools:       []string{"write_file", "bash", "edit_file", "bash"},
		MinHits:     2,
	},
	{
		Name:        "search-then-edit",
		Description: "Search codebase with grep, read the hit, edit the file",
		Tools:       []string{"grep", "read_file", "edit_file"},
		MinHits:     2,
	},
	{
		Name:        "audit-then-fix",
		Description: "Run code audit, then fix found issues",
		Tools:       []string{"ok-verify", "edit_file"},
		MinHits:     2,
	},
	{
		Name:        "dependency-update",
		Description: "Read dependency file, update, tidy",
		Tools:       []string{"read_file", "bash", "bash"},
		MinHits:     2,
	},
	{
		Name:        "build-verify-deploy",
		Description: "Build, run tests, deploy",
		Tools:       []string{"bash", "bash", "deploy"},
		MinHits:     2,
	},
	{
		Name:        "research-then-write",
		Description: "Search/read docs, then create/edit code",
		Tools:       []string{"web_fetch", "write_file"},
		MinHits:     2,
	},
	{
		Name:        "git-commit-cycle",
		Description: "Stage changes, commit, push",
		Tools:       []string{"git", "git", "git"},
		MinHits:     2,
	},
	{
		Name:        "debug-cycle",
		Description: "Run command, check error, fix, re-run",
		Tools:       []string{"bash", "read_file", "edit_file", "bash"},
		MinHits:     2,
	},
}

// detectWorkflows scans recent episodic entries for pre-defined workflow
// signatures. Returns a list of "workflow:<name>:<count>" patterns suitable
// for merging into the existing pattern pipeline.
//
// This complements findPatterns() (individual tool frequency) and
// detectSequencePatterns() (2/3-step sequences) with named, semantic
// workflows that carry more signal than raw tool counts.
func detectWorkflows(recent []string) []string {
	if len(recent) < 2 {
		return nil
	}

	var patterns []string
	for _, sig := range builtinSignatures {
		count := countWorkflowHits(recent, sig.Tools)
		if count >= sig.MinHits {
			patterns = append(patterns, "workflow:"+sig.Name+":"+itoa(count))
		}
	}
	return patterns
}

// countWorkflowHits checks how many episodic entries contain the full tool
// sequence (in order, not necessarily contiguous).
func countWorkflowHits(entries []string, tools []string) int {
	if len(tools) == 0 {
		return 0
	}
	hits := 0
	for _, entry := range entries {
		if containsSequence(entry, tools) {
			hits++
		}
	}
	return hits
}

// containsSequence reports whether the entry text contains the tool names
// in the given order (greedy left-to-right scan). Tools are detected by
// substring match in the lowercased entry.
func containsSequence(entry string, tools []string) bool {
	lower := strings.ToLower(entry)
	pos := 0
	for _, tool := range tools {
		idx := strings.Index(lower[pos:], strings.ToLower(tool))
		if idx < 0 {
			return false
		}
		pos += idx + len(tool)
	}
	return true
}

// ─── Workflow fingerprint (Layer 2) ──────────────────────────────────────

// workflowFingerprint aggregates tool usage across multiple turns into a
// richer signal than isolated frequency counts. It captures:
//
//   - ToolFrequency: how often each tool appears
//   - ToolPairs: adjacent tool pairs and their co-occurrence count
//   - TemporalDensity: whether tools appear clustered (bursts) or spread out
//
// This is used by detectFingerprintPatterns to emit "fingerprint:<type>:<count>"
// patterns with higher signal-to-noise than raw repeated-tool patterns.
type workflowFingerprint struct {
	toolFreq  map[string]int
	toolPairs map[string]int // "bash→grep" → count
	bursts    map[string]int // tool → max consecutive appearances
}

func newFingerprint() *workflowFingerprint {
	return &workflowFingerprint{
		toolFreq:  make(map[string]int),
		toolPairs: make(map[string]int),
		bursts:    make(map[string]int),
	}
}

// detectFingerprintPatterns builds a workflow fingerprint from recent entries
// and emits high-signal patterns. A pattern qualifies when:
//
//   - Tool frequency ≥ 4 (stronger than raw repeated-tool threshold of 3)
//   - Pair frequency ≥ 3 (stronger than raw sequence threshold of 2)
//   - Burst count ≥ 3 (tool used in 3+ consecutive entries)
func detectFingerprintPatterns(recent []string) []string {
	if len(recent) < 3 {
		return nil
	}

	fp := newFingerprint()

	// Build fingerprint.
	var prevTools []string
	for _, entry := range recent {
		tools := orderedTools(entry)
		for _, t := range tools {
			fp.toolFreq[t]++
		}
		// Track pairs from previous entry → this entry.
		for _, pt := range prevTools {
			for _, ct := range tools {
				key := pt + "→" + ct
				fp.toolPairs[key]++
			}
		}
		// Track bursts (consecutive appearances of the same tool).
		if len(tools) > 0 {
			for _, t := range tools {
				if len(prevTools) > 0 && containsTool(prevTools, t) {
					fp.bursts[t]++
				}
			}
		}
		prevTools = tools
	}

	// Emit high-signal patterns.
	var patterns []string

	// Frequent tools (threshold: 4).
	for tool, count := range fp.toolFreq {
		if count >= 4 {
			patterns = append(patterns, "fingerprint:tool:"+tool+":"+itoa(count))
		}
	}

	// Frequent pairs (threshold: 3).
	type pairEntry struct {
		key   string
		count int
	}
	var pairs []pairEntry
	for k, v := range fp.toolPairs {
		if v >= 3 {
			pairs = append(pairs, pairEntry{k, v})
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].count > pairs[j].count })
	for _, p := range pairs {
		patterns = append(patterns, "fingerprint:pair:"+p.key+":"+itoa(p.count))
	}

	// Burst tools (threshold: 3 consecutive appearances).
	for tool, count := range fp.bursts {
		if count >= 3 {
			patterns = append(patterns, "fingerprint:burst:"+tool+":"+itoa(count))
		}
	}

	return patterns
}

func containsTool(tools []string, needle string) bool {
	for _, t := range tools {
		if t == needle {
			return true
		}
	}
	return false
}

// ─── Unified semantic detection ──────────────────────────────────────────

// semanticPatterns aggregates all semantic-layer detections:
// workflow signatures, fingerprint patterns, and (when available) LLM analysis.
// This is the entry point called by detectAndGenerate.
func semanticPatterns(recent []string) []string {
	var all []string
	all = append(all, detectWorkflows(recent)...)
	all = append(all, detectFingerprintPatterns(recent)...)
	return all
}

// ─── LLM-driven semantic analysis (Layer 1) ──────────────────────────────

// SemanticInsight is a high-level pattern extracted by LLM analysis of
// episodic memory. Unlike raw tool counts, these carry human-readable
// descriptions of abstract workflows.
type SemanticInsight struct {
	Pattern     string  `json:"pattern"`     // e.g. "incremental-commit"
	Description string  `json:"description"` // e.g. "User commits after every file edit"
	Confidence  float64 `json:"confidence"`  // 0.0–1.0
	Evidence    string  `json:"evidence"`    // supporting episodic entries
}

// AnalyzeRequest is the input to LLM-driven semantic analysis.
type AnalyzeRequest struct {
	Entries []string `json:"entries"` // recent episodic memory entries
	MaxN    int      `json:"maxN"`    // max insights to return
}

// systemPromptSemantic is the system prompt for the semantic analyzer.
const systemPromptSemantic = `You are a semantic pattern detector for an AI agent's self-evolution engine.

Analyze the episodic memory entries below. Each entry shows what the user asked
(input) and what the agent did (output).

Extract ABSTRACT WORKFLOW PATTERNS — not just tool names, but the user's
working style, preferences, and recurring task structures.

Examples of good patterns:
- "User prefers TDD: writes test first, then implementation"
- "Security audit always precedes deployment"
- "User reviews diffs before committing"
- "Incremental development: small edits followed by immediate testing"

Respond with a JSON array of patterns:
[{"pattern":"kebab-name","description":"...","confidence":0.8}]

Confidence should reflect how consistently this pattern appears. Only return
patterns you are reasonably confident about (confidence >= 0.5). Return [] if
no clear patterns emerge.`

// itoa is a lightweight integer-to-string converter (avoids importing fmt).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
