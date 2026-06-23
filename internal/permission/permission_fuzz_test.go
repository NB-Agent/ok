package permission

import (
	"context"
	"encoding/json"
	"testing"
)

// FuzzParseRule tests ParseRule invariants.
func FuzzParseRule(f *testing.F) {
	f.Add("bash")
	f.Add("write_file(*)")
	f.Add("read_file(/etc/*)")
	f.Add("")
	f.Add("()")
	f.Add("bad(")
	f.Add("(naked)")
	f.Add("\x00(null)")
	f.Add("\t bash\n")
	f.Add("edit_file(**/*.go)")

	f.Fuzz(func(t *testing.T, s string) {
		rule, ok := ParseRule(s)
		if ok && rule.Tool == "" {
			t.Errorf("ParseRule(%q): ok=true with empty Tool", s)
		}
	})
}

// FuzzPolicyNewGate tests Policy+Gate construction invariants.
func FuzzPolicyNewGate(f *testing.F) {
	f.Add("allow", "bash")
	f.Add("ask", "write_file(*)")
	f.Add("deny", "read_file(/etc/*)")
	f.Add("allow", "\x00")
	f.Add("ask", "\t\n")

	f.Fuzz(func(t *testing.T, action, rule string) {
		r, ok := ParseRule(rule)
		var p Policy
		switch action {
		case "allow":
			if ok {
				p.Allow = append(p.Allow, r)
			} else {
				p.Allow = nil
			}
		case "ask":
			if ok {
				p.Ask = append(p.Ask, r)
			}
		case "deny":
			if ok {
				p.Deny = append(p.Deny, r)
			}
		}
		g := NewGate(p, nil)
		if g == nil {
			t.Fatal("NewGate returned nil")
		}
		// Check must never panic with empty tool+args
		_, _, _ = g.Check(context.Background(), "test-tool", json.RawMessage(`{}`), true)
	})
}
