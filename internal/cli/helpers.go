package cli

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/NB-Agent/ok/internal/control"
	"github.com/NB-Agent/ok/internal/event"
)

func fetchBalance(ctrl *control.Controller) tea.Cmd {
	return func() tea.Msg {
		b, err := ctrl.Balance(context.Background()) // CLI entry point — no parent context
		if err != nil || b == nil {
			return balanceMsg{}
		}
		return balanceMsg{text: b.Display()}
	}
}

func waitForAgentEvent(ch chan event.Event) tea.Cmd {
	return func() tea.Msg { return agentEventMsg(<-ch) }
}

func elapsedTick() tea.Cmd {
	return tea.Tick(time.Second, func(_ time.Time) tea.Msg { return elapsedTickMsg{} })
}

func finalize(m chatTUI, cmds []tea.Cmd) tea.Cmd {
	_ = m
	return tea.Batch(cmds...)
}

func chunkLines(s string, n int) []string {
	if n < 1 {
		n = 1
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return []string{s}
	}
	var out []string
	for i := 0; i < len(lines); i += n {
		end := i + n
		if end > len(lines) {
			end = len(lines)
		}
		out = append(out, strings.Join(lines[i:end], "\n"))
	}
	return out
}

func clampWidth(s string, width int) string {
	if width <= 0 {
		return s
	}
	return ansi.Hardwrap(s, width, false)
}

func (m *chatTUI) commitLine(s string) {
	m.lines = append(m.lines, s)
	*m.pendingCommit = append(*m.pendingCommit, s)
}

func (m *chatTUI) commitReasoning() {
	if m.reasoning.Len() == 0 {
		return
	}
	raw := strings.TrimRight(m.reasoning.String(), "\n")
	var b strings.Builder
	for i, line := range strings.Split(raw, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		if m.width > 0 && visibleWidth(line) > m.width {
			b.WriteString(wrapAnsi(line, m.width))
		} else {
			b.WriteString(line)
		}
	}
	m.commitLine(renderThinkingBubble(b.String(), m.width))
	m.reasoning.Reset()
}

func (m *chatTUI) commitPending() {
	if m.pending.Len() == 0 {
		return
	}
	raw := m.pending.String()
	rendered := m.renderer.Render(raw)
	if rendered == "" {
		rendered = raw
	}
	m.commitLine(renderAssistantBubble(strings.TrimRight(rendered, "\n"), m.width))
	m.pending.Reset()
}

func (m *chatTUI) growInputToFit() {
	h := strings.Count(m.input.Value(), "\n") + 1
	if h < 1 {
		h = 1
	}
	if h > 10 {
		h = 10
	}
	m.input.SetHeight(h)
}

func (m *chatTUI) cycleMode() {
	m.planMode = !m.planMode
	m.ctrl.SetPlanMode(m.planMode)
}

func (m *chatTUI) startTurn(sent, displayed string) tea.Cmd {
	m.pendingBubble = displayed
	m.bubblePending = true
	m.turnDiscarded = false
	m.turnTokens = 0
	m.state = tuiRunning
	m.runStart = time.Now()
	m.elapsed = 0
	m.ctrl.Send(sent)
	return tea.Batch(elapsedTick(), waitForAgentEvent(m.eventCh))
}

func (m *chatTUI) commitPendingBubble() {
	if !m.bubblePending || m.pendingBubble == "" {
		return
	}
	m.commitLine("")
	m.commitLine(renderUserBubble(m.pendingBubble, m.width, m.planMode))
	m.bubblePending = false
	m.pendingBubble = ""
}

func (m *chatTUI) unsendPending() {
	if !m.bubblePending {
		return
	}
	m.input.SetValue(m.pendingBubble)
	m.bubblePending = false
	m.pendingBubble = ""
	m.turnDiscarded = true
	m.state = tuiIdle
}
