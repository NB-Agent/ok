package dstvalid

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractPathFromArgs(t *testing.T) {
	tests := []struct {
		name string
		args string
		want string
	}{
		{"edit_file", `{"path":"main.go","old_string":"a","new_string":"b"}`, "main.go"},
		{"write_file", `{"path":"/tmp/test.go","content":"package main"}`, "/tmp/test.go"},
		{"multi_edit", `{"path":"main.go","edits":[{"old":"a","new":"b"}]}`, "main.go"},
		{"no path", `{"old_string":"a","new_string":"b"}`, ""},
		{"invalid json", `not json`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPathFromArgs(json.RawMessage(tt.args))
			if got != tt.want {
				t.Errorf("extractPathFromArgs() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPreToolUse_WriterSnapshots(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	os.WriteFile(path, []byte("original"), 0o644)

	h := NewDSTHooks(dir)
	args, _ := json.Marshal(map[string]string{"path": path, "old_string": "original", "new_string": "modified"})

	// PreToolUse should snapshot the file.
	block, msg := h.PreToolUse(context.Background(), "edit_file", args)
	if block {
		t.Fatalf("unexpected block: %s", msg)
	}

	// Verify snapshot captured.
	if len(h.snapshot.Captured()) != 1 {
		t.Fatalf("expected 1 captured file, got %d", len(h.snapshot.Captured()))
	}

	// Modify the file.
	os.WriteFile(path, []byte("modified"), 0o644)

	// PostToolUse with no compile command should confirm (no rollback).
	h.PostToolUse(context.Background(), "edit_file", args, "edited")

	// Verify file kept the modification.
	b, _ := os.ReadFile(path)
	if string(b) != "modified" {
		t.Fatalf("expected 'modified', got %q", string(b))
	}

	// Verify snapshot cleared.
	if len(h.snapshot.Captured()) != 0 {
		t.Fatal("snapshot should be cleared after successful PostToolUse")
	}
}

func TestSplitCommand(t *testing.T) {
	tests := []struct {
		input    string
		want     []string
		shellCmd bool // true = wraps in shell (3-element result)
	}{
		{"go build ./...", []string{"go", "build", "./..."}, false},
		{"echo hello world", []string{"echo", "hello", "world"}, false},
		{"cargo check && cargo test", nil, true},
		{"npm run build | tee log.txt", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := splitCommand(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if tt.shellCmd {
				// Shell-wrapped: [shell, flag, command]
				if len(got) != 3 {
					t.Fatalf("shell-wrapped command expected 3 elements, got %v", got)
				}
				if got[2] != tt.input {
					t.Fatalf("shell-wrapped command body mismatch: got %q, want %q", got[2], tt.input)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}
