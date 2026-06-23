package cli

import (
	"github.com/NB-Agent/ok/internal/control"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/provider"
	"strings"
)

type tuiState int

const (
	tuiIdle tuiState = iota
	tuiRunning
	tuiSearching
)

type agentEventMsg event.Event
type elapsedTickMsg struct{}
type forceRepaintMsg struct{}
type balanceMsg struct{ text string }

type promptResolvedMsg struct {
	display string
	sent    string
	err     error
}

type refsResolvedMsg struct {
	line  string
	block string
	errs  []string
}

const planApprovalTool = control.PlanApprovalTool

type eventSink struct{ ch chan<- event.Event }

func (s *eventSink) Emit(e *event.Event) { s.ch <- *e }

func replaySectionsFor(history []provider.Message, width int, renderer *mdRenderer) []string {
	var out []string
	for _, m := range history {
		switch m.Role {
		case provider.RoleUser:
			out = append(out, renderUserBubble(m.Content, width, false)+"\n\n")
		case provider.RoleAssistant:
			body := strings.TrimSpace(m.Content)
			if body == "" {
				continue
			}
			rendered := renderer.Render(body)
			if rendered == "" {
				rendered = body
			}
			out = append(out, rendered+"\n")
		}
	}
	return out
}
