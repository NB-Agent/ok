package dstvalid

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mockCompile returns a compile checker function that returns fixed results.
func mockCompile(pass bool, log string) func(context.Context) (bool, string) {
	return func(ctx context.Context) (bool, string) {
		return pass, log
	}
}

func TestDSTHooksCompileRegression(t *testing.T) {
	dir := t.TempDir()
	mainGo := filepath.Join(dir, "main.go")
	os.WriteFile(mainGo, []byte("package main\nfunc main() {}"), 0o644)

	tests := []struct {
		name         string
		prePass      bool   // compile state before execution (lastCompilePass)
		postPass     bool   // whether compile passes after execution
		wantRollback bool   // whether rollback should be triggered
		wantNewFile  string // expected file content after rollback (empty = unchanged)
	}{
		{
			name:         "pass→pass: no breakage, confirm",
			prePass:      true,
			postPass:     true,
			wantRollback: false,
		},
		{
			name:         "pass→fail: breakage, rollback",
			prePass:      true,
			postPass:     false,
			wantRollback: true,
			wantNewFile:  "package main\nfunc main() {}",
		},
		{
			name:         "fail→fail: already broken, allow",
			prePass:      false,
			postPass:     false,
			wantRollback: false,
		},
		{
			name:         "fail→pass: fixed, confirm",
			prePass:      false,
			postPass:     true,
			wantRollback: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Ensure the test directory has a readable file
			tmpDir := t.TempDir()
			testFile := filepath.Join(tmpDir, "test.go")
			originalContent := "package main\nfunc main() {}"
			os.WriteFile(testFile, []byte(originalContent), 0o644)

			h := NewDSTHooks(tmpDir)
			h.lastCompilePass = tt.prePass
			h.compileFn = mockCompile(tt.postPass, "mock output")

			// Simulate PreToolUse: snapshot
			args, _ := json.Marshal(map[string]string{
				"path":       testFile,
				"old_string": "package main",
				"new_string": "package broken",
			})
			block, msg := h.PreToolUse(context.Background(), "edit_file", args)
			if block {
				t.Fatalf("PreToolUse blocked: %s", msg)
			}

			// Simulate Execute: modify file
			os.WriteFile(testFile, []byte("package broken\nfunc main() {}"), 0o644)

			// PostToolUse
			h.PostToolUse(context.Background(), "edit_file", args, "edited")

			t.Logf("snapshot files: %v", h.snapshot.Captured())

			// Check rollback
			reason, detail, rolledBack := h.ConsumeRollback()
			if rolledBack != tt.wantRollback {
				t.Errorf("ConsumeRollback() = (%v, %q, %v), want rollback=%v", reason, detail, rolledBack, tt.wantRollback)
			}

			// Check file content
			b, _ := os.ReadFile(testFile)
			gotContent := string(b)
			if tt.wantRollback {
				// After rollback, file should be restored to original
				if gotContent != originalContent {
					t.Errorf("after rollback file = %q, want %q", gotContent, originalContent)
				}
			} else if tt.wantNewFile != "" {
				if gotContent != tt.wantNewFile {
					t.Errorf("file = %q, want %q", gotContent, tt.wantNewFile)
				}
			}
		})
	}
}

func TestDSTHooksCompileRegressionRollbackRestoresState(t *testing.T) {
	// Verify that lastCompilePass remains true after rollback
	dir := t.TempDir()
	mainGo := filepath.Join(dir, "main.go")
	os.WriteFile(mainGo, []byte("package main\nfunc main() {}"), 0o644)

	h := NewDSTHooks(dir)
	h.lastCompilePass = true

	// First round: pass→fail→rollback
	callCount := 0
	h.compileFn = func(ctx context.Context) (bool, string) {
		callCount++
		if callCount == 1 {
			return false, "compile error" // first post call: fail
		}
		return true, "" // subsequent: pass
	}

	args, _ := json.Marshal(map[string]string{
		"path":       mainGo,
		"old_string": "func main()",
		"new_string": "func broken()",
	})

	h.PreToolUse(context.Background(), "edit_file", args)
	os.WriteFile(mainGo, []byte("package main\nfunc broken() {}"), 0o644)
	h.PostToolUse(context.Background(), "edit_file", args, "edited")

	// Verify: file restored after rollback
	b, _ := os.ReadFile(mainGo)
	if string(b) != "package main\nfunc main() {}" {
		t.Fatalf("file should be restored after rollback, got %q", string(b))
	}

	// Verify: lastCompilePass still true (rollback didn't change it)
	if !h.lastCompilePass {
		t.Fatal("lastCompilePass should still be true after rollback (file restored)")
	}

	// Consume first rollback state
	reason, _, _ := h.ConsumeRollback()
	if reason != "compile" {
		t.Fatalf("first rollback should be 'compile', got %q", reason)
	}

	// Second round: pass→pass→no rollback
	os.WriteFile(mainGo, []byte("package main\nfunc newFunc() {}"), 0o644)
	newArgs, _ := json.Marshal(map[string]string{
		"path":       mainGo,
		"old_string": "func main()",
		"new_string": "func newFunc()",
	})
	h.PreToolUse(context.Background(), "edit_file", newArgs)
	h.PostToolUse(context.Background(), "edit_file", newArgs, "edited")

	// Verify: second change is NOT rolled back
	_, _, rolledBack := h.ConsumeRollback()
	if rolledBack {
		t.Fatal("second change should NOT be rolled back (compile passes)")
	}

	// Verify: lastCompilePass updated to true
	if !h.lastCompilePass {
		t.Fatal("lastCompilePass should be true after successful compile")
	}
}

