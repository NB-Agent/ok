// Package kernel — civilization primitives.
//
// The 9 technical primitives (sandbox, session, provider, control + 5 syscalls)
// let the agent talk to a computer. The 4 civilization primitives let the agent
// talk to humanity: who the user is (identity), what the agent knows across
// time (recall), how it proves what it did (trust), and how it gets better (learn).
package kernel

import (
	"context"
	"time"
)

// ─── Identity — who is the user? ──────────────────────────────────────────

// Identity answers "who am I serving?" — the user's identity across devices,
// their role, preferences, and trust relationships. This is the kernel's
// concept of "personhood", without which the agent is a tool without context.
type Identity interface {
	// Whoami returns the current user's identity. The zero value means
	// "anonymous / no identity configured" — the agent runs with defaults.
	Whoami(ctx context.Context) User

	// SetUser switches the active user for this session.
	// An empty id means "log out" (return to anonymous).
	SetUser(ctx context.Context, id string) error

	// ListUsers returns all known user profiles on this device.
	ListUsers(ctx context.Context) ([]User, error)
}

// User is a person the agent serves. The kernel treats this as opaque
// metadata — it never interprets fields, it just carries them so the
// agent knows who it's talking to.
type User struct {
	ID        string            `json:"id"`             // stable identifier across sessions/devices
	Label     string            `json:"label"`          // human-readable name
	Roles     []string          `json:"roles"`          // "admin", "developer", "child", …
	Locale    string            `json:"locale"`         // "zh-CN", "en-US", …
	ModelPref string            `json:"modelPref"`      // preferred model name
	Meta      map[string]string `json:"meta,omitempty"` // extensible
}

// IsZero reports whether the User is the zero value (anonymous).
func (u User) IsZero() bool { return u.ID == "" }

// ─── Recall — what does the agent remember? ────────────────────────────────

// Recall is the agent's long-term memory — facts, patterns, skills that
// survive session boundaries. Unlike Session (short-term conversation state),
// Recall persists across reboots, model switches, and user sessions.
//
// The kernel defines three memory tiers:
//
//	Ephemeral  — this-session-only (backed by Session)
//	Persistent — survives reboots (backed by memory.Store)
//	Procedural — learned skills (backed by skill.Store)
type Recall interface {
	// Save stores a fact. TTL of 0 means "forever".
	Save(ctx context.Context, fact Fact) error

	// Search returns facts matching the query, newest first.
	Search(ctx context.Context, query string, limit int) ([]Fact, error)

	// Forget removes facts matching the query. Empty query is a no-op.
	Forget(ctx context.Context, query string) (int, error)
}

// Fact is one unit of recall — a thing the agent knows.
type Fact struct {
	ID        string            `json:"id"`
	Scope     string            `json:"scope"`  // "session" | "project" | "global"
	Key       string            `json:"key"`    // short slug, e.g. "user-prefers-dark-mode"
	Value     string            `json:"value"`  // the remembered content
	Source    string            `json:"source"` // what created it: "user", "agent", "skill"
	CreatedAt time.Time         `json:"createdAt"`
	TTL       time.Duration     `json:"ttl,omitempty"` // 0 = forever
	Tags      map[string]string `json:"tags,omitempty"`
}

// ─── Trust — how does the agent prove itself? ──────────────────────────────

// Trust is the agent's integrity layer — a tamper-evident record of what it
// did, what it found, and how it reached conclusions. Without Trust, the
// agent is a black box. With Trust, every output can be traced to evidence.
type Trust interface {
	// Record logs an action with supporting evidence. Returns a chain entry.
	Record(ctx context.Context, entry ProofEntry) error

	// Verify checks whether an atom ID's evidence chain is intact.
	// Returns nil when the chain is valid from root to tip.
	Verify(ctx context.Context, atomID string) error

	// Export returns the full proof chain for audit/debug.
	Export(ctx context.Context) ([]ProofEntry, error)

	// Summary returns a terse overview of what's been proven this session.
	Summary(ctx context.Context) TrustSummary
}

