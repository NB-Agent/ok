// Package agent provides the Planner — a hierarchical task decomposition engine.
// It takes a high-level goal, decomposes it into sub-tasks with dependency edges,
// executes them in topological order, and re-plans on failure.
package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/NB-Agent/ok/internal/log"
)

// PlanTaskStatus tracks each sub-task's state.
type PlanTaskStatus string

const (
	TaskPending PlanTaskStatus = "pending"
	TaskRunning PlanTaskStatus = "running"
	TaskDone    PlanTaskStatus = "done"
	TaskFailed  PlanTaskStatus = "failed"
	TaskBlocked PlanTaskStatus = "blocked" // dependency failed, won't run
)

// TaskInProgress is deprecated; use TaskRunning. Kept for backward
// compatibility with existing serialized plans.
const TaskInProgress PlanTaskStatus = "in_progress"

// PlanTask is one atomic unit of work in a plan.
type PlanTask struct {
	ID          string         `json:"id"`
	Description string         `json:"description"`
	DependsOn   []string       `json:"depends_on,omitempty"`
	Status      PlanTaskStatus `json:"status"`
	Result      string         `json:"result,omitempty"`
	Error       string         `json:"error,omitempty"`
	Attempts    int            `json:"attempts,omitempty"` // retry count
	CreatedAt   time.Time      `json:"created_at"`
	CompletedAt time.Time      `json:"completed_at,omitempty"`
}

// Task is the public alias for PlanTask — see the multi-agent protocol in REASONIX.md.
// See PlanTask for the full definition.
type Task = PlanTask

// TaskResult is the output of a completed task — see the multi-agent protocol in REASONIX.md.
type TaskResult struct {
	TaskID  string
	Summary string
	Output  string
	Error   string
}

// Plan is a hierarchical task decomposition.
type Plan struct {
	Goal      string     `json:"goal"`
	Tasks     []PlanTask `json:"tasks"`
	CreatedAt time.Time  `json:"created_at"`
	mu        sync.Mutex `json:"-"`
}

// NewPlan creates an empty plan for the given goal.
func NewPlan(goal string) *Plan {
	return &Plan{
		Goal:      goal,
		Tasks:     make([]PlanTask, 0),
		CreatedAt: time.Now(),
	}
}

// AddTask appends a task to the plan.
func (p *Plan) AddTask(id, desc string, dependsOn ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Tasks = append(p.Tasks, PlanTask{
		ID:          id,
		Description: desc,
		DependsOn:   dependsOn,
		Status:      TaskPending,
		CreatedAt:   time.Now(),
	})
}

// SetStatus updates a task's status.
func (p *Plan) SetStatus(id string, status PlanTaskStatus, result, errMsg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.Tasks {
		if p.Tasks[i].ID == id {
			p.Tasks[i].Status = status
			p.Tasks[i].Result = result
			p.Tasks[i].Error = errMsg
			if status == TaskDone || status == TaskFailed {
				p.Tasks[i].CompletedAt = time.Now()
			}
			return
		}
	}
}

// GetReadyTasks returns tasks whose dependencies are all done and that are pending.
func (p *Plan) GetReadyTasks() []*PlanTask {
	p.mu.Lock()
	defer p.mu.Unlock()
	var ready []*PlanTask
	statusMap := make(map[string]PlanTaskStatus, len(p.Tasks))
	for i := range p.Tasks {
		statusMap[p.Tasks[i].ID] = p.Tasks[i].Status
	}
	for i := range p.Tasks {
		if p.Tasks[i].Status != TaskPending {
			continue
		}
		allDepsDone := true
		for _, dep := range p.Tasks[i].DependsOn {
			if statusMap[dep] != TaskDone {
				allDepsDone = false
				break
			}
		}
		if allDepsDone {
			ready = append(ready, &p.Tasks[i])
		}
	}
	return ready
}

