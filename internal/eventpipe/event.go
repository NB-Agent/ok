// Package eventpipe implements a Reasonix-inspired event-sourcing pipeline:
// discriminated-union typed events → pure-function reducer projections →
// composable sink middleware chain → JSONL event log.
//
// It lives alongside the old event.Event struct in internal/event; consumers
// can migrate event by event.
package eventpipe

import (
	"encoding/json"
	"time"
)

// --- Meta / Event interface (sealed discriminated union) ---

// Meta carries common fields embedded in every concrete event.
type Meta struct {
	ID   int    `json:"id"`
	TS   string `json:"ts"`
	Turn int    `json:"turn"`
}

func newMeta(turn int, id int) Meta {
	return Meta{
		ID:   id,
		TS:   time.Now().UTC().Format(time.RFC3339Nano),
		Turn: turn,
	}
}

// Event is the marker interface. Only implementations in this package satisfy it.
// Consumers type-switch on the concrete type to access typed fields.
type Event interface {
	event()
	Meta() Meta
	Type() string // machine-readable type tag, e.g. "user.message"
}

// envelope wraps any Event for JSON serialization.
type envelope struct {
	Type string      `json:"type"`
	Meta Meta        `json:"meta"`
	Body interface{} `json:"body"`
}

// MarshalEvent serializes any Event to JSON. The top-level {"type","meta","body"}
// envelope makes it self-describing for log/replay.
func MarshalEvent(ev Event) ([]byte, error) {
	return json.Marshal(envelope{
		Type: ev.Type(),
		Meta: ev.Meta(),
		Body: ev,
	})
}

// --- Event types ---

type UserMessageEvent struct {
	meta Meta
	Text string `json:"text"`
}

func NewUserMessageEvent(turn int, id int, text string) UserMessageEvent {
	return UserMessageEvent{meta: newMeta(turn, id), Text: text}
}
func (e UserMessageEvent) event()       {}
func (e UserMessageEvent) Meta() Meta   { return e.meta }
func (e UserMessageEvent) Type() string { return "user.message" }

type ModelTurnStartedEvent struct {
	meta            Meta
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort"`
	PrefixHash      string `json:"prefixHash"`
}

func NewModelTurnStartedEvent(turn int, id int, model, effort, prefixHash string) ModelTurnStartedEvent {
	return ModelTurnStartedEvent{meta: newMeta(turn, id), Model: model, ReasoningEffort: effort, PrefixHash: prefixHash}
}
func (e ModelTurnStartedEvent) event()       {}
func (e ModelTurnStartedEvent) Meta() Meta   { return e.meta }
func (e ModelTurnStartedEvent) Type() string { return "model.turn.started" }

type ModelDeltaEvent struct {
	meta    Meta
	Channel string `json:"channel"` // "content" | "reasoning" | "tool_args"
	Text    string `json:"text"`
}

func NewModelDeltaEvent(turn int, id int, channel, text string) ModelDeltaEvent {
	return ModelDeltaEvent{meta: newMeta(turn, id), Channel: channel, Text: text}
}
func (e ModelDeltaEvent) event()       {}
func (e ModelDeltaEvent) Meta() Meta   { return e.meta }
func (e ModelDeltaEvent) Type() string { return "model.delta" }

type Usage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
	CacheHitTokens   int `json:"prompt_cache_hit_tokens,omitempty"`
	CacheMissTokens  int `json:"prompt_cache_miss_tokens,omitempty"`
}

type ModelFinalEvent struct {
	meta             Meta
	Content          string      `json:"content"`
	ReasoningContent string      `json:"reasoningContent,omitempty"`
	Usage            Usage       `json:"usage,omitempty"`
	CostUSD          float64     `json:"costUsd"`
	ForcedSummary    bool        `json:"forcedSummary,omitempty"`
	CacheDiagnostics interface{} `json:"-"`
}

func NewModelFinalEvent(turn int, id int, content, reasoning string, usage Usage, cost float64) ModelFinalEvent {
	return ModelFinalEvent{meta: newMeta(turn, id), Content: content, ReasoningContent: reasoning, Usage: usage, CostUSD: cost}
}
func (e ModelFinalEvent) event()       {}
func (e ModelFinalEvent) Meta() Meta   { return e.meta }
func (e ModelFinalEvent) Type() string { return "model.final" }

type ToolPreparingEvent struct {
	meta   Meta
	CallID string `json:"callId"`
	Name   string `json:"name"`
}

func NewToolPreparingEvent(turn int, id int, callID, name string) ToolPreparingEvent {
	return ToolPreparingEvent{meta: newMeta(turn, id), CallID: callID, Name: name}
}
func (e ToolPreparingEvent) event()       {}
func (e ToolPreparingEvent) Meta() Meta   { return e.meta }
func (e ToolPreparingEvent) Type() string { return "tool.preparing" }

type ToolIntentEvent struct {
	meta     Meta
	CallID   string `json:"callId"`
	Name     string `json:"name"`
	Args     string `json:"args"`
	ReadOnly bool   `json:"readOnly,omitempty"`
	ParentID string `json:"parentId,omitempty"`
}

