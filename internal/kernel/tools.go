package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/tool"
)

// ─── identity tool ──────────────────────────────────────────────────────

type identityTool struct{ inner Identity }

// NewIdentityTool wraps a kernel.Identity as a callable tool.Tool.
func NewIdentityTool(inner Identity) tool.Tool { return &identityTool{inner: inner} }

func (*identityTool) Name() string   { return "identity" }
func (*identityTool) ReadOnly() bool { return true }
func (*identityTool) Description() string {
	return "Who the user is — role, preferences, locale. Returns current user profile."
}
func (*identityTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["whoami","set_user","list_users"]},"user_id":{"type":"string"}},"required":["action"]}`)
}

func (t *identityTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action string `json:"action"`
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("identity: invalid args: %w", err)
	}
	switch p.Action {
	case "whoami":
		u := t.inner.Whoami(ctx)
		return formatUser(u), nil
	case "set_user":
		if err := t.inner.SetUser(ctx, p.UserID); err != nil {
			return "", err
		}
		u := t.inner.Whoami(ctx)
		return fmt.Sprintf("User switched to: %s (%s)", u.Label, u.ID), nil
	case "list_users":
		users, err := t.inner.ListUsers(ctx)
		if err != nil {
			return "", err
		}
		if len(users) == 0 {
			return "No users configured.", nil
		}
		var b strings.Builder
		b.WriteString("# Users\n\n")
		for _, u := range users {
			b.WriteString(fmt.Sprintf("- **%s** (`%s`)  \n", u.Label, u.ID))
			if u.Locale != "" {
				b.WriteString(fmt.Sprintf("  locale: %s  \n", u.Locale))
			}
			if len(u.Roles) > 0 {
				b.WriteString(fmt.Sprintf("  roles: %s  \n", strings.Join(u.Roles, ", ")))
			}
		}
		return b.String(), nil
	default:
		return "", fmt.Errorf("identity: unknown action %q (use whoami, set_user, or list_users)", p.Action)
	}
}

func formatUser(u User) string {
	roles := "none"
	if len(u.Roles) > 0 {
		roles = strings.Join(u.Roles, ", ")
	}
	return fmt.Sprintf(
		"# User Profile\n\n- **ID**: `%s`\n- **Name**: %s\n- **Roles**: %s\n- **Locale**: %s\n- **Model pref**: %s\n",
		u.ID, u.Label, roles, u.Locale, u.ModelPref,
	)
}

// ─── recall tool ─────────────────────────────────────────────────────────

type recallTool struct{ inner Recall }

// NewRecallTool wraps a kernel.Recall as a callable tool.Tool.
func NewRecallTool(inner Recall) tool.Tool { return &recallTool{inner: inner} }

func (*recallTool) Name() string   { return "recall" }
func (*recallTool) ReadOnly() bool { return false }
func (*recallTool) Description() string {
	return "Long-term memory — remember facts, search memories, forget. Cross-session persistence."
}
func (*recallTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["save","search","forget"]},"key":{"type":"string"},"value":{"type":"string"},"query":{"type":"string"},"scope":{"type":"string","enum":["session","project","global"]}},"required":["action"]}`)
}

