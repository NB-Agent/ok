package cli

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/NB-Agent/ok/internal/agent"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/i18n"
)

func (m chatTUI) handleApprovalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	answer := func(allow, session bool) (tea.Model, tea.Cmd) {
		if allow && m.pendingApproval.Tool == planApprovalTool {
			m.planMode = false
		}
		m.ctrl.Approve(m.pendingApproval.ID, allow, session)
		m.pendingApproval = nil
		return m, nil
	}
	switch msg.String() {
	case "ctrl+c":
		m.ctrl.Cancel()
		return answer(false, false)
	case "enter":
		return answer(true, false)
	case "esc":
		return answer(false, false)
	}
	switch strings.ToLower(msg.String()) {
	case "y":
		return answer(true, false)
	case "a":
		return answer(true, true)
	case "n":
		return answer(false, false)
	}
	return m, nil
}

func (m *chatTUI) ingestEvent(e *event.Event) {
	if m.turnDiscarded {
		if e.Kind == event.TurnDone {
			m.turnDiscarded = false
			m.state = tuiIdle
		}
		return
	}
	if e.Kind != event.TurnStarted && e.Kind != event.TurnDone && m.bubblePending {
		m.commitPendingBubble()
	}
	switch e.Kind {
	case event.Reasoning:
		if m.reasoning.Len() == 0 {
			m.reasoning.WriteString(dim("  ▎ thinking") + "\n")
		}
		m.reasoning.WriteString(dim(e.Text))

	case event.Text:
		m.commitReasoning()
		m.pending.WriteString(e.Text)

	case event.Message:
		m.commitReasoning()
		m.commitPending()

	case event.ToolDispatch:
		if e.Tool.Partial {
			break
		}
		m.finalizeStreamed()
		switch e.Tool.Name {
		case "todo_write":
			m.todoArgs = e.Tool.Args
		case planApprovalTool:
		default:
			m.commitLine(fmt.Sprintf("  -> %s %s", e.Tool.Name, compactArgs(e.Tool.Args)))
		}

	case event.ToolResult:
		if e.Tool.Err != "" {
			m.finalizeStreamed()
			m.commitLine(fmt.Sprintf("  ⊘ %s %s", e.Tool.Name, e.Tool.Err))
		}

	case event.Usage:
		if e.Usage != nil {
			m.turnTokens += e.Usage.CompletionTokens
		}
		if line := agent.FormatUsageLine(e.Usage, e.Pricing); line != "" {
			m.finalizeStreamed()
			m.commitLine(line)
		}

	case event.Notice:
		// Skip the auto-discovered plugin list — noisy, not useful in-chat.
		// Controlled by plugin_quiet in ok.toml, but double-filter here so the
		// TUI never shows it regardless of binary age or config loading order.
		if strings.HasPrefix(e.Text, "auto-discovered ") && strings.Contains(e.Text, "plugin(s)") {
			break
		}
		glyph := "·"
		if e.Level == event.LevelWarn {
			glyph = "!"
		}
		m.finalizeStreamed()
		m.commitLine(fmt.Sprintf("  %s %s", glyph, e.Text))

	case event.Phase:
		m.finalizeStreamed()
		m.commitLine(fmt.Sprintf("[%s]", e.Text))

	case event.ApprovalRequest:
		a := e.Approval
		m.pendingApproval = &a

	case event.AskRequest:
		m.finalizeStreamed()
		m.chooser = newChooser(e.Ask)

	case event.TurnDone:
		m.commitReasoning()
		m.commitPending()
		if e.Err == nil || !strings.Contains(e.Err.Error(), "context canceled") {
			m.commitPendingBubble()
		} else {
			m.bubblePending = false
			m.pendingBubble = ""
		}
		m.state = tuiIdle
		if err := m.ctrl.Snapshot(); err != nil {
			fmt.Fprintf(os.Stderr, "cli: snapshot after turn: %v\n", err)
		}
		if e.Err != nil && e.Err.Error() != "" && !strings.Contains(e.Err.Error(), "context canceled") {
			m.commitLine(wrapForViewport(i18n.M.ErrorPrefix+" "+e.Err.Error(), m.width, lipgloss.Color("3")))
		}
	}
}

func (m *chatTUI) finalizeStreamed() {
	m.commitReasoning()
	m.commitPending()
}
