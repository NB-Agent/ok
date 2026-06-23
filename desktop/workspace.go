package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NB-Agent/ok/internal/config"
)

// The desktop is a GUI app: launched from Finder or `open`, it starts with the
// working directory set to "/" (read-only), so anything cwd-relative — config,
// .env writes, memory/skill discovery — fails or lands nowhere useful. We keep a
// real working folder instead: remember the last one the user picked and chdir
// into it at startup, falling back to the home directory when there's none and
// cwd isn't writable.

// workspaceStatePath is where the last working folder is remembered (under the
// user config dir, shared with the rest of OK's state).
func workspaceStatePath() string {
	dir := config.MemoryUserDir() // …/ok
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "desktop-workspace")
}

// saveWorkspace records dir as the last working folder.
func saveWorkspace(dir string) {
	p := workspaceStatePath()
	if p == "" || dir == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(p, []byte(dir), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "desktop: save workspace state: %v\n", err)
	}
}

// loadWorkspace returns the remembered working folder, or "" if none.
func loadWorkspace() string {
	p := workspaceStatePath()
	if p == "" {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ensureWorkspace establishes a writable working directory at startup: the
// remembered folder if it's still a directory, else the home directory when the
// current cwd isn't writable (the Finder/`open` "/" case). A writable cwd with no
// remembered folder (e.g. `wails dev` in the repo) is left untouched.
func ensureWorkspace() {
	if ws := loadWorkspace(); ws != "" {
		if info, err := os.Stat(ws); err == nil && info.IsDir() && os.Chdir(ws) == nil {
			return
		}
	}
	if cwdWritable() {
		return
	}
	if home, err := os.UserHomeDir(); err == nil {
		if err := os.Chdir(home); err != nil {
			fmt.Fprintf(os.Stderr, "desktop: chdir to home: %v\n", err)
		}
	}
}

// cwdWritable reports whether the current directory accepts a file write — the
// reliable test for the read-only "/" a GUI launch lands in.
func cwdWritable() bool {
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	f, err := os.CreateTemp(cwd, ".ok-wtest-*")
	if err != nil {
		return false
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "desktop: close wtest file: %v\n", err)
	}
	_ = os.Remove(name)
	return true
}
