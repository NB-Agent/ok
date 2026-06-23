package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestCommandDirsIncludeConventions verifies command discovery covers the
// cross-tool convention dirs (so .claude/commands etc. migrate in) and that the
// canonical .ok project dir is highest priority (last, since command.Load
// lets a later dir win on a name clash).
func TestCommandDirsIncludeConventions(t *testing.T) {
	dirs := CommandDirs()
	joined := strings.Join(dirs, "\n")
	for _, want := range []string{
		filepath.Join(".claude", "commands"),
		filepath.Join(".agents", "commands"),
		filepath.Join(".agent", "commands"),
		filepath.Join(".ok", "commands"),
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("CommandDirs missing %q\ngot:\n%s", want, joined)
		}
	}
	// The project's .ok/commands must be the highest-priority (last) entry.
	if last := dirs[len(dirs)-1]; last != filepath.Join(".ok", "commands") {
		t.Errorf("project .ok/commands should be highest priority (last), got %q", last)
	}
}
