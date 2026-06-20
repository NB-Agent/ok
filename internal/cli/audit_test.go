package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/NB-Agent/ok/internal/core"
)

func TestAuditTableEmpty(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := auditTable(nil)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !bytes.Contains(buf.Bytes(), []byte("no audit records")) {
		t.Error("should print 'no audit records' for empty input")
	}
}

func TestAuditJSONOutput(t *testing.T) {
	records := []core.AuditRecord{
		{Index: 0, Timestamp: time.Now(), Tool: "read_file", Allowed: true, Hash: "abc"},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := auditJSON(records)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	var decoded []core.AuditRecord
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Errorf("should be valid JSON: %v", err)
	}
	if len(decoded) != 1 {
		t.Errorf("got %d records, want 1", len(decoded))
	}
}

func TestAuditVerifyEmpty(t *testing.T) {
	code := auditVerify(nil)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestAuditExportEmpty(t *testing.T) {
	code := auditExport(nil)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestTruncateResult(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"hello", "hello"},
		{"hello\nworld", "hello"},
	}
	for _, tt := range tests {
		got := truncateResult(tt.input)
		if got != tt.want {
			t.Errorf("truncateResult(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTruncateArgs(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{`{"path":"foo.go"}`, "foo.go"},
		{`{"command":"make build"}`, "make build"},
	}
	for _, tt := range tests {
		got := truncateArgs(tt.input)
		if got != tt.want {
			t.Errorf("truncateArgs(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
