package agent

import (
	"testing"
)

func TestParseDecomposeResponseCleansFences(t *testing.T) {
	raw := "```json\n{\"tasks\":[{\"id\":\"1\",\"description\":\"do X\",\"depends_on\":[]}]}\n```"
	plan, err := parseDecomposeResponse(raw, "test goal")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(plan.Tasks))
	}
	if plan.Tasks[0].ID != "1" || plan.Tasks[0].Description != "do X" {
		t.Errorf("task mismatch: %+v", plan.Tasks[0])
	}
	if plan.Goal != "test goal" {
		t.Errorf("goal = %q, want test goal", plan.Goal)
	}
}

func TestParseDecomposeResponseNoFences(t *testing.T) {
	raw := `{"tasks":[{"id":"a","description":"first","depends_on":[]},{"id":"b","description":"second","depends_on":["a"]}]}`
	plan, err := parseDecomposeResponse(raw, "multi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(plan.Tasks))
	}
	if plan.Tasks[1].DependsOn[0] != "a" {
		t.Errorf("task b should depend on a, got %v", plan.Tasks[1].DependsOn)
	}
}

func TestParseDecomposeResponseNakedJSON(t *testing.T) {
	// Some models wrap JSON in explanatory text
	raw := "Here is the plan:\n\n{\"tasks\":[{\"id\":\"1\",\"description\":\"do it\",\"depends_on\":[]}]}\n\nDone."
	plan, err := parseDecomposeResponse(raw, "goal")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(plan.Tasks))
	}
}

func TestParseDecomposeResponseEmptyTasks(t *testing.T) {
	raw := `{"tasks":[]}`
	plan, err := parseDecomposeResponse(raw, "empty")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(plan.Tasks))
	}
}

func TestParseDecomposeResponseSkipsInvalidTasks(t *testing.T) {
	raw := `{"tasks":[{"id":"","description":"no id"},{"id":"2","description":"good","depends_on":[]}]}`
	plan, err := parseDecomposeResponse(raw, "skip empty")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Tasks) != 1 {
		t.Fatalf("expected 1 valid task, got %d", len(plan.Tasks))
	}
	if plan.Tasks[0].ID != "2" {
		t.Errorf("expected task id=2, got %s", plan.Tasks[0].ID)
	}
}

func TestParseDecomposeResponseMalformedJSON(t *testing.T) {
	raw := "not json at all"
	_, err := parseDecomposeResponse(raw, "broken")
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestParseDecomposeResponseCommentedFences(t *testing.T) {
	raw := "```\n{\"tasks\":[{\"id\":\"x\",\"description\":\"task x\",\"depends_on\":[]}]}\n```"
	plan, err := parseDecomposeResponse(raw, "commented")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Tasks) != 1 || plan.Tasks[0].ID != "x" {
		t.Errorf("task mismatch: %+v", plan.Tasks)
	}
}

func TestNewReasonerDefaults(t *testing.T) {
	r := NewReasoner(nil, nil, nil, nil, 0, 0.7, nil)
	if r.maxConcurrent != 3 {
		t.Errorf("default maxConcurrent = %d, want 3", r.maxConcurrent)
	}
	if r.temperature != 0.7 {
		t.Errorf("temperature = %v, want 0.7", r.temperature)
	}
}

func TestPlanExecutionOrder(t *testing.T) {
	plan := NewPlan("test order")
	plan.AddTask("a", "first")
	plan.AddTask("b", "second", "a")
	plan.AddTask("c", "third", "a")
	plan.AddTask("d", "fourth", "b", "c")

	// First batch: only "a" should be ready.
	ready := plan.GetReadyTasks()
	if len(ready) != 1 || ready[0].ID != "a" {
		t.Fatalf("first ready batch: expected [a], got %v", taskIDs(ready))
	}

	// Complete "a".
	plan.SetStatus("a", TaskDone, "done", "")

	// Second batch: "b" and "c" should both be ready (parallel).
	ready = plan.GetReadyTasks()
	if len(ready) != 2 {
		t.Fatalf("second ready batch: expected [b, c], got %v", taskIDs(ready))
	}

	// Complete "b", "c" still pending.
	plan.SetStatus("b", TaskDone, "done", "")

	// Third batch: only "c" should be ready.
	ready = plan.GetReadyTasks()
	if len(ready) != 1 || ready[0].ID != "c" {
		t.Fatalf("third ready batch: expected [c], got %v", taskIDs(ready))
	}

	// Complete "c".
	plan.SetStatus("c", TaskDone, "done", "")

	// Fourth batch: "d" should be ready.
	ready = plan.GetReadyTasks()
	if len(ready) != 1 || ready[0].ID != "d" {
		t.Fatalf("fourth ready batch: expected [d], got %v", taskIDs(ready))
	}
}

func TestPlanMarkBlocked(t *testing.T) {
	plan := NewPlan("test blocked")
	plan.AddTask("a", "first")
	plan.AddTask("b", "depends on a", "a")
	plan.AddTask("c", "also depends on a", "a")

	// Fail "a".
	plan.SetStatus("a", TaskFailed, "", "failed")

	// Mark blocked: b and c should be blocked.
	plan.MarkBlocked()

	summary := plan.Summary()
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
	// b and c should be blocked.
	ready := plan.GetReadyTasks()
	if len(ready) != 0 {
		t.Errorf("expected 0 ready tasks after marking blocked, got %v", taskIDs(ready))
	}
}

func taskIDs(tasks []*PlanTask) []string {
	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}
	return ids
}
