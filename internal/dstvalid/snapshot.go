package dstvalid

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type snapEntry struct {
	content string      // file content (empty means file didn't exist before write)
	mode    os.FileMode // original permission bits; 0 if file didn't exist
}

// Snapshot captures file content before a write so it can be restored on
// compile/test failure.
type Snapshot struct {
	mu    sync.Mutex
	files map[string]snapEntry // path → snapshot entry
}

// NewSnapshot creates a snapshot manager.
func NewSnapshot() *Snapshot {
	return &Snapshot{files: make(map[string]snapEntry)}
}

// Capture saves the current content of the specified files. Calling Capture
// repeatedly on the same file keeps the first-captured content — only the
// original state matters for rollback.
func (s *Snapshot) Capture(paths ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, path := range paths {
		abs, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("snapshot resolve %s: %w", path, err)
		}
		if _, ok := s.files[abs]; ok {
			continue // already captured, don't overwrite
		}
		info, err := os.Stat(abs)
		if err != nil {
			if os.IsNotExist(err) {
				s.files[abs] = snapEntry{} // empty → rollback deletes
				continue
			}
			return fmt.Errorf("snapshot stat %s: %w", abs, err)
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			return fmt.Errorf("snapshot read %s: %w", abs, err)
		}
		s.files[abs] = snapEntry{content: string(b), mode: info.Mode()}
	}
	return nil
}

// Rollback restores all captured files to their pre-write state.
// files that didn't exist at capture are removed; modified files are restored
// to their original content.
func (s *Snapshot) Rollback() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var errs []error
	for path, entry := range s.files {
		if entry.content == "" && entry.mode == 0 {
			// File didn't exist before write — delete it.
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("rollback remove %s: %w", path, err))
			}
		} else {
			mode := entry.mode
			if mode == 0 {
				mode = 0o644
			}
			if err := os.WriteFile(path, []byte(entry.content), mode); err != nil {
				errs = append(errs, fmt.Errorf("rollback write %s: %w", path, err))
			}
		}
	}

	s.files = make(map[string]snapEntry) // clear all snapshots
	if len(errs) > 0 {
		return fmt.Errorf("rollback errors: %w", errors.Join(errs...))
	}
	return nil
}

// Clear discards all snapshots, signaling that the current operation passed
// verification and no rollback is needed.
func (s *Snapshot) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files = make(map[string]snapEntry)
}

// Commit removes the given paths from the snapshot so they are NOT rolled back.
// Used after a per-tool compile check passes — the tool's changes are good and
// should survive even if a later tool in the same turn triggers a rollback.
func (s *Snapshot) Commit(paths ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, path := range paths {
		abs, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		delete(s.files, abs)
	}
}

// Captured returns the absolute paths of all snapshotted files.
func (s *Snapshot) Captured() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	paths := make([]string, 0, len(s.files))
	for p := range s.files {
		paths = append(paths, p)
	}
	return paths
}

// CaptureDir recursively captures all regular files under dir. Directories and
// symlinks are skipped; .git, node_modules, and .codegraph are excluded.
func (s *Snapshot) CaptureDir(dir string) error {
	var paths []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // correct Walk callback pattern: skip unreadable
		}
		if info.IsDir() {
			// Skip source-control, dependency, and build artifact directories.
			if info.Name() == ".git" || info.Name() == "node_modules" || info.Name() == ".codegraph" {
				return filepath.SkipDir
			}
			if info.Name() == "bin" || info.Name() == "dist" || info.Name() == "build" || info.Name() == "out" {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		// Skip compiled binaries, shared libraries, and other large non-text
		// artifacts that are never rolled back meaningfully.
		ext := filepath.Ext(path)
		switch ext {
		case ".exe", ".dll", ".so", ".dylib", ".o", ".a", ".wasm":
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return fmt.Errorf("snapshot walk %s: %w", dir, err)
	}
	return s.Capture(paths...)
}
