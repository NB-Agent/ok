package control

import (
	"context"

	"github.com/NB-Agent/ok/internal/event"
)

// Cancel ends the current turn (if any) without changing state.
func (c *Controller) Cancel() {
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Unlock()
}

// Approve answers an outstanding approval prompt. id is the Approval.ID from the
// event; session=true persists the grant for this session. The reply is
// consumed by the turn that issued the request; unknown ids are silently ignored.
func (c *Controller) Approve(id string, allow, session bool) {
	c.approval.Approve(id, allow, session)
}

// Answer replies to an outstanding ask prompt.
func (c *Controller) Answer(id string, answers []event.AskAnswer) {
	c.approval.Answer(id, answers)
}

// AnswerQuestion is an alias for Answer; used by some frontends.
func (c *Controller) AnswerQuestion(id string, answers []event.AskAnswer) {
	c.approval.Answer(id, answers)
}

// Ask implements agent.Asker. It emits an Ask event and blocks for the answer.
func (c *Controller) Ask(ctx context.Context, questions []event.AskQuestion) ([]event.AskAnswer, error) {
	return c.approval.Ask(ctx, questions)
}

// SetPlanMode toggles plan mode on the controller and the underlying executor.
func (c *Controller) SetPlanMode(v bool) {
	c.mu.Lock()
	c.planMode = v
	c.mu.Unlock()
	if c.executor != nil {
		c.executor.SetPlanMode(v)
	}
}

// PlanMode reports whether plan mode is on.
func (c *Controller) PlanMode() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.planMode
}


