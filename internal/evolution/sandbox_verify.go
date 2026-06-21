// Package evolution — Gap 3: Sandbox execution validation.
//
// Replaces the weak validatePattern (episodic memory keyword check) with
// layered skill validation:
//
//	Layer 0 — Structural validation (zero-LLM, always available):
//	  Checks skill body completeness: frontmatter, required sections.
//	  Stricter than skill.ValidateStructure — requires evolution-specific
//	  sections ("## When to Use", "## Steps", "## Verification").
//
//	Layer 1 — Safety pattern scanning:
//	  Delegates to skill.ValidateSafety — the canonical implementation
//	  shared with Store.parse() so hand-written skills are also checked.
//
//	Layer 2 — LLM safety review (Learn tool path, provider available).
//
// This file is referenced by learn_interface.go as the P1 smart closure
// component of the self-evolution engine.
package evolution

import (
	"fmt"
	"strings"

	"github.com/NB-Agent/ok/internal/skill"
)

// ─── Layer 0: Structural validation ──────────────────────────────────────

// ValidateSkillStructure checks a skill body for structural completeness.
// Stricter than the skill-package version: requires evolution-generated
// sections ("## When to Use", "## Steps", "## Verification").
func ValidateSkillStructure(body string) error {
	if body == "" {
		return fmt.Errorf("skill body is empty")
	}
	if !strings.HasPrefix(strings.TrimSpace(body), "---") {
		return fmt.Errorf("missing frontmatter (--- block)")
	}
	required := []string{"## When to Use", "## Steps", "## Verification"}
	for _, section := range required {
		if !strings.Contains(body, section) {
			return fmt.Errorf("missing required section: %s", section)
		}
	}
	const minBodyLen = 80
	if len(body) < minBodyLen {
		return fmt.Errorf("skill body too short (%d chars, minimum %d)", len(body), minBodyLen)
	}
	return nil
}

// ─── Layer 1: Safety ─────────────────────────────────────────────────────

// ValidateSkillSafety scans the skill body for dangerous shell patterns.
// Delegates to skill.ValidateSafety.
func ValidateSkillSafety(body string) error {
	return skill.ValidateSafety(body)
}

// ValidateSkillReferences checks that the skill body only references known
// tool names. Delegates to skill.ValidateReferences.
func ValidateSkillReferences(body string, knownTools []string) error {
	return skill.ValidateReferences(body, knownTools)
}

// KnownTools returns the canonical set of tool names a skill may reference.
// Delegates to skill.KnownTools.
func KnownTools() []string {
	return skill.KnownTools()
}

// looksLikeTool returns true when s looks like a tool name.
// Thin forwarder for tests — delegates to skill.LooksLikeToolName.
func looksLikeTool(s string) bool {
	return skill.LooksLikeToolName(s)
}

// ─── Layer 2: LLM safety review ──────────────────────────────────────────

// SafetyReviewRequest is the input for LLM-based skill safety review.
type SafetyReviewRequest struct {
	SkillName string `json:"skillName"`
	SkillBody string `json:"skillBody"`
}

// SafetyReviewResult is the LLM's verdict on skill safety.
type SafetyReviewResult struct {
	Safe    bool     `json:"safe"`
	Issues  []string `json:"issues,omitempty"`
	Summary string   `json:"summary"`
}

const systemPromptSafetyReview = `You are a safety reviewer for AI agent skills.

Review the following auto-generated skill and check for:
1. Dangerous instructions (rm -rf, curl | sh, system modification)
2. Unintended side effects
3. Instructions that could cause data loss
4. Overly broad permissions or access

Respond with valid JSON:
{"safe":true/false,"issues":["issue1","issue2"],"summary":"..."}

Be strict: flag anything that could cause harm. False positives are acceptable.`

// BuildSafetyReviewPrompt creates the LLM prompt for safety review.
func BuildSafetyReviewPrompt(req SafetyReviewRequest) string {
	var b strings.Builder
	b.WriteString(systemPromptSafetyReview)
	b.WriteString("\n\n---\n\n")
	b.WriteString("## Skill: ")
	b.WriteString(req.SkillName)
	b.WriteString("\n\n")
	b.WriteString(req.SkillBody)
	b.WriteString("\n\n---\n\nReview the skill for safety issues:")
	return b.String()
}

// ─── Unified validation ──────────────────────────────────────────────────

// ValidateSkill runs all validation layers against a skill.
func ValidateSkill(body string, knownTools []string) error {
	if err := ValidateSkillStructure(body); err != nil {
		return fmt.Errorf("structure: %w", err)
	}
	if err := ValidateSkillSafety(body); err != nil {
		return fmt.Errorf("safety: %w", err)
	}
	if len(knownTools) > 0 {
		if err := ValidateSkillReferences(body, knownTools); err != nil {
			return fmt.Errorf("references: %w", err)
		}
	}
	return nil
}
