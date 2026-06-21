package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/NB-Agent/ok/internal/tool"
)

// toolGroupsTool lets the agent switch tool groups to reduce schema tokens.
// It holds a reference to the session's registry for live group switching.
type toolGroupsTool struct {
	reg tool.RegistrySetGroups // interface to switch groups
}

// NewToolGroupsTool creates a tool-groups tool bound to a registry.
func NewToolGroupsTool(reg tool.RegistrySetGroups) tool.Tool {
	return &toolGroupsTool{reg: reg}
}

func (t *toolGroupsTool) Name() string { return "tool-groups" }

func (t *toolGroupsTool) Description() string {
	return "Switch tool groups to control which tools the model sees. Start with 'core' (files/search/task), then activate 'advanced' (git/db/debug/desktop/workflow) or 'knowledge' (rag/search) when needed. Groups: core, advanced, knowledge, admin."
}

func (t *toolGroupsTool) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"action":{"enum":["activate","list"],"type":"string"},"groups":{"type":"string"}},"required":["action"],"type":"object"}`)
}

func (t *toolGroupsTool) ReadOnly() bool { return true }

func (t *toolGroupsTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action string `json:"action"`
		Groups string `json:"groups"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}

	switch p.Action {
	case "list":
		all := t.reg.AllNames()
		visible := t.reg.Names()
		active := t.reg.ActiveGroupNames()

		var b strings.Builder
		b.WriteString("# Tool Groups\n\n")
		if len(active) == 0 {
			b.WriteString("Active: all tools visible (default)\n")
		} else {
			b.WriteString(fmt.Sprintf("Active: %s\n", strings.Join(append([]string{"core"}, active...), ", ")))
		}
		b.WriteString(fmt.Sprintf("Visible: %d/%d tools\n\n", len(visible), len(all)))
		b.WriteString("Groups:\n")
		b.WriteString("- core (always active)\n")
		b.WriteString("- advanced — git, database, debug, deploy, desktop, digest, schedule, workflow, plan, auto-heal\n")
		b.WriteString("- knowledge — rag, semantic-search, symbol-find, style-check, image-read, ocr\n")
		b.WriteString("- admin — repo, make-tool, go-profile, vuln-check, translate, computer-use\n")
		return b.String(), nil

	case "activate":
		if p.Groups == "" {
			return "", fmt.Errorf("groups is required (comma-separated, e.g. 'advanced,knowledge')")
		}
		groups := strings.Split(p.Groups, ",")
		cleaned := make([]string, 0, len(groups))
		for _, g := range groups {
			g = strings.TrimSpace(g)
			if g != "" {
				cleaned = append(cleaned, g)
			}
		}
		if len(cleaned) == 0 {
			return "", fmt.Errorf("at least one group name is required")
		}
		sort.Strings(cleaned)

		t.reg.ActivateGroups(cleaned...)

		return fmt.Sprintf("✅ Will activate tool groups on next turn: %s\n(Deferred to keep prompt cache warm — current turn uses unchanged schemas.)",
			strings.Join(cleaned, ", ")), nil

	default:
		return "", fmt.Errorf("unknown action: %s", p.Action)
	}
}
