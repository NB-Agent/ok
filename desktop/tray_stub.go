//go:build !windows

package main

import "context"

// startTray is a no-op on non-Windows platforms.
func startTray(_ context.Context, _ *App) {}
