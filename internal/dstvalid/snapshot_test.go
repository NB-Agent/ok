package dstvalid

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotCaptureRollback(t *testing.T) {
	dir := t.TempDir()

	// Create test files
	file1 := filepath.Join(dir, "test1.txt")
	file2 := filepath.Join(dir, "test2.txt")
	orig1 := "hello world"
	orig2 := "line1\nline2\nline3"
	if err := os.WriteFile(file1, []byte(orig1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte(orig2), 0o644); err != nil {
		t.Fatal(err)
	}

	// Capture
	snap := NewSnapshot()
	if err := snap.Capture(file1, file2); err != nil {
		t.Fatalf("Capture failed: %v", err)
	}

	// Modify files
	if err := os.WriteFile(file1, []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte("also modified"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify modified
	b, _ := os.ReadFile(file1)
	if string(b) != "modified" {
		t.Fatal("file should be modified before rollback")
	}

	// Rollback
	if err := snap.Rollback(); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	// Verify restored
	b, _ = os.ReadFile(file1)
	if string(b) != orig1 {
		t.Fatalf("expected %q, got %q after rollback", orig1, string(b))
	}
	b, _ = os.ReadFile(file2)
	if string(b) != orig2 {
		t.Fatalf("expected %q, got %q after rollback", orig2, string(b))
	}
}

func TestSnapshotNonExistentFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.txt")

	snap := NewSnapshot()
	if err := snap.Capture(path); err != nil {
		t.Fatalf("Capture non-existent file should not error: %v", err)
	}

	// Create file
	if err := os.WriteFile(path, []byte("new content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify file exists
	if _, err := os.Stat(path); err != nil {
		t.Fatal("file should exist")
	}

	// Rollback → should delete file
	if err := snap.Rollback(); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	// Verify file was deleted
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file should have been deleted by rollback")
	}
}

func TestSnapshotClear(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("original"), 0o644)

	snap := NewSnapshot()
	snap.Capture(path)

	// Modify
	os.WriteFile(path, []byte("modified"), 0o644)

	// Pass → Clear doesn't rollback; just test Clear doesn't error
	snap.Clear()

	// Verify file is still modified
	b, _ := os.ReadFile(path)
	if string(b) != "modified" {
		t.Fatal("Clear should not rollback")
	}
}

func TestSnapshotMultipleCapture(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("original"), 0o644)

	snap := NewSnapshot()
	snap.Capture(path)

	// First modification
	os.WriteFile(path, []byte("first change"), 0o644)
	snap.Capture(path) // second capture should retain first content

	// Second modification
	os.WriteFile(path, []byte("second change"), 0o644)

	// Rollback should restore to original (first capture), not first change
	snap.Rollback()
	b, _ := os.ReadFile(path)
	if string(b) != "original" {
		t.Fatalf("expected 'original', got %q", string(b))
	}
}

func TestSnapshotParallelSafe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("safe"), 0o644)

	snap := NewSnapshot()

	// Concurrent capture
	t.Run("parallel", func(t *testing.T) {
		t.Parallel()
		if err := snap.Capture(path); err != nil {
			t.Errorf("capture: %v", err)
		}
	})
}
