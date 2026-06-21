// Package evolution — Gap 2: Intelligent skill synthesis.
//
// Replaces the template-based generateSkillBody with a layered approach:
//
//	Layer 0 (automatic hook path, zero-LLM):
//	  generateSkillBody produces structured playbooks from detected patterns.
//	  This is the existing function, now enhanced with workflow-aware
//	  descriptions.
//
//	Layer 1 (Learn tool path, LLM-driven):
//	  SynthesizeSkill sends patterns + episodic context to the LLM and
//	  receives a high-quality skill prompt that captures:
//	    - When to invoke the skill
//	    - Step-by-step workflow
//	    - Expected outcomes and verification steps
//	    - Common pitfalls and edge cases
//
// The resulting skill is usable immediately — not just a skeleton, but a
// real instruction set the agent can follow.
package evolution

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ─── Layer 0: Enhanced template-based generation ─────────────────────────

// generateSkillBodyEnhanced produces a richer skill body than the original
// generateSkillBody. It adds:
//
//   - Workflow pattern descriptions (when detected)
//   - Suggested trigger conditions
//   - Verification steps
//
// This is the zero-LLM path — always available, generates usable skills.
func generateSkillBodyEnhanced(name string, patterns []string, workflowHints []string) string {
	var toolPatterns []string
	var seqPatterns []string
	var workflowPatterns []string

	for _, p := range patterns {
		switch {
		case strings.HasPrefix(p, "workflow:"):
			workflowPatterns = append(workflowPatterns, formatWorkflowPattern(p))
		case strings.HasPrefix(p, "sequence:"):
			seqPatterns = append(seqPatterns, formatSequencePattern(p))
		case strings.HasPrefix(p, "fingerprint:"):
			// Fingerprint patterns are informational — mention in context.
			toolPatterns = append(toolPatterns, formatFingerprintPattern(p))
		default:
			// repeated-tool patterns
			toolPatterns = append(toolPatterns, formatRepeatedPattern(p))
		}
	}

	desc := buildSkillDescription(patterns, workflowHints)
	trigger := buildTriggerSection(patterns)
	steps := buildStepsSection(patterns)
	verify := buildVerifySection(patterns)

	return fmt.Sprintf(`---
name: %s
description: %s
runAs: inline
---

# %s

Auto-generated from repeated usage patterns:

## Patterns Detected

%s
%s
%s

## When to Use

%s

## Steps

%s

## Verification

%s
`, name, desc, name,
		formatSection("Repeated Tools", toolPatterns),
		formatSection("Workflow Sequences", seqPatterns),
		formatSection("Workflow Signatures", workflowPatterns),
		trigger,
		steps,
		verify,
	)
}

func formatWorkflowPattern(p string) string {
	// Format: workflow:<name>:<count>
	rest := strings.TrimPrefix(p, "workflow:")
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) < 2 {
		return "- " + rest
	}
	name := parts[0]
	count := parts[1]

	// Look up the human-readable description.
	desc := name
	for _, sig := range builtinSignatures {
		if sig.Name == name {
			desc = sig.Description
			break
		}
	}
	return fmt.Sprintf("- **%s** (%s times): %s", name, count, desc)
}

func formatSequencePattern(p string) string {
	rest := strings.TrimPrefix(p, "sequence:")
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) < 2 {
		return "- " + rest
	}
	return fmt.Sprintf("- `%s` (repeated %s times)", parts[0], parts[1])
}

func formatFingerprintPattern(p string) string {
	rest := strings.TrimPrefix(p, "fingerprint:")
	// fingerprint:tool:bash:4  or  fingerprint:pair:bash→grep:3
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) < 3 {
		return "- " + rest
	}
	switch parts[0] {
	case "tool":
		return fmt.Sprintf("- `%s` used %s times across sessions", parts[1], parts[2])
	case "pair":
		return fmt.Sprintf("- Cross-turn pattern: `%s` (%s times)", parts[1], parts[2])
	case "burst":
		return fmt.Sprintf("- `%s` used in %s consecutive turns", parts[1], parts[2])
	}
	return "- " + rest
}

func formatRepeatedPattern(p string) string {
	parts := strings.SplitN(p, ":", 3)
	if len(parts) < 3 {
		return "- " + strings.TrimPrefix(p, "repeated-tool:")
	}
	return fmt.Sprintf("- `%s` (used %s times)", parts[1], parts[2])
}

