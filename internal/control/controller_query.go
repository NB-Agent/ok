package control

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/NB-Agent/ok/internal/config"
	"github.com/NB-Agent/ok/internal/permission"
)

// --- audit display ---

func (c *Controller) showAudit() {
	records := c.AuditLog()
	if len(records) == 0 {
		c.notice("audit: empty (auditing disabled or no tools called yet)")
		return
	}
	// Verify chain integrity.
	if c.auditChain != nil {
		if err := c.auditChain.VerifyChain(); err != nil {
			c.notice("audit: ❌ CHAIN BROKEN — " + err.Error())
		} else {
			c.notice("audit: ✅ chain verified — " + itoa(c.auditChain.Len()) + " entries intact")
		}
	}
	// Show last 15 entries.
	start := 0
	if len(records) > 15 {
		start = len(records) - 15
	}
	for _, r := range records[start:] {
		tag := "✓"
		if !r.Allowed {
			tag = "✗ BLOCKED"
		}
		c.notice(fmt.Sprintf("[%d] %s %s — %s", r.Index, tag, r.Tool, r.Timestamp.Format("15:04:05")))
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func (c *Controller) showSearch(term string) {
	msgs := c.History()
	term = strings.ToLower(strings.TrimSpace(term))
	if term == "" {
		c.notice("/search <term> — search conversation history")
		return
	}
	count := 0
	for _, m := range msgs {
		if strings.Contains(strings.ToLower(m.Content), term) {
			c.notice(fmt.Sprintf("[%s] %s", string(m.Role), clipRunes(m.Content, 200)))
			count++
			if count >= 10 {
				c.notice("… (10 matches shown; refine your search for more)")
				break
			}
		}
	}
	if count == 0 {
		c.notice("no matches for \"" + term + "\"")
	}
}

func clipRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// --- permissions management ---

// handlePermissions processes /permissions slash commands.
func (c *Controller) handlePermissions(input string) {
	parts := strings.Fields(input)
	if len(parts) == 1 {
		c.showPermissions()
		return
	}
	sub := parts[1]
	switch sub {
	case "allow", "ask", "deny":
		if len(parts) < 3 {
			c.notice(fmt.Sprintf("usage: /permissions %s <rule>", sub))
			return
		}
		c.addPermission(sub, parts[2])
	case "remove":
		if len(parts) < 3 {
			c.notice("usage: /permissions remove <rule>")
			return
		}
		c.removePermission(parts[2])
	case "reset":
		c.resetPermissions()
	case "save":
		if err := c.savePermissions(); err != nil {
			c.notice("permissions: save failed: " + err.Error())
		} else {
			c.notice("permissions saved to ok.toml")
		}
	default:
		c.notice("unknown subcommand: " + sub)
	}
}

func (c *Controller) showPermissions() {
	ruleStr := func(rules []permission.Rule) []string {
		out := make([]string, len(rules))
		for i, r := range rules {
			if r.Subject != "" {
				out[i] = r.Tool + "(" + r.Subject + ")"
			} else {
				out[i] = r.Tool
			}
		}
		return out
	}
	join := func(ss []string) string {
		if len(ss) == 0 {
			return "(none)"
		}
		return strings.Join(ss, ", ")
	}
	c.notice("── Permissions ──")
	c.notice("Allow: " + join(ruleStr(c.policy.Allow)))
	c.notice("Ask:   " + join(ruleStr(c.policy.Ask)))
	c.notice("Deny:  " + join(ruleStr(c.policy.Deny)))
}

func (c *Controller) addPermission(kind, rule string) {
	r, ok := permission.ParseRule(rule)
	if !ok {
		c.notice("invalid rule: " + rule)
		return
	}
	c.mu.Lock()
	switch kind {
	case "allow":
		c.policy.Allow = append(c.policy.Allow, r)
	case "ask":
		c.policy.Ask = append(c.policy.Ask, r)
	case "deny":
		c.policy.Deny = append(c.policy.Deny, r)
	}
	c.mu.Unlock()
	c.notice("added " + kind + ": " + formatRuleStr(r))
}

func formatRuleStr(r permission.Rule) string {
	if r.Subject != "" {
		return r.Tool + "(" + r.Subject + ")"
	}
	return r.Tool
}

func (c *Controller) removePermission(rule string) {
	r, ok := permission.ParseRule(rule)
	if !ok {
		c.notice("invalid rule: " + rule)
		return
	}
	c.mu.Lock()
	n := len(c.policy.Allow) + len(c.policy.Ask) + len(c.policy.Deny)
	c.policy.Allow = removeRule(c.policy.Allow, r)
	c.policy.Ask = removeRule(c.policy.Ask, r)
	c.policy.Deny = removeRule(c.policy.Deny, r)
	m := len(c.policy.Allow) + len(c.policy.Ask) + len(c.policy.Deny)
	c.mu.Unlock()
	if n > m {
		c.notice("removed: " + formatRuleStr(r))
	} else {
		c.notice("rule not found: " + formatRuleStr(r))
	}
}

func removeRule(slice []permission.Rule, r permission.Rule) []permission.Rule {
	filtered := slice[:0]
	for _, r2 := range slice {
		if r2.Tool != r.Tool || r2.Subject != r.Subject {
			filtered = append(filtered, r2)
		}
	}
	return filtered
}

func (c *Controller) resetPermissions() {
	c.policy.Allow = nil
	c.policy.Ask = nil
	c.policy.Deny = nil
	c.notice("all permissions reset — use /permissions save to persist")
}

func (c *Controller) savePermissions() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.Mode.Allow = ruleStrings(c.policy.Allow)
	cfg.Mode.Ask = ruleStrings(c.policy.Ask)
	cfg.Mode.Deny = ruleStrings(c.policy.Deny)
	return cfg.Save()
}

func ruleStrings(rules []permission.Rule) []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		if r.Subject != "" {
			out[i] = r.Tool + "(" + r.Subject + ")"
		} else {
			out[i] = r.Tool
		}
	}
	return out
}
