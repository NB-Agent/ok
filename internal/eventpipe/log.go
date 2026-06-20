package eventpipe

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/NB-Agent/ok/internal/log"
)

// LogConfig controls event log behaviour.
type LogConfig struct {
	Dir       string // directory to write event log files
	SessionID string // session identifier for the filename
}

// LogSink writes every event to a JSONL file for replay and auditing.
// Each line is a self-describing {"type":"...","meta":{...},"body":{...}} JSON object.
type LogSink struct {
	mu       sync.Mutex
	f        *os.File
	bw       *bufio.Writer
	enc      *json.Encoder
	path     string
	writeErr error // first write error, surfaced by Flush/Close
}

// NewLogSink opens (or creates) a JSONL event log file at dir/sessionID.events.jsonl.
func NewLogSink(cfg LogConfig) (*LogSink, error) {
	if cfg.Dir == "" {
		cfg.Dir = "."
	}
	if err := os.MkdirAll(cfg.Dir, 0755); err != nil {
		return nil, fmt.Errorf("eventpipe: mkdir event log dir: %w", err)
	}
	name := cfg.SessionID
	if name == "" {
		name = "events"
	}
	path := filepath.Join(cfg.Dir, name+".events.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("eventpipe: open event log: %w", err)
	}
	bw := bufio.NewWriter(f)
	return &LogSink{
		f:    f,
		bw:   bw,
		enc:  json.NewEncoder(bw),
		path: path,
	}, nil
}

// Path returns the log file path.
func (l *LogSink) Path() string { return l.path }

// Emit appends one event to the log file. After the first write
// error, subsequent events are silently dropped; the error is
// surfaced via Flush() and Close().
func (l *LogSink) Emit(ev Event) {
	data, err := MarshalEvent(ev)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.writeErr != nil {
		return // already in error state
	}
	if _, err := l.bw.Write(data); err != nil {
		l.writeErr = err
		return
	}
	if err := l.bw.WriteByte('\n'); err != nil {
		l.writeErr = err
	}
}

// Flush writes buffered data to disk. Reports the first prior
// write error, if any, alongside the flush result.
func (l *LogSink) Flush() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.writeErr != nil {
		return l.writeErr
	}
	return l.bw.Flush()
}

// Close flushes and closes the log file. Reports the first prior
// write error, if any.
func (l *LogSink) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.writeErr != nil {
		l.bw.Flush() // best-effort
		l.f.Close()
		return l.writeErr
	}
	l.bw.Flush()
	return l.f.Close()
}

// --- Replay: read back a JSONL event log ---

// ReadLog reads a JSONL file and returns the parsed events.
func ReadLog(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer log.Close("event log", f)

	var events []Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		ev, err := unmarshalEvent(line)
		if err != nil {
			continue
		}
		events = append(events, ev)
	}
	return events, scanner.Err()
}

// --- JSON wire format ---

type rawEnvelope struct {
	Type string          `json:"type"`
	Meta Meta            `json:"meta"`
	Body json.RawMessage `json:"body"`
}

func unmarshalEvent(data []byte) (Event, error) {
	var raw rawEnvelope
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return unmarshalBody(raw.Type, raw.Body, raw.Meta)
}

