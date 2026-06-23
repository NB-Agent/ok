package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// errActiveSession is returned when a delete targets the session in use.
var errActiveSession = errors.New("can't delete the session you're in — start a new one first")

// sessions.go holds the desktop-only session-management state that the shared
// kernel doesn't model: custom display titles. A session on disk is just a JSONL
// transcript named by timestamp+model, with no title slot — so the history panel
// stores user-chosen names in a sidecar map (basename → title) next to the .jsonl
// files. The preview (first user message) is the default name; a title overrides
// it. Deleting a session also drops its title entry.

const sessionTitlesFile = ".titles.json"

func sessionTitlesPath(dir string) string { return filepath.Join(dir, sessionTitlesFile) }

// loadSessionTitles reads the basename→title map (missing/corrupt → empty).
func loadSessionTitles(dir string) map[string]string {
	m := map[string]string{}
	b, err := os.ReadFile(sessionTitlesPath(dir))
	if err != nil {
		return m
	}
	if err := json.Unmarshal(b, &m); err != nil {
		fmt.Fprintf(os.Stderr, "desktop: unmarshal session titles: %v\n", err)
		return m
	}
	return m
}

// saveSessionTitles writes the map atomically (temp file + rename).
func saveSessionTitles(dir string, m map[string]string) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".titles.*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, sessionTitlesPath(dir))
}

// setSessionTitle sets (or, with an empty title, clears) a session's custom name.
func setSessionTitle(dir, sessionPath, title string) error {
	m := loadSessionTitles(dir)
	key := filepath.Base(sessionPath)
	if strings.TrimSpace(title) == "" {
		delete(m, key)
	} else {
		m[key] = strings.TrimSpace(title)
	}
	return saveSessionTitles(dir, m)
}

// deleteSessionFile removes a session's .jsonl and its title entry.
func deleteSessionFile(dir, sessionPath string) error {
	if err := os.Remove(sessionPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	m := loadSessionTitles(dir)
	if _, ok := m[filepath.Base(sessionPath)]; ok {
		delete(m, filepath.Base(sessionPath))
		return saveSessionTitles(dir, m)
	}
	return nil
}
