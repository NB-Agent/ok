//go:build !windows

package winhide

import "os/exec"

func hideCmd(cmd *exec.Cmd) {
	// No-op on non-Windows platforms.
}
