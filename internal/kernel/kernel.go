// Package kernel defines the minimal, immutable core of the OK agent.
// Everything outside this package is a plugin — including 54 built-in tools
// and any number of MCP plugins.
//
// The kernel has exactly 13 components:
//
//	4 platform services — sandbox, session, provider, control
//	5 LLM syscalls     — bash, read_file, write_file, edit_file, grep
//	4 civilization primitives — identity, recall, trust, learn
//
// These 4 civilization primitives give the agent a concept of personhood
// (identity), long-term memory (recall), verifiability (trust), and
// self-evolution (learn). Without them the agent is a tool without context;
// with them it becomes a civilization-scale infrastructure.
//
// The Kernel struct stores pre-built adapters; create via boot.Build() which
// wires all 13 components. A zero-value Kernel will not panic on construction
// but calling methods on nil interface fields will — always use boot.Build().
//
// # Design note — adapter location
//
// The concrete adapter implementations (adapters.go, tools.go) live in this
// package alongside the interfaces. This is a pragmatic choice: Go does not
// allow same-level subpackages to import their parent, so moving adapters to
// kernel/adapter/ would require duplicating all interface types and break
// type identity (kernel.Sandbox ≠ adapter.Sandbox). The adapters depend on 7
// internal packages (agent, provider, sandbox, etc.) — those dependencies are
// compile-time and do not create circular imports because they all flow through
// the boot package (the composition root). A future Go version with cleaner
// module-level dependency management may allow a kernel/adapter/ split.
package kernel

import (
	"context"
	"encoding/json"
)

// ─── Platform Services ───────────────────────────────────────────────────

// Sandbox isolates command execution. Plugins run inside it.
type Sandbox interface {
	// Run executes a command confined by the sandbox policy.
	// Returns stdout+stderr combined.
	Run(ctx context.Context, command string, opts RunOptions) RunResult

	// Available reports whether the sandbox is functional on this platform.
	Available() bool

	// PluginSpec returns the isolation spec for a plugin subprocess.
	// nil means no additional isolation beyond the OS default.
	PluginSpec() *PluginIsolation
}

type RunOptions struct {
	WorkDir    string
	TimeoutSec int
	Network    bool
	Env        map[string]string
}

type RunResult struct {
	Output   string
	ExitCode int
	Error    string // non-empty on timeout, OOM, or sandbox denial
}

type PluginIsolation struct {
	Mode       string // "appcontainer" | "landlock" | "seccomp" | "docker"
	Network    bool
	ReadRoots  []string
	WriteRoots []string
}

// Session manages conversation state. All plugins share it.
type Session interface {
	Add(msg Message)
	Snapshot() []Message
	Compact(ctx context.Context) error
}

type Message struct {
	Role    string          `json:"role"` // "user" | "assistant" | "tool"
	Content string          `json:"content"`
	Name    string          `json:"name,omitempty"`
	Meta    json.RawMessage `json:"meta,omitempty"`
}

// Provider connects to an LLM. Kernel is model-agnostic.
type Provider interface {
	Name() string
	Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}

type Request struct {
	Messages    []Message    `json:"messages"`
	Tools       []ToolSchema `json:"tools,omitempty"`
	Temperature float64      `json:"temperature,omitempty"`
}

