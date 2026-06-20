// Package slashcmd is the slash-command dispatcher — extracted from control.Controller
// to keep the turn loop lean. It classifies and routes slash commands to a Handler
// interface that the Controller implements. Completion logic lives here too so both
// the CLI TUI and desktop frontends share one implementation.
package slashcmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/NB-Agent/ok/internal/command"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/i18n"
	"github.com/NB-Agent/ok/internal/plugin"
	"github.com/NB-Agent/ok/internal/skill"
)

// ── Handler interface ──

// Handler is what the dispatcher needs from its host (control.Controller).
type Handler interface {
	// List accessors for management notices
	Label() string
	MemoryDocs() []MemoryDoc
	Skills() []skill.Skill
	HookDescs() []HookDesc
	MCPServerNames() []string

	// Turn orchestration
	Compose(text string) string
	RunTurn(ctx context.Context, input string) error
	CustomCommand(input string) (sent string, found bool)
	RunSkill(input string) (sent string, found bool)
	MCPPrompt(ctx context.Context, input string) (sent string, found bool, err error)

	// Session
	Compact(ctx context.Context) error
	Snapshot() error
	NewSession() error
	Running() bool

	// Memory
	QuickAdd(note string) (string, error)

	// DST
	IsDSTAvailable() bool
	SetDSTEnabled(v bool)
	DSTEnabled() bool

	// Turn control
	Cancel()

	// Display
	ShowAudit()
	ShowSearch(term string)
	HandlePermissions(input string)
}

// MemoryDoc is a simplified memory document for listing.
type MemoryDoc struct {
	Scope string
	Path  string
}

// HookDesc is a simplified hook description for listing.
type HookDesc struct {
	Event   string
	Scope   string
	Match   string
	Command string
}

// ── Completion types ──

// SlashItem is one slash-completion suggestion.
type SlashItem struct {
	Label   string `json:"label"`
	Insert  string `json:"insert"`
	Hint    string `json:"hint"`
	Descend bool   `json:"descend"`
}

// ArgData supplies dynamic data for slash-argument completion.
type ArgData struct {
	Skills       []skill.Skill
	ServerNames  []string
	ModelRefs    []string
	CurrentModel string
}

// ── Dispatcher ──

// Dispatcher routes slash commands to the handler.
type Dispatcher struct {
	h      Handler
	cmds   []command.Command
	skills []skill.Skill
	host   *plugin.Host
	sink   event.Sink
	refs   RefResolver
}

// RefResolver resolves @-references in user input.
type RefResolver interface {
	HasRefs(line string) bool
	ResolveRefs(ctx context.Context, line string) (block string, errs []string)
}

// New creates a Dispatcher.
func New(h Handler, cmds []command.Command, skills []skill.Skill, host *plugin.Host, sink event.Sink, refs RefResolver) *Dispatcher {
	return &Dispatcher{h: h, cmds: cmds, skills: skills, host: host, sink: sink, refs: refs}
}

// Sink returns the event sink so the caller (Controller) can emit events not
// covered by this dispatcher (e.g., plan-mode approval).
func (d *Dispatcher) Sink() event.Sink { return d.sink }

// Dispatch classifies a raw input and returns an Action. It does NOT execute
// side effects — the caller (Controller) does that, keeping the turn loop in one
// place. This separation means Dispatch is pure classification; the Controller
// remains the sole execution engine.
func (d *Dispatcher) Classify(input string) Action {
	trimmed := strings.TrimSpace(input)

	// # memory note
	if strings.HasPrefix(trimmed, "#") {
		note := strings.TrimSpace(trimmed[1:])
		return Action{Kind: ActMemory, MemoryNote: note}
	}

	if !strings.HasPrefix(trimmed, "/") {
		return Action{Kind: ActNormal, RawInput: input}
	}

	// Management notices
	if act, ok := d.classifyManagement(trimmed); ok {
		return act
	}

	switch trimmed {
	case "/compact":
		return Action{Kind: ActCompact}
	case "/new":
		return Action{Kind: ActNewSession}
	case "/cancel":
		return Action{Kind: ActCancel}
	}

	if strings.HasPrefix(trimmed, "/dst") {
		return d.classifyDST(trimmed)
	}

	switch {
	case trimmed == "/audit":
		return Action{Kind: ActShowAudit}
	case strings.HasPrefix(trimmed, "/search"):
		return Action{Kind: ActShowSearch, SearchTerm: strings.TrimPrefix(trimmed, "/search ")}
	case strings.HasPrefix(trimmed, "/permissions"):
		return Action{Kind: ActPermissions, PermInput: trimmed}
	}

	if strings.HasPrefix(trimmed, "/mcp__") {
		return Action{Kind: ActMCP, RawInput: trimmed}
	}

	if sent, ok := d.h.CustomCommand(trimmed); ok {
		return Action{Kind: ActCustomCmd, Composed: d.h.Compose(sent)}
	}
	if sent, ok := d.h.RunSkill(trimmed); ok {
		return Action{Kind: ActSkill, Composed: d.h.Compose(sent)}
	}

	return Action{Kind: ActUnknown, RawInput: trimmed}
}

