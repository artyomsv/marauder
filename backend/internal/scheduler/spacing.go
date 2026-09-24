package scheduler

import (
	"context"
	"sync"
	"time"

	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// checkSpacer hands out start times for the checks of a tracker that
// implements registry.WithCheckSpacing, so no two of them start closer
// together than the tracker asks (issue #198).
//
// It reserves rather than locks: each caller claims the next free slot and
// sleeps until it, so N due topics start spacing apart in the order they were
// dispatched, and a slow check does not hold the next one back beyond its
// slot. The zero value is ready to use.
type checkSpacer struct {
	mu   sync.Mutex
	next map[string]time.Time // tracker name → earliest start of the next check
	now  func() time.Time     // nil means time.Now; a test seam
}

// reserve claims the tracker's next start slot and returns how long the
// caller must wait for it. Zero means start now.
func (c *checkSpacer) reserve(tracker string, spacing time.Duration) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.now != nil {
		now = c.now()
	}
	if c.next == nil {
		c.next = map[string]time.Time{}
	}
	slot := now
	if n, ok := c.next[tracker]; ok && n.After(now) {
		slot = n
	}
	c.next[tracker] = slot.Add(spacing)
	return slot.Sub(now)
}

// waitForCheckSlot blocks until tr may start another check. It reports false
// when ctx ends first; the topic then stays due and a later tick picks it up.
//
// The wait holds a worker. That is deliberate: it keeps the per-tracker order
// without a second queue. The cost is bounded — DueForCheck hands out at most
// workers*4 topics per tick, and the burst only happens when many topics of
// one spaced tracker fall due together (a restart, a bulk "Check now"). The
// checks then finish spacing apart, so their next_check_at values stay spaced
// and later ticks do not queue them together again.
func (s *Scheduler) waitForCheckSlot(ctx context.Context, tr registry.Tracker) bool {
	ws, ok := tr.(registry.WithCheckSpacing)
	if !ok {
		return true
	}
	spacing := ws.CheckSpacing()
	if spacing <= 0 {
		return true
	}
	wait := s.spacer.reserve(tr.Name(), spacing)
	if wait <= 0 {
		return true
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