type Chunk struct {
	Type   ChunkType       `json:"type"`
	Text   string          `json:"text,omitempty"`
	ToolID string          `json:"tool_id,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}

type ChunkType int

const (
	ChunkText ChunkType = iota
	ChunkReasoning
	ChunkToolCall
	ChunkToolResult
	ChunkUsage
	ChunkError
)

type ToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

// Controller is the transport-agnostic session driver.
type Controller interface {
	Send(ctx context.Context, input string) error
	Cancel()
	Events() <-chan Event
}

type Event struct {
	Kind  EventKind  `json:"kind"`
	Text  string     `json:"text,omitempty"`
	Tool  *ToolEvent `json:"tool,omitempty"`
	Error string     `json:"error,omitempty"`
}

type EventKind int

const (
	EventTurnStarted EventKind = iota
	EventReasoning
	EventText
	EventToolCall
	EventToolResult
	EventTurnDone
)

type ToolEvent struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Input    string `json:"input"`
	Output   string `json:"output,omitempty"`
	Error    string `json:"error,omitempty"`
	ReadOnly bool   `json:"read_only"`
}

// ─── LLM Syscalls ────────────────────────────────────────────────────────

// Bash is LLM's universal execution channel.
type Bash interface {
	Exec(ctx context.Context, command string, bg bool) BashOut
}

type BashOut struct {
	Output   string `json:"output"`
	JobID    string `json:"job_id,omitempty"`
	ExitCode int    `json:"exit_code"`
}

// ReadFile gives LLM precise file reading.
type ReadFile interface {
	Read(ctx context.Context, path string, offset, limit int) FileContent
}

type FileContent struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Lines     int    `json:"lines"`
	Size      int    `json:"size"`
	Truncated bool   `json:"truncated,omitempty"`
	Binary    bool   `json:"binary,omitempty"`
	Error     string `json:"error,omitempty"`
}

// WriteFile gives LLM precise file writing.
type WriteFile interface {
	Write(ctx context.Context, path, content string) error
}

// EditFile gives LLM precise string replacement.
type EditFile interface {
	Edit(ctx context.Context, path, oldString, newString string) error
}

// Grep gives LLM instant regex search.
type Grep interface {
	Search(ctx context.Context, pattern, path string) ([]Match, error)
}

type Match struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Text    string `json:"text"`
	Context string `json:"context,omitempty"`
}

// ─── Kernel — the composition root ───────────────────────────────────────

// Kernel carries 4 civilization primitive adapters (Identity, Recall, Trust,
// Learn) that frontends can read for agent metadata. The remaining 9 fields
// (Sandbox, Session, Provider, Controller, Bash, ReadFile, WriteFile, EditFile,
// Grep) are architectural declarations — only the civ primitives are populated
// at boot. Create via boot.Build(); zero-value Kernel will panic on method
// calls against nil interfaces.
type Kernel struct {
	Sandbox    Sandbox
	Session    Session
	Provider   Provider
	Controller Controller

	Bash      Bash
	ReadFile  ReadFile
	WriteFile WriteFile
	EditFile  EditFile
	Grep      Grep

	// Civilization primitives
	Identity Identity
	Recall   Recall
	Trust    Trust
	Learn    Learn
}

// Schema returns the LLM tool schema for the 5 syscalls.
// This is the ONLY schema the kernel exposes — plugins add their own.
func (k *Kernel) Schema() []ToolSchema {
	return []ToolSchema{
		{Name: "bash", Description: "Execute a shell command",
			Schema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"run_in_background":{"type":"boolean"}},"required":["command"]}`)},
		{Name: "read_file", Description: "Read a text file with optional offset/limit",
			Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer"},"limit":{"type":"integer"}},"required":["path"]}`)},
		{Name: "write_file", Description: "Write content to a file",
			Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)},
		{Name: "edit_file", Description: "Replace an exact string in a file",
			Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"}},"required":["path","old_string"]}`)},
		{Name: "grep", Description: "Search for a regex pattern in files",
			Schema: json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"}},"required":["pattern"]}`)},
		// Civilization primitives
		{Name: "identity", Description: "Who the user is - role, preferences, locale. Returns current user profile.",
			Schema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["whoami","set_user","list_users"]},"user_id":{"type":"string"}},"required":["action"]}`)},
		{Name: "recall", Description: "Long-term memory - remember facts, search memories, forget. Cross-session persistence.",
			Schema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["save","search","forget"]},"key":{"type":"string"},"value":{"type":"string"},"query":{"type":"string"},"scope":{"type":"string","enum":["session","project","global"]}},"required":["action"]}`)},
		{Name: "trust", Description: "Proof chain - record evidence, verify claims, export audit trail. Tamper-evident integrity.",
			Schema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["record","verify","export","summary"]},"proposition":{"type":"string"},"evidence":{"type":"string"},"atom_id":{"type":"string"}},"required":["action"]}`)},
		{Name: "learn", Description: "Self-evolution - extract patterns from tasks, generate skills, validate, publish. Gets better over time.",
			Schema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["extract","generate","validate","publish","stats"]},"skill_name":{"type":"string"},"skill_body":{"type":"string"}},"required":["action"]}`)},
	}
}

// SyscallNames returns the 5 built-in syscall tool names.
func (k *Kernel) SyscallNames() []string {
	return []string{"bash", "read_file", "write_file", "edit_file", "grep"}
}

// IsSyscall reports whether name is a kernel syscall.
func (k *Kernel) IsSyscall(name string) bool {
	switch name {
	case "bash", "read_file", "write_file", "edit_file", "grep":
		return true
	}
	return false
}