// MarkBlocked marks pending tasks with failed dependencies as blocked,
// and unblocks blocked tasks whose dependencies are no longer failed
// (e.g. after a retry resets a failed dependency to pending).
func (p *Plan) MarkBlocked() {
	p.mu.Lock()
	defer p.mu.Unlock()
	statusMap := make(map[string]PlanTaskStatus, len(p.Tasks))
	for i := range p.Tasks {
		statusMap[p.Tasks[i].ID] = p.Tasks[i].Status
	}
	for i := range p.Tasks {
		switch p.Tasks[i].Status {
		case TaskBlocked:
			// Unblock if no dependency is still failed.
			stillBlocked := false
			for _, dep := range p.Tasks[i].DependsOn {
				if statusMap[dep] == TaskFailed {
					stillBlocked = true
					break
				}
			}
			if !stillBlocked {
				p.Tasks[i].Status = TaskPending
				p.Tasks[i].Error = ""
			}
		case TaskPending:
			for _, dep := range p.Tasks[i].DependsOn {
				if statusMap[dep] == TaskFailed {
					p.Tasks[i].Status = TaskBlocked
					p.Tasks[i].Error = fmt.Sprintf("dependency %s failed", dep)
					break
				}
			}
		}
	}
}

// Summary returns a human-readable status of the plan.
func (p *Plan) Summary() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Plan: %s\n\n", p.Goal))
	var done, failed, running, pending, blocked int
	for _, t := range p.Tasks {
		switch t.Status {
		case TaskDone:
			done++
		case TaskFailed:
			failed++
		case TaskRunning, TaskInProgress:
			running++
		case TaskBlocked:
			blocked++
		default:
			pending++
		}
	}
	b.WriteString(fmt.Sprintf("Tasks: %d total · ✅ %d done · 🔄 %d running · ⏳ %d pending · ❌ %d failed · 🚫 %d blocked\n\n", len(p.Tasks), done, running, pending, failed, blocked))
	for _, t := range p.Tasks {
		icon := "⏳"
		switch t.Status {
		case TaskDone:
			icon = "✅"
		case TaskFailed:
			icon = "❌"
		case TaskRunning, TaskInProgress:
			icon = "🔄"
		case TaskBlocked:
			icon = "🚫"
		default: // unknown — ignore
		}
		b.WriteString(fmt.Sprintf("%s `%s` — %s\n", icon, t.ID, t.Description))
		if t.Error != "" {
			b.WriteString(fmt.Sprintf("   ⚠️  %s\n", t.Error))
		}
	}
	return b.String()
}

// PlannerRunner executes tasks in a plan. The actual execution is delegated to a
// function so the tool layer can wire in the agent's run loop.
type PlannerRunner func(ctx context.Context, task PlanTask) (string, error)

// Planner orchestrates hierarchical task execution with dependency ordering.
// Decomposition is handled by Reasoner.decompose() (LLM-based) or by the
// plan tool's explicit steps mode — Planner only manages execution and retry.
type Planner struct {
	maxRetries int
}

// NewPlanner creates a planner with default settings.
func NewPlanner() *Planner {
	return &Planner{maxRetries: 1}
}

