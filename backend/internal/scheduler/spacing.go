package scheduler

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// checkSpacer hands out start times for the checks of a tracker that
// implements registry.WithCheckSpacing, so no two of them start closer
// together than the tracker asks (issue #198).
//
// It reserves rather than locks: each caller claims the next free slot and
// sleeps until it, and a slot already in the past is free at once. The zero
// value is ready to use.
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

// spacingOf reports the gap tr asks for between check starts, or zero.
func spacingOf(tr registry.Tracker) time.Duration {
	ws, ok := tr.(registry.WithCheckSpacing)
	if !ok {
		return 0
	}
	return max(ws.CheckSpacing(), 0)
}

// awaitCheckTurn blocks until tr may start another check, then confirms the
// topic is still the one this worker was handed. It reports false when the
// check must not run: ctx ended first, or the topic's check state changed
// while it waited. Either way the topic stays due and a later tick picks it up.
//
// A spaced topic can wait minutes in its lane, and a reset, recheck or delete
// in that time moves its check-state token. Running the check anyway would
// spend requests on a rate-limited site only for RecordCheckResult to throw
// the result away, and a second click on "Check now" would double the load in
// exactly the burst issue #198 is about.
func (s *Scheduler) awaitCheckTurn(ctx context.Context, log zerolog.Logger, t *domain.Topic, tr registry.Tracker) bool {
	spacing := spacingOf(tr)
	if spacing == 0 {
		return true
	}
	if wait := s.spacer.reserve(tr.Name(), spacing); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return false
		}
	}
	err := s.topics.VerifyCheckState(ctx, t)
	switch {
	case errors.Is(err, repo.ErrStaleCheckResult):
		log.Info().Msg("check skipped: topic changed while waiting for its turn")
		return false
	case err != nil:
		// Not staleness, so not a reason to drop the check; the write guard
		// in RecordCheckResult still protects the result.
		log.Warn().Err(err).Msg("verify check state before check failed; checking anyway")
	}
	return true
}

// queueFor picks the queue a due topic goes to: its tracker's lane when the
// tracker asks for spacing, the shared worker queue otherwise.
func (s *Scheduler) queueFor(ctx context.Context, t *domain.Topic) (chan<- *domain.Topic, bool) {
	tr := s.lookupTracker(t.TrackerName)
	if tr == nil || spacingOf(tr) == 0 {
		return s.jobs, false
	}
	return s.lane(ctx, tr.Name()), true
}

// lane returns the tracker's queue, starting its goroutine on first use.
//
// One goroutine per spaced tracker instead of the shared workers: a worker
// asleep until its slot is a worker no other tracker can use, and a burst of
// one spaced tracker's topics (a restart, a bulk "Check now") once parked all
// of them and stopped every other tracker's checks for every user. One
// goroutine loses nothing, because the checks of a spaced tracker start
// one after another anyway.
func (s *Scheduler) lane(ctx context.Context, tracker string) chan<- *domain.Topic {
	s.lanesMu.Lock()
	defer s.lanesMu.Unlock()
	if ch, ok := s.lanes[tracker]; ok {
		return ch
	}
	if s.lanes == nil {
		s.lanes = map[string]chan *domain.Topic{}
	}
	ch := make(chan *domain.Topic, s.cfg.SchedulerWorkers*4)
	s.lanes[tracker] = ch
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.drain(ctx, s.log.With().Str("lane", tracker).Logger(), ch)
	}()
	return ch
}

// closeLanes ends every lane goroutine once its queue is empty. It runs on
// the dispatching goroutine, after its last dispatch, so nothing sends on a
// closed lane.
func (s *Scheduler) closeLanes() {
	s.lanesMu.Lock()
	defer s.lanesMu.Unlock()
	for _, ch := range s.lanes {
		close(ch)
	}
	s.lanes = nil
}
