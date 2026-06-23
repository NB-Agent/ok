// Package log is a thin wrapper around log/slog that writes structured logs
// to stderr. Use Warn/Error for actionable issues; Info for startup banners
// and status lines. Also provides Close helper for safe resource cleanup.
package log

import (
	"fmt"
	"log/slog"
	"os"
)

var logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

// Info logs at Info level.
func Info(msg string, args ...any) { logger.Info(msg, args...) }

// Warn logs at Warn level.
func Warn(msg string, args ...any) { logger.Warn(msg, args...) }

// Error logs at Error level.
func Error(msg string, args ...any) { logger.Error(msg, args...) }

// Debug logs at Debug level.
func Debug(msg string, args ...any) { logger.Debug(msg, args...) }

// Close closes c and writes a warning to stderr on error. name identifies the
// resource in the log message. Use for deferred Close() calls where the error
// is not actionable (best-effort cleanup).
func Close(name string, c interface{ Close() error }) {
	if c == nil {
		return
	}
	if err := c.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "close %s: %v\n", name, err)
	}
}

// CloseSimple is like Close but for types whose Close method returns nothing.
func CloseSimple(name string, c interface{ Close() }) {
	if c == nil {
		return
	}
	c.Close()
}
