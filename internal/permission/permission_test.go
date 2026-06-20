package permission

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestParseRule(t *testing.T) {
	cases := []struct {
		in       string
		wantTool string
		wantSubj string
		wantOK   bool
	}{
		{"bash", "bash", "", true},
		{"bash(rm -rf*)", "bash", "rm -rf*", true},
		{"  read_file  ", "read_file", "", true},
		{"bash( go test ./... )", "bash", " go test ./... ", true},
		{"bash(echo (hi))", "bash", "echo (hi)", true},
		{"", "", "", false},
		{"(noTool)", "", "", false},
	}
	for _, c := range cases {
		r, ok := ParseRule(c.in)
		if ok != c.wantOK {
			t.Errorf("ParseRule(%q) ok = %v, want %v", c.in, ok, c.wantOK)
			continue
		}
		if ok && (r.Tool != c.wantTool || r.Subject != c.wantSubj) {
			t.Errorf("ParseRule(%q) = {%q,%q}, want {%q,%q}", c.in, r.Tool, r.Subject, c.wantTool, c.wantSubj)
		}
	}
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"rm -rf*", "rm -rf /tmp/x", true},
		{"go test*", "go test ./...", true},
		{"go test*", "go build", false},
		{"*", "anything at all", true},
		{"git ?ush", "git push", true},
		{"git ?ush", "git rush", true},
		{"git ?ush", "git pull", false},
		{"exact", "exact", true},
		{"exact", "exactly", false},
		{"a*c", "abbbc", true},
		{"a*c", "abbbd", false},
		{"*.go", "main.go", true},
		{"*.go", "main.rs", false},
	}
	for _, c := range cases {
		if got := matchGlob(c.pattern, c.name); got != c.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestSubject(t *testing.T) {
	cases := []struct {
		args string
		want string
	}{
		{`{"command":"go test ./..."}`, "go test ./..."},
		{`{"file_path":"/a/b.go"}`, "/a/b.go"},
		{`{"path":"/c/d"}`, "/c/d"},
		{`{"pattern":"TODO","path":"/x"}`, "/x"},
		{`{"other":"x"}`, ""},
		{`{}`, ""},
		{``, ""},
		{`not json`, ""},
	}
	for _, c := range cases {
		if got := Subject(json.RawMessage(c.args)); got != c.want {
			t.Errorf("Subject(%q) = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestPolicyDenied(t *testing.T) {
	p := NewDenyPolicy([]string{"bash(rm -rf*)", "bash(curl*)"})

	cases := []struct {
		name     string
		tool     string
		args     string
		wantDeny bool
	}{
		{"blocked command", "bash", `{"command":"rm -rf /"}`, true},
		{"other command allowed", "bash", `{"command":"go test"}`, false},
		{"unlisted tool", "write_file", `{"path":"/a"}`, false},
		{"unlisted reader", "grep", `{"pattern":"x"}`, false},
		{"no subject", "bash", `{}`, false},
	}
	for _, c := range cases {
		_, denied := p.Denied(c.tool, json.RawMessage(c.args))
		if denied != c.wantDeny {
			t.Errorf("%s: Denied(%q, %s) = %v, want %v", c.name, c.tool, c.args, denied, c.wantDeny)
		}
	}
}

func TestPolicyDenySubjectRuleNeedsSubject(t *testing.T) {
	p := NewDenyPolicy([]string{"bash(rm*)"})
	_, denied := p.Denied("bash", json.RawMessage(`{}`))
	if denied {
		t.Error("subject rule matched against empty args")
	}
}

type stubApprover struct {
	allow    bool
	remember bool
	err      error
	calls    int
}

func (s *stubApprover) Approve(ctx context.Context, tool, subject string, args json.RawMessage) (bool, bool, error) {
	s.calls++
	return s.allow, s.remember, s.err
}

func TestGateDenyBlocks(t *testing.T) {
	g := NewGate(NewDenyPolicy([]string{"bash(rm*)"}), nil)
	_, reason, err := g.Check(context.Background(), "bash", json.RawMessage(`{"command":"rm file"}`), false)
	if err != nil || reason == "" {
		t.Errorf("denied call = (_,%q,%v), want blocked with reason", reason, err)
	}
}

func TestGateNoApproverAllowsWriters(t *testing.T) {
	g := NewGate(NewDenyPolicy(nil), nil)
	allow, _, err := g.Check(context.Background(), "bash", json.RawMessage(`{"command":"git commit"}`), false)
	if err != nil || !allow {
		t.Errorf("headless allow = (%v,%v), want allow", allow, err)
	}
}

func TestGateReadersAlwaysAllow(t *testing.T) {
	g := NewGate(NewDenyPolicy([]string{"grep(secret*)"}), nil)
	_, reason, err := g.Check(context.Background(), "grep", json.RawMessage(`{"pattern":"secret.txt"}`), true)
	if err != nil || reason == "" {
		t.Errorf("denied reader call = (_,%q,%v), want blocked with reason", reason, err)
	}
	// Reader that doesn't match a deny rule should still pass.
	allow, _, err := g.Check(context.Background(), "grep", json.RawMessage(`{"pattern":"public.txt"}`), true)
	if err != nil || !allow {
		t.Errorf("non-denied reader = (%v,%v), want allow", allow, err)
	}
}

func TestGateInteractiveApproval(t *testing.T) {
	var remembered string
	ap := &stubApprover{allow: true, remember: true}
	g := NewGate(NewDenyPolicy(nil), ap)
	g.OnRemember = func(rule string) { remembered = rule }

	allow, _, err := g.Check(context.Background(), "bash", json.RawMessage(`{"command":"go build"}`), false)
	if err != nil || !allow {
		t.Fatalf("approved call = (%v,%v), want allow", allow, err)
	}
	if ap.calls != 1 {
		t.Errorf("approver calls = %d, want 1", ap.calls)
	}
	if remembered != "bash(go build)" {
		t.Errorf("remembered rule = %q, want %q", remembered, "bash(go build)")
	}
}

func TestGateDecline(t *testing.T) {
	ap := &stubApprover{allow: false}
	g := NewGate(NewDenyPolicy(nil), ap)
	allow, reason, _ := g.Check(context.Background(), "write_file", json.RawMessage(`{"path":"/a"}`), false)
	if allow || reason == "" {
		t.Errorf("declined call = (%v,%q), want blocked with reason", allow, reason)
	}
}

func TestGateApproverError(t *testing.T) {
	ap := &stubApprover{err: errors.New("ctx canceled")}
	g := NewGate(NewDenyPolicy(nil), ap)
	if _, _, err := g.Check(context.Background(), "bash", json.RawMessage(`{"command":"x"}`), false); err == nil {
		t.Error("approver error should propagate")
	}
}

func TestGateReadersSkipApprover(t *testing.T) {
	ap := &stubApprover{allow: false}
	g := NewGate(NewDenyPolicy(nil), ap)
	allow, _, _ := g.Check(context.Background(), "read_file", json.RawMessage(`{"path":"/a"}`), true)
	if !allow || ap.calls != 0 {
		t.Errorf("reader reached approver: allow=%v calls=%d", allow, ap.calls)
	}
}
