package agent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/NB-Agent/ok/internal/event"
)

// maxToolOutputBytes returns the per-tool output truncation limit.
// When contextWindow is available (non-zero), use contextWindow/32 clamped
// to [16KB, 128KB]. Otherwise fall back to 32KB.
func maxToolOutputBytes(contextWindow int) int {
	switch {
	case contextWindow <= 0:
		return 32 * 1024
	case contextWindow < 512*1024:
		return 16 * 1024
	default:
		n := contextWindow / 32
		if n > 128*1024 {
			return 128 * 1024
		}
		return n
	}
}

const defaultToolTimeout = 15 * time.Minute

type Renderer interface{ Render(string) string }
type Asker interface {
	Ask(context.Context, []event.AskQuestion) ([]event.AskAnswer, error)
}
type callContextKey struct{}
type callContext struct {
	parentID string
	sink     event.Sink
	asker    Asker
}

func withCallContext(ctx context.Context, parentID string, sink event.Sink, asker Asker) context.Context {
	return context.WithValue(ctx, callContextKey{}, callContext{parentID, sink, asker})
}
func CallContext(ctx context.Context) (string, event.Sink, Asker, bool) {
	cc, ok := ctx.Value(callContextKey{}).(callContext)
	if !ok {
		return "", nil, nil, false
	}
	return cc.parentID, cc.sink, cc.asker, true
}

type Gate interface {
	Check(context.Context, string, json.RawMessage, bool) (bool, string, error)
}
type ToolHooks interface {
	PreToolUse(context.Context, string, json.RawMessage) (bool, string)
	PostToolUse(context.Context, string, json.RawMessage, string)
	ConsumeRollback() (string, string, bool)
}
