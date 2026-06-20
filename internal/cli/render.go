package cli

import (
	"encoding/json"
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/NB-Agent/ok/internal/i18n"
)

func (m chatTUI) contextTag() string {
	used, window := m.ctrl.ContextSnapshot()
	if used == 0 || window == 0 {
		return ""
	}
	pct := used * 100 / window
	body := fmt.Sprintf("%s / %s ctx (%d%%)", shortTokens(used), shortTokens(window), pct)
	switch {
	case pct >= 85:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(body)
	case pct >= 60:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Render(body)
	default:
		return dim(body)
	}
}

func (m chatTUI) cacheTag() string {
	now := ""
	if u := m.ctrl.LastUsage(); u != nil {
		d := u.CacheHitTokens + u.CacheMissTokens
		if d == 0 {
			d = u.PromptTokens
		}
		if d > 0 {
			now = fmt.Sprintf("cache %d%%", u.CacheHitTokens*100/d)
		}
	}
	avg := ""
	if hit, miss := m.ctrl.SessionCache(); hit+miss > 0 {
		avg = fmt.Sprintf("avg %d%%", hit*100/(hit+miss))
	}
	switch {
	case now != "" && avg != "":
		return dim(now + " · " + avg)
	case now != "":
		return dim(now)
	case avg != "":
		return dim(avg)
	}
	return ""
}

func (m chatTUI) jobsTag() string {
	n := len(m.ctrl.Jobs())
	if n == 0 {
		return ""
	}
	return dim(fmt.Sprintf("⚙ %d", n))
}

func shortTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dK", n/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func (m chatTUI) renderApprovalBanner(cardW int) string {
	w := cardW
	if w < 10 {
		w = 10
	}
	if m.pendingApproval == nil {
		return ""
	}
	if m.pendingApproval.Tool == planApprovalTool {
		return approvalBannerStyle.Width(w).Render("⏸ " + i18n.M.PlanApprovalPrompt)
	}
	subj := strings.TrimSpace(m.pendingApproval.Subject)
	if subj != "" {
		subj = " " + truncateSubject(subj, w)
	}
	text := fmt.Sprintf(i18n.M.ToolApprovalPromptFmt, m.pendingApproval.Tool, subj)
	return approvalBannerStyle.Width(w).Render("⏸ " + text)
}

const todoPanelMaxRows = 8

func (m chatTUI) renderTodoPanel(cardW int) string {
	var p struct {
		Todos []struct {
			Content    string `json:"content"`
			Status     string `json:"status"`
			ActiveForm string `json:"activeForm"`
		} `json:"todos"`
	}
	if err := json.Unmarshal([]byte(m.todoArgs), &p); err != nil {
		return ""
	}
	// Filter out items with empty content — they'd render as blank lines.
	valid := p.Todos[:0]
	for _, t := range p.Todos {
		if strings.TrimSpace(t.Content) != "" {
			valid = append(valid, t)
		}
	}
	if len(valid) == 0 {
		return ""
	}
	done := 0
	for _, t := range valid {
		if t.Status == "completed" {
			done++
		}
	}
	if done == len(valid) {
		return ""
	}

	w := cardW - 4
	if w < 10 {
		w = 10
	}
	var b strings.Builder
	header := fmt.Sprintf("%s %s", accent("To-dos"), dim(fmt.Sprintf("%d/%d", done, len(valid))))
	b.WriteString(header)

	shown := 0
	for _, t := range valid {
		if shown >= todoPanelMaxRows {
			break
		}
		line := taskLine(t, w)
		if line != "" {
			b.WriteString("\n" + line)
			shown++
		}
	}
	if shown < len(valid) {
		b.WriteString("\n" + dim(fmt.Sprintf("  +%d more", len(valid)-shown)))
	}
	return todoPanelStyle.Width(cardW).Render(b.String())
}

func taskLine(t struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"activeForm"`
}, width int) string {
	label := t.Content
	if t.Status == "in_progress" && t.ActiveForm != "" {
		label = t.ActiveForm
	}
	label = clampStatusLine(label, width)
	switch t.Status {
	case "completed":
		return dim("  ✓ " + label)
	case "in_progress":
		return accent("  ● ") + label
	default:
		return dim("  ○ " + label)
	}
}

func truncateSubject(s string, width int) string {
	if width < 20 {
		width = 20
	}
	max := width - 30
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

func clampStatusLine(s string, width int) string {
	s = strings.TrimRight(s, " ")
	if len(s) > width {
		s = s[:width]
	}
	return s
}

func wrapForViewport(text string, width int, fg color.Color) string {
	if width <= 0 {
		width = 80
	}
	return lipgloss.NewStyle().
		Foreground(fg).
		Width(width).
		Render(text)
}

func renderTUIBanner(label, missing string, width int) string {
	var b strings.Builder
	b.WriteString(accent("◆") + " " + bold("ok chat") + "  " + dim("· "+label) + "\n")
	b.WriteString(dim("  "+i18n.M.ChatTip) + "\n")
	if missing != "" {
		b.WriteString(wrapForViewport("  ! "+missing, width, lipgloss.Color("3")) + "\n")
	}
	return b.String()
}

func compactArgs(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > 120 {
		return string(r[:120]) + "..."
	}
	return s
}

// Legacy wrappers for test backward compatibility.
func renderUserBubble(line string, _ int, _ bool) string {
	return "› " + line
}
func renderAssistantBubble(text string, width int) string { return text }
func renderThinkingBubble(text string, width int) string  { return text }
