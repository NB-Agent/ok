package cli

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/NB-Agent/ok/internal/command"
	"github.com/NB-Agent/ok/internal/control"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/i18n"
	"github.com/NB-Agent/ok/internal/memory"
	"github.com/NB-Agent/ok/internal/plugin"
	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/skill"
)

type chatTUI struct {
	ctrl    *control.Controller
	label   string
	missing string

	width         int
	height        int
	repaintToggle bool

	input   textarea.Model
	spinner spinner.Model

	state      tuiState
	runStart   time.Time
	elapsed    int
	turnTokens int

	balance  string
	todoArgs string
	planMode bool
	history  []provider.Message

	// lines holds all committed chat content rendered in View().
	lines []string

	reasoning     *strings.Builder
	pending       *strings.Builder
	pendingCommit *[]string
	renderer      *mdRenderer
	eventCh       chan event.Event
	started       bool

	pendingBubble string
	bubblePending bool
	turnDiscarded bool

	pendingApproval *event.Approval
	chooser         *chooser
	host            *plugin.Host
	commands        []command.Command
	skills          []skill.Skill

	buildController func(ref string, carry []provider.Message) (*control.Controller, error)
	modelRef        string
	completion      completion

	// search mode (Ctrl+F)
	searchTerm    string
	searchResults []provider.Message
	searchCursor  int
}

var (
	inputBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true, false, true, true).
			BorderForeground(lipgloss.Color("173")).
			PaddingLeft(1)

	approvalBannerStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder(), true, false, true, false).
				BorderForeground(lipgloss.Color("220")).
				Foreground(lipgloss.Color("220")).
				Bold(true).
				PaddingLeft(1)

	todoPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true, false, false, false).
			BorderForeground(lipgloss.Color("240")).
			PaddingLeft(1)

	statusStyle = lipgloss.NewStyle().Faint(true)
)

func newChatTUI(ctrl *control.Controller, missing string, eventCh chan event.Event, termW int) chatTUI {
	ti := textarea.New()
	ti.Prompt = ""
	ti.CharLimit = 16384
	ti.SetHeight(1)
	ti.ShowLineNumbers = false
	ti.SetVirtualCursor(false)
	ti.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	ti.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("173"))

	return chatTUI{
		ctrl:      ctrl,
		label:     ctrl.Label(),
		missing:   missing,
		input:     ti,
		spinner:   sp,
		reasoning: &strings.Builder{},
		pending:   &strings.Builder{},
		renderer:  newMarkdownRenderer(max(termW-2, 40)),
		eventCh:   eventCh,
		history:   ctrl.History(),
		host:      ctrl.Host(),
		commands:  ctrl.Commands(),
		skills:    ctrl.Skills(),
	}
}

func (m chatTUI) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, waitForAgentEvent(m.eventCh), fetchBalance(m.ctrl))
}