// Execute runs a plan's tasks respecting dependency order.
// It runs ready tasks concurrently up to maxConcurrent, and re-plans on failure.
func (p *Planner) Execute(ctx context.Context, plan *Plan, runner PlannerRunner, maxConcurrent int) (string, error) {
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}

	// Cycle guard: track the set of ready task IDs across iterations.
	// A cycle exists only when the EXACT SAME set of tasks is ready on
	// consecutive iterations with no progress. Counting alone produces
	// false positives when retries reset the same tasks to pending.
	var prevReadyIDs map[string]bool
	var cycleWarned bool

	for {
		select {
		case <-ctx.Done():
			return plan.Summary(), ctx.Err()
		default:
		}

		// Check if all tasks are done.
		plan.mu.Lock()
		allDone := true
		for _, t := range plan.Tasks {
			if t.Status != TaskDone {
				allDone = false
				break
			}
		}
		plan.mu.Unlock()
		if allDone {
			return plan.Summary(), nil
		}

		// Mark blocked tasks (and unblock any whose deps are no longer failed).
		plan.MarkBlocked()

		// Get ready tasks.
		ready := plan.GetReadyTasks()
		if len(ready) == 0 {
			// No ready tasks but not all done — cycle, blocked, or failed.
			plan.mu.Lock()
			var failedCount, blockedCount int
			for _, t := range plan.Tasks {
				switch t.Status {
				case TaskFailed:
					failedCount++
				case TaskBlocked:
					blockedCount++
				}
			}
			plan.mu.Unlock()
			if failedCount > 0 {
				return plan.Summary(), fmt.Errorf("plan stalled: %d task(s) failed unrecoverably", failedCount)
			}
			if blockedCount > 0 {
				return plan.Summary(), fmt.Errorf("plan stalled: %d task(s) blocked by failed dependencies", blockedCount)
			}
			return plan.Summary(), fmt.Errorf("plan stalled: no ready tasks but not all complete — possible circular dependency")
		}

		// Cycle detection: compare the exact set of ready task IDs against
		// the previous iteration. Same IDs = no progress = possible cycle.
		// Using IDs (not count) avoids false positives when a retry resets
		// a task to pending and it becomes ready again.
		sameIDs := prevReadyIDs != nil && len(prevReadyIDs) == len(ready)
		if sameIDs {
			for _, t := range ready {
				if !prevReadyIDs[t.ID] {
					sameIDs = false
					break
				}
			}
		}
		if sameIDs {
			if cycleWarned {
				return plan.Summary(), fmt.Errorf("plan stalled: cycle detected — "+
					"the same %d task(s) are ready but never complete on consecutive iterations",
					len(ready))
			}
			cycleWarned = true
		} else {
			prevReadyIDs = make(map[string]bool, len(ready))
			for _, t := range ready {
				prevReadyIDs[t.ID] = true
			}
			cycleWarned = false
		}

		// Run ready tasks (up to maxConcurrent at a time).
		sem := make(chan struct{}, maxConcurrent)
		var wg sync.WaitGroup
		var mu sync.Mutex
		var firstErr error

		for i := range ready {
			task := ready[i]
			plan.mu.Lock()
			task.Status = TaskRunning
			plan.mu.Unlock()

			wg.Add(1)
			sem <- struct{}{}
			go func(t *PlanTask) {
				defer wg.Done()
				defer func() { <-sem }()
				defer func() {
					if r := recover(); r != nil {
						log.Error("goroutine panic", "recover", r)
						plan.mu.Lock()
						t.Status = TaskFailed
						t.Error = fmt.Sprintf("panic: %v", r)
						t.CompletedAt = time.Now()
						plan.mu.Unlock()
						mu.Lock()
						if firstErr == nil {
							firstErr = fmt.Errorf("task %s panic: %v", t.ID, r)
						}
						mu.Unlock()
						fmt.Fprintf(os.Stderr, "planner: panic in task runner %s: %v\n", t.ID, r)
					}
				}()

				result, err := runner(ctx, *t)
				plan.mu.Lock()
				t.Status = TaskDone
				t.Result = result
				if err != nil {
					t.Status = TaskFailed
					t.Error = err.Error()
				}
				t.CompletedAt = time.Now()
				plan.mu.Unlock()

				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
				}
			}(task)
		}
		wg.Wait()

		if firstErr != nil {
			// Retry failed tasks up to maxRetries times each.
			plan.mu.Lock()
			anyRetried := false
			for i := range plan.Tasks {
				if plan.Tasks[i].Status == TaskFailed && plan.Tasks[i].Attempts < p.maxRetries {
					plan.Tasks[i].Attempts++
					plan.Tasks[i].Status = TaskPending
					plan.Tasks[i].Error = ""
					plan.Tasks[i].Result = ""
					anyRetried = true
				}
			}
			plan.mu.Unlock()
			if anyRetried {
				continue // re-enter the outer loop to retry
			}
			return plan.Summary(), firstErr
		}
	}
}
