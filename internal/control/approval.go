package control

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/permission"
)

// ApprovalManager manages the approval lifecycle for writer tools.
type ApprovalManager struct {
	mu            sync.Mutex
	nextID        atomic.Uint64
	approvals     map[string]chan approvalResult
	approvalMeta  map[string]approvalRecord // id → {tool, subject} for session grant
	sessionGrants map[string]bool           // "tool\x00subject" → allowed for this session
	autoApprove   bool
	bypass        bool
	sink          event.Sink
	lastRemember  bool // set by Approve, read by gateApprover via requestApproval
}

type approvalRecord struct {
	tool    string
	subject string
}

type approvalResult struct {
	allow    bool
	reason   string
	remember bool
	answers  []event.AskAnswer
}

// NewApprovalManager creates an ApprovalManager.
func NewApprovalManager(sink event.Sink) *ApprovalManager {
	return &ApprovalManager{
		approvals:     make(map[string]chan approvalResult, 16),
		approvalMeta:  make(map[string]approvalRecord, 16),
		sessionGrants: make(map[string]bool, 32),
		sink:          sink,
	}
}

// SetAutoApprove sets auto-approve mode (used during plan execution).
func (m *ApprovalManager) SetAutoApprove(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.autoApprove = v
}

// SetBypass enables/disables bypass mode.
func (m *ApprovalManager) SetBypass(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bypass = v
}

// Bypass reports bypass mode state.
func (m *ApprovalManager) Bypass() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bypass
}

// Approve resolves an outstanding approval by id.
// When remember is true, the grant is cached for this session so subsequent
// calls to the same tool+subject auto-approve without prompting.
func (m *ApprovalManager) Approve(id string, allow, remember bool) {
	m.mu.Lock()
	ch, ok := m.approvals[id]
	meta, hasMeta := m.approvalMeta[id]
	if ok {
		delete(m.approvals, id)
	}
	if hasMeta {
		delete(m.approvalMeta, id)
	}
	if allow && remember && hasMeta {
		key := meta.tool + "\x00" + meta.subject
		m.sessionGrants[key] = true
	}
	m.mu.Unlock()
	if !ok {
		return
	}
	reason := ""
	if !allow {
		reason = "denied by user"
	}
	ch <- approvalResult{allow: allow, reason: reason, remember: remember}
}

// Answer resolves an outstanding ask by id with the user's answers.
func (m *ApprovalManager) Answer(id string, answers []event.AskAnswer) {
	m.mu.Lock()
	ch, ok := m.approvals[id]
	delete(m.approvals, id)
	delete(m.approvalMeta, id)
	m.mu.Unlock()
	if !ok {
		return
	}
	ch <- approvalResult{allow: true, answers: answers}
}

// Ask implements agent.Asker — emits an Ask event and blocks for the answer.
func (m *ApprovalManager) Ask(ctx context.Context, questions []event.AskQuestion) ([]event.AskAnswer, error) {
	m.mu.Lock()
	id := fmt.Sprintf("ask-%d", m.nextID.Add(1)-1)
	ch := make(chan approvalResult, 1)
	m.approvals[id] = ch
	m.mu.Unlock()

	m.sink.Emit(&event.Event{Kind: event.AskRequest, Ask: event.Ask{ID: id, Questions: questions}})

	timer := time.NewTimer(10 * time.Minute)
	defer timer.Stop()

	select {
	case r := <-ch:
		return r.answers, nil
	case <-ctx.Done():
		m.mu.Lock()
		delete(m.approvals, id)
		m.mu.Unlock()
		// Drain the channel in case Approve raced in between ctx.Done
		// and the map delete — a buffered write to a now-unread channel
		// is harmless, but we consume it explicitly so the producer
		// goroutine's buffer never fills on an unbuffered channel.
		select {
		case <-ch:
		default:
		}
		return nil, ctx.Err()
	case <-timer.C:
		m.mu.Lock()
		delete(m.approvals, id)
		m.mu.Unlock()
		select {
		case <-ch:
		default:
		}
		return nil, fmt.Errorf("ask timed out after 10 minutes")
	}
}

// GateApprover returns a permission.Approver that routes through this manager.
func (m *ApprovalManager) GateApprover() permission.Approver {
	return &gateApprover{m: m}
}

// gateApprover adapts ApprovalManager to permission.Approver.
type gateApprover struct {
	m *ApprovalManager
}

func (g *gateApprover) Approve(ctx context.Context, toolName string, subject string, rawArgs json.RawMessage) (allow bool, remember bool, err error) {
	// Check session grant cache first.
	key := toolName + "\x00" + subject
	g.m.mu.Lock()
	if g.m.sessionGrants[key] {
		g.m.mu.Unlock()
		return true, false, nil
	}
	g.m.mu.Unlock()

	a, _, e := g.m.requestApproval(ctx, toolName, subject)
	g.m.mu.Lock()
	r := g.m.lastRemember
	g.m.mu.Unlock()
	return a, r, e
}

// requestApproval emits an ApprovalRequest event and waits for the user's decision.
func (m *ApprovalManager) requestApproval(ctx context.Context, toolName, reason string) (allow bool, reasonOut string, err error) {
	m.mu.Lock()
	if m.autoApprove || m.bypass {
		m.lastRemember = false // bypass modes never persist rules
		m.mu.Unlock()
		return true, "", nil
	}
	id := fmt.Sprintf("%s-%d", toolName, m.nextID.Add(1)-1)
	ch := make(chan approvalResult, 1)
	m.approvals[id] = ch
	m.approvalMeta[id] = approvalRecord{tool: toolName, subject: reason}
	m.mu.Unlock()

	m.sink.Emit(&event.Event{
		Kind:     event.ApprovalRequest,
		Approval: event.Approval{ID: id, Tool: toolName, Subject: reason},
	})

	timer := time.NewTimer(10 * time.Minute)
	defer timer.Stop()

	select {
	case r := <-ch:
		m.mu.Lock()
		delete(m.approvals, id)
		delete(m.approvalMeta, id)
		m.lastRemember = r.remember
		m.mu.Unlock()
		return r.allow, r.reason, nil
	case <-ctx.Done():
		m.mu.Lock()
		delete(m.approvals, id)
		delete(m.approvalMeta, id)
		m.mu.Unlock()
		select {
		case <-ch:
		default:
		}
		return false, "", ctx.Err()
	case <-timer.C:
		m.mu.Lock()
		delete(m.approvals, id)
		delete(m.approvalMeta, id)
		m.mu.Unlock()
		select {
		case <-ch:
		default:
		}
		return false, "", fmt.Errorf("approval timed out after 10 minutes")
	}
}