func (m chatTUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// ── Search mode ──
	if m.state == tuiSearching {
		switch msg := msg.(type) {
		case tea.KeyPressMsg:
			switch msg.String() {
			case "esc":
				m.state = tuiIdle
				m.input.Reset()
				m.input.SetHeight(1)
				m.searchResults = nil
				m.searchTerm = ""
				m.searchCursor = 0
				return m, nil
			case "enter":
				// Exit search, keep the term as the last filtered view.
				m.state = tuiIdle
				m.input.Reset()
				m.input.SetHeight(1)
				return m, nil
			}
			var ic tea.Cmd
			m.input, ic = m.input.Update(msg)
			m.searchTerm = m.input.Value()
			m.growInputToFit()
			return m, ic
		case agentEventMsg:
			// Swallow events during search.
			return m, nil
		default:
			var ic tea.Cmd
			m.input, ic = m.input.Update(msg)
			return m, ic
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		resized := m.started && (m.width != msg.Width || m.height != msg.Height)
		m.width = msg.Width
		m.height = msg.Height
		m.input.SetWidth(msg.Width - 4)
		m.renderer = newMarkdownRenderer(msg.Width - 2)
		if !m.started {
			m.started = true
			var b strings.Builder
			b.WriteString(renderTUIBanner(m.label, m.missing, msg.Width))
			if len(m.history) > 0 {
				r := newMarkdownRenderer(msg.Width)
				for _, sec := range replaySectionsFor(m.history, msg.Width, r) {
					b.WriteString(sec)
				}
				m.history = nil
			}
			m.commitLine(strings.TrimRight(b.String(), "\n"))
		}
		if resized {
			cmds = append(cmds, func() tea.Msg { return forceRepaintMsg{} })
		}

	case forceRepaintMsg:
		m.repaintToggle = !m.repaintToggle

	case tea.KeyPressMsg:
		if m.chooser != nil {
			if m.chooser.typing {
				switch msg.String() {
				case "enter":
					val := strings.TrimSpace(m.input.Value())
					m.input.Reset()
					m.input.SetHeight(1)
					m.chooser.typing = false
					if val == "" {
						return m, finalize(m, cmds)
					}
					m.chooser.custom[m.chooser.tab] = val
					m.chooser.sel[m.chooser.tab] = map[int]bool{}
					return m.chooserAdvance()
				case "esc":
					m.chooser.typing = false
					m.input.Reset()
					m.input.SetHeight(1)
					return m, finalize(m, cmds)
				}
				var ic tea.Cmd
				m.input, ic = m.input.Update(msg)
				cmds = append(cmds, ic)
				m.growInputToFit()
				return m, finalize(m, cmds)
			}
			return m.handleChooserKey(msg)
		}
		if m.pendingApproval != nil {
			return m.handleApprovalKey(msg)
		}
		if m.completion.active {
			switch msg.String() {
			case "up":
				m.moveCompletion(-1)
				return m, nil
			case "down":
				m.moveCompletion(1)
				return m, nil
			case "tab", "enter":
				m.acceptCompletion()
				return m, nil
			case "esc":
				m.completion = completion{}
				return m, nil
			}
		}
		switch msg.String() {
		case "esc":
			switch {
			case m.state == tuiRunning && m.bubblePending:
				m.unsendPending()
			case m.state == tuiRunning:
				m.ctrl.Cancel()
			case m.ctrl.Bypass():
				m.ctrl.SetBypass(false)
			case m.planMode:
				m.planMode = false
				m.ctrl.SetPlanMode(false)
			default:
				m.input.Reset()
			}
			return m, nil
		case "ctrl+f":
			if m.state == tuiIdle {
				m.state = tuiSearching
				m.input.Reset()
				m.input.SetHeight(1)
				m.searchTerm = ""
				m.searchCursor = 0
				return m, nil
			}
			return m, nil
		case "ctrl+c":
			if m.state == tuiRunning {
				if m.bubblePending {
					m.unsendPending()
				} else {
					m.ctrl.Cancel()
				}
				return m, nil
			}
			return m, tea.Quit
		case "ctrl+d":
			return m, tea.Quit
		case "tab":
			if m.state == tuiRunning {
				break
			}
			m.cycleMode()
			return m, nil
		case "enter":
			if m.state == tuiRunning {
				return m, nil
			}
			line := strings.TrimSpace(m.input.Value())
			if line == "" {
				return m, nil
			}
			if line == "exit" || line == "quit" || line == ":q" {
				return m, tea.Quit
			}
			if strings.HasPrefix(line, "#") {
				m.input.Reset()
				m.input.SetHeight(1)
				note := strings.TrimSpace(strings.TrimPrefix(line, "#"))
				if note == "" {
					m.notice("nothing to remember")
				} else if path, err := m.ctrl.QuickAdd(memory.ScopeProject, note); err != nil {
					m.notice("memory: " + err.Error())
				} else {
					m.notice("remembered → " + path)
				}
				return m, finalize(m, cmds)
			}
			if strings.HasPrefix(line, "/") {
				m.input.Reset()
				m.input.SetHeight(1)
				cmds = append(cmds, m.runSlashCommand(line))
				return m, finalize(m, cmds)
			}
			m.input.Reset()
			m.input.SetHeight(1)
			if m.ctrl.HasRefs(line) {
				cmds = append(cmds, m.resolveRefs(line))
				return m, finalize(m, cmds)
			}
			cmds = append(cmds, m.startTurn(m.ctrl.Compose(line), line))
			return m, finalize(m, cmds)
		}

	case agentEventMsg:
		ev := event.Event(msg)
		m.ingestEvent(&ev)
		cmds = append(cmds, waitForAgentEvent(m.eventCh))
		if ev.Kind == event.TurnDone {
			cmds = append(cmds, fetchBalance(m.ctrl))
		}

	case balanceMsg:
		m.balance = msg.text

	case promptResolvedMsg:
		switch {
		case msg.err != nil:
			m.commitLine(wrapForViewport(i18n.M.ErrorPrefix+" "+msg.err.Error(), m.width, lipgloss.Color("3")))
		case strings.TrimSpace(msg.sent) == "":
			m.notice(i18n.M.SlashPromptEmpty)
		default:
			cmds = append(cmds, m.startTurn(m.ctrl.Compose(msg.sent), msg.display))
		}

	case refsResolvedMsg:
		for _, e := range msg.errs {
			m.notice(e)
		}
		sent := msg.line
		if msg.block != "" {
			sent = "Referenced context:\n\n" + msg.block + "\n\n" + msg.line
		}
		cmds = append(cmds, m.startTurn(m.ctrl.Compose(sent), msg.line))

	case elapsedTickMsg:
		if m.state == tuiRunning {
			m.elapsed = int(time.Since(m.runStart).Seconds())
			cmds = append(cmds, elapsedTick())
		}

	case spinner.TickMsg:
		if m.state == tuiRunning {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	var ic tea.Cmd
	m.input, ic = m.input.Update(msg)
	cmds = append(cmds, ic)
	m.growInputToFit()
	if _, ok := msg.(tea.KeyPressMsg); ok {
		m.updateCompletion()
	}

	return m, finalize(m, cmds)
}

func (m chatTUI) View() tea.View {
	if m.state == tuiSearching {
		return m.searchView()
	}
	w := m.width
	if w < 10 {
		w = 10
	}
	cardW := w

	// ── Render card stream: each line wrapped in a card style ──
	var contentLines []string
	userCard := lipgloss.NewStyle().Width(cardW).Padding(0, 1).Background(lipgloss.Color("236"))
	toolCard := lipgloss.NewStyle().Width(cardW).Padding(0, 1)
	defaultCard := lipgloss.NewStyle().Width(cardW).Padding(0, 1)

	for _, entry := range m.lines {
		for _, line := range strings.Split(entry, "\n") {
			styled := line
			switch {
			case strings.HasPrefix(line, "› "):
				styled = userCard.Render(line)
			case strings.HasPrefix(line, "→ ") || strings.HasPrefix(line, "⊘ "):
				styled = toolCard.Render(line)
			default:
				styled = defaultCard.Render(line)
			}
			contentLines = append(contentLines, styled)
		}
	}
	cardBlock := strings.Join(contentLines, "\n")

	// ── Input box: cardW wide ──
	box := inputBoxStyle.Width(cardW - 2).Render(m.input.View())

	// ── Mode tag ──
	var modeTag string
	switch {
	case m.ctrl.Bypass():
		modeTag = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true).Render("[YOLO]")
	case m.planMode:
		modeTag = yellow("[plan]")
	default:
		modeTag = dim("[auto]")
	}

	// ── Status ──
	var status string
	switch {
	case m.chooser != nil:
		status = modeTag + " · " + i18n.M.ChatStatusQuestion
	case m.pendingApproval != nil && m.pendingApproval.Tool == planApprovalTool:
		status = modeTag + " · " + i18n.M.ChatStatusPlanApproval
	case m.pendingApproval != nil:
		status = modeTag + " · " + i18n.M.ChatStatusToolApproval
	case m.state == tuiRunning:
		status = fmt.Sprintf("%s · "+i18n.M.ChatStatusThinkingFmt, modeTag, m.spinner.View(), m.elapsed)
		if m.turnTokens > 0 {
			status += " · ↓" + shortTokens(m.turnTokens)
		}
	default:
		status = modeTag + " · " + i18n.M.ChatStatusIdle
	}

	// ── Data line ──
	var data []string
	if ctxTag := m.contextTag(); ctxTag != "" {
		data = append(data, ctxTag)
	}
	if cache := m.cacheTag(); cache != "" {
		data = append(data, cache)
	}
	if jt := m.jobsTag(); jt != "" {
		data = append(data, jt)
	}
	if m.balance != "" {
		data = append(data, dim(m.balance))
	}
	dataLine := strings.Join(data, " · ")
	if m.repaintToggle {
		dataLine = "\u200b" + dataLine
	}

	// ── Status bar: cardW wide, 1-char left pad ──
	statusLine := statusStyle.Render(" " + status)
	dataOut := statusStyle.Render(" " + dataLine)

	// ── Assemble ──
	var parts []string
	rowsAboveBox := 0
	if todo := m.renderTodoPanel(cardW); todo != "" {
		parts = append(parts, todo)
		rowsAboveBox += strings.Count(todo, "\n") + 1
	}
	if banner := m.renderApprovalBanner(cardW); banner != "" {
		parts = append(parts, banner)
		rowsAboveBox += strings.Count(banner, "\n") + 1
	}
	if card := m.renderChooser(cardW); card != "" {
		parts = append(parts, card)
		rowsAboveBox += strings.Count(card, "\n") + 1
	}
	if menu := m.renderCompletion(); menu != "" {
		parts = append(parts, menu)
		rowsAboveBox += strings.Count(menu, "\n") + 1
	}
	parts = append(parts, cardBlock, box, statusLine, dataOut)

	joined := strings.Join(parts, "\n")

	v := tea.NewView(joined)
	if cur := m.input.Cursor(); cur != nil {
		cur.X += 1
		cur.Y += rowsAboveBox + 1
		v.Cursor = cur
	}
	return v
}

func (m chatTUI) searchView() tea.View {
	searchBar := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), true, false, true, false).
		BorderForeground(lipgloss.Color("75")).
		PaddingLeft(1).
		Width(m.width).
		Render(m.input.View())

	term := strings.ToLower(strings.TrimSpace(m.searchTerm))
	header := dim(fmt.Sprintf("Ctrl+F search · %s · Enter to apply, Esc to close", bold("type to filter")))
	if term == "" {
		return tea.NewView(strings.Join([]string{header, searchBar, dim("(start typing to filter history…)")}, "\n"))
	}

	var b strings.Builder
	b.WriteString(header + "\n")
	b.WriteString(searchBar + "\n")

	history := m.ctrl.History()
	count := 0
	for _, msg := range history {
		lowerContent := strings.ToLower(msg.Content)
		if strings.Contains(lowerContent, term) {
			count++
			if count > 20 {
				b.WriteString(dim(fmt.Sprintf("… (%d more matches)", len(history)-count+1)))
				break
			}
			preview := msg.Content
			if len(preview) > 120 {
				preview = preview[:120] + "…"
				lowerContent = strings.ToLower(preview) // re-lowercase truncated preview
			}
			// Highlight match
			idx := strings.Index(lowerContent, term)
			if idx < 0 {
				idx = 0
			}
			before := preview[:idx]
			matched := preview[idx : idx+len(term)]
			after := ""
			if idx+len(term) < len(preview) {
				after = preview[idx+len(term):]
			}
			line := dim("["+string(msg.Role)+"] ") + before + yellow(matched) + after
			b.WriteString(line + "\n")
		}
	}

	if count == 0 {
		b.WriteString(dim("no matches"))
	}
	return tea.NewView(b.String())
}
