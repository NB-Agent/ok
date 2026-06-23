package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUndoSingleFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "undo_test.txt")

	saveUndo(f, "", 0)
	os.WriteFile(f, []byte("new content\n"), 0o644)

	ut := undoTool{}
	out, err := ut.Execute(context.Background(), mustMarshal(t, map[string]any{"n": 1}))
	if err != nil {
		t.Fatal("undo failed:", err)
	}
	if !strings.Contains(out, "Restored") {
		t.Errorf("unexpected undo output: %s", out)
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Error("file should have been removed by undo (it was created by the reverted op)")
	}
}

func TestUndoOverwriteFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "existing.txt")
	original := "original\n"
	os.WriteFile(f, []byte(original), 0o644)

	saveUndo(f, original, 0o644)
	os.WriteFile(f, []byte("modified\n"), 0o644)

	ut := undoTool{}
	out, err := ut.Execute(context.Background(), mustMarshal(t, map[string]any{"n": 1}))
	if err != nil {
		t.Fatal("undo failed:", err)
	}
	if !strings.Contains(out, "Restored") {
		t.Errorf("unexpected undo output: %s", out)
	}
	got, _ := os.ReadFile(f)
	if string(got) != original {
		t.Errorf("undo restored = %q, want %q", got, original)
	}
}

func TestUndoEmptyStack(t *testing.T) {
	// Drain the stack quietly via popUndo without touching real files.
	for popUndo() != nil {
	}
	ut := undoTool{}
	_, err := ut.Execute(context.Background(), mustMarshal(t, map[string]any{"n": 1}))
	if err == nil {
		t.Error("expected error on empty undo stack")
	}
}

func TestUndoMultiStep(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "f1.txt")
	f2 := filepath.Join(dir, "f2.txt")

	saveUndo(f1, "", 0)
	os.WriteFile(f1, []byte("f1\n"), 0o644)

	saveUndo(f2, "", 0)
	os.WriteFile(f2, []byte("f2\n"), 0o644)

	ut := undoTool{}
	out, err := ut.Execute(context.Background(), mustMarshal(t, map[string]any{"n": 2}))
	if err != nil {
		t.Fatal("undo failed:", err)
	}
	if !strings.Contains(out, "Restored") {
		t.Errorf("unexpected undo output: %s", out)
	}
	if _, err := os.Stat(f1); !os.IsNotExist(err) {
		t.Error("f1 should have been removed")
	}
	if _, err := os.Stat(f2); !os.IsNotExist(err) {
		t.Error("f2 should have been removed")
	}
}

func TestUndoRespectsMax(t *testing.T) {
	for popUndo() != nil {
	}
	for i := 0; i < maxUndo+10; i++ {
		saveUndo("/fake/path", "content", 0o644)
	}
	if undoSize() > maxUndo {
		t.Errorf("undo stack size %d exceeds max %d", undoSize(), maxUndo)
	}
	for popUndo() != nil {
	}
}

func TestWriteFileDiffOutput(t *testing.T) {
	f := filepath.Join(t.TempDir(), "new.txt")
	w := writeFile{}

	out := runTool(t, w, map[string]any{
		"path":    f,
		"content": "hello world\n",
	})
	if !strings.Contains(out, "# Write") {
		t.Errorf("write file output missing header: %s", out)
	}
	if !strings.Contains(out, "undo") {
		t.Errorf("write file output missing undo hint: %s", out)
	}
}

func TestEditFileDiffOutput(t *testing.T) {
	f := filepath.Join(t.TempDir(), "edit.txt")
	os.WriteFile(f, []byte("original\n"), 0o644)

	e := editFile{}
	out := runTool(t, e, map[string]any{
		"path":       f,
		"old_string": "original",
		"new_string": "modified",
	})
	if !strings.Contains(out, "# Edit") {
		t.Errorf("edit file output missing header: %s", out)
	}
	if !strings.Contains(out, "```diff") {
		t.Errorf("edit file output missing diff: %s", out)
	}
	if !strings.Contains(out, "undo") {
		t.Errorf("edit file output missing undo hint: %s", out)
	}
}

func TestMultiEditDiffOutput(t *testing.T) {
	f := filepath.Join(t.TempDir(), "multi.txt")
	os.WriteFile(f, []byte("x y z\n"), 0o644)

	m := multiEdit{}
	out := runTool(t, m, map[string]any{
		"path": f,
		"edits": []map[string]any{
			{"old_string": "y", "new_string": "Y"},
		},
	})
	if !strings.Contains(out, "# Edit") {
		t.Errorf("multi_edit output missing header: %s", out)
	}
	if !strings.Contains(out, "```diff") {
		t.Errorf("multi_edit output missing diff: %s", out)
	}
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal("marshal:", err)
	}
	return b
}
