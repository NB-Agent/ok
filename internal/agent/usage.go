package agent

import (
	"sync"

	"github.com/NB-Agent/ok/internal/provider"
)

// UsageTracker records per-turn and session-aggregate token telemetry. It was
// extracted from Agent to keep the Agent struct focused on the run loop rather
// than owning every concern directly. It is safe for concurrent use: a
// status-bar goroutine may read LastUsage / SessionCache while the run-loop
// goroutine calls Record.
type UsageTracker struct {
	mu            sync.Mutex
	lastUsage     *provider.Usage
	sessCacheHit  int
	sessCacheMiss int
}

// NewUsageTracker returns an initialized UsageTracker.
func NewUsageTracker() *UsageTracker { return &UsageTracker{} }

// Record stores the latest per-turn usage and accumulates session cache tokens.
// Call from the run-loop goroutine on each ChunkUsage event.
func (t *UsageTracker) Record(u provider.Usage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Store a copy so the caller can reuse the chunk struct.
	cp := u
	t.lastUsage = &cp
	t.sessCacheHit += u.CacheHitTokens
	t.sessCacheMiss += u.CacheMissTokens
}

// LastUsage returns the most recent per-turn telemetry, or nil when no turn has
// completed.
func (t *UsageTracker) LastUsage() *provider.Usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastUsage
}

// SessionCache returns the cumulative cache hit/miss prompt tokens across every
// API call this session, so frontends can show the aggregate hit-rate.
func (t *UsageTracker) SessionCache() (hit, miss int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sessCacheHit, t.sessCacheMiss
}

// Snapshot returns a copy of the current state for use in events.
func (t *UsageTracker) Snapshot() (last *provider.Usage, hit, miss int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastUsage, t.sessCacheHit, t.sessCacheMiss
}
