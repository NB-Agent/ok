// Package safeclose provides a helper to safely close resources with error logging.
package safeclose

import (
	"fmt"
	"os"
)

// Log closes c and writes a warning to stderr on error. name identifies the
// resource in the log message. Use for deferred Close() calls where the error
// is not actionable (best-effort cleanup).
func Log(name string, c interface{ Close() error }) {
	if err := c.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "safeclose: close %s: %v\n", name, err)
	}
}

// LogSimple is like Log but for types whose Close method returns nothing.
func LogSimple(name string, c interface{ Close() }) {
	c.Close()
}
