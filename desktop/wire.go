package main

import (
	"strings"

	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/eventpipe"
)

// Wire types are aliases to eventpipe.WireEvent (the canonical shared format
// between desktop, HTTP/SSE serve, and the TypeScript frontend). The local
// toWire function converts old-style *event.Event to eventpipe.WireEvent;
// when the controller migrates to typed events this conversion becomes a
// direct eventpipe.ToWire(ev) call.

type wireEvent = eventpipe.WireEvent
type wireAskOption = eventpipe.WireOption
type wireAskQuestion = eventpipe.WireQuestion
type wireAsk = eventpipe.WireAsk
type wireTool = eventpipe.WireTool
type wireUsage = eventpipe.WireUsage
type wireApproval = eventpipe.WireApproval

// kindNames maps the event.Kind enum to stable wire strings.
var kindNames = map[event.Kind]string{
	event.TurnStarted:     "turn_started",
	event.Reasoning:       "reasoning",
	event.Text:            "text",
	event.Message:         "message",
	event.ToolDispatch:    "tool_dispatch",
	event.ToolResult:      "tool_result",
	event.Usage:           "usage",
	event.Notice:          "notice",
	event.Phase:           "phase",
	event.ApprovalRequest: "approval_request",
	event.AskRequest:      "ask_request",
	event.TurnDone:        "turn_done",
}

// toWireAsk converts an event.Ask into its JSON wire form.
func toWireAsk(a event.Ask) *wireAsk {
	qs := make([]wireAskQuestion, len(a.Questions))
	for i, q := range a.Questions {
		opts := make([]wireAskOption, len(q.Options))
		for j, o := range q.Options {
			opts[j] = wireAskOption{Label: o.Label, Description: o.Description}
		}
		qs[i] = wireAskQuestion{ID: q.ID, Header: q.Header, Prompt: q.Prompt, Options: opts, Multi: q.Multi}
	}
	return &wireAsk{ID: a.ID, Questions: qs}
}

// toWire converts an event.Event into the shared wire format.
func toWire(e *event.Event) wireEvent {
	w := wireEvent{Kind: kindNames[e.Kind], Text: e.Text, Reasoning: e.Reasoning}
	// Extract usage from any event kind that carries it (Message, Usage, etc.),
	// so a frontend always gets session-cumulative values regardless of kind.
	if u := e.Usage; u != nil {
		w.Usage = &wireUsage{
			PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens,
			TotalTokens: u.TotalTokens, CacheHitTokens: u.CacheHitTokens,
			CacheMissTokens: u.CacheMissTokens, ReasoningTokens: u.ReasoningTokens,
			SessionCacheHitTokens: e.SessionHit, SessionCacheMissTokens: e.SessionMiss,
		}
		if e.Pricing != nil {
			w.Usage.CostUSD = e.Pricing.Cost(u)
		}
	}
	switch e.Kind {
	case event.Notice:
		if e.Level == event.LevelWarn {
			w.Level = "warn"
		} else {
			w.Level = "info"
		}
		// Skip the auto-discovered plugin list — noisy, not useful in-chat.
		// Controlled by plugin_quiet in ok.toml, but double-filter here so the
		// desktop never shows it regardless of binary age or config loading order.
		if strings.HasPrefix(e.Text, "auto-discovered ") && strings.Contains(e.Text, "plugin(s)") {
			return wireEvent{}
		}
	case event.ToolDispatch, event.ToolResult:
		w.Tool = &wireTool{
			ID: e.Tool.ID, Name: e.Tool.Name, Args: e.Tool.Args,
			Output: e.Tool.Output, Err: e.Tool.Err,
			ReadOnly: e.Tool.ReadOnly, Truncated: e.Tool.Truncated,
			Partial: e.Tool.Partial, ParentID: e.Tool.ParentID,
		}
	case event.ApprovalRequest:
		w.Approval = &wireApproval{ID: e.Approval.ID, Tool: e.Approval.Tool, Subject: e.Approval.Subject}
	case event.AskRequest:
		w.Ask = toWireAsk(e.Ask)
	case event.TurnDone:
		if e.Err != nil {
			w.Err = e.Err.Error()
		}
	}
	return w
}
