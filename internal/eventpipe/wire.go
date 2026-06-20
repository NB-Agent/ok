package eventpipe

// WireEvent is the JSON wire format shared by HTTP/SSE and desktop frontends.
// It mirrors the old toWire contract field-for-field so both transports emit
// the identical typed stream and a single JS/TS type definition (wireEvent)
// serves both.
//
// This replaces the previously duplicated toWire in internal/serve/wire.go and
// desktop/wire.go.
type WireEvent struct {
	Kind      string        `json:"kind"`
	Text      string        `json:"text,omitempty"`
	Reasoning string        `json:"reasoning,omitempty"`
	Level     string        `json:"level,omitempty"`
	Tool      *WireTool     `json:"tool,omitempty"`
	Usage     *WireUsage    `json:"usage,omitempty"`
	Approval  *WireApproval `json:"approval,omitempty"`
	Ask       *WireAsk      `json:"ask,omitempty"`
	Err       string        `json:"err,omitempty"`
}

type WireOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type WireQuestion struct {
	ID      string       `json:"id"`
	Header  string       `json:"header,omitempty"`
	Prompt  string       `json:"prompt"`
	Options []WireOption `json:"options"`
	Multi   bool         `json:"multi,omitempty"`
}

type WireAsk struct {
	ID        string         `json:"id"`
	Questions []WireQuestion `json:"questions"`
}

type WireTool struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Args      string `json:"args,omitempty"`
	Output    string `json:"output,omitempty"`
	Err       string `json:"err,omitempty"`
	ReadOnly  bool   `json:"readOnly"`
	Truncated bool   `json:"truncated,omitempty"`
	Partial   bool   `json:"partial,omitempty"`
	ParentID  string `json:"parentId,omitempty"`
}

type WireUsage struct {
	PromptTokens           int     `json:"promptTokens"`
	CompletionTokens       int     `json:"completionTokens"`
	TotalTokens            int     `json:"totalTokens"`
	CacheHitTokens         int     `json:"cacheHitTokens"`
	CacheMissTokens        int     `json:"cacheMissTokens"`
	ReasoningTokens        int     `json:"reasoningTokens,omitempty"`
	SessionCacheHitTokens  int     `json:"sessionCacheHitTokens"`
	SessionCacheMissTokens int     `json:"sessionCacheMissTokens"`
	CostUSD                float64 `json:"costUsd,omitempty"`
}

type WireApproval struct {
	ID      string `json:"id"`
	Tool    string `json:"tool"`
	Subject string `json:"subject"`
}

// ToWire converts an eventpipe.Event to the shared JSON wire format.
func ToWire(ev Event) WireEvent {
	switch e := ev.(type) {
	case UserMessageEvent:
		return WireEvent{Kind: "text", Text: e.Text}

	case ModelTurnStartedEvent:
		return WireEvent{Kind: "turn_started"}

	case ModelDeltaEvent:
		switch e.Channel {
		case "reasoning":
			return WireEvent{Kind: "reasoning", Text: e.Text}
		default:
			return WireEvent{Kind: "text", Text: e.Text}
		}

	case ModelFinalEvent:
		u := WireUsage{
			PromptTokens:           e.Usage.PromptTokens,
			CompletionTokens:       e.Usage.CompletionTokens,
			TotalTokens:            e.Usage.TotalTokens,
			CacheHitTokens:         e.Usage.CacheHitTokens,
			CacheMissTokens:        e.Usage.CacheMissTokens,
			SessionCacheHitTokens:  e.Usage.CacheHitTokens,
			SessionCacheMissTokens: e.Usage.CacheMissTokens,
			CostUSD:                e.CostUSD,
		}
		return WireEvent{
			Kind: "message", Text: e.Content,
			Reasoning: e.ReasoningContent, Usage: &u,
		}

	case ToolPreparingEvent:
		return WireEvent{
			Kind: "tool_dispatch",
			Tool: &WireTool{ID: e.CallID, Name: e.Name, Partial: true},
		}

	case ToolIntentEvent:
		return WireEvent{
			Kind: "tool_dispatch",
			Tool: &WireTool{
				ID: e.CallID, Name: e.Name, Args: e.Args,
				ReadOnly: e.ReadOnly, Partial: false, ParentID: e.ParentID,
			},
		}

	case ToolDeniedEvent:
		return WireEvent{
			Kind: "tool_result",
			Tool: &WireTool{ID: e.CallID, Name: e.Name, Err: e.Reason},
		}

	case ToolResultEvent:
		w := WireTool{
			ID: e.CallID, Name: e.Name, Output: e.Output,
			Truncated: e.Truncated,
		}
		if e.Err != "" {
			w.Err = e.Err
		}
		return WireEvent{Kind: "tool_result", Tool: &w}

	case StatusEvent:
		return WireEvent{Kind: "status", Text: e.Text}

	case ErrorEvent:
		return WireEvent{Kind: "error", Text: e.Message}

	case NoticeEvent:
		lvl := "info"
		if e.Level == NoticeLevelWarn {
			lvl = "warn"
		}
		return WireEvent{Kind: "notice", Text: e.Text, Level: lvl}

	case PhaseEvent:
		return WireEvent{Kind: "phase", Text: e.Text}

	case ApprovalRequestEvent:
		return WireEvent{
			Kind:     "approval_request",
			Approval: &WireApproval{ID: e.AskID, Tool: e.Tool, Subject: e.Subject},
		}

	case AskRequestEvent:
		questions := make([]WireQuestion, len(e.Questions))
		for i, q := range e.Questions {
			opts := make([]WireOption, len(q.Options))
			for j, o := range q.Options {
				opts[j] = WireOption(o)
			}
			questions[i] = WireQuestion{
				ID: q.ID, Header: q.Header, Prompt: q.Prompt,
				Options: opts, Multi: q.Multi,
			}
		}
		return WireEvent{
			Kind: "ask_request",
			Ask:  &WireAsk{ID: e.AskID, Questions: questions},
		}

	case TurnDoneEvent:
		w := WireEvent{Kind: "turn_done"}
		if e.Err != nil {
			w.Err = e.Err.Error()
		}
		return w

	case TurnAbortedEvent:
		return WireEvent{Kind: "turn_done", Err: e.Reason}

	case PlanSubmittedEvent:
		return WireEvent{Kind: "notice", Text: "plan: " + e.Body, Level: "info"}

	case PlanStepChangedEvent:
		return WireEvent{Kind: "notice", Text: "plan step: " + e.Status + " " + e.StepID, Level: "info"}

	case BudgetWarningEvent:
		return WireEvent{Kind: "notice", Text: "budget warning", Level: "warn"}

	case BudgetBlockedEvent:
		return WireEvent{Kind: "notice", Text: "budget blocked", Level: "warn"}

	case SessionOpenedEvent:
		return WireEvent{Kind: "notice", Text: "session: " + e.Name, Level: "info"}

	case SessionCompactedEvent:
		return WireEvent{Kind: "notice", Text: "session compacted", Level: "info"}

	default:
		return WireEvent{Kind: "unknown"}
	}
}
