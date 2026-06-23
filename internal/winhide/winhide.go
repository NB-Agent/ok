// Package winhide provides a cross-platform helper to hide console windows
// when spawning external commands. On Windows it sets HideWindow; on other
// platforms it is a no-op.
package winhide

import (
	"context"
	"os/exec"
)

// Cmd configures cmd so that no console window is shown.
// On Windows it sets HideWindow; on other platforms it does nothing.
func Cmd(cmd *exec.Cmd) {
	hideCmd(cmd)
}

// CommandContext is like exec.CommandContext but also hides the console window
// on Windows. Use it as a drop-in replacement for exec.CommandContext when
// spawning external commands that should not flash a terminal window.
func CommandContext(ctx context.Context, name string, arg ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, arg...)
	Cmd(cmd)
	return cmd
}

// Command is like exec.Command but also hides the console window on Windows.
func Command(name string, arg ...string) *exec.Cmd {
	cmd := exec.Command(name, arg...)
	Cmd(cmd)
	return cmd
}
