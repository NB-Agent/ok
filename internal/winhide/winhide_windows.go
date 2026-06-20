//go:build windows

package winhide

import (
	"os/exec"
	"syscall"
)

const (
	// CREATE_NO_WINDOW prevents the process from creating a console window,
	// even if it's a console-mode executable (e.g. cmd.exe). Combined with
	// HideWindow this eliminates all window flashing.
	createNoWindow = 0x08000000
)

func hideCmd(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
