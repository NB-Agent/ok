// Package evolution — kernel.Learn interface implementation.
//
// This file makes evolution.Engine implement kernel.Learn, unifying the two
// previously separate evolution paths:
//  1. learnAdapter (adapters.go) — called by LLM via the "learn" tool
//  2. evolution.Engine — called by OnTurnComplete hook automatically
//
// After this unification, learnAdapter delegates to evolution.Engine,
// so both manual (LLM) and automatic (hook) evolution share one engine.
package evolution

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/kernel"
)

// Learn interface compliance check.
var _ kernel.Learn = (*Engine)(nil)

// Extract analyzes a completed task and returns patterns.
// Satisfies kernel.Learn.Extract.
func (e *Engine) Extract(_ context.Context, task kernel.TaskRecord) ([]kernel.Pattern, error) {
	if task.ToolCalls == nil || !task.Success {
		return nil, nil
	}
	seq := make([]string, 0, len(task.ToolCalls))
	for _, tc := range task.ToolCalls {
		seq = append(seq, tc.Name)
	}
	if len(seq) < 2 {
		return nil, nil
	}
	return []kernel.Pattern{{
		ID:           fmt.Sprintf("p-%d", time.Now().UnixNano()),
		Description:  fmt.Sprintf("Tool sequence: %s", strings.Join(seq, " \u2192 ")),
		ToolSequence: seq,
		Frequency:    1,
		Confidence:   0.5,
	}}, nil
}

// Generate creates a candidate skill from successful patterns using the
// enhanced synthesis pipeline (Gap 2). When pattern context is available,
// it produces a rich playbook; otherwise falls back to template generation.
// Satisfies kernel.Learn.Generate.
func (e *Engine) Generate(_ context.Context, patterns []kernel.Pattern) (kernel.Skill, error) {
	if len(patterns) == 0 {
		return kernel.Skill{}, fmt.Errorf("no patterns to generate from")
	}
	p := patterns[0]
	name := "auto-" + strings.ReplaceAll(strings.ToLower(p.Description), " ", "-")
	if len(name) > 40 {
		name = name[:40]
	}

	// Convert kernel.Pattern to string patterns for synthesis.
	var patStrings []string
	for _, pp := range patterns {
		patStrings = append(patStrings, pp.Description)
	}

	// Use enhanced synthesis — produces a rich playbook with trigger
	// conditions, steps, and verification.
	body := generateSkillBodyEnhanced(name, patStrings, nil)

	return kernel.Skill{
		Name:        name,
		Description: p.Description,
		Body:        body,
		Source:      "extracted",
		Version:     1,
	}, nil
}

// Validate runs the full sandbox-validation pipeline on a skill:
//
//	Layer 0 — structural completeness
//	Layer 1 — safety pattern scan + tool reference check
//	Layer 2 — duplicate name check
//
// Satisfies kernel.Learn.Validate.
func (e *Engine) Validate(_ context.Context, skill kernel.Skill) error {
	if skill.Name == "" || skill.Body == "" {
		return fmt.Errorf("skill name and body required")
	}

	// Layer 0 + 1: Structural and safety validation.
	if err := ValidateSkill(skill.Body, KnownTools()); err != nil {
		return fmt.Errorf("validation: %w", err)
	}

	// Layer 2: Duplicate name check.
	if e.skillStor != nil {
		for _, existing := range e.skillStor.List() {
			if existing.Name == skill.Name {
				return fmt.Errorf("skill %q already exists — use a different name or delete the existing skill first", skill.Name)
			}
		}
	}
	return nil
}

// Publish makes a validated skill available to the agent's skill store.
// Satisfies kernel.Learn.Publish.
func (e *Engine) Publish(_ context.Context, skill kernel.Skill) error {
	if e.skillStor == nil {
		return fmt.Errorf("no skill store configured — evolution is disabled")
	}
	_, err := e.skillStor.CreateWithContent(skill.Name, "project", skill.Body)
	return err
}

// Stats returns learning metrics.
// Satisfies kernel.Learn.Stats.
func (e *Engine) Stats(_ context.Context) kernel.LearnStats {
	total := 0
	if e.skillStor != nil {
		total = len(e.skillStor.List())
	}
	return kernel.LearnStats{
		TotalSkills:    total,
		ExtractedToday: 0, // populated by the hook-driven path
		SuccessRate:    1.0,
		AvgConfidence:  0.5,
	}
}
