// Package evolution — P1: Skill auto-validation and auto-merging.
package evolution

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// validateAndInstall reads all pending-review candidates, validates them against
// recent episodic memory, checks for duplicate existing skills, and auto-installs.
func (e *Engine) validateAndInstall() (installed, merged, skipped int) {
	if e.skillStor == nil || e.dir == "" {
		return 0, 0, 0
	}

	candidatesDir := filepath.Join(e.dir, "candidates")
	entries, err := os.ReadDir(candidatesDir)
	if err != nil {
		return 0, 0, 0
	}

	existing := e.existingSkillNames()

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(candidatesDir, entry.Name())

		cand := e.readCandidate(path)
		if cand == nil || cand.Status != "pending-review" {
			continue
		}

		// 1. Validate: check recent episodic memory for the pattern
		if !e.validatePattern(cand.Patterns) {
			e.updateCandidateStatus(path, "expired")
			skipped++
			continue
		}

		// 2. Check duplicates against existing skills
		skillName := patternToSkillName(cand.Patterns)
		if existing[skillName] {
			e.updateCandidateStatus(path, "merged")
			merged++
			continue
		}

		// 3. Auto-generate and install (enhanced body with workflow hints)
		var workflows []string
		for _, p := range cand.Patterns {
			if strings.HasPrefix(p, "workflow:") {
				rest := strings.TrimPrefix(p, "workflow:")
				parts := strings.SplitN(rest, ":", 2)
				if len(parts) >= 2 {
					workflows = append(workflows, parts[0])
				}
			}
		}
		body := generateSkillBodyEnhanced(skillName, cand.Patterns, workflows)
		_, err := e.skillStor.CreateWithContent(skillName, "project", body)
		if err != nil {
			log.Printf("evolution: install skill %q: %v", skillName, err)
			skipped++
			continue
		}

		e.updateCandidateStatus(path, "installed")
		installed++
		existing[skillName] = true
	}
	return
}

type candidate struct {
	Status   string
	Patterns []string
}

func (e *Engine) readCandidate(path string) *candidate {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	content := string(data)
	c := &candidate{}

	if idx := strings.Index(content, "status:"); idx >= 0 {
		end := strings.Index(content[idx:], "\n")
		if end > 0 {
			c.Status = strings.TrimSpace(content[idx+7 : idx+end])
		}
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "repeated-tool:") || strings.HasPrefix(line, "sequence:") {
			c.Patterns = append(c.Patterns, line)
		}
	}
	if c.Status == "" || len(c.Patterns) == 0 {
		return nil
	}
	return c
}

func (e *Engine) updateCandidateStatus(path, status string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	content := string(data)
	newContent := strings.Replace(content, "status: pending-review", "status: "+status, 1)
	if newContent == content {
		newContent = strings.Replace(content, "status: "+status, "status: "+status, 1)
	}
	// Best-effort: status updates may be lost on I/O failure, but evolution
	// continues — the candidate will be re-evaluated on the next cycle.
	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		log.Printf("evolution: update status %q: %v", status, err)
	}
}

func (e *Engine) existingSkillNames() map[string]bool {
	set := make(map[string]bool)
	if e.skillStor == nil {
		return set
	}
	for _, sk := range e.skillStor.List() {
		set[sk.Name] = true
	}
	return set
}

func (e *Engine) validatePattern(patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	episodicDir := filepath.Join(e.dir, "episodic")
	entries, err := os.ReadDir(episodicDir)
	if err != nil || len(entries) < 1 {
		return false
	}
	n := min(len(entries), 5)
	recent := make([]string, 0, n)
	for i := len(entries) - n; i < len(entries); i++ {
		data, err := os.ReadFile(filepath.Join(episodicDir, entries[i].Name()))
		if err != nil {
			continue
		}
		recent = append(recent, string(data))
	}
	combined := strings.ToLower(strings.Join(recent, " "))
	for _, p := range patterns {
		if strings.HasPrefix(p, "sequence:") {
			// For sequence patterns, check the tools in the sequence are all present
			rest := strings.TrimPrefix(p, "sequence:")
			parts := strings.SplitN(rest, ":", 2)
			if len(parts) >= 2 {
				for _, tool := range strings.Split(parts[0], "\u2192") {
					tool = strings.TrimSpace(tool)
					if tool != "" && !strings.Contains(combined, tool) {
						return false
					}
				}
			}
		} else {
			parts := strings.SplitN(p, ":", 3)
			if len(parts) >= 2 {
				neededTool := parts[1]
				if !strings.Contains(combined, neededTool) {
					return false
				}
			}
		}
	}
	return true
}

