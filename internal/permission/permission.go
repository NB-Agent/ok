// Package permission implements a three-layer authorization model:
//  1. Core Covenant — immutable ethical bedrock checked at compile-time
//  2. Plan Mode — rejects all write operations during planning
//  3. Policy Gate — Allow/Ask/Deny rules per tool and subject
//
// It is used by the agent to gate every tool invocation.
package permission

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Rule matches tool calls. Tool is the tool name; Subject, when non-empty, is a
// glob (see matchGlob) the call's subject must match. An empty Subject matches
// every call to Tool.
type Rule struct {
	Tool    string
	Subject string
}

// ParseRule parses "ToolName" or "ToolName(glob)".
func ParseRule(s string) (Rule, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Rule{}, false
	}
	if i := strings.IndexByte(s, '('); i >= 0 && strings.HasSuffix(s, ")") {
		tool := strings.TrimSpace(s[:i])
		if tool == "" {
			return Rule{}, false
		}
		return Rule{Tool: tool, Subject: s[i+1 : len(s)-1]}, true
	}
	return Rule{Tool: s}, true
}

func parseRules(ss []string) []Rule {
	var out []Rule
	for _, s := range ss {
		if r, ok := ParseRule(s); ok {
			out = append(out, r)
		} else {
			fmt.Fprintf(os.Stderr, "permission: malformed rule %q dropped\n", s)
		}
	}
	return out
}

// Policy holds allow, ask, and deny rules. Precedence: deny > ask > allow.
// A tool call not matching any rule falls through to the mode's default
// (plan=block writers, normal=prompt, yolo=auto-allow).
type Policy struct {
	Allow []Rule
	Ask   []Rule
	Deny  []Rule
}

// NewPolicy builds a Policy from allow/ask/deny rule lists.
func NewPolicy(allow, ask, deny []string) Policy {
	return Policy{
		Allow: parseRules(allow),
		Ask:   parseRules(ask),
		Deny:  parseRules(deny),
	}
}

// NewDenyPolicy builds a Policy from a deny rule list only (backward compat).
func NewDenyPolicy(deny []string) Policy {
	return Policy{Deny: parseRules(deny)}
}

// Classify returns "deny", "ask", "allow", or "" (fall through) for a tool call.
func (p Policy) Classify(toolName string, args json.RawMessage) (decision string, reason string) {
	subject := Subject(args)
	if matchAny(p.Deny, toolName, subject) {
		return "deny", "denied by policy (" + ruleDesc(toolName, subject) + ")"
	}
	if matchAny(p.Ask, toolName, subject) {
		return "ask", ""
	}
	if matchAny(p.Allow, toolName, subject) {
		return "allow", ""
	}
	return "", ""
}

// Denied reports whether a tool call matches a deny rule, with a reason.
func (p Policy) Denied(toolName string, args json.RawMessage) (string, bool) {
	subject := Subject(args)
	if matchAny(p.Deny, toolName, subject) {
		if subject != "" {
			return "denied by policy (" + toolName + "(" + subject + "))", true
		}
		return "denied by policy (" + toolName + ")", true
	}
	return "", false
}

func ruleDesc(toolName, subject string) string {
	if subject != "" {
		return toolName + "(" + subject + ")"
	}
	return toolName
}

// matchAny reports whether any rule matches the (toolName, subject) pair.
func matchAny(rules []Rule, toolName, subject string) bool {
	for _, r := range rules {
		if r.Tool != toolName {
			continue
		}
		if r.Subject == "" {
			return true
		}
		if subject != "" && matchGlob(r.Subject, subject) {
			return true
		}
	}
	return false
}

// subjectKeys are the JSON argument keys, in priority order, that carry a tool
// call's "subject".
var subjectKeys = []string{"command", "file_path", "path", "pattern"}

