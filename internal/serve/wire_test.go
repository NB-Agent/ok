package serve

import (
	"testing"

	"github.com/NB-Agent/ok/internal/eventpipe"
)

func TestToWire(t *testing.T) {
	t.Run("tool dispatch", func(t *testing.T) {
		w := eventpipe.ToWire(
			eventpipe.NewToolIntentEvent(0, 0, "call-1", "bash", `{"cmd":"ls"}`, false, ""),
		)
		if w.Kind != "tool_dispatch" || w.Tool == nil || w.Tool.Name != "bash" || w.Tool.Args != `{"cmd":"ls"}` {
			t.Errorf("dispatch = %+v / %+v", w, w.Tool)
		}
	})

	t.Run("notice warn", func(t *testing.T) {
		w := eventpipe.ToWire(eventpipe.NewNoticeEvent(0, 0, "warn msg", eventpipe.NoticeLevelWarn))
		if w.Kind != "notice" || w.Text != "warn msg" || w.Level != "warn" {
			t.Errorf("notice warn = %+v", w)
		}
	})

	t.Run("notice info", func(t *testing.T) {
		w := eventpipe.ToWire(eventpipe.NewNoticeEvent(0, 0, "info msg", eventpipe.NoticeLevelInfo))
		if w.Kind != "notice" || w.Text != "info msg" || w.Level != "info" {
			t.Errorf("notice info = %+v", w)
		}
	})

	t.Run("turn abort", func(t *testing.T) {
		w := eventpipe.ToWire(eventpipe.NewTurnAbortedEvent(0, 0, nil, "", "oops"))
		if w.Kind != "turn_done" || w.Err != "oops" {
			t.Errorf("abort = %+v", w)
		}
	})

	t.Run("approval", func(t *testing.T) {
		w := eventpipe.ToWire(eventpipe.NewApprovalRequestEvent(0, 0, "ask-1", "bash", "run ls"))
		if w.Kind != "approval_request" || w.Approval == nil || w.Approval.ID != "ask-1" {
			t.Errorf("approval = %+v", w)
		}
	})

	t.Run("turn done no error", func(t *testing.T) {
		w := eventpipe.ToWire(eventpipe.NewTurnDoneEvent(0, 0, nil))
		if w.Kind != "turn_done" || w.Err != "" {
			t.Errorf("turn done = %+v", w)
		}
	})

	t.Run("tool denied", func(t *testing.T) {
		w := eventpipe.ToWire(eventpipe.NewToolDeniedEvent(0, 0, "call-2", "rm", "not allowed"))
		if w.Kind != "tool_result" || w.Tool == nil || w.Tool.Err != "not allowed" {
			t.Errorf("denied = %+v", w)
		}
	})
}