func unmarshalBody(typ string, body json.RawMessage, meta Meta) (Event, error) {
	switch typ {
	case "user.message":
		var e struct{ Text string }
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		r := UserMessageEvent{}
		r.meta = meta
		r.Text = e.Text
		return r, nil

	case "model.turn.started":
		var e struct {
			Model           string `json:"model"`
			ReasoningEffort string `json:"reasoningEffort"`
			PrefixHash      string `json:"prefixHash"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		return ModelTurnStartedEvent{meta: meta, Model: e.Model, ReasoningEffort: e.ReasoningEffort, PrefixHash: e.PrefixHash}, nil

	case "model.delta":
		var e struct {
			Channel string `json:"channel"`
			Text    string `json:"text"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		return ModelDeltaEvent{meta: meta, Channel: e.Channel, Text: e.Text}, nil

	case "model.final":
		var e struct {
			Content          string  `json:"content"`
			ReasoningContent string  `json:"reasoningContent,omitempty"`
			Usage            Usage   `json:"usage,omitempty"`
			CostUSD          float64 `json:"costUsd"`
			ForcedSummary    bool    `json:"forcedSummary,omitempty"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		return ModelFinalEvent{meta: meta, Content: e.Content, ReasoningContent: e.ReasoningContent, Usage: e.Usage, CostUSD: e.CostUSD, ForcedSummary: e.ForcedSummary}, nil

	case "tool.preparing":
		var e struct {
			CallID string `json:"callId"`
			Name   string `json:"name"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		return ToolPreparingEvent{meta: meta, CallID: e.CallID, Name: e.Name}, nil

	case "tool.intent":
		var e struct {
			CallID   string `json:"callId"`
			Name     string `json:"name"`
			Args     string `json:"args"`
			ReadOnly bool   `json:"readOnly,omitempty"`
			ParentID string `json:"parentId,omitempty"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		return ToolIntentEvent{meta: meta, CallID: e.CallID, Name: e.Name, Args: e.Args, ReadOnly: e.ReadOnly, ParentID: e.ParentID}, nil

	case "tool.denied":
		var e struct {
			CallID string `json:"callId"`
			Name   string `json:"name"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		return ToolDeniedEvent{meta: meta, CallID: e.CallID, Name: e.Name, Reason: e.Reason}, nil

	case "tool.result":
		var e struct {
			CallID     string `json:"callId"`
			Name       string `json:"name"`
			OK         bool   `json:"ok"`
			Output     string `json:"output,omitempty"`
			Err        string `json:"err,omitempty"`
			Truncated  bool   `json:"truncated,omitempty"`
			DurationMs int64  `json:"durationMs,omitempty"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		return ToolResultEvent{meta: meta, CallID: e.CallID, Name: e.Name, OK: e.OK, Output: e.Output, Err: e.Err, Truncated: e.Truncated, DurationMs: e.DurationMs}, nil

	case "status":
		var e struct{ Text string }
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		return StatusEvent{meta: meta, Text: e.Text}, nil

	case "error":
		var e struct {
			Message     string `json:"message"`
			Recoverable bool   `json:"recoverable"`
			Name        string `json:"name,omitempty"`
			Code        string `json:"code,omitempty"`
			Phase       string `json:"phase,omitempty"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		return ErrorEvent{meta: meta, Message: e.Message, Recoverable: e.Recoverable, Name: e.Name, Code: e.Code, Phase: e.Phase}, nil

	case "notice":
		var e struct {
			Text  string      `json:"text"`
			Level NoticeLevel `json:"level"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		return NoticeEvent{meta: meta, Text: e.Text, Level: e.Level}, nil

	case "phase":
		var e struct{ Text string }
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		return PhaseEvent{meta: meta, Text: e.Text}, nil

	case "approval.request":
		var e struct {
			AskID   string `json:"askId"`
			Tool    string `json:"tool"`
			Subject string `json:"subject"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		return ApprovalRequestEvent{meta: meta, AskID: e.AskID, Tool: e.Tool, Subject: e.Subject}, nil

	case "ask.request":
		var e struct {
			AskID     string        `json:"askId"`
			Questions []AskQuestion `json:"questions"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		return AskRequestEvent{meta: meta, AskID: e.AskID, Questions: e.Questions}, nil

	case "turn.done":
		var e struct {
			Text string `json:"text,omitempty"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		return TurnDoneEvent{meta: meta, Text: e.Text}, nil

	case "turn.aborted":
		var e struct {
			Covenant string `json:"covenant"`
			Reason   string `json:"reason"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		return TurnAbortedEvent{meta: meta, Covenant: e.Covenant, Reason: e.Reason}, nil

	case "plan.submitted":
		var e struct {
			Steps []PlanStep `json:"steps"`
			Body  string     `json:"body"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		return PlanSubmittedEvent{meta: meta, Steps: e.Steps, Body: e.Body}, nil

	case "plan.step.changed":
		var e struct {
			StepID string `json:"stepId"`
			Title  string `json:"title,omitempty"`
			Notes  string `json:"notes,omitempty"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		return PlanStepChangedEvent{meta: meta, StepID: e.StepID, Title: e.Title, Notes: e.Notes, Status: e.Status}, nil

	case "budget.warning":
		var e struct {
			SpentUSD float64 `json:"spentUsd"`
			CapUSD   float64 `json:"capUsd"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		return BudgetWarningEvent{meta: meta, SpentUSD: e.SpentUSD, CapUSD: e.CapUSD}, nil

	case "budget.blocked":
		var e struct {
			SpentUSD float64 `json:"spentUsd"`
			CapUSD   float64 `json:"capUsd"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		return BudgetBlockedEvent{meta: meta, SpentUSD: e.SpentUSD, CapUSD: e.CapUSD}, nil

	case "session.opened":
		var e struct {
			Name            string `json:"name"`
			ResumedFromTurn int    `json:"resumedFromTurn"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		return SessionOpenedEvent{meta: meta, Name: e.Name, ResumedFromTurn: e.ResumedFromTurn}, nil

	case "session.compacted":
		var e struct {
			BeforeMessages int    `json:"beforeMessages"`
			AfterMessages  int    `json:"afterMessages"`
			Reason         string `json:"reason"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		return SessionCompactedEvent{meta: meta, BeforeMessages: e.BeforeMessages, AfterMessages: e.AfterMessages, Reason: e.Reason}, nil

	default:
		return nil, fmt.Errorf("unknown event type: %s", typ)
	}
}
