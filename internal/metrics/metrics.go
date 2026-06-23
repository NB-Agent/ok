// Package metrics is a zero-dependency, lock-free performance counter registry
// embedded in the agent. It tracks turns, tokens, tool calls, cache hits,
// errors, and latencies using atomic counters so it's safe to read from
// any goroutine without blocking the hot path.
//
// The counters are exposed via `ok doctor --metrics` and can be introspected
// by the agent itself at runtime (self-aware infrastructure).
package metrics

import (
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// Registry is a singleton; all counters are global for simplicity.
var r Registry

// Registry holds all atomic performance counters.
type Registry struct {
	Turns          atomic.Int64 // total turns submitted
	TurnsSucceeded atomic.Int64 // turns that completed without error
	TurnsCancelled atomic.Int64 // turns cancelled by user
	TokensPrompt   atomic.Int64 // cumulative prompt tokens
	TokensOutput   atomic.Int64 // cumulative output tokens
	ToolCalls      atomic.Int64 // total tool dispatch attempts
	ToolResults    atomic.Int64 // tool calls that returned successfully
	ToolErrors     atomic.Int64 // tool calls that returned errors
	ToolDenied     atomic.Int64 // tool calls denied by permission gate
	CacheHits      atomic.Int64 // estimated prefix-cache hits (turns where tokens were >0 and last turn was compacted)
	CacheMisses    atomic.Int64 // turns where compaction ran (cache was evicted)
	ErrorsNonRetry atomic.Int64 // permanent errors (auth, quota)
	ErrorsRetry    atomic.Int64 // transient errors that were retried
	Compactions    atomic.Int64 // session compactions performed
	Snapshots      atomic.Int64 // session snapshots written
	Panics         atomic.Int64 // goroutine panics recovered
	Uptime         atomic.Int64 // agent start time unix nano

	// Latency histograms are approximated via sum+count for mean calculation.
	TurnLatencySum   atomic.Int64 // cumulative turn duration in microseconds
	TurnLatencyCount atomic.Int64 // number of turns with measured latency
	ToolLatencySum   atomic.Int64 // cumulative tool duration in microseconds
	ToolLatencyCount atomic.Int64 // number of tools with measured latency
}

// Start marks the agent as alive.
func Start() {
	r.Uptime.Store(time.Now().UnixNano())
}

// Uptime returns the duration since Start.
func Uptime() time.Duration {
	start := r.Uptime.Load()
	if start == 0 {
		return 0
	}
	return time.Since(time.Unix(0, start))
}

// Turn records a turn submission.
func Turn() { r.Turns.Add(1) }

// TurnSucceeded records a successful turn completion.
func TurnSucceeded() { r.TurnsSucceeded.Add(1) }

// TurnCancelled records a user-cancelled turn.
func TurnCancelled() { r.TurnsCancelled.Add(1) }

// Tokens records token usage for a turn.
func Tokens(prompt, output int64) {
	r.TokensPrompt.Add(prompt)
	r.TokensOutput.Add(output)
}

// ToolCall records a tool dispatch.
func ToolCall() { r.ToolCalls.Add(1) }

// ToolResult records a successful tool return.
func ToolResult() { r.ToolResults.Add(1) }

// ToolError records a tool that returned an error.
func ToolError() { r.ToolErrors.Add(1) }

// ToolDenied records a tool denied by the permission gate.
func ToolDenied() { r.ToolDenied.Add(1) }

// CacheHit records a cache hit (no compaction needed).
func CacheHit() { r.CacheHits.Add(1) }

// CacheMiss records a compaction (cache eviction).
func CacheMiss() { r.CacheMisses.Add(1) }

// ErrorNonRetry records a permanent error.
func ErrorNonRetry() { r.ErrorsNonRetry.Add(1) }

// ErrorRetry records a transient, retried error.
func ErrorRetry() { r.ErrorsRetry.Add(1) }

// Compaction records a session compaction.
func Compaction() { r.Compactions.Add(1) }

// Snapshot records a session snapshot write.
func Snapshot() { r.Snapshots.Add(1) }

// Panic records a recovered goroutine panic.
func Panic() { r.Panics.Add(1) }

// TurnLatency records the duration of a completed turn in microseconds.
func TurnLatency(d time.Duration) {
	r.TurnLatencySum.Add(d.Microseconds())
	r.TurnLatencyCount.Add(1)
}

// ToolLatency records the duration of a tool execution in microseconds.
func ToolLatency(d time.Duration) {
	r.ToolLatencySum.Add(d.Microseconds())
	r.ToolLatencyCount.Add(1)
}

// MeanTurnLatency returns the average turn duration, or 0.
func MeanTurnLatency() time.Duration {
	n := r.TurnLatencyCount.Load()
	if n == 0 {
		return 0
	}
	return time.Duration(r.TurnLatencySum.Load()/n) * time.Microsecond
}

// MeanToolLatency returns the average tool execution duration, or 0.
func MeanToolLatency() time.Duration {
	n := r.ToolLatencyCount.Load()
	if n == 0 {
		return 0
	}
	return time.Duration(r.ToolLatencySum.Load()/n) * time.Microsecond
}

// CacheHitRate returns the cache hit ratio (0..1), or 0 if no data.
func CacheHitRate() float64 {
	hits := r.CacheHits.Load()
	misses := r.CacheMisses.Load()
	total := hits + misses
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total)
}

