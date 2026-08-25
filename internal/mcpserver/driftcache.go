package mcpserver

import (
	"context"
	"sync"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// driftTTL bounds how often a status call re-runs the O(N) coverage audit.
// am_status is the call the protocol mandates first for every session, so the
// full two-sided audit (PointsByIDs sweeps over both halves) must not run per
// status call; one audit per driftTTL per team keeps the number on the wake-up
// call fresh enough to act on while the deep per-point audit stays the
// operator command (doctor --index) it was designed to be.
const driftTTL = 60 * time.Second

// driftFetch loads a fresh coverage audit for a team.
type driftFetch func(ctx context.Context, teamID string) (palace.DriftReport, error)

// driftCache is the per-team TTL cache in front of the coverage audit. A
// failed audit is not cached (a failure is a fact about the attempt, not about
// the palace), so the next status call retries it.
type driftCache struct {
	mu      sync.Mutex
	fetch   driftFetch
	ttl     time.Duration
	perTeam map[string]driftEntry
}

type driftEntry struct {
	report palace.DriftReport
	at     time.Time
}

func newDriftCache(fetch driftFetch, ttl time.Duration) *driftCache {
	return &driftCache{fetch: fetch, ttl: ttl, perTeam: map[string]driftEntry{}}
}

// get returns the team's coverage report, refreshing it when the cached copy
// is absent or past the TTL. A refresh runs the audit once and stores the
// result; concurrent misses within one TTL each run it (the audit is
// read-only, and status is best-effort — bounded cost, never a stampede
// amplifier beyond the natural concurrency of sessions starting).
func (c *driftCache) get(ctx context.Context, teamID string) (palace.DriftReport, error) {
	c.mu.Lock()
	if e, ok := c.perTeam[teamID]; ok && time.Since(e.at) < c.ttl {
		c.mu.Unlock()
		return e.report, nil
	}
	c.mu.Unlock()

	report, err := c.fetch(ctx, teamID)
	if err != nil {
		return palace.DriftReport{}, err
	}
	c.mu.Lock()
	c.perTeam[teamID] = driftEntry{report: report, at: time.Now()}
	c.mu.Unlock()
	return report, nil
}