func (t *recallTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action string `json:"action"`
		Key    string `json:"key"`
		Value  string `json:"value"`
		Query  string `json:"query"`
		Scope  string `json:"scope"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("recall: invalid args: %w", err)
	}
	switch p.Action {
	case "save":
		if p.Key == "" {
			p.Key = fmt.Sprintf("fact-%d", time.Now().UnixNano())
		}
		if p.Scope == "" {
			p.Scope = "project"
		}
		err := t.inner.Save(ctx, Fact{Key: p.Key, Value: p.Value, Scope: p.Scope, Source: "agent"})
		if err != nil {
			return "", fmt.Errorf("recall save: %w", err)
		}
		return fmt.Sprintf("Remembered `%s` (%s scope)", p.Key, p.Scope), nil
	case "search":
		if p.Query == "" {
			return "", fmt.Errorf("recall search: query is required")
		}
		limit := 10
		facts, err := t.inner.Search(ctx, p.Query, limit)
		if err != nil {
			return "", fmt.Errorf("recall search: %w", err)
		}
		if len(facts) == 0 {
			return fmt.Sprintf("No memories found matching %q.", p.Query), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "# Memories matching %q\n\n", p.Query)
		for _, f := range facts {
			fmt.Fprintf(&b, "- **%s** (scope: %s, source: %s)\n", f.Key, f.Scope, f.Source)
		}
		return b.String(), nil
	case "forget":
		if p.Query == "" {
			return "", fmt.Errorf("recall forget: query is required")
		}
		n, err := t.inner.Forget(ctx, p.Query)
		if err != nil {
			return "", fmt.Errorf("recall forget: %w", err)
		}
		return fmt.Sprintf("Forgot %d memories matching %q.", n, p.Query), nil
	default:
		return "", fmt.Errorf("recall: unknown action %q (use save, search, or forget)", p.Action)
	}
}

// ─── trust tool ──────────────────────────────────────────────────────────

type trustTool struct{ inner Trust }

// NewTrustTool wraps a kernel.Trust as a callable tool.Tool.
func NewTrustTool(inner Trust) tool.Tool { return &trustTool{inner: inner} }

func (*trustTool) Name() string   { return "trust" }
func (*trustTool) ReadOnly() bool { return true }
func (*trustTool) Description() string {
	return "Proof chain — record evidence, verify claims, export audit trail. Tamper-evident integrity."
}
func (*trustTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["record","verify","export","summary"]},"proposition":{"type":"string"},"evidence":{"type":"string"},"atom_id":{"type":"string"}},"required":["action"]}`)
}

func (t *trustTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action      string `json:"action"`
		Proposition string `json:"proposition"`
		Evidence    string `json:"evidence"`
		AtomID      string `json:"atom_id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("trust: invalid args: %w", err)
	}
	switch p.Action {
	case "record":
		if p.Proposition == "" || p.Evidence == "" {
			return "", fmt.Errorf("trust record: proposition and evidence are required")
		}
		aid := p.AtomID
		if aid == "" {
			aid = fmt.Sprintf("trust-%d", time.Now().UnixNano())
		}
		err := t.inner.Record(ctx, ProofEntry{
			AtomID:      aid,
			Proposition: p.Proposition,
			Evidence:    p.Evidence,
			Timestamp:   time.Now(),
		})
		if err != nil {
			return "", fmt.Errorf("trust record: %w", err)
		}
		return fmt.Sprintf("Recorded proof atom `%s`: %s", aid, p.Proposition), nil
	case "verify":
		if p.AtomID == "" {
			return "", fmt.Errorf("trust verify: atom_id is required")
		}
		err := t.inner.Verify(ctx, p.AtomID)
		if err != nil {
			return fmt.Sprintf("❌ Verification **FAILED** for `%s`: %v", p.AtomID, err), nil
		}
		return fmt.Sprintf("✅ Verified: atom `%s` has an intact evidence chain.", p.AtomID), nil
	case "export":
		entries, err := t.inner.Export(ctx)
		if err != nil {
			return "", fmt.Errorf("trust export: %w", err)
		}
		if len(entries) == 0 {
			return "Proof chain is empty.", nil
		}
		var b strings.Builder
		b.WriteString("# Proof Chain\n\n")
		for _, e := range entries {
			sha := e.SHA256
			if len(sha) > 12 {
				sha = sha[:12]
			}
			fmt.Fprintf(&b, "- `%s`: %s (sha256: `%s`)\n", e.AtomID, e.Proposition, sha)
		}
		fmt.Fprintf(&b, "\n**Total entries:** %d\n", len(entries))
		return b.String(), nil
	case "summary":
		s := t.inner.Summary(ctx)
		return fmt.Sprintf("**Trust Summary**\n- Entries: %d\n- Last action: %s\n- Healthy: %v", s.EntryCount, s.LastAction, s.Healthy), nil
	default:
		return "", fmt.Errorf("trust: unknown action %q (use record, verify, export, or summary)", p.Action)
	}
}

// ─── learn tool ──────────────────────────────────────────────────────────

type learnTool struct{ inner Learn }

// NewLearnTool wraps a kernel.Learn as a callable tool.Tool.
func NewLearnTool(inner Learn) tool.Tool { return &learnTool{inner: inner} }

func (*learnTool) Name() string   { return "learn" }
func (*learnTool) ReadOnly() bool { return false }
func (*learnTool) Description() string {
	return "Self-evolution — extract patterns from tasks, generate skills, validate, publish. Gets better over time."
}
func (*learnTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["extract","generate","validate","publish","stats"]},"skill_name":{"type":"string"},"skill_body":{"type":"string"},"patterns":{"type":"string","description":"JSON array of pattern descriptions, e.g. [\"repeated-tool:bash:3\"]"}},"required":["action"]}`)
}

