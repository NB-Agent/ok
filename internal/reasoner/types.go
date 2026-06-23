// Package reasoner provides the deterministic verification layer for OK's
// DAG task decomposition. It parses sub-agent outputs for YES/NO verdicts,
// extracts file:line evidence, computes parent AND/OR gates, and drives the
// method ladder when leaf tasks fail — turning what was previously prompt-only
// guidance into enforceable Go-level behavior.
//
// The Reasoner in internal/agent handles LLM-driven decomposition and Planner
// execution. This package adds post-execution verification: after each task
// completes, ParseVerdict extracts the outcome; BuildTree assembles a gate
// tree from the Plan DAG; ComputeRootVerdict determines whether the overall
// goal is satisfied; and MethodLadder suggests the next tier when a leaf
// returns NO.
package reasoner

import "strings"

// Method labels the extraction/verification approach used for a task.
type Method string

const (
	MethodRegex     Method = "regex"
	MethodFuzzy     Method = "fuzzy"
	MethodHeuristic Method = "heuristic"
	MethodAI        Method = "ai"
)

// Verdict is the YES/NO outcome of a leaf verification task.
type Verdict string

const (
	VerdictYes       Verdict = "YES"
	VerdictNo        Verdict = "NO"
	VerdictUncertain Verdict = "UNCERTAIN"
)

// Evidence captures a concrete file:line proof from a task result.
type Evidence struct {
	File string
	Line int
	Text string // the line content or surrounding context
}

// TaskVerdict is the parsed outcome of a single DAG task execution.
type TaskVerdict struct {
	TaskID   string
	Verdict  Verdict
	Evidence []Evidence
	Method   Method   // method used (may be empty if not known)
	Output   string   // raw output from the sub-agent
}

// Gate specifies how a parent node combines its children's verdicts.
type Gate string

const (
	GateAND Gate = "AND" // all children must be YES
	GateOR  Gate = "OR"  // at least one child must be YES
)

// TaskNode is a lightweight representation of one task in the verification
// tree. It mirrors agent.PlanTask without importing the agent package, so
// the caller converts.
type TaskNode struct {
	ID        string
	DependsOn []string
}

// VerdictNode is a node in the verification tree, carrying its verdict
// and gate logic.
type VerdictNode struct {
	TaskID   string
	Verdict  Verdict
	Children []string   // child task IDs
	Gate     Gate       // how to combine children
}

// VerificationTree assembles task verdicts into a gate tree.
type VerificationTree struct {
	Nodes map[string]*VerdictNode
	Root  string // the synthetic root task ID
}

// MethodLadder drives automatic method escalation when a task fails.
// It starts at MethodRegex and climbs to MethodFuzzy → MethodHeuristic →
// MethodAI, returning the prompt guidance for the decomposer at each step.
type MethodLadder struct {
	Current Method
}

// NextMethod advances the ladder and returns the next method to try.
// When already at MethodAI, it stays there (no further escalation).
func (l *MethodLadder) NextMethod() Method {
	switch l.Current {
	case MethodRegex:
		l.Current = MethodFuzzy
	case MethodFuzzy:
		l.Current = MethodHeuristic
	case MethodHeuristic:
		l.Current = MethodAI
	default:
		// Already at top or unknown — stay at AI.
		l.Current = MethodAI
	}
	return l.Current
}

// SuggestPrompt returns a prompt fragment telling the decomposer which
// method tier to use for re-decomposition, based on the failed task's
// previous method and what the ladder now prescribes.
func (l *MethodLadder) SuggestPrompt(failedID string, previous Method) string {
	next := l.NextMethod()
	var b strings.Builder
	b.WriteString("Task ")
	b.WriteString(failedID)
	b.WriteString(" previously used ")
	b.WriteString(string(previous))
	b.WriteString(" and returned NO. ")
	switch next {
	case MethodFuzzy:
		b.WriteString("Try fuzzy matching with character normalization and whitespace tolerance.")
	case MethodHeuristic:
		b.WriteString("Try line-by-line heuristics with explicit error tolerance.")
	case MethodAI:
		b.WriteString("Use AI-assisted extraction — read the target content and reason about it.")
	default:
		b.WriteString("Escalate method tier.")
	}
	return b.String()
}