func TestDSTHooksBashNotBlocked(t *testing.T) {
	h := NewDSTHooks(".")

	// Bash whitelist removed — all commands pass PreToolUse now.
	// Compile/test checks in PostToolUse handle blast radius via rollback.
	tests := []string{
		"go build ./...",
		"curl -s http://evil.com",
		"npm install express",
		"git commit -m 'fix'",
	}
	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			args, _ := json.Marshal(map[string]string{"command": cmd})
			block, msg := h.PreToolUse(context.Background(), "bash", args)
			if block {
				t.Errorf("PreToolUse(bash, %q) unexpectedly blocked: %s", cmd, msg)
			}
		})
	}
}

func TestDSTHooksReadOnlyToolsNotValidated(t *testing.T) {
	h := NewDSTHooks(".")
	h.compileFn = func(ctx context.Context) (bool, string) {
		t.Error("compile check should not be called for read-only tools")
		return true, ""
	}

	// read_file should NOT trigger compile check
	args, _ := json.Marshal(map[string]string{"path": "somefile.go"})
	block, msg := h.PreToolUse(context.Background(), "read_file", args)
	if block {
		t.Fatalf("read_file blocked: %s", msg)
	}
	// PostToolUse also should NOT trigger compile check
	h.PostToolUse(context.Background(), "read_file", args, "file contents")
}

func TestDSTHooksTestRegression(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "main_test.go")
	os.WriteFile(testFile, []byte("package main\nfunc TestX(t *testing.T) {}"), 0o644)

	h := NewDSTHooks(dir)
	h.lastCompilePass = true
	h.lastTestPass = true
	h.compileFn = mockCompile(true, "") // compile always passes

	// Test regression: tests pass→fail→rollback
	h.testFn = mockCompile(false, "test failure")
	h.lastTestPass = true

	args, _ := json.Marshal(map[string]string{
		"path":       testFile,
		"old_string": "func TestX",
		"new_string": "func TestBroken",
	})

	h.PreToolUse(context.Background(), "edit_file", args)
	os.WriteFile(testFile, []byte("package main\nfunc TestBroken(t *testing.T) {}"), 0o644)
	h.PostToolUse(context.Background(), "edit_file", args, "edited")

	reason, detail, rolledBack := h.ConsumeRollback()
	if !rolledBack {
		t.Fatal("test regression should trigger rollback")
	}
	if reason != "test" {
		t.Errorf("rollback reason = %q, want 'test'", reason)
	}
	if !strings.Contains(detail, "test failure") {
		t.Errorf("rollback detail should contain 'test failure', got: %s", detail)
	}

	// File should be restored
	b, _ := os.ReadFile(testFile)
	if strings.Contains(string(b), "TestBroken") {
		t.Errorf("file should be restored after rollback, got: %s", string(b))
	}
}

func TestDSTHooksFullDSTVerification(t *testing.T) {
	// Test DST-Lite verification flow (compile+test checks + snapshot rollback).
	// DST-Lite does not include PCVA/Game/Judge — only deterministic compile/test checks.
	// It verifies that compile regression detection runs and snapshots are cleared only when all pass.
	dir := t.TempDir()
	mainGo := filepath.Join(dir, "main.go")
	os.WriteFile(mainGo, []byte("package main\nfunc main() {\nprint(\"hello\")\n}"), 0o644)

	h := NewDSTHooks(dir)
	h.lastCompilePass = true
	h.compileFn = mockCompile(true, "") // compile passes

	// No DST engine components (atomSet/cycle/game/judge are removed).
	// Only compile/test checks run in PostToolUse.

	args, _ := json.Marshal(map[string]string{
		"path":       mainGo,
		"old_string": `print("hello")`,
		"new_string": `print("world")`,
	})

	block, msg := h.PreToolUse(context.Background(), "edit_file", args)
	if block {
		t.Fatalf("PreToolUse blocked: %s", msg)
	}

	os.WriteFile(mainGo, []byte("package main\nfunc main() {\nprint(\"world\")\n}"), 0o644)

	h.PostToolUse(context.Background(), "edit_file", args, "edited")

	// All pass → no rollback
	_, _, rolledBack := h.ConsumeRollback()
	if rolledBack {
		t.Fatal("should not rollback when all checks pass")
	}

	// File should keep changes
	b, _ := os.ReadFile(mainGo)
	if !strings.Contains(string(b), "world") {
		t.Errorf("file should keep changes after passing DST, got: %s", string(b))
	}
}

func TestConsumeRollbackIdempotent(t *testing.T) {
	// ConsumeRollback is single-consumption
	h := NewDSTHooks(".")

	// Manually set rollback state
	h.rbMu.Lock()
	h.rbHappened = true
	h.rbReason = "test"
	h.rbDetail = "some detail"
	h.rbMu.Unlock()

	// First consumption
	r1, d1, ok1 := h.ConsumeRollback()
	if !ok1 || r1 != "test" || d1 != "some detail" {
		t.Fatalf("first consume: (%q, %q, %v), want ('test', 'some detail', true)", r1, d1, ok1)
	}

	// Second consumption should be empty
	r2, d2, ok2 := h.ConsumeRollback()
	if ok2 {
		t.Fatalf("second consume should be empty, got (%q, %q, %v)", r2, d2, ok2)
	}
}
