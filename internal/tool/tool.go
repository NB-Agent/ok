// Package tool defines the Tool abstraction and a Registry. Built-in tools live
// in tool/builtin and self-register via init(); plugin-provided tools are added
// to a runtime Registry alongside the enabled built-ins. The agent sees only a
// *Registry, never the global built-in set directly.
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/NB-Agent/ok/internal/diff"
	"github.com/NB-Agent/ok/internal/provider"
)

// Tool is a capability the model can invoke.
type Tool interface {
	Name() string
	Description() string
	// Schema returns the JSON Schema for the tool's parameters.
	Schema() json.RawMessage
	// Execute parses the model-generated raw JSON args and returns result text
	// to feed back to the model.
	Execute(ctx context.Context, args json.RawMessage) (string, error)
	// ReadOnly reports whether the tool has no observable side effects on the
	// host. The agent parallelises a batch of tool calls only when every call
	// in the batch is ReadOnly; mixed batches stay sequential so write/read
	// ordering is preserved. bash and plugin tools must return false because
	// their effects can't be inferred statically from args.
	ReadOnly() bool
}

// Previewer is an optional capability a writer Tool may implement: given the
// same raw JSON args Execute would receive, compute the file change the call
// *would* make — without touching disk. A front-end uses it to show an approval
// card or a changed-files panel before the call runs (the permission gate, not
// Preview, decides whether it may proceed). Type-assert a Tool to Previewer to
// discover support; the file-writing built-ins implement it, most tools do not.
type Previewer interface {
	Preview(args json.RawMessage) (diff.Change, error)
}

// ToolGrouper is an optional interface a Tool may implement to declare its
// group membership for hierarchical tool registration. Groups let the agent
// expose only a subset of tools to the model based on task complexity,
// saving ~50% schema tokens per turn.
//
// Built-in groups (case-insensitive):
//
//	core      — always visible (read/write files, search, task, etc.)
//	advanced  — git, database, debug, deploy, desktop, etc.
//	knowledge — rag, semantic-search, code analysis
//	admin     — repo, make-tool, go-profile, etc.
//
// Tools that don't implement ToolGrouper default to "core".
type ToolGrouper interface {
	Group() string
}

// --- process-global built-in set (populated by builtin subpackage init) ---

var (
	builtins   = map[string]Tool{}
	builtinsMu sync.RWMutex
)

// RegisterBuiltin registers a compile-time built-in tool. Intended for init().
// A duplicate name is logged to stderr and skipped — a duplicate is a
// compile-time wiring mistake, but crashing the binary is worse for the user.
func RegisterBuiltin(t Tool) {
	builtinsMu.Lock()
	defer builtinsMu.Unlock()
	name := t.Name()
	if _, dup := builtins[name]; dup {
		fmt.Fprintf(os.Stderr, "tool: duplicate built-in %q skipped\n", name)
		return
	}
	builtins[name] = t
}

// Builtins returns all registered built-in tools, sorted by name.
func Builtins() []Tool {
	builtinsMu.RLock()
	defer builtinsMu.RUnlock()
	names := make([]string, 0, len(builtins))
	for n := range builtins {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Tool, 0, len(names))
	for _, n := range names {
		out = append(out, builtins[n])
	}
	return out
}

// LookupBuiltin returns a registered built-in by name.
func LookupBuiltin(name string) (Tool, bool) {
	builtinsMu.RLock()
	defer builtinsMu.RUnlock()
	t, ok := builtins[name]
	return t, ok
}

// --- per-run registry instance ---

// ToolRegistry is the interface the agent (and anyone who needs to look up or
// enumerate tools) depends on. A *Registry implements it; tests can satisfy it
// with a fake rather than importing the concrete implementation.
type ToolRegistry interface {
	Get(name string) (Tool, bool)
	GetAny(name string) (Tool, bool) // lookup regardless of group visibility
	Names() []string
	Schemas() []provider.ToolSchema
	Len() int
}

// RegistrySetGroups is the interface for switching tool groups at runtime.
// Separated from ToolRegistry so only the boot wiring exposes it.
type RegistrySetGroups interface {
	ToolRegistry
	ActivateGroups(groups ...string)
	AllNames() []string
	ActiveGroupNames() []string
}

// Registry is a per-run set of tools: enabled built-ins plus plugin tools.
// Safe for concurrent use: reads (Get, Names, Schemas, Len) take RLock;
// writes (Add, RemovePrefix) take Lock.
// Supports hierarchical tool groups: when groups are empty (default), all tools
// are visible. When groups are set via ActivateGroups, only tools in those
// groups are returned by Schemas/Names/Get — cutting schema token costs.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
	order []string

	// groups: toolName → groupName. Populated by AddToGroup.
	groups map[string]string
	// activeGroups: set of group names currently visible. Empty = all visible.
	activeGroups map[string]bool

	// schema cache — invalidated on any Add/Remove/group change.
	schemaGen      int64
	schemaCache    []provider.ToolSchema
	schemaCacheGen int64
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		tools:        map[string]Tool{},
		groups:       map[string]string{},
		activeGroups: map[string]bool{},
	}
}

