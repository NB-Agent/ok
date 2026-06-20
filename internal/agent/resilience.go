// Package agent — session resilience: mid-turn persistence, crash recovery,
// graceful context management, and reconnect support.
package agent

import (
	"context"
	"fmt"

	"github.com/NB-Agent/ok/internal/provider"
)

// ─── Aggressive compact ──────────────────────────────────────────────────

func (a *Agent) AggressiveCompact(ctx context.Context) error {
	msgs := a.session.Snapshot()
	snapGen := a.session.Gen()
	if len(msgs) < 3 {
		return nil
	}
	head := 0
	if len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
		head = 1
	}
	keep := a.recentKeep
	if keep < 4 {
		keep = 4
	}
	start := len(msgs) - keep
	if start < head+1 {
		start = head + 1
	}
	if start < len(msgs)-1 && msgs[start].Role == provider.RoleTool {
		start++
	}
	dropped := len(msgs) - keep
	summary := fmt.Sprintf("[Aggressive compact: %d earlier messages dropped to prevent context overflow. Remaining: %d]", dropped, head+1+len(msgs)-start)
	compacted := make([]provider.Message, 0, head+1+len(msgs)-start)
	compacted = append(compacted, msgs[:head]...)
	compacted = append(compacted, provider.Message{
		Role:    provider.RoleUser,
		Content: summary,
	})
	compacted = append(compacted, msgs[start:]...)
	if !a.session.ReplaceIfUnchanged(compacted, snapGen) {
		return fmt.Errorf("session changed during aggressive compact")
	}
	return nil
}

// ErrContextOverflow is returned when the context window is exhausted.
var ErrContextOverflow = fmt.Errorf("context window exhausted — send /compact to compress, or /new to start fresh")

func (a *Agent) CheckContextOverflow() error {
	if a.contextWindow <= 0 {
		return nil
	}
	msgs := a.session.Snapshot()
	estTokens := 0
	for _, m := range msgs {
		estTokens += len(m.Content)/4 + len(m.ReasoningContent)/4 + 20
	}
	if estTokens >= a.contextWindow {
		return ErrContextOverflow
	}
	return nil
}

// ContextUsage returns the estimated fraction (0.0–1.0) of the context window
// used by the current session. Returns 0 when the window is unconfigured.
func (a *Agent) ContextUsage() float64 {
	if a.contextWindow <= 0 {
		return 0
	}
	msgs := a.session.Snapshot()
	estTokens := 0
	for _, m := range msgs {
		estTokens += len(m.Content)/4 + len(m.ReasoningContent)/4 + 20
	}
	if estTokens <= 0 {
		return 0
	}
	return float64(estTokens) / float64(a.contextWindow)
}
