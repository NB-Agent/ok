package agent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/NB-Agent/ok/internal/event"
)

const maxToolOutputBytes = 32 * 1024
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
