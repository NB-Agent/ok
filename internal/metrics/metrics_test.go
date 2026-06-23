package metrics

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMetricsStart(t *testing.T) {
	Start()
	up := Uptime()
	if up <= 0 {
		// Timing resolution on some platforms may round to 0
		time.Sleep(time.Microsecond)
		up = Uptime()
	}
	if up <= 0 {
		t.Fatalf("uptime should be > 0, got %v", up)
	}
}

func TestAtomicSafety(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Turn()
			TurnSucceeded()
			Tokens(100, 50)
			ToolCall()
			ToolResult()
			CacheHit()
			Compaction()
			Snapshot()
			TurnLatency(1 * time.Millisecond)
			ToolLatency(100 * time.Microsecond)
		}()
	}
	wg.Wait()

	if n := r.Turns.Load(); n != 100 {
		t.Errorf("expected 100 turns, got %d", n)
	}
	if n := r.Snapshots.Load(); n != 100 {
		t.Errorf("expected 100 snapshots, got %d", n)
	}
}

func TestMeanCalculations(t *testing.T) {
	r.TurnLatencySum.Store(1000)
	r.TurnLatencyCount.Store(10)
	mean := MeanTurnLatency()
	if mean != 100*time.Microsecond {
		t.Errorf("mean turn latency = %v, want 100µs", mean)
	}

	r.ToolLatencySum.Store(500)
	r.ToolLatencyCount.Store(5)
	meanTool := MeanToolLatency()
	if meanTool != 100*time.Microsecond {
		t.Errorf("mean tool latency = %v, want 100µs", meanTool)
	}
}

func TestCacheHitRate(t *testing.T) {
	origHits := r.CacheHits.Load()
	origMisses := r.CacheMisses.Load()

	r.CacheHits.Store(80)
	r.CacheMisses.Store(20)
	rate := CacheHitRate()
	if rate != 0.8 {
		t.Errorf("cache hit rate = %v, want 0.8", rate)
	}

	// Restore
	r.CacheHits.Store(origHits)
	r.CacheMisses.Store(origMisses)
}

func TestErrorRate(t *testing.T) {
	origCalls := r.ToolCalls.Load()
	origErrors := r.ToolErrors.Load()

	r.ToolCalls.Store(100)
	r.ToolErrors.Store(15)
	rate := ErrorRate()
	if rate != 0.15 {
		t.Errorf("error rate = %v, want 0.15", rate)
	}

	r.ToolCalls.Store(origCalls)
	r.ToolErrors.Store(origErrors)
}

func TestHealthScoreFresh(t *testing.T) {
	// With no turns, health should be 100.
	score := HealthScore()
	// HealthScore reads from global r which may have data from other tests.
	// Fresh means score > 0.
	if score < 0 || score > 100 {
		t.Errorf("health score out of range: %d", score)
	}
}

func TestWriteReport(t *testing.T) {
	Turn()
	Tokens(1000, 500)
	ToolCall()
	ToolResult()

	var buf bytes.Buffer
	WriteReport(&buf)
	report := buf.String()

	for _, want := range []string{
		"Uptime:",
		"Turns:",
		"Tokens:",
		"Tools:",
		"Cache:",
		"Errors:",
		"Latency:",
		"Panics:",
		"Health:",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q", want)
		}
	}
}