func buildSkillDescription(patterns []string, hints []string) string {
	var parts []string

	// Extract tool names for description.
	tools := extractToolNames(patterns)
	if len(tools) > 0 {
		unique := uniqueStrings(tools)
		parts = append(parts, "Auto-generated skill for "+strings.Join(unique, "/"))
	}

	// Add workflow hints.
	if len(hints) > 0 {
		parts = append(parts, "Workflow: "+strings.Join(hints, "; "))
	} else if len(patterns) > 0 {
		// Describe the top pattern.
		top := patterns[0]
		if strings.HasPrefix(top, "workflow:") {
			for _, sig := range builtinSignatures {
				if strings.Contains(top, sig.Name) {
					parts = append(parts, sig.Description)
					break
				}
			}
		}
	}

	if len(parts) == 0 {
		parts = append(parts, "Auto-generated skill from usage patterns")
	}
	return strings.Join(parts, ". ")
}

func buildTriggerSection(patterns []string) string {
	var triggers []string

	for _, p := range patterns {
		if strings.HasPrefix(p, "workflow:") {
			rest := strings.TrimPrefix(p, "workflow:")
			parts := strings.SplitN(rest, ":", 2)
			if len(parts) < 2 {
				continue
			}
			switch parts[0] {
			case "tdd-workflow":
				triggers = append(triggers, "When implementing a new feature or fixing a bug")
			case "search-then-edit":
				triggers = append(triggers, "When you need to locate and modify code across the codebase")
			case "audit-then-fix":
				triggers = append(triggers, "After making changes, before considering work done")
			case "dependency-update":
				triggers = append(triggers, "When updating project dependencies")
			case "build-verify-deploy":
				triggers = append(triggers, "When preparing to deploy or release")
			case "research-then-write":
				triggers = append(triggers, "When implementing something you haven't worked with before")
			case "git-commit-cycle":
				triggers = append(triggers, "After completing a logical unit of work")
			case "debug-cycle":
				triggers = append(triggers, "When test or build failures occur")
			}
		}
	}

	if len(triggers) == 0 {
		triggers = append(triggers, "When performing this repeated operation")
	}

	return "- " + strings.Join(triggers, "\n- ")
}

func buildStepsSection(patterns []string) string {
	var steps []string
	seen := make(map[string]bool)

	for _, p := range patterns {
		if strings.HasPrefix(p, "sequence:") || strings.HasPrefix(p, "workflow:") {
			tools := extractToolNames([]string{p})
			for _, t := range tools {
				if seen[t] {
					continue
				}
				seen[t] = true
				steps = append(steps, fmt.Sprintf("%d. Use `%s`", len(steps)+1, t))
			}
		}
	}

	if len(steps) == 0 {
		// Fallback: list tools from repeated-tool patterns.
		for _, p := range patterns {
			if strings.HasPrefix(p, "repeated-tool:") {
				parts := strings.SplitN(p, ":", 3)
				if len(parts) >= 2 && !seen[parts[1]] {
					seen[parts[1]] = true
					steps = append(steps, fmt.Sprintf("%d. Use `%s`", len(steps)+1, parts[1]))
				}
			}
		}
	}

	if len(steps) == 0 {
		steps = append(steps, "1. Follow the detected pattern")
	}

	return strings.Join(steps, "\n")
}

func buildVerifySection(patterns []string) string {
	var checks []string

	// Check for TDD workflow → suggest running tests.
	for _, p := range patterns {
		if strings.Contains(p, "tdd") {
			checks = append(checks, "Run `go test ./...` to verify all tests pass")
			break
		}
	}

	// Check for audit workflow → suggest ok-verify.
	for _, p := range patterns {
		if strings.Contains(p, "audit") {
			checks = append(checks, "Run `ok-verify` to confirm no issues remain")
			break
		}
	}

	// Check for build → suggest build check.
	for _, p := range patterns {
		if strings.Contains(p, "build") || strings.Contains(p, "deploy") {
			checks = append(checks, "Run `go build ./...` to confirm compilation")
			break
		}
	}

	if len(checks) == 0 {
		checks = append(checks, "Review the output to confirm the operation succeeded")
	}

	return "- " + strings.Join(checks, "\n- ")
}

func formatSection(title string, items []string) string {
	if len(items) == 0 {
		return ""
	}
	return fmt.Sprintf("### %s\n\n%s\n", title, strings.Join(items, "\n"))
}

