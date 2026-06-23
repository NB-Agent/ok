package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/NB-Agent/ok/internal/agent"
	"github.com/NB-Agent/ok/internal/config"
	"github.com/NB-Agent/ok/internal/control"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/i18n"
	"github.com/NB-Agent/ok/internal/provider"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"
)

// chatREPL is an interactive session: a single persistent agent/session and a
// prompt loop that keeps conversation context across turns. Exit with
// 'exit'/'quit' or Ctrl-D.
func chatREPL(args []string) int {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	model := fs.String("model", "", "provider name (default: config default_model)")
	maxSteps := fs.Int("max-steps", 0, "max tool-call rounds (0 = use config/default)")
	cont := fs.Bool("continue", false, "resume the most recent saved session")
	fs.BoolVar(cont, "c", false, "shorthand for --continue")
	resume := fs.Bool("resume", false, "list saved sessions and pick one to resume")
	yolo := fs.Bool("dangerously-skip-permissions", false, "YOLO: auto-approve every tool call this session (deny rules still apply)")
	fs.BoolVar(yolo, "yolo", false, "alias for --dangerously-skip-permissions")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Decide whether we're starting fresh or resuming. --resume opens an
	// interactive picker; --continue / -c jumps straight into the newest.
	var resumePath string
	switch {
	case *resume:
		path, rc := pickSessionToResume()
		if rc != 0 {
			return rc
		}
		resumePath = path
	case *cont:
		sessions, err := agent.ListSessions(config.SessionDir())
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		if len(sessions) == 0 {
			fmt.Fprintln(os.Stderr, i18n.M.NoSessionToResume)
			return 1
		}
		resumePath = sessions[0].Path
	}

	// Background check for updates (blocking, quick). Times out in 5s.
	if shouldCheckUpdate() {
		updateCh := checkForUpdate(cliVersion)
		select {
		case result := <-updateCh:
			printUpdateNotice(result)
			if result.Err == nil {
				markUpdateChecked()
			}
		case <-time.After(6 * time.Second):
		}
	}

	ctx := context.Background() // CLI entry point — no parent context

	// Plumb the controller's typed event stream through a channel so each event
	// can become a tea.Msg inside the TUI's update loop. Buffered generously:
	// streaming bursts (tool results, long answers) shouldn't backpressure the
	// agent goroutine.
	eventCh := make(chan event.Event, eventChannelCap)

	sink := &eventSink{ch: eventCh}
	ctrl, err := setup(ctx, *model, *maxSteps, false, sink)
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return 1
	}

	// Decide where this conversation's auto-save lands. A resume reuses the
	// file so closing/reopening keeps appending to the same history; a fresh
	// session lands in a new file stamped with the model name.
	if resumePath != "" {
		if loaded, err := agent.LoadSession(resumePath); err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		} else {
			ctrl.Resume(loaded, resumePath)
		}
	} else if ctrl.SessionDir() != "" {
		ctrl.SetSessionPath(agent.NewSessionPath(ctrl.SessionDir(), ctrl.Label()))
	}

	// Surface a missing-key warning inside the TUI banner so the first message
	// failing is at least pre-announced; the user can still enter chat.
	missing := ""
	if cfg, loadErr := config.Load(); loadErr == nil {
		name := *model
		if name == "" {
			name = cfg.DefaultModel
		}
		if vErr := cfg.Validate(name); vErr != nil {
			missing = vErr.Error()
		}
	}
	// Warn about environment issues before entering the TUI.
	if s := DoctorSummary(); !isInteractive() && s != "" {
		fmt.Fprintln(os.Stderr, s)
	}

	// Initial terminal width — the TUI re-flows on every WindowSizeMsg so
	// this is just a starting estimate before the first resize event lands.
	termW := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		termW = w
	}

	// Route "ask" decisions to the TUI: the controller emits an ApprovalRequest
	// event and blocks until the user answers via ctrl.Approve. Sub-agents (the
	// task tool) keep their headless gate from setup — no UI to prompt through.
	ctrl.EnableInteractiveApproval()
	// YOLO: skip every approval prompt for the session (deny rules still apply).
	if *yolo {
		ctrl.SetBypass(true)
	}

	m := newChatTUI(ctrl, missing, eventCh, termW)

	// /model support: a pure builder the TUI calls to rebuild on a different
	// model (carrying the conversation). It must NOT touch the running model —
	// runModelSubcommand performs the swap on the live copy. The same stable sink
	// feeds the new controller, so events keep flowing to this TUI.
	m.buildController = func(ref string, carry []provider.Message) (*control.Controller, error) {
		c, err := setup(ctx, ref, *maxSteps, false, sink)
		if err != nil {
			return nil, err
		}
		path := ""
		if dir := c.SessionDir(); dir != "" {
			path = agent.NewSessionPath(dir, c.Label())
		}
		if len(carry) > 0 {
			c.Resume(&agent.Session{Messages: carry}, path)
		} else if path != "" {
			c.SetSessionPath(path)
		}
		c.EnableInteractiveApproval()
		if *yolo {
			c.SetBypass(true)
		}
		return c, nil
	}
	if cfg, e := config.Load(); e == nil {
		name := *model
		if name == "" {
			name = cfg.DefaultModel
		}
		if entry, ok := cfg.ResolveModel(name); ok {
			m.modelRef = entry.Name + "/" + entry.Model
		}
	}

	// No alt-screen: finalized transcript lines are committed to the terminal's
	// normal buffer (via tea.Println) so native scrollback, the wheel, and copy
	// all work — the bubbletea-managed region is just the bottom input/status.
	p := tea.NewProgram(m)
	final, runErr := p.Run()
	// Close the controller that's active at exit — /model may have swapped it
	// (each prior controller was already closed at switch time), so close the
	// final one here rather than the initial handle.
	if fm, ok := final.(chatTUI); ok && fm.ctrl != nil {
		fm.ctrl.Close()
	} else {
		ctrl.Close()
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, runErr)
		return 1
	}
	return 0
}

// pickSessionToResume scans the session dir, takes the 10 most recent, and
// shows a single-choice menu with timestamp + turn count + first user
// message so the user can pick one. Returns the chosen path and a process
// exit code (non-zero when there's nothing to pick or the user canceled).
func pickSessionToResume() (string, int) {
	sessions, err := agent.ListSessions(config.SessionDir())
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return "", 1
	}
	if len(sessions) == 0 {
		fmt.Fprintln(os.Stderr, i18n.M.NoSessionToResume)
		return "", 1
	}
	if !isInteractive() {
		fmt.Fprintln(os.Stderr, i18n.M.ResumeRequiresTTY)
		return "", 1
	}
	const cap = 10
	if len(sessions) > cap {
		sessions = sessions[:cap]
	}
	items := make([]menuItem, len(sessions))
	for i, s := range sessions {
		when := s.ModTime.Local().Format("01-02 15:04")
		preview := s.Preview
		if preview == "" {
			preview = "(no user message yet)"
		}
		items[i] = menuItem{
			name: when,
			desc: fmt.Sprintf("%d turns · %s", s.Turns, preview),
		}
	}
	idx, err := selectOne(i18n.M.PickSessionLabel, items)
	if err != nil {
		return "", 1
	}
	return sessions[idx].Path, 0
}