func NewToolIntentEvent(turn int, id int, callID, name, args string, readOnly bool, parentID string) ToolIntentEvent {
	return ToolIntentEvent{meta: newMeta(turn, id), CallID: callID, Name: name, Args: args, ReadOnly: readOnly, ParentID: parentID}
}
func (e ToolIntentEvent) event()       {}
func (e ToolIntentEvent) Meta() Meta   { return e.meta }
func (e ToolIntentEvent) Type() string { return "tool.intent" }

type ToolDeniedEvent struct {
	meta   Meta
	CallID string `json:"callId"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

func NewToolDeniedEvent(turn int, id int, callID, name, reason string) ToolDeniedEvent {
	return ToolDeniedEvent{meta: newMeta(turn, id), CallID: callID, Name: name, Reason: reason}
}
func (e ToolDeniedEvent) event()       {}
func (e ToolDeniedEvent) Meta() Meta   { return e.meta }
func (e ToolDeniedEvent) Type() string { return "tool.denied" }

type ToolResultEvent struct {
	meta       Meta
	CallID     string `json:"callId"`
	Name       string `json:"name"`
	OK         bool   `json:"ok"`
	Output     string `json:"output,omitempty"`
	Err        string `json:"err,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
}

func NewToolResultEvent(turn int, id int, callID, name string, ok bool, output, errMsg string, truncated bool, durMs int64) ToolResultEvent {
	return ToolResultEvent{meta: newMeta(turn, id), CallID: callID, Name: name, OK: ok, Output: output, Err: errMsg, Truncated: truncated, DurationMs: durMs}
}
func (e ToolResultEvent) event()       {}
func (e ToolResultEvent) Meta() Meta   { return e.meta }
func (e ToolResultEvent) Type() string { return "tool.result" }

type StatusEvent struct {
	meta Meta
	Text string `json:"text"`
}

func NewStatusEvent(turn int, id int, text string) StatusEvent {
	return StatusEvent{meta: newMeta(turn, id), Text: text}
}
func (e StatusEvent) event()       {}
func (e StatusEvent) Meta() Meta   { return e.meta }
func (e StatusEvent) Type() string { return "status" }

type ErrorEvent struct {
	meta        Meta
	Message     string `json:"message"`
	Recoverable bool   `json:"recoverable"`
	Name        string `json:"name,omitempty"`
	Code        string `json:"code,omitempty"`
	Phase       string `json:"phase,omitempty"`
}

func NewErrorEvent(turn int, id int, msg string, recoverable bool) ErrorEvent {
	return ErrorEvent{meta: newMeta(turn, id), Message: msg, Recoverable: recoverable}
}
func (e ErrorEvent) event()       {}
func (e ErrorEvent) Meta() Meta   { return e.meta }
func (e ErrorEvent) Type() string { return "error" }

type NoticeLevel int

const (
	NoticeLevelInfo NoticeLevel = iota
	NoticeLevelWarn
)

type NoticeEvent struct {
	meta  Meta
	Text  string      `json:"text"`
	Level NoticeLevel `json:"level"`
}

func NewNoticeEvent(turn int, id int, text string, level NoticeLevel) NoticeEvent {
	return NoticeEvent{meta: newMeta(turn, id), Text: text, Level: level}
}
func (e NoticeEvent) event()       {}
func (e NoticeEvent) Meta() Meta   { return e.meta }
func (e NoticeEvent) Type() string { return "notice" }

type PhaseEvent struct {
	meta Meta
	Text string `json:"text"`
}

func NewPhaseEvent(turn int, id int, text string) PhaseEvent {
	return PhaseEvent{meta: newMeta(turn, id), Text: text}
}
func (e PhaseEvent) event()       {}
func (e PhaseEvent) Meta() Meta   { return e.meta }
func (e PhaseEvent) Type() string { return "phase" }

type ApprovalRequestEvent struct {
	meta    Meta
	AskID   string `json:"askId"`
	Tool    string `json:"tool"`
	Subject string `json:"subject"`
}

func NewApprovalRequestEvent(turn int, id int, askID, tool, subject string) ApprovalRequestEvent {
	return ApprovalRequestEvent{meta: newMeta(turn, id), AskID: askID, Tool: tool, Subject: subject}
}
func (e ApprovalRequestEvent) event()       {}
func (e ApprovalRequestEvent) Meta() Meta   { return e.meta }
func (e ApprovalRequestEvent) Type() string { return "approval.request" }

type AskOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type AskQuestion struct {
	ID      string      `json:"id"`
	Header  string      `json:"header"`
	Prompt  string      `json:"prompt"`
	Options []AskOption `json:"options"`
	Multi   bool        `json:"multi"`
}

type AskRequestEvent struct {
	meta      Meta
	AskID     string        `json:"askId"`
	Questions []AskQuestion `json:"questions"`
}

func NewAskRequestEvent(turn int, id int, askID string, questions []AskQuestion) AskRequestEvent {
	return AskRequestEvent{meta: newMeta(turn, id), AskID: askID, Questions: questions}
}
func (e AskRequestEvent) event()       {}
func (e AskRequestEvent) Meta() Meta   { return e.meta }
func (e AskRequestEvent) Type() string { return "ask.request" }