// ClassifyWithRefs resolves @-references for plain-text input and returns Action.
func (d *Dispatcher) ClassifyWithRefs(ctx context.Context, input string) Action {
	act := d.Classify(input)
	if act.Kind != ActNormal {
		return act
	}
	block, errs := d.refs.ResolveRefs(ctx, input)
	act.RefErrs = errs
	if block != "" {
		act.RawInput = block + "\n\n" + input
	} else {
		act.RawInput = input
	}
	return act
}

// ── Action types ──

// ActKind tells the Controller what to do.
type ActKind int

const (
	ActNormal      ActKind = iota // run a normal turn
	ActMemory                     // # quick-add
	ActCompact                    // /compact
	ActNewSession                 // /new
	ActCancel                     // /cancel
	ActDST                        // /dst on|off|status|run
	ActShowAudit                  // /audit
	ActShowSearch                 // /search
	ActPermissions                // /permissions
	ActMCP                        // /mcp__...
	ActCustomCmd                  // custom command
	ActSkill                      // run skill
	ActNotice                     // management notice (already emitted)
	ActUnknown                    // unknown command
)

// Action is a classified user input with enough context for the Controller to execute.
type Action struct {
	Kind       ActKind
	RawInput   string   // original or ref-expanded input
	Composed   string   // already composed (for custom commands / skills)
	MemoryNote string   // for ActMemory
	SearchTerm string   // for ActShowSearch
	PermInput  string   // for ActPermissions
	DSTCmd     string   // for ActDST
	NoticeText string   // pre-formatted notice (for ActNotice)
	RefErrs    []string // @-ref resolution errors
}

// ── Classification helpers ──

func (d *Dispatcher) classifyManagement(trimmed string) (Action, bool) {
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return Action{}, false
	}
	var text string
	switch fields[0] {
	case "/model":
		text = d.modelListText()
	case "/memory":
		text = d.memoryListText()
	case "/skill", "/skills":
		text = d.skillListText()
	case "/hooks":
		text = d.hookListText()
	case "/mcp":
		text = d.mcpListText()
	default:
		return Action{}, false
	}
	return Action{Kind: ActNotice, NoticeText: text}, true
}

func (d *Dispatcher) classifyDST(trimmed string) Action {
	return Action{Kind: ActDST, DSTCmd: trimmed}
}

// ── Management notice builders ──

func (d *Dispatcher) modelListText() string {
	var b strings.Builder
	fmt.Fprintf(&b, i18n.M.ListModelsHeaderFmt+"\n", d.h.Label())
	// model refs are built from the arg data passed in — for now use a simple format
	b.WriteString(i18n.M.ListModelsHint)
	return strings.TrimRight(b.String(), "\n")
}

func (d *Dispatcher) memoryListText() string {
	docs := d.h.MemoryDocs()
	if len(docs) == 0 {
		return i18n.M.ListMemoryNone
	}
	var b strings.Builder
	b.WriteString(i18n.M.ListMemoryHeader + "\n")
	for _, doc := range docs {
		fmt.Fprintf(&b, "  (%s) %s\n", doc.Scope, doc.Path)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (d *Dispatcher) skillListText() string {
	skills := d.h.Skills()
	if len(skills) == 0 {
		return i18n.M.ListSkillsNone
	}
	var b strings.Builder
	fmt.Fprintf(&b, i18n.M.ListSkillsHeaderFmt+"\n", len(skills))
	for _, s := range skills {
		tag := ""
		if s.RunAs == "subagent" {
			tag = " 🧬"
		}
		fmt.Fprintf(&b, "  /%s%s — %s\n", s.Name, tag, s.Description)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (d *Dispatcher) hookListText() string {
	hooks := d.h.HookDescs()
	if len(hooks) == 0 {
		return i18n.M.ListHooksNone
	}
	var b strings.Builder
	fmt.Fprintf(&b, i18n.M.ListHooksHeaderFmt+"\n", len(hooks))
	for _, h := range hooks {
		match := h.Match
		if match == "" {
			match = "*"
		}
		fmt.Fprintf(&b, "  %s [%s] %s — %s\n", h.Event, h.Scope, match, h.Command)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (d *Dispatcher) mcpListText() string {
	names := d.h.MCPServerNames()
	if len(names) == 0 {
		return i18n.M.ListMcpNone
	}
	var b strings.Builder
	b.WriteString(i18n.M.ListMcpHeader + "\n")
	for _, name := range names {
		fmt.Fprintf(&b, "  %s\n", name)
	}
	return strings.TrimRight(b.String(), "\n")
}