// Add inserts (or replaces) a tool, preserving first-seen order.
// Determines the group from ToolGrouper if implemented, defaulting to "core".
func (r *Registry) Add(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := t.Name()
	if _, ok := r.tools[name]; !ok {
		r.order = append(r.order, name)
	}
	r.tools[name] = t
	// Detect group.
	if g, ok := t.(ToolGrouper); ok {
		r.groups[name] = g.Group()
	} else {
		r.groups[name] = "core"
	}
	r.bumpSchemaGen()
}

// AddToGroup inserts a tool with an explicit group override.
func (r *Registry) AddToGroup(t Tool, group string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := t.Name()
	if _, ok := r.tools[name]; !ok {
		r.order = append(r.order, name)
	}
	r.tools[name] = t
	if group == "" {
		group = "core"
	}
	r.groups[name] = group
	r.bumpSchemaGen()
}

// SetGroup changes the group of an already-registered tool by name.
// Used by boot to apply hierarchical group assignments after init().
// No-op for unknown names.
func (r *Registry) SetGroup(name, group string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[name]; ok {
		r.groups[name] = group
		r.bumpSchemaGen()
	}
}

// ActivateGroups sets which groups are visible. Schemas/Names/Get will only
// return tools in these groups. An empty or nil set = all tools visible (default).
func (r *Registry) ActivateGroups(groups ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.activeGroups = make(map[string]bool, len(groups))
	for _, g := range groups {
		r.activeGroups[g] = true
	}
	r.bumpSchemaGen()
}

// DeactivateGroups hides groups entirely. After this only "core" tools remain.
func (r *Registry) DeactivateGroups(groups ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, g := range groups {
		delete(r.activeGroups, g)
	}
	r.bumpSchemaGen()
}

// ActiveGroupNames returns the currently active group names.
func (r *Registry) ActiveGroupNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.activeGroups))
	for g := range r.activeGroups {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// isToolVisible checks whether a named tool is visible in the current group set.
// When activeGroups is empty, all tools are visible (default compat).
func (r *Registry) isToolVisible(name string) bool {
	if len(r.activeGroups) == 0 {
		return true // backward compat: all visible
	}
	g, ok := r.groups[name]
	if !ok {
		return false
	}
	return r.activeGroups[g]
}

// RemovePrefix unregisters every tool whose name starts with prefix — used to
// drop an MCP server's "mcp__<server>__" namespace when it's disconnected — and
// returns the count removed.
func (r *Registry) RemovePrefix(prefix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.order[:0]
	removed := 0
	for _, name := range r.order {
		if strings.HasPrefix(name, prefix) {
			delete(r.tools, name)
			delete(r.groups, name)
			removed++
			continue
		}
		kept = append(kept, name)
	}
	r.order = kept
	r.bumpSchemaGen()
	return removed
}

// Get looks up a tool by name. Returns false if the tool is not in the
// current active group set (or doesn't exist).
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.isToolVisible(name) {
		return nil, false
	}
	t, ok := r.tools[name]
	return t, ok
}

// GetAny looks up a tool by name regardless of group visibility.
// Used internally for tool execution — the agent can still call a tool
// that was used in a previous turn even if its group is now inactive.
func (r *Registry) GetAny(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Len returns the number of visible registered tools.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.activeGroups) == 0 {
		return len(r.order)
	}
	n := 0
	for _, name := range r.order {
		if r.isToolVisible(name) {
			n++
		}
	}
	return n
}

// Names returns the visible registered tool names in insertion order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.activeGroups) == 0 {
		out := make([]string, len(r.order))
		copy(out, r.order)
		return out
	}
	out := make([]string, 0, len(r.order))
	for _, name := range r.order {
		if r.isToolVisible(name) {
			out = append(out, name)
		}
	}
	return out
}

// AllNames returns every registered tool name regardless of group visibility.
func (r *Registry) AllNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Schemas exports tool definitions in insertion order for the provider,
// returning only tools in the currently active groups. Results are cached
// until the registry is mutated (Add/Remove/group change).
func (r *Registry) Schemas() []provider.ToolSchema {
	r.mu.RLock()
	if r.schemaGen == r.schemaCacheGen && r.schemaCache != nil {
		// Return a shallow copy — callers must not mutate.
		out := make([]provider.ToolSchema, len(r.schemaCache))
		copy(out, r.schemaCache)
		r.mu.RUnlock()
		return out
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	// Double-check: another goroutine may have rebuilt while we waited for Lock.
	if r.schemaGen == r.schemaCacheGen && r.schemaCache != nil {
		out := make([]provider.ToolSchema, len(r.schemaCache))
		copy(out, r.schemaCache)
		return out
	}

	out := make([]provider.ToolSchema, 0, len(r.order))
	for _, name := range r.order {
		if !r.isToolVisible(name) {
			continue
		}
		t := r.tools[name]
		out = append(out, provider.ToolSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Schema(),
		})
	}
	r.schemaCache = out
	r.schemaCacheGen = r.schemaGen
	// Return a copy.
	cpy := make([]provider.ToolSchema, len(out))
	copy(cpy, out)
	return cpy
}

// bumpSchemaGen invalidates the schema cache. Must be called under mu.Lock.
func (r *Registry) bumpSchemaGen() {
	r.schemaGen++
}