// ErrorRate returns error ratio per tool call.
func ErrorRate() float64 {
	calls := r.ToolCalls.Load()
	if calls == 0 {
		return 0
	}
	return float64(r.ToolErrors.Load()) / float64(calls)
}

// HealthScore returns 0-100 reflecting overall agent health.
func HealthScore() int {
	if r.Turns.Load() == 0 {
		return 100 // no data = healthy
	}
	score := 100
	// Cache misses hurt: each miss costs ~5-10x in token cost.
	if rate := CacheHitRate(); rate < 0.5 {
		score -= 20
	} else if rate < 0.8 {
		score -= 10
	}
	// High error rates are bad.
	if rate := ErrorRate(); rate > 0.1 {
		score -= 30
	} else if rate > 0.05 {
		score -= 15
	}
	// Panics are never acceptable.
	if p := r.Panics.Load(); p > 0 {
		score -= int(p * 10)
	}
	// Too many cancellations may indicate UX issues.
	cancels := r.TurnsCancelled.Load()
	total := r.Turns.Load()
	if total > 5 && float64(cancels)/float64(total) > 0.3 {
		score -= 10
	}
	if score < 0 {
		score = 0
	}
	return score
}

// WriteReport writes a human-readable metrics report to w.
func WriteReport(w io.Writer) {
	up := Uptime()
	fmt.Fprintf(w, "  Uptime: %s\n", up.Round(time.Second))
	fmt.Fprintf(w, "  Turns:  %d total / %d succeeded / %d cancelled\n",
		r.Turns.Load(), r.TurnsSucceeded.Load(), r.TurnsCancelled.Load())
	fmt.Fprintf(w, "  Tokens: %d prompt / %d output\n",
		r.TokensPrompt.Load(), r.TokensOutput.Load())
	fmt.Fprintf(w, "  Tools:  %d calls / %d ok / %d errors / %d denied\n",
		r.ToolCalls.Load(), r.ToolResults.Load(), r.ToolErrors.Load(), r.ToolDenied.Load())
	fmt.Fprintf(w, "  Cache:  %.1f%% hit rate (%d hits / %d compactions)\n",
		CacheHitRate()*100, r.CacheHits.Load(), r.Compactions.Load())
	fmt.Fprintf(w, "  Errors: %d permanent / %d retried\n",
		r.ErrorsNonRetry.Load(), r.ErrorsRetry.Load())
	fmt.Fprintf(w, "  Latency: %v mean turn / %v mean tool\n",
		MeanTurnLatency().Round(time.Microsecond), MeanToolLatency().Round(time.Microsecond))
	fmt.Fprintf(w, "  Panics:  %d recovered\n", r.Panics.Load())
	fmt.Fprintf(w, "  Health:  %d/100\n", HealthScore())
}