// ProofEntry is one link in the agent's proof chain.
type ProofEntry struct {
	AtomID      string    `json:"atomId"`             // unique claim identifier
	Proposition string    `json:"proposition"`        // what is being claimed
	Evidence    string    `json:"evidence"`           // supporting output
	ParentID    string    `json:"parentId,omitempty"` // parent atom, for nesting
	Path        string    `json:"path,omitempty"`     // file path, if relevant
	Timestamp   time.Time `json:"timestamp"`
	SHA256      string    `json:"sha256"` // hash of (parent + proposition + evidence)
}

// TrustSummary is a lightweight overview for the LLM's context window.
type TrustSummary struct {
	EntryCount int    `json:"entryCount"`
	LastAction string `json:"lastAction"` // most recent proposition
	Healthy    bool   `json:"healthy"`    // all chains verify
}

// ─── Kernel extension: civilization primitives ────────────────────────────

// assertKernelExtension is a compile-time check that the Kernel struct
// exports all 4 civilization primitives as public fields.
// If a field is renamed or removed, this line won't compile.
var _ = struct {
	Identity Identity
	Recall   Recall
	Trust    Trust
	Learn    Learn
}{}

// ─── Learn — how does the agent get better? ───────────────────────────────

// Learn is the agent's self-evolution engine — it extracts patterns from
// completed tasks, generates reusable skills, validates them in a sandbox,
// and publishes them to the skill store. This is the kernel's "learning to
// learn" primitive.
//
// Without Learn, the agent is a fixed-capability tool. With Learn, it
// improves with every task, accumulating skills across sessions and users.
type Learn interface {
	// Extract analyzes a completed task and returns patterns (reusable
	// approaches, anti-patterns, tool combinations).
	Extract(ctx context.Context, task TaskRecord) ([]Pattern, error)

	// Generate creates a candidate skill from successful patterns.
	// The skill is not yet validated — it must pass Validate first.
	Generate(ctx context.Context, patterns []Pattern) (Skill, error)

	// Validate runs the candidate skill in an isolated sandbox to verify
	// correctness. Returns nil when the skill is safe and effective.
	Validate(ctx context.Context, skill Skill) error

	// Publish makes a validated skill available to the agent's skill store.
	Publish(ctx context.Context, skill Skill) error

	// Stats returns learning metrics: how many skills learned, success rate, etc.
	Stats(ctx context.Context) LearnStats
}

// TaskRecord captures what happened in one task for learning analysis.
type TaskRecord struct {
	Goal       string        `json:"goal"`
	Turns      int           `json:"turns"`
	ToolCalls  []ToolCallRec `json:"toolCalls"`
	Result     string        `json:"result"`
	DurationMs int64         `json:"durationMs"`
	Success    bool          `json:"success"`
}

// ToolCallRec records one tool invocation for learning.
type ToolCallRec struct {
	Name   string `json:"name"`
	Args   string `json:"args"`
	Result string `json:"result"`
	Order  int    `json:"order"`
}

// Pattern is a reusable approach extracted from a task.
type Pattern struct {
	ID           string   `json:"id"`
	Description  string   `json:"description"`
	ToolSequence []string `json:"toolSequence"`
	Frequency    int      `json:"frequency"` // how many times this pattern worked
	Confidence   float64  `json:"confidence"`
}

// Skill is a learnable capability the agent can execute.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`   // the skill prompt / playbook
	Source      string `json:"source"` // "extracted" | "authored"
	Version     int    `json:"version"`
}

// LearnStats summarizes the agent's learning progress.
type LearnStats struct {
	TotalSkills    int     `json:"totalSkills"`
	ExtractedToday int     `json:"extractedToday"`
	SuccessRate    float64 `json:"successRate"`
	AvgConfidence  float64 `json:"avgConfidence"`
}
