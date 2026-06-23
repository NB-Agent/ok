package reasoner

import (
	"testing"
)

func TestBuildTree_SimpleChain(t *testing.T) {
	tasks := []TaskNode{
		{ID: "probe-data", DependsOn: nil},
		{ID: "extract", DependsOn: []string{"probe-data"}},
		{ID: "verify", DependsOn: []string{"extract"}},
	}
	verdicts := map[string]TaskVerdict{
		"probe-data": {TaskID: "probe-data", Verdict: VerdictYes},
		"extract":    {TaskID: "extract", Verdict: VerdictYes},
		"verify":     {TaskID: "verify", Verdict: VerdictYes},
	}

	tree := BuildTree(tasks, verdicts)
	root := tree.ComputeRootVerdict()
	if root != VerdictYes {
		t.Errorf("expected root YES, got %s", root)
	}
}

func TestBuildTree_OneLeafNo(t *testing.T) {
	tasks := []TaskNode{
		{ID: "a", DependsOn: nil},
		{ID: "b", DependsOn: nil},
	}
	verdicts := map[string]TaskVerdict{
		"a": {TaskID: "a", Verdict: VerdictYes},
		"b": {TaskID: "b", Verdict: VerdictNo},
	}

	tree := BuildTree(tasks, verdicts)
	root := tree.ComputeRootVerdict()
	if root != VerdictNo {
		t.Errorf("expected root NO, got %s", root)
	}

	failed := tree.FailedLeaves()
	if len(failed) != 1 || failed[0].TaskID != "b" {
		t.Errorf("expected 'b' as failed leaf, got %v", failed)
	}
}

func TestBuildTree_AllYes(t *testing.T) {
	tasks := []TaskNode{
		{ID: "task1", DependsOn: nil},
		{ID: "task2", DependsOn: []string{"task1"}},
	}
	verdicts := map[string]TaskVerdict{
		"task1": {TaskID: "task1", Verdict: VerdictYes},
		"task2": {TaskID: "task2", Verdict: VerdictYes},
	}

	tree := BuildTree(tasks, verdicts)
	root := tree.ComputeRootVerdict()
	if root != VerdictYes {
		t.Errorf("expected root YES, got %s", root)
	}
}

func TestBuildTree_UncertainLeaf(t *testing.T) {
	tasks := []TaskNode{
		{ID: "x", DependsOn: nil},
	}
	verdicts := map[string]TaskVerdict{
		"x": {TaskID: "x", Verdict: VerdictUncertain},
	}

	tree := BuildTree(tasks, verdicts)
	root := tree.ComputeRootVerdict()
	if root != VerdictUncertain {
		t.Errorf("expected root UNCERTAIN, got %s", root)
	}
}

func TestBuildTree_EmptyTasks(t *testing.T) {
	tree := BuildTree(nil, nil)
	root := tree.ComputeRootVerdict()
	if root != VerdictUncertain {
		t.Errorf("expected UNCERTAIN for empty tree, got %s", root)
	}
}

func TestMethodLadder(t *testing.T) {
	l := MethodLadder{Current: MethodRegex}

	if m := l.NextMethod(); m != MethodFuzzy {
		t.Errorf("expected fuzzy after regex, got %s", m)
	}
	if m := l.NextMethod(); m != MethodHeuristic {
		t.Errorf("expected heuristic after fuzzy, got %s", m)
	}
	if m := l.NextMethod(); m != MethodAI {
		t.Errorf("expected ai after heuristic, got %s", m)
	}
	if m := l.NextMethod(); m != MethodAI {
		t.Errorf("expected ai at top, got %s", m)
	}
}

func TestMethodLadder_SuggestPrompt(t *testing.T) {
	l := MethodLadder{Current: MethodRegex}
	prompt := l.SuggestPrompt("extract-names", MethodRegex)
	if prompt == "" {
		t.Error("expected non-empty prompt")
	}
	// After the prompt, the ladder should have advanced.
	if l.Current != MethodFuzzy {
		t.Errorf("expected ladder at fuzzy, got %s", l.Current)
	}
}
