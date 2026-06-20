//go:build !darwin && !linux && !windows

package sandbox

import (
	"fmt"
	"os"
	"runtime"
)

func Command(spec Spec, shell, command string) ([]string, bool) {
	if err := enforceCheck(); err != nil {
		return []string{"", command}, false
	}
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", command}, false
	}
	return []string{shell, "-c", command}, false
}

func Available() bool { return false }

// LandlockExec is a stub — the --landlock-exec re-exec path is Linux-only.
func LandlockExec(args []string) {
	fmt.Fprintln(os.Stderr, "landlock-exec: not on this platform")
	os.Exit(1)
}
