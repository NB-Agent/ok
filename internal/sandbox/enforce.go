// Package sandbox provides OS-level sandboxing for agent-executed commands.
//
// enforce.go — adds a sandbox enforcement mode
package sandbox

import (
	"fmt"
	"sync/atomic"
)

// Mode controls how the sandbox handles unavailable isolation.
type Mode int32

const (
	// ModeWarn logs a warning but runs without sandbox (current default).
	ModeWarn Mode = iota
	// ModeEnforce refuses to run if sandbox is unavailable.
	ModeEnforce
)

// ErrSandboxRequired is returned when sandbox enforcement is active but
// the platform cannot provide isolation.
var ErrSandboxRequired = fmt.Errorf("sandbox enforcement: no isolation available — set sandbox.mode = \"warn\" to allow, or install AppContainer (Windows) / Landlock (Linux 5.13+) / sandbox-exec (macOS)")

// currentMode controls sandbox strictness. Set via config.
var currentMode atomic.Int32

func init() { currentMode.Store(int32(ModeWarn)) }

// SetMode changes the sandbox enforcement mode.
func SetMode(m Mode) {
	currentMode.Store(int32(m))
}

// enforceCheck returns nil if the command can proceed, or ErrSandboxRequired
// if enforcement is on and no sandbox is available.
func enforceCheck() error {
	if Mode(currentMode.Load()) == ModeEnforce && !isSandboxAvailable() {
		return ErrSandboxRequired
	}
	return nil
}

// isSandboxAvailable returns true when the current platform has a working
// sandbox mechanism.
func isSandboxAvailable() bool {
	return Available()
}

// Enforce returns the current enforcement mode string.
func Enforce() string {
	switch Mode(currentMode.Load()) {
	case ModeEnforce:
		return "enforce"
	default:
		return "warn"
	}
}
