package builtin

import (
	"testing"
)

func TestScheduleToolBasic(t *testing.T) {
	s := &scheduleTool{tmap: map[string]*scheduledTask{}}
	if s.Name() != "schedule" {
		t.Errorf("Name = %q, want 'schedule'", s.Name())
	}
	if s.ReadOnly() {
		t.Error("schedule should not be read-only")
	}
	schema := s.Schema()
	if len(schema) < 50 {
		t.Error("schema too short or empty")
	}
}

func TestWorkflowToolBasic(t *testing.T) {
	w := &workflowTool{}
	if w.Name() != "workflow" {
		t.Errorf("Name = %q, want 'workflow'", w.Name())
	}
	if w.Description() == "" {
		t.Error("Description should not be empty")
	}
	schema := w.Schema()
	if len(schema) < 50 {
		t.Error("schema too short or empty")
	}
}