func (t *learnTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action    string `json:"action"`
		SkillName string `json:"skill_name"`
		SkillBody string `json:"skill_body"`
		Patterns  string `json:"patterns"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("learn: invalid args: %w", err)
	}
	switch p.Action {
	case "extract":
		// The evolution engine auto-extracts patterns from completed turns
		// (via OnTurnComplete). This manual trigger is for ad-hoc extraction.
		return "The evolution engine automatically extracts patterns every 3 turns via OnTurnComplete. To force extraction now, describe the completed task as a patterns string and use 'generate' to create a skill. Example: patterns='[\"repeated-tool:bash:3\"]'", nil
	case "generate":
		if p.SkillName == "" {
			return "", fmt.Errorf("learn generate: skill_name is required")
		}
		var pats []Pattern
		if p.Patterns != "" {
			var descs []string
			if err := json.Unmarshal([]byte(p.Patterns), &descs); err != nil {
				return "", fmt.Errorf("learn generate: invalid patterns JSON: %w", err)
			}
			for i, d := range descs {
				pats = append(pats, Pattern{
					ID:           fmt.Sprintf("p-%d", i),
					Description:  d,
					ToolSequence: []string{d},
					Frequency:    1,
					Confidence:   0.5,
				})
			}
		}
		sk, err := t.inner.Generate(ctx, pats)
		if err != nil {
			return "", fmt.Errorf("learn generate: %w", err)
		}
		return fmt.Sprintf("Generated skill `%s` (v%d)", sk.Name, sk.Version), nil
	case "validate":
		if p.SkillName == "" {
			return "", fmt.Errorf("learn validate: skill_name is required")
		}
		err := t.inner.Validate(ctx, Skill{Name: p.SkillName, Body: p.SkillBody})
		if err != nil {
			return fmt.Sprintf("❌ Validation **FAILED**: %v", err), nil
		}
		return fmt.Sprintf("✅ Skill `%s` validated successfully.", p.SkillName), nil
	case "publish":
		if p.SkillName == "" || p.SkillBody == "" {
			return "", fmt.Errorf("learn publish: skill_name and skill_body are required")
		}
		err := t.inner.Publish(ctx, Skill{Name: p.SkillName, Body: p.SkillBody, Version: 1})
		if err != nil {
			return "", fmt.Errorf("learn publish: %w", err)
		}
		return fmt.Sprintf("Published skill `%s`.", p.SkillName), nil
	case "stats":
		s := t.inner.Stats(ctx)
		return fmt.Sprintf("**Learn Stats**\n- Total skills: %d\n- Success rate: %.0f%%\n- Avg confidence: %.0f%%",
			s.TotalSkills, s.SuccessRate*100, s.AvgConfidence*100), nil
	default:
		return "", fmt.Errorf("learn: unknown action %q (use extract, generate, validate, publish, or stats)", p.Action)
	}
}
