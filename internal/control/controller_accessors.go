package control

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/NB-Agent/ok/internal/billing"
	"github.com/NB-Agent/ok/internal/command"
	"github.com/NB-Agent/ok/internal/core"
	"github.com/NB-Agent/ok/internal/dstsetup"
	"github.com/NB-Agent/ok/internal/hook"
	"github.com/NB-Agent/ok/internal/jobs"
	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/skill"
)

// --- query accessors ---

// Run sends input directly to the runner (bypassing Submit's slash dispatch and
// @-ref expansion). Used by headless/ACP callers that compose their own input.
func (c *Controller) Run(ctx context.Context, input string) error {
	return c.runner.Run(ctx, input)
}

// History returns the executor's current message log.
func (c *Controller) History() []provider.Message {
	if c.executor == nil {
		return nil
	}
	return c.executor.Session().Snapshot()
}

// ContextSnapshot returns (promptTokens, contextWindow) from the most recent turn.
func (c *Controller) ContextSnapshot() (int, int) {
	if c.executor == nil {
		return 0, 0
	}
	u := c.executor.LastUsage()
	if u == nil {
		return 0, c.executor.ContextWindow()
	}
	return u.PromptTokens, c.executor.ContextWindow()
}

// LastUsage returns the most recent turn's token telemetry (nil before the first turn).
func (c *Controller) LastUsage() *provider.Usage {
	if c.executor == nil {
		return nil
	}
	return c.executor.LastUsage()
}

// SessionCache returns cumulative cache hit/miss prompt tokens for the session.
func (c *Controller) SessionCache() (hit, miss int) {
	if c.executor == nil {
		return 0, 0
	}
	return c.executor.SessionCache()
}

// Balance queries the active provider's wallet balance, or (nil, nil) when the
// provider declares no balance_url.
func (c *Controller) Balance(ctx context.Context) (*billing.Balance, error) {
	if strings.TrimSpace(c.balanceURL) == "" {
		return nil, nil
	}
	return billing.Fetch(ctx, c.balanceURL, c.balanceKey)
}

// Commands returns the loaded custom slash commands.
func (c *Controller) Commands() []command.Command { return c.commands }

// Skills returns the discoverable skills.
func (c *Controller) Skills() []skill.Skill { return c.skills }

// HookRunner returns the session's hook runner (nil-safe; may hold zero hooks).
func (c *Controller) HookRunner() *hook.Runner { return c.hooks }

// DSTBrain returns the single DST facade (nil when DST is unavailable).
func (c *Controller) DSTBrain() *dstsetup.DSTRunner { return c.dst }

// IsDSTAvailable reports whether the DST brain is initialised and available.
func (c *Controller) IsDSTAvailable() bool { return c.dst != nil && c.dst.IsAvailable() }

// SetDSTEnabled turns per-step DST verification on or off.
func (c *Controller) SetDSTEnabled(v bool) {
	if c.dst == nil {
		return
	}
	if v {
		c.dst.Enable()
	} else {
		c.dst.Disable()
	}
}

// DSTEnabled reports whether DST per-step verification is active.
func (c *Controller) DSTEnabled() bool {
	if c.dst == nil {
		return false
	}
	return c.dst.IsEnabled()
}

// Jobs returns the still-running background jobs for the status bar.
func (c *Controller) Jobs() []jobs.View {
	if c.jobs == nil {
		return nil
	}
	return c.jobs.Running()
}

// AuditLog returns the complete audit trail.
func (c *Controller) AuditLog() []core.AuditRecord {
	if c.executor == nil {
		return nil
	}
	return c.executor.AuditLog()
}

// AuditLogJSON returns the audit trail as a JSON string.
func (c *Controller) AuditLogJSON() (string, error) {
	records := c.AuditLog()
	if records == nil {
		return "[]", nil
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
