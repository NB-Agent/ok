//go:build !windows

package sandbox

import (
	"fmt"
	"os"
)

// WrapProcess is a no-op on non-Windows platforms — OS-level sandbox
// confinement is applied in-process (Seatbelt on macOS, Landlock on Linux)
// before exec, not after start.
func WrapProcess(pid int, spec Spec) error { return nil }

// SandboxExec is a stub — the --sandbox-exec re-exec path is Windows-only.
func SandboxExec(args []string) {
	fmt.Fprintln(os.Stderr, "sandbox-exec: not on this platform")
	os.Exit(1)
}