func patternToSkillName(patterns []string) string {
	if len(patterns) == 0 {
		return "auto-skill"
	}
	var bestTool string
	bestCount := 0
	for _, p := range patterns {
		parts := strings.SplitN(p, ":", 3)
		if len(parts) >= 3 {
			count := 0
			fmt.Sscanf(parts[2], "%d", &count)
			if count > bestCount {
				bestCount = count
				bestTool = parts[1]
			}
		}
	}
	if bestTool == "" {
		bestTool = strings.TrimPrefix(patterns[0], "repeated-tool:")
		parts := strings.SplitN(bestTool, ":", 2)
		bestTool = parts[0]
	}
	name := "auto-" + bestTool
	if !skillFileExists(name) {
		return name
	}
	h := sha256.Sum256([]byte(strings.Join(patterns, "|")))
	return name + "-" + hex.EncodeToString(h[:4])
}

func generateSkillBody(name string, patterns []string) string {
	var toolList []string
	var seqList []string
	for _, p := range patterns {
		if strings.HasPrefix(p, "sequence:") {
			rest := strings.TrimPrefix(p, "sequence:")
			parts := strings.SplitN(rest, ":", 2)
			if len(parts) == 2 {
				seqList = append(seqList, fmt.Sprintf("- %s (repeated %s times)", parts[0], parts[1]))
			}
		} else {
			parts := strings.SplitN(p, ":", 3)
			if len(parts) >= 2 {
				toolList = append(toolList, fmt.Sprintf("- %s (used %s times)", parts[1], parts[2]))
			}
		}
	}
	desc := "Auto-generated skill for " + strings.Join(extractToolNames(patterns), "/")
	if len(seqList) > 0 {
		desc += " with sequence patterns"
	}

	return fmt.Sprintf(`---
name: %s
description: %s
runAs: inline
---

# %s

Auto-generated from repeated usage patterns:

%s
%s

## Usage

Invoke this skill when you need to perform these operations:
%s
`, name, desc, name, strings.Join(toolList, "\n"),
		formatSequenceSection(seqList),
		formatToolUsage(patterns))
}

func extractToolNames(patterns []string) []string {
	var names []string
	for _, p := range patterns {
		if strings.HasPrefix(p, "sequence:") {
			rest := strings.TrimPrefix(p, "sequence:")
			parts := strings.SplitN(rest, ":", 2)
			if len(parts) >= 2 {
				// sequence:bash→grep→read_file:2 → extract each tool name
				for _, tool := range strings.Split(parts[0], "\u2192") {
					tool = strings.TrimSpace(tool)
					if tool != "" {
						names = append(names, tool)
					}
				}
			}
		} else if strings.HasPrefix(p, "workflow:") {
			// Look up the workflow signature's tools.
			rest := strings.TrimPrefix(p, "workflow:")
			parts := strings.SplitN(rest, ":", 2)
			if len(parts) >= 2 {
				for _, sig := range builtinSignatures {
					if sig.Name == parts[0] {
						names = append(names, sig.Tools...)
						break
					}
				}
			}
		} else {
			parts := strings.SplitN(p, ":", 3)
			if len(parts) >= 2 {
				names = append(names, parts[1])
			}
		}
	}
	return names
}

func formatToolUsage(patterns []string) string {
	var lines []string
	for _, p := range patterns {
		if strings.HasPrefix(p, "sequence:") {
			rest := strings.TrimPrefix(p, "sequence:")
			parts := strings.SplitN(rest, ":", 2)
			if len(parts) >= 2 {
				lines = append(lines, fmt.Sprintf("\n### Sequence: %s\n\nRepeat the tool sequence `%s` when this workflow is needed.",
					parts[0], parts[0]))
			}
		} else {
			parts := strings.SplitN(p, ":", 3)
			if len(parts) >= 2 {
				lines = append(lines, fmt.Sprintf("\n### %s\n\nUse the `%s` tool when this operation is needed.",
					parts[1], parts[1]))
			}
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "")
}

func formatSequenceSection(seqList []string) string {
	if len(seqList) == 0 {
		return ""
	}
	return "### Detected Workflow Sequences\n\n" + strings.Join(seqList, "\n")
}

func skillFileExists(name string) bool {
	for _, dir := range []string{".ok/skills"} {
		flat := filepath.Join(dir, name+".md")
		if _, err := os.Stat(flat); err == nil {
			return true
		}
		folder := filepath.Join(dir, name, "SKILL.md")
		if _, err := os.Stat(folder); err == nil {
			return true
		}
	}
	return false
}
