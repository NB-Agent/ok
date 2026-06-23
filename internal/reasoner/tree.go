package reasoner

import "fmt"

// BuildTree constructs a VerificationTree from a flat list of task nodes and
// their parsed verdicts. It adds a synthetic root ("__root__") whose children
// are all tasks with no dependents (the "output" tasks). The root gate is AND
// — all leaf chains must succeed for the goal to be verified.
//
// Each parent node's gate is AND by default (all children must be YES). Callers
// may override individual gates after construction if a task should use OR logic.
func BuildTree(tasks []TaskNode, verdicts map[string]TaskVerdict) *VerificationTree {
	tree := &VerificationTree{
		Nodes: make(map[string]*VerdictNode, len(tasks)+1),
	}

	// Identify which tasks are depended upon (i.e., are parents).
	parentsOf := make(map[string][]string) // taskID → children IDs
	hasDependents := make(map[string]bool)
	leafIDs := make(map[string]bool)

	for _, t := range tasks {
		leafIDs[t.ID] = true
		for _, dep := range t.DependsOn {
			parentsOf[dep] = append(parentsOf[dep], t.ID)
			hasDependents[dep] = true
		}
	}

	// Create a VerdictNode for each task.
	for _, t := range tasks {
		v, ok := verdicts[t.ID]
		if !ok {
			v = TaskVerdict{TaskID: t.ID, Verdict: VerdictUncertain}
		}
		children := parentsOf[t.ID] // tasks that depend on this one
		tree.Nodes[t.ID] = &VerdictNode{
			TaskID:   t.ID,
			Verdict:  v.Verdict,
			Children: children,
			Gate:     GateAND, // default; parents combine child verdicts
		}
	}

	// The root's children are tasks with no dependents (output leaves of the DAG).
	var rootChildren []string
	for id := range leafIDs {
		if !hasDependents[id] {
			rootChildren = append(rootChildren, id)
		}
	}
	tree.Nodes["__root__"] = &VerdictNode{
		TaskID:   "__root__",
		Verdict:  VerdictUncertain,
		Children: rootChildren,
		Gate:     GateAND,
	}
	tree.Root = "__root__"

	return tree
}

// ComputeRootVerdict propagates leaf verdicts upward through the tree and
// returns the root verdict. For AND gates, the parent is YES only if all
// children are YES. For OR gates, YES if any child is YES. UNCERTAIN
// propagates as UNCERTAIN (insufficient evidence).
//
// This mutates the tree's node verdicts in place (post-order computation).
func (t *VerificationTree) ComputeRootVerdict() Verdict {
	t.computeVerdict(t.Root)
	return t.Nodes[t.Root].Verdict
}

// computeVerdict recursively computes the verdict for a node.
func (t *VerificationTree) computeVerdict(nodeID string) Verdict {
	node, ok := t.Nodes[nodeID]
	if !ok {
		return VerdictUncertain
	}

	// If this node already has a definitive verdict from ParseVerdict,
	// use it (leaf nodes).
	if node.Verdict == VerdictYes || node.Verdict == VerdictNo {
		return node.Verdict
	}

	// No children — standalone node without a parsed verdict.
	if len(node.Children) == 0 {
		return VerdictUncertain
	}

	// Compute children first (post-order).
	var yesCount, noCount, uncertainCount int
	for _, childID := range node.Children {
		v := t.computeVerdict(childID)
		switch v {
		case VerdictYes:
			yesCount++
		case VerdictNo:
			noCount++
		default:
			uncertainCount++
		}
	}

	switch node.Gate {
	case GateAND:
		if noCount > 0 {
			node.Verdict = VerdictNo
		} else if uncertainCount > 0 {
			node.Verdict = VerdictUncertain
		} else {
			node.Verdict = VerdictYes
		}
	case GateOR:
		if yesCount > 0 {
			node.Verdict = VerdictYes
		} else if uncertainCount > 0 {
			node.Verdict = VerdictUncertain
		} else {
			node.Verdict = VerdictNo
		}
	}
	return node.Verdict
}

// FailedLeaves returns all leaf nodes whose verdict is NO.
func (t *VerificationTree) FailedLeaves() []*VerdictNode {
	var out []*VerdictNode
	for _, node := range t.Nodes {
		if node.Verdict != VerdictNo {
			continue
		}
		if len(node.Children) == 0 {
			out = append(out, node)
		}
	}
	return out
}

// Summary returns a human-readable tree status with YES/NO/UNCERTAIN counts.
func (t *VerificationTree) Summary() string {
	var yes, no, unc int
	for _, n := range t.Nodes {
		switch n.Verdict {
		case VerdictYes:
			yes++
		case VerdictNo:
			no++
		default:
			unc++
		}
	}
	rootV := t.Nodes[t.Root].Verdict
	return fmt.Sprintf("VerificationTree: root=%s ✅%d ❌%d ❓%d (%d nodes)",
		rootV, yes, no, unc, len(t.Nodes))
}