type TurnDoneEvent struct {
	meta Meta
	Err  error  `json:"-"`
	Text string `json:"text,omitempty"`
}

func NewTurnDoneEvent(turn int, id int, err error) TurnDoneEvent {
	return TurnDoneEvent{meta: newMeta(turn, id), Err: err}
}
func (e TurnDoneEvent) event()       {}
func (e TurnDoneEvent) Meta() Meta   { return e.meta }
func (e TurnDoneEvent) Type() string { return "turn.done" }

type TurnAbortedEvent struct {
	meta     Meta
	Err      error  `json:"-"`
	Covenant string `json:"covenant"`
	Reason   string `json:"reason"`
}

func NewTurnAbortedEvent(turn int, id int, err error, covenantID, reason string) TurnAbortedEvent {
	return TurnAbortedEvent{meta: newMeta(turn, id), Err: err, Covenant: covenantID, Reason: reason}
}
func (e TurnAbortedEvent) event()       {}
func (e TurnAbortedEvent) Meta() Meta   { return e.meta }
func (e TurnAbortedEvent) Type() string { return "turn.aborted" }

type PlanStep struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Action string `json:"action"`
	Risk   string `json:"risk,omitempty"`
}

type PlanSubmittedEvent struct {
	meta  Meta
	Steps []PlanStep `json:"steps"`
	Body  string     `json:"body"`
}

func NewPlanSubmittedEvent(turn int, id int, steps []PlanStep, body string) PlanSubmittedEvent {
	return PlanSubmittedEvent{meta: newMeta(turn, id), Steps: steps, Body: body}
}
func (e PlanSubmittedEvent) event()       {}
func (e PlanSubmittedEvent) Meta() Meta   { return e.meta }
func (e PlanSubmittedEvent) Type() string { return "plan.submitted" }

type PlanStepChangedEvent struct {
	meta   Meta
	StepID string `json:"stepId"`
	Title  string `json:"title,omitempty"`
	Notes  string `json:"notes,omitempty"`
	Status string `json:"status"`
}

func NewPlanStepChangedEvent(turn int, id int, stepID, title, notes, status string) PlanStepChangedEvent {
	return PlanStepChangedEvent{meta: newMeta(turn, id), StepID: stepID, Title: title, Notes: notes, Status: status}
}
func (e PlanStepChangedEvent) event()       {}
func (e PlanStepChangedEvent) Meta() Meta   { return e.meta }
func (e PlanStepChangedEvent) Type() string { return "plan.step.changed" }

type BudgetWarningEvent struct {
	meta     Meta
	SpentUSD float64 `json:"spentUsd"`
	CapUSD   float64 `json:"capUsd"`
}

func NewBudgetWarningEvent(turn int, id int, spent, cap float64) BudgetWarningEvent {
	return BudgetWarningEvent{meta: newMeta(turn, id), SpentUSD: spent, CapUSD: cap}
}
func (e BudgetWarningEvent) event()       {}
func (e BudgetWarningEvent) Meta() Meta   { return e.meta }
func (e BudgetWarningEvent) Type() string { return "budget.warning" }

type BudgetBlockedEvent struct {
	meta     Meta
	SpentUSD float64 `json:"spentUsd"`
	CapUSD   float64 `json:"capUsd"`
}

func NewBudgetBlockedEvent(turn int, id int, spent, cap float64) BudgetBlockedEvent {
	return BudgetBlockedEvent{meta: newMeta(turn, id), SpentUSD: spent, CapUSD: cap}
}
func (e BudgetBlockedEvent) event()       {}
func (e BudgetBlockedEvent) Meta() Meta   { return e.meta }
func (e BudgetBlockedEvent) Type() string { return "budget.blocked" }

type SessionOpenedEvent struct {
	meta            Meta
	Name            string `json:"name"`
	ResumedFromTurn int    `json:"resumedFromTurn"`
}

func NewSessionOpenedEvent(turn int, id int, name string, resumedFromTurn int) SessionOpenedEvent {
	return SessionOpenedEvent{meta: newMeta(turn, id), Name: name, ResumedFromTurn: resumedFromTurn}
}
func (e SessionOpenedEvent) event()       {}
func (e SessionOpenedEvent) Meta() Meta   { return e.meta }
func (e SessionOpenedEvent) Type() string { return "session.opened" }

type SessionCompactedEvent struct {
	meta           Meta
	BeforeMessages int    `json:"beforeMessages"`
	AfterMessages  int    `json:"afterMessages"`
	Reason         string `json:"reason"`
}

func NewSessionCompactedEvent(turn int, id int, before, after int, reason string) SessionCompactedEvent {
	return SessionCompactedEvent{meta: newMeta(turn, id), BeforeMessages: before, AfterMessages: after, Reason: reason}
}
func (e SessionCompactedEvent) event()       {}
func (e SessionCompactedEvent) Meta() Meta   { return e.meta }
func (e SessionCompactedEvent) Type() string { return "session.compacted" }
