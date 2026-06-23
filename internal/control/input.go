package control

import (
	"context"
	"fmt"
	"strings"

	"github.com/NB-Agent/ok/internal/skill"
)

// Compose enriches user text with mid-session memory updates, completed
// background-job notifications, periodic environment diagnosis, and proof-chain
// summaries — all riding the user message so the cache-stable prompt prefix is
// left untouched.
func (c *Controller) Compose(text string) string {
	c.mu.Lock()
	notes := c.pendingMemory
	c.pendingMemory = nil
	turn := c.turn
	c.mu.Unlock()

	// First-turn inject: memory, shared knowledge, and skill index ride the
	// first user message (not the system prompt) so the cache-stable prefix
	// stays byte-identical across sessions. Turn 0 = first call from Submit.
	if c.firstTurnInject != "" && turn == 0 {
		text = c.firstTurnInject + "\n\n" + text
	}

	// Memory added mid-session rides the turn (never the cached system prefix),
	// so it takes effect now without invalidating the prompt cache. It folds into
	// the system prefix on the next session, where it costs nothing per turn.
	if len(notes) > 0 {
		var b strings.Builder
		b.WriteString("<memory-update>\n")
		b.WriteString("The following was just saved to project memory and applies from now on:\n")
		for _, n := range notes {
			b.WriteString("- " + n + "\n")
		}
		b.WriteString("</memory-update>\n\n")
		text = b.String() + text
	}

	// Background jobs that finished since the last turn ride the turn too, so the
	// model learns of completions even though the user-facing notices don't reach
	// its context. Like memory, this never touches the cache-stable prefix.
	if c.jobs != nil {
		if note := c.jobs.DrainCompletedNote(); note != "" {
			text = "<background-jobs>\n" + note + "\n</background-jobs>\n\n" + text
		}
	}

	// Environment/diagnosis context is injected every envDiagInterval turns (default 50)
	// so the cache-stable system-prompt prefix stays byte-identical (warm cache).
	if c.envDiagnosis != "" && c.envDiagInterval > 0 {
		c.mu.Lock()
		turn := c.turn
		c.mu.Unlock()
		if turn > 0 && turn%c.envDiagInterval == 0 {
			text = fmt.Sprintf("# Environment\n\n%s\n\n", c.envDiagnosis) + text
		}
	}

	// Proof-chain summary: injected only when the chain has new items (proofDirty),
	// so the agent sees fresh verification results without wasting ~200 tokens/turn
	// on an identical summary. Rides the turn (never the cache prefix).
	// Summary is cached and only recomputed when the proof chain mutates.
	if c.proofChain != nil {
		curLen := c.proofChain.Len()
		if curLen != c.proofSummaryLen || c.cachedProofSummary == "" {
			c.proofSummaryLen = curLen
			// Prefer tree view when sub-agent paths are present.
			if ts := c.proofChain.TreeSummary(20); ts != "" {
				c.cachedProofSummary = ts
				c.proofDirty = true
			} else if ps := c.proofChain.ProofSummary(20); ps != "" {
				c.cachedProofSummary = ps
				c.proofDirty = true
			} else {
				c.cachedProofSummary = ""
				c.proofDirty = false // empty summary — nothing to inject
			}
		}
		if c.cachedProofSummary != "" && c.proofDirty {
			text = c.cachedProofSummary + "\n\n" + text
			c.proofDirty = false
		}
	}

	return text
}

// CustomCommand resolves a "/name args…" line against the loaded custom slash
// commands, returning the rendered prompt to send (found=false when no command
// matches). It does not apply the plan-mode marker — call Compose for that.
func (c *Controller) CustomCommand(input string) (sent string, found bool) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return "", false
	}
	name := strings.TrimPrefix(fields[0], "/")
	for _, cmd := range c.commands {
		if cmd.Name == name {
			return cmd.Render(fields[1:]), true
		}
	}
	return "", false
}

// RunSkill resolves a "/<name> args…" line against the loaded skills, returning
// the skill's rendered body to send as a turn (found=false when no skill
// matches). Invoking a skill by slash always inlines its body — the model reads
// and follows the playbook in the main loop; a subagent skill's isolation is
// only engaged when the model calls it via run_skill / the dedicated tool. The
// caller applies Compose for plan-mode/memory framing.
func (c *Controller) RunSkill(input string) (sent string, found bool) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return "", false
	}
	name := strings.TrimPrefix(fields[0], "/")
	for _, sk := range c.skills {
		if sk.Name == name {
			return skill.Render(sk, strings.Join(fields[1:], " ")), true
		}
	}
	return "", false
}

// MCPPrompt resolves a "/mcp__server__prompt args…" line: it maps the positional
// args onto the prompt's declared arguments and fetches the rendered prompt from
// the MCP server (an async prompts/get). found is false when no such prompt
// exists; err carries a fetch failure. Honors ctx.
func (c *Controller) MCPPrompt(ctx context.Context, input string) (sent string, found bool, err error) {
	if c.host == nil {
		return "", false, nil
	}
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return "", false, nil
	}
	name := strings.TrimPrefix(fields[0], "/")

	prompts := c.host.Prompts()
	idx := -1
	for i := range prompts {
		if prompts[i].Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", false, nil
	}

	args := map[string]string{}
	for i, a := range prompts[idx].Args {
		if i+1 < len(fields) {
			args[a.Name] = fields[i+1]
		}
	}
	text, err := prompts[idx].Get(ctx, args)
	if err != nil {
		return "", true, err
	}
	return text, true, nil
}
