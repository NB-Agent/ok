package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/NB-Agent/ok/internal/tool"
)

func init() { tool.RegisterBuiltin(undoTool{}) }

// --- Undo stack (global, thread-safe) ---

type undoEntry struct {
	Path    string
	Content string // the file content before the mutation
	Mode    os.FileMode
}

var (
	undoStack   []undoEntry
	undoStackMu sync.Mutex
	maxUndo     = 50 // keep at most the last 50 mutations
)

// saveUndo snapshots the file before a mutation so it can be rolled back.
// If the file didn't exist (create), content is "" and mode is 0.
func saveUndo(path, oldContent string, mode os.FileMode) {
	undoStackMu.Lock()
	defer undoStackMu.Unlock()
	undoStack = append(undoStack, undoEntry{Path: path, Content: oldContent, Mode: mode})
	if len(undoStack) > maxUndo {
		n := len(undoStack) - maxUndo
		undoStack = append([]undoEntry(nil), undoStack[n:]...)
	}
}

// popUndo returns the most recent undo entry, or nil if the stack is empty.
func popUndo() *undoEntry {
	undoStackMu.Lock()
	defer undoStackMu.Unlock()
	if len(undoStack) == 0 {
		return nil
	}
	e := undoStack[len(undoStack)-1]
	undoStack = undoStack[:len(undoStack)-1]
	return &e
}

// undoSize returns the current undo stack depth.
func undoSize() int {
	undoStackMu.Lock()
	defer undoStackMu.Unlock()
	return len(undoStack)
}

// --- undo tool ---

type undoTool struct{}

func (undoTool) Name() string { return "undo" }
func (undoTool) Description() string {
	return "Undo the last file mutation(s) — restore files to their previous state. Use after an edit_file, write_file, or multi_edit that produced unwanted changes."
}
func (undoTool) ReadOnly() bool { return false }
func (undoTool) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"n":{"type":"integer"}},"type":"object"}`)
}

func (undoTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		N int `json:"n"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("undo: invalid args: %w", err)
	}
	if p.N <= 0 {
		p.N = 1
	}
	if p.N > 20 {
		return "", fmt.Errorf("undo: cannot undo more than 20 steps at once")
	}

	var undone []string
	for i := 0; i < p.N; i++ {
		e := popUndo()
		if e == nil {
			if len(undone) == 0 {
				return "", fmt.Errorf("undo: nothing to undo")
			}
			break
		}
		if e.Content == "" && e.Mode == 0 {
			if err := os.Remove(e.Path); err != nil && !os.IsNotExist(err) {
				saveUndo(e.Path, e.Content, e.Mode)
				return "", fmt.Errorf("undo: cannot remove %s: %w", e.Path, err)
			}
		} else {
			if err := os.WriteFile(e.Path, []byte(e.Content), e.Mode); err != nil {
				saveUndo(e.Path, e.Content, e.Mode)
				return "", fmt.Errorf("undo: cannot restore %s: %w", e.Path, err)
			}
		}
		undone = append(undone, e.Path)
	}

	var b strings.Builder
	b.WriteString("# Undo\n\n")
	for _, u := range undone {
		b.WriteString(fmt.Sprintf("- Restored `%s` (stack depth: %d)\n", u, undoSize()))
	}
	if undoSize() == 0 {
		b.WriteString("\nUndo stack is now empty.\n")
	}
	return b.String(), nil
}