func uniqueStrings(ss []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ─── Layer 1: LLM-driven skill synthesis ──────────────────────────────────

// SynthesisRequest is the input to LLM-driven skill synthesis.
type SynthesisRequest struct {
	Name      string   `json:"name"`      // proposed skill name
	Patterns  []string `json:"patterns"`  // detected patterns
	Context   []string `json:"context"`   // recent episodic entries for context
	Workflows []string `json:"workflows"` // detected workflow names
	ToolNames []string `json:"toolNames"` // tools involved
}

// SynthesisResult is the output of LLM-driven skill synthesis.
type SynthesisResult struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"` // full skill markdown body
}

// systemPromptSynthesis guides the LLM to produce high-quality skill bodies.
const systemPromptSynthesis = `You are a skill synthesizer for an AI agent's self-evolution engine.

Given detected usage patterns, create a high-quality, actionable skill that
another instance of the agent can follow.

The skill body must include:
1. A frontmatter block (---) with name and description
2. "## When to Use" — specific trigger conditions
3. "## Steps" — numbered, concrete steps using specific tools
4. "## Verification" — how to confirm success
5. "## Notes" — edge cases, gotchas, alternatives

Make each step concrete: name the tool, describe what to do, and what to
expect. The agent follows these literally — be precise.

Return ONLY the skill body as markdown. No preamble, no commentary.`

// SynthesizeBody generates a skill body from patterns using the LLM. The
// provider argument is optional — when nil, falls back to enhanced template
// generation (Layer 0).
//
// This is the Layer 1 path, called via the Learn tool when a provider is
// available.
func SynthesizeBody(req SynthesisRequest) string {
	// When no LLM context is provided, use the enhanced template path.
	if len(req.Context) == 0 {
		return generateSkillBodyEnhanced(req.Name, req.Patterns, req.Workflows)
	}

	// Build the synthesis prompt.
	prompt := buildSynthesisPrompt(req)

	// The caller injects the prompt into their LLM provider. We return the
	// prompt so the Learn tool can stream it to the model and collect the
	// response. The response IS the skill body.
	return prompt
}

// SynthesizePrompt returns the prompt to send to the LLM. The response
// should be the full skill body.
func SynthesizePrompt(req SynthesisRequest) string {
	return buildSynthesisPrompt(req)
}

func buildSynthesisPrompt(req SynthesisRequest) string {
	var b strings.Builder
	b.WriteString(systemPromptSynthesis)
	b.WriteString("\n\n---\n\n")

	b.WriteString("## Skill Name\n")
	b.WriteString(req.Name)
	b.WriteString("\n\n")

	b.WriteString("## Detected Patterns\n")
	for _, p := range req.Patterns {
		b.WriteString("- ")
		b.WriteString(p)
		b.WriteString("\n")
	}
	b.WriteString("\n")

	if len(req.Workflows) > 0 {
		b.WriteString("## Workflow Context\n")
		for _, w := range req.Workflows {
			b.WriteString("- ")
			b.WriteString(w)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(req.ToolNames) > 0 {
		b.WriteString("## Tools Involved\n")
		for _, t := range req.ToolNames {
			b.WriteString("- ")
			b.WriteString(t)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(req.Context) > 0 {
		b.WriteString("## Recent Context\n")
		for i, c := range req.Context {
			if i >= 3 {
				b.WriteString("... (")
				b.WriteString(itoa(len(req.Context) - 3))
				b.WriteString(" more entries omitted)\n")
				break
			}
			b.WriteString(c)
			b.WriteString("\n\n")
		}
	}

	b.WriteString("\n---\n\nGenerate the skill body now:")
	return b.String()
}

// DecodeSynthesisResult parses an LLM response into a SynthesisResult.
// The response is expected to be a markdown skill body; we extract the
// frontmatter to populate Name and Description.
func DecodeSynthesisResult(response string) SynthesisResult {
	result := SynthesisResult{Body: response}

	// Extract frontmatter name/description.
	lines := strings.Split(response, "\n")
	inFM := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "---" {
			if !inFM {
				inFM = true
				continue
			}
			break
		}
		if inFM {
			if strings.HasPrefix(line, "name:") {
				result.Name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
			}
			if strings.HasPrefix(line, "description:") {
				result.Description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			}
		}
	}

	return result
}

// MarshalPatternsForLLM serializes patterns to a JSON string suitable for
// the LLM context. Used by the Learn tool path.
func MarshalPatternsForLLM(patterns []string) string {
	b, _ := json.Marshal(patterns)
	return string(b)
}
