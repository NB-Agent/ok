package eventpipe

// Reducer is a pure function that folds an Event into a view. Deterministic,
// no I/O, no mutation. Given the same (view, event) pair it always returns
// the same result.
type Reducer[T any] func(view T, ev Event) T

// --- ConversationView: user↔assistant message log ---

type PendingTool struct {
	CallID string `json:"callId"`
	Name   string `json:"name"`
}

type ConversationView struct {
	Messages         []ChatMsg     `json:"messages"`
	PendingToolCalls []PendingTool `json:"pendingToolCalls"`
}

// ChatMsg is a simplified representation for the conversation transcript.
type ChatMsg struct {
	Role    string `json:"role"` // "user" | "assistant" | "tool"
	Content string `json:"content"`
}

func EmptyConversation() ConversationView {
	return ConversationView{
		Messages:         []ChatMsg{},
		PendingToolCalls: []PendingTool{},
	}
}

var ConversationReducer Reducer[ConversationView] = func(v ConversationView, ev Event) ConversationView {
	switch e := ev.(type) {
	case UserMessageEvent:
		return pushMsg(v, ChatMsg{Role: "user", Content: e.Text})

	case ModelFinalEvent:
		return pushMsg(v, ChatMsg{Role: "assistant", Content: e.Content})

	case ToolIntentEvent:
		return ConversationView{
			Messages:         v.Messages,
			PendingToolCalls: append(v.PendingToolCalls, PendingTool{CallID: e.CallID, Name: e.Name}),
		}

	case ToolResultEvent:
		return ConversationView{
			Messages:         append(v.Messages, ChatMsg{Role: "tool", Content: e.Output}),
			PendingToolCalls: removePending(v.PendingToolCalls, e.CallID),
		}

	case ToolDeniedEvent:
		return ConversationView{
			Messages:         append(v.Messages, ChatMsg{Role: "tool", Content: "denied: " + e.Reason}),
			PendingToolCalls: removePending(v.PendingToolCalls, e.CallID),
		}

	default:
		return v
	}
}

func pushMsg(v ConversationView, msg ChatMsg) ConversationView {
	return ConversationView{
		Messages:         append(v.Messages, msg),
		PendingToolCalls: v.PendingToolCalls,
	}
}

func removePending(pending []PendingTool, callID string) []PendingTool {
	for i, p := range pending {
		if p.CallID == callID {
			return append(pending[:i], pending[i+1:]...)
		}
	}
	return pending
}

// --- BudgetView: cost + token tracking ---

type BudgetView struct {
	SpentUSD         float64 `json:"spentUsd"`
	CapUSD           float64 `json:"capUsd,omitempty"`
	PromptTokens     int     `json:"promptTokens"`
	CompletionTokens int     `json:"completionTokens"`
	CacheHitTokens   int     `json:"cacheHitTokens"`
	CacheMissTokens  int     `json:"cacheMissTokens"`
	Warned           bool    `json:"warned"`
	Blocked          bool    `json:"blocked"`
}

func EmptyBudget(capUSD float64) BudgetView {
	return BudgetView{CapUSD: capUSD}
}

var BudgetReducer Reducer[BudgetView] = func(v BudgetView, ev Event) BudgetView {
	switch e := ev.(type) {
	case ModelFinalEvent:
		v.SpentUSD += e.CostUSD
		v.PromptTokens += e.Usage.PromptTokens
		v.CompletionTokens += e.Usage.CompletionTokens
		v.CacheHitTokens += e.Usage.CacheHitTokens
		v.CacheMissTokens += e.Usage.CacheMissTokens
		return v
	case BudgetWarningEvent:
		v.Warned = true
		v.SpentUSD = e.SpentUSD
		return v
	case BudgetBlockedEvent:
		v.Blocked = true
		v.SpentUSD = e.SpentUSD
		return v
	default:
		return v
	}
}

// --- PlanView: execution plan ---

type PlanStepView struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Action    string `json:"action"`
	Risk      string `json:"risk,omitempty"`
	Completed bool   `json:"completed"`
	Notes     string `json:"notes,omitempty"`
}

type PlanView struct {
	Steps         []PlanStepView `json:"steps"`
	Body          string         `json:"body,omitempty"`
	SubmittedTurn int            `json:"submittedTurn,omitempty"`
}

func EmptyPlan() PlanView { return PlanView{} }

var PlanReducer Reducer[PlanView] = func(v PlanView, ev Event) PlanView {
	switch e := ev.(type) {
	case PlanSubmittedEvent:
		steps := make([]PlanStepView, len(e.Steps))
		for i, s := range e.Steps {
			steps[i] = PlanStepView{
				ID: s.ID, Title: s.Title, Action: s.Action,
				Risk: s.Risk, Completed: false,
			}
		}
		return PlanView{Steps: steps, Body: e.Body, SubmittedTurn: e.meta.Turn}

	case PlanStepChangedEvent:
		for i := range v.Steps {
			if v.Steps[i].ID == e.StepID {
				v.Steps[i].Completed = e.Status == "completed"
				v.Steps[i].Notes = e.Notes
				break
			}
		}
		return v

	default:
		return v
	}
}

// --- WorkspaceView: file changes ---

type FileChange struct {
	Path string `json:"path"`
	Mode string `json:"mode"` // "create" | "edit" | "delete"
}

type WorkspaceView struct {
	FilesTouched   []FileChange `json:"filesTouched"`
	LastCheckpoint string       `json:"lastCheckpoint,omitempty"`
}

func EmptyWorkspace() WorkspaceView {
	return WorkspaceView{FilesTouched: []FileChange{}}
}

// WorkspaceReducer reduces file-touch and checkpoint events. Currently a
// placeholder — wire up actual FileTouchedEvent / CheckpointEvent when those
// typed events are added.
var WorkspaceReducer Reducer[WorkspaceView] = func(v WorkspaceView, ev Event) WorkspaceView {
	_ = ev // reserved
	return v
}

// --- ProjectionSet: all projections in one struct ---

type ProjectionSet struct {
	Conversation ConversationView `json:"conversation"`
	Budget       BudgetView       `json:"budget"`
	Plan         PlanView         `json:"plan"`
	Workspace    WorkspaceView    `json:"workspace"`
}

func EmptyProjections(capUSD float64) ProjectionSet {
	return ProjectionSet{
		Conversation: EmptyConversation(),
		Budget:       EmptyBudget(capUSD),
		Plan:         EmptyPlan(),
		Workspace:    EmptyWorkspace(),
	}
}

// Apply folds one event into all projections.
func Apply(state ProjectionSet, ev Event) ProjectionSet {
	return ProjectionSet{
		Conversation: ConversationReducer(state.Conversation, ev),
		Budget:       BudgetReducer(state.Budget, ev),
		Plan:         PlanReducer(state.Plan, ev),
		Workspace:    WorkspaceReducer(state.Workspace, ev),
	}
}

// Replay folds an event sequence into a projection set.
func Replay(events []Event, capUSD float64) ProjectionSet {
	s := EmptyProjections(capUSD)
	for _, ev := range events {
		s = Apply(s, ev)
	}
	return s
}
