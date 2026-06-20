// Copyright 2026 OK Authors
// SPDX-License-Identifier: LicenseRef-OK

package winhide

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestCommandNotNil(t *testing.T) {
	cmd := Command("echo", "hello")
	if cmd == nil {
		t.Fatal("Command returned nil")
	}
}

func TestCommandContextNotNil(t *testing.T) {
	cmd := CommandContext(context.Background(), "echo", "hello")
	if cmd == nil {
		t.Fatal("CommandContext returned nil")
	}
}

func TestCommandArgs(t *testing.T) {
	cmd := Command("git", "commit", "-m", "test")
	if cmd == nil {
		t.Fatal("Command returned nil")
	}
	if len(cmd.Args) < 4 {
		t.Fatalf("expected at least 4 args, got %v", cmd.Args)
	}
	// exec.Command resolves the path; check the basename or the first arg
	if filepath.Base(cmd.Args[0]) != "git" && filepath.Base(cmd.Args[0]) != "git.exe" {
		t.Errorf("expected git, got %q", cmd.Args[0])
	}
	if cmd.Args[1] != "commit" {
		t.Errorf("expected args[1]=commit, got %q", cmd.Args[1])
	}
	if cmd.Args[2] != "-m" {
		t.Errorf("expected args[2]=-m, got %q", cmd.Args[2])
	}
	if cmd.Args[3] != "test" {
		t.Errorf("expected args[3]=test, got %q", cmd.Args[3])
	}
}

func TestCommandMultipleArgs(t *testing.T) {
	cmd := Command("go", "build", "-o", "bin/test", "./...")
	if cmd == nil {
		t.Fatal("Command returned nil")
	}
	if len(cmd.Args) < 5 {
		t.Fatalf("expected at least 5 args, got %d: %v", len(cmd.Args), cmd.Args)
	}
	if filepath.Base(cmd.Args[0]) != "go" && filepath.Base(cmd.Args[0]) != "go.exe" {
		t.Errorf("expected go, got %q", cmd.Args[0])
	}
	expectedTail := []string{"build", "-o", "bin/test", "./..."}
	for i, want := range expectedTail {
		if cmd.Args[i+1] != want {
			t.Errorf("args[%d] = %q, want %q", i+1, cmd.Args[i+1], want)
		}
	}
}

func TestCommandContextArgs(t *testing.T) {
	ctx := context.Background()
	cmd := CommandContext(ctx, "npm", "run", "build")
	if cmd == nil {
		t.Fatal("CommandContext returned nil")
	}
	if len(cmd.Args) < 3 {
		t.Fatalf("expected at least 3 args, got %v", cmd.Args)
	}
	if filepath.Base(cmd.Args[0]) != "npm" && filepath.Base(cmd.Args[0]) != "npm.cmd" && filepath.Base(cmd.Args[0]) != "npm.exe" {
		t.Errorf("expected npm, got %q", cmd.Args[0])
	}
	if cmd.Args[1] != "run" || cmd.Args[2] != "build" {
		t.Errorf("expected args [npm run build], got %v", cmd.Args)
	}
}

func TestCommandContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	cmd := CommandContext(ctx, "sleep", "10")
	if cmd == nil {
		t.Fatal("CommandContext returned nil")
	}
	// The context should be wired to the command
	if err := cmd.Start(); err != nil {
		// On some platforms the cmd may fail to start at all with a cancelled context
		return
	}
	defer cmd.Wait()
	// The process should be killed/ended quickly due to context cancellation
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("expected error from cancelled context, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Error("command not cancelled within timeout")
	}
}