// Subject extracts the matchable subject string from a call's raw JSON args.
// It recursively flattens nested values (e.g. {"command": {"name": "whoami"}})
// so that permission rules don't silently miss deeply nested tool calls.
func Subject(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}
	for _, k := range subjectKeys {
		v, ok := m[k]
		if !ok {
			continue
		}
		s := subjectValue(v)
		if s != "" {
			// If this is a command field, strip shell wrappers
			// so that rules like "bash(npm install)" can match
			// "timeout 30s nice -n 10 npm install".
			if k == "command" {
				return CleanCommand(s)
			}
			return s
		}
	}
	return ""
}

// subjectValue extracts a string from a JSON value, recursively flattening
// nested objects to their deepest string value.
func subjectValue(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case map[string]any:
		// Recursively search nested values
		for _, nv := range val {
			if s := subjectValue(nv); s != "" {
				return s
			}
		}
	case []any:
		// Arrays: take the first non-empty element
		for _, ev := range val {
			if s := subjectValue(ev); s != "" {
				return s
			}
		}
	}
	return ""
}

// matchGlob reports whether name matches pattern, where '*' matches any run of
// characters (including separators) and '?' matches exactly one.
func matchGlob(pattern, name string) bool {
	var px, nx, starPx, starNx int
	starPx = -1
	for nx < len(name) {
		switch {
		case px < len(pattern) && (pattern[px] == '?' || pattern[px] == name[nx]):
			px++
			nx++
		case px < len(pattern) && pattern[px] == '*':
			starPx = px
			starNx = nx
			px++
		case starPx != -1:
			px = starPx + 1
			starNx++
			nx = starNx
		default:
			return false
		}
	}
	for px < len(pattern) && pattern[px] == '*' {
		px++
	}
	return px == len(pattern)
}

// Approver resolves a writer tool prompt interactively.
type Approver interface {
	Approve(ctx context.Context, toolName, subject string, args json.RawMessage) (allow, remember bool, err error)
}

// Gate is what the agent consults at execute time.
type Gate struct {
	Policy     Policy
	Approver   Approver
	OnRemember func(rule string)
}

// NewGate wires a Policy to an Approver (nil for non-interactive/YOLO use).
func NewGate(p Policy, a Approver) *Gate { return &Gate{Policy: p, Approver: a} }

// Check decides whether a tool call may run.
//  1. If the call matches a deny rule → block
//  2. If the call matches an ask rule and Approver is present → prompt
//  3. If the call matches an allow rule → allow (even for writers)
//  4. If readOnly or no Approver → allow
//  5. Otherwise (writer + Approver present) → prompt (mode default)
func (g *Gate) Check(ctx context.Context, toolName string, args json.RawMessage, readOnly bool) (bool, string, error) {
	subject := Subject(args)

	// Policy classification
	decision, reason := g.Policy.Classify(toolName, args)
	switch decision {
	case "deny":
		return false, reason, nil
	case "ask":
		if g.Approver == nil {
			return true, "", nil // nothing can prompt us
		}
		return g.promptUser(ctx, toolName, subject, args)
	case "allow":
		return true, "", nil
	}

	// Fall through: no explicit rule → use mode default
	if readOnly || g.Approver == nil {
		return true, "", nil
	}
	return g.promptUser(ctx, toolName, subject, args)
}

func (g *Gate) promptUser(ctx context.Context, toolName, subject string, args json.RawMessage) (bool, string, error) {
	allow, remember, err := g.Approver.Approve(ctx, toolName, subject, args)
	if err != nil {
		return false, "approval aborted", err
	}
	if !allow {
		return false, "the user declined this tool call — do not retry it; ask how they would like to proceed or choose another approach.", nil
	}
	if remember && g.OnRemember != nil {
		g.OnRemember(rememberRule(toolName, subject))
	}
	return true, "", nil
}

func rememberRule(toolName, subject string) string {
	if subject == "" {
		return toolName
	}
	return toolName + "(" + subject + ")"
}
