package jobs

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/NB-Agent/ok/internal/event"
)

func TestManagerStartDone(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()

	var ran bool
	j := m.Start("bash", "ls", func(ctx context.Context, w io.Writer) (string, error) {
		ran = true
		w.Write([]byte("output"))
		return "result", nil
	})

	if j.ID == "" {
		t.Fatal("job must have an ID")
	}
	if j.Kind != "bash" {
		t.Errorf("Kind = %q, want bash", j.Kind)
	}
	if j.Label != "ls" {
		t.Errorf("Label = %q, want ls", j.Label)
	}

	// Wait for the job goroutine to finish.
	m.Wait(context.Background(), []string{j.ID}, 1)

	if !ran {
		t.Fatal("job function must have run")
	}

	text, _, ok := m.Output(j.ID)
	if !ok {
		t.Fatal("Output should find the job")
	}
	if text != "output" {
		t.Errorf("Output = %q, want output", text)
	}

	res := m.Wait(context.Background(), []string{j.ID}, 1)
	if len(res) != 1 {
		t.Fatalf("Wait returned %d results, want 1", len(res))
	}
	if res[0].Status != Done {
		t.Errorf("Status = %v, want Done", res[0].Status)
	}
	if res[0].Output != "result" {
		t.Errorf("Output = %q, want result", res[0].Output)
	}

	// Draining should return the completion note.
	note := m.DrainCompletedNote()
	if note == "" {
		t.Error("DrainCompletedNote must return a non-empty note")
	}

	// Second drain is empty.
	if m.DrainCompletedNote() != "" {
		t.Error("DrainCompletedNote must be empty after draining")
	}
}

func TestManagerStartFailed(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()

	j := m.Start("task", "sub", func(ctx context.Context, w io.Writer) (string, error) {
		return "", errors.New("boom")
	})

	res := m.Wait(context.Background(), []string{j.ID}, 1)
	if len(res) != 1 {
		t.Fatalf("Wait = %d results", len(res))
	}
	if res[0].Status != Failed {
		t.Errorf("Status = %v, want Failed", res[0].Status)
	}
	if res[0].Output != "boom" {
		t.Errorf("Output = %q, want boom", res[0].Output)
	}
}

func TestManagerKill(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()

	started := make(chan struct{})
	j := m.Start("bash", "sleep", func(ctx context.Context, w io.Writer) (string, error) {
		close(started)
		<-ctx.Done()
		return "killed", nil
	})

	<-started // wait for job to start

	if !m.Kill(j.ID) {
		t.Error("Kill must return true for a running job")
	}

	// Wait for the job goroutine to mark itself killed before checking
	// that a second Kill is correctly rejected.
	res := m.Wait(context.Background(), []string{j.ID}, 1)
	if res[0].Status != Killed {
		t.Errorf("Status = %v, want Killed", res[0].Status)
	}
	if m.Kill(j.ID) {
		t.Error("Kill must return false for an already-finished job")
	}
}

func TestManagerKillUnknown(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()
	if m.Kill("nonexistent") {
		t.Error("Kill must return false for unknown ID")
	}
}

func TestManagerOutputUnknown(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()
	if _, _, ok := m.Output("nonexistent"); ok {
		t.Error("Output must return ok=false for unknown ID")
	}
}

func TestManagerRunning(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()

	block := make(chan struct{})
	started := make(chan struct{})
	m.Start("bash", "blocker", func(ctx context.Context, w io.Writer) (string, error) {
		close(started)
		<-block
		return "", nil
	})
	<-started

	running := m.Running()
	if len(running) != 1 {
		t.Fatalf("Running = %d, want 1", len(running))
	}
	if running[0].Kind != "bash" {
		t.Errorf("Kind = %q", running[0].Kind)
	}

	close(block)
	// Wait for the job to finish via the job manager.
	m.Wait(context.Background(), nil, 1)

	if len(m.Running()) != 0 {
		t.Error("Running must be empty after all jobs finish")
	}
}

func TestManagerWaitTimeout(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()

	j := m.Start("bash", "slow", func(ctx context.Context, w io.Writer) (string, error) {
		<-ctx.Done()
		return "", nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	res := m.Wait(ctx, []string{j.ID}, 0)
	if len(res) != 1 {
		t.Fatalf("Wait timeout must still return %d results, got %d", len(res), 1)
	}
	// Kill the hanging job.
	m.Kill(j.ID)
}

func TestManagerWaitAll(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()

	j1 := m.Start("bash", "a", func(ctx context.Context, w io.Writer) (string, error) {
		return "a-ok", nil
	})
	j2 := m.Start("task", "b", func(ctx context.Context, w io.Writer) (string, error) {
		return "b-ok", nil
	})

	// Wait for specific IDs — works even after they finish.
	res := m.Wait(context.Background(), []string{j1.ID, j2.ID}, 1)
	if len(res) != 2 {
		t.Errorf("Wait([a,b]) = %d results, want 2", len(res))
	}
}

func TestManagerClose(t *testing.T) {
	m := NewManager(event.Discard)

	started := make(chan struct{})
	m.Start("bash", "x", func(ctx context.Context, w io.Writer) (string, error) {
		close(started)
		<-ctx.Done()
		return "done", nil
	})

	<-started
	m.Close() // cancels root context

	// Wait for job to pick up cancellation.
	m.Wait(context.Background(), nil, 1)
	if len(m.Running()) != 0 {
		t.Error("Close must kill all running jobs")
	}
}

func TestManagerConcurrentAccess(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()

	const n = 10
	var wg sync.WaitGroup
	ids := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			j := m.Start("bash", "concurrent", func(ctx context.Context, w io.Writer) (string, error) {
				return "ok", nil
			})
			ids[idx] = j.ID
		}(i)
	}
	wg.Wait()

	// Wait for explicit IDs — works even after they finish.
	all := m.Wait(context.Background(), ids, 1)
	if len(all) != n {
		t.Errorf("Wait got %d results, want %d", len(all), n)
	}
}

func TestDrainCompletedNoteMultipleJobs(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()

	j1 := m.Start("bash", "a", func(ctx context.Context, w io.Writer) (string, error) { return "", nil })
	j2 := m.Start("task", "b", func(ctx context.Context, w io.Writer) (string, error) { return "", errors.New("fail") })

	// Wait to ensure completion is recorded.
	m.Wait(context.Background(), []string{j1.ID, j2.ID}, 1)

	note := m.DrainCompletedNote()
	if note == "" {
		t.Fatal("drain must return a note after multiple completions")
	}
	if m.DrainCompletedNote() != "" {
		t.Error("drain must return empty on second call")
	}
}

func TestManagerStartPanic(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()

	j := m.Start("bash", "panic", func(ctx context.Context, w io.Writer) (string, error) {
		panic("oops")
	})

	res := m.Wait(context.Background(), []string{j.ID}, 1)
	if len(res) != 1 {
		t.Fatal("Wait must return result for panicked job")
	}
	if res[0].Status != Failed {
		t.Errorf("Status = %v, want Failed", res[0].Status)
	}
}

func TestWithManagerFromContext(t *testing.T) {
	m := NewManager(event.Discard)
	ctx := WithManager(context.Background(), m)

	got, ok := FromContext(ctx)
	if !ok {
		t.Fatal("FromContext must find the manager")
	}
	if got != m {
		t.Error("FromContext must return the same manager")
	}
}

func TestFromContextNoManager(t *testing.T) {
	_, ok := FromContext(context.Background())
	if ok {
		t.Error("FromContext must return false with no manager")
	}
}
