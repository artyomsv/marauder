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
// It works in two steps. reserve hands each caller the next free slot to
// sleep until, so a lane's goroutines take turns in order instead of all
// waking at once; a slot already in the past is free at once. claim then
// records the actual start, and refuses one that comes less than spacing
// after the previous actual start — the guarantee, since a caller can be
// held up between its slot and its start. The zero value is ready to use.
type checkSpacer struct {
	mu      sync.Mutex
	next    map[string]time.Time // tracker name → earliest start of the next check
	started map[string]time.Time // tracker name → when the last check actually started
	now     func() time.Time     // nil means time.Now; a test seam
}

func (c *checkSpacer) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// claim records a check start for tracker now and returns zero, unless the
// previous start was less than spacing ago: then it records nothing and
// returns how long is left.
func (c *checkSpacer) claim(tracker string, spacing time.Duration) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock()
	if last, ok := c.started[tracker]; ok {
		if left := last.Add(spacing).Sub(now); left > 0 {
			return left
		}
	}
	if c.started == nil {
		c.started = map[string]time.Time{}
	}
	c.started[tracker] = now
	return 0
}

// reserve claims the tracker's next start slot and returns how long the
// caller must wait for it. Zero means start now.
func (c *checkSpacer) reserve(tracker string, spacing time.Duration) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock()
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

// awaitCheckTurn blocks until tr may start another check, then re-reads the
// topic. It returns the topic to check, or nil when the check must not run:
// ctx ended first, or the topic was deleted or paused while it waited. In the
// nil case the topic, if still due, is picked up by a later tick.
//
// The re-read is what a spaced topic needs and a worker's topic does not: it
// can wait minutes in its lane, while its inflight mark keeps a later tick from
// dispatching a fresh copy. A reset or recheck in that time moves its
// check-state token, so the snapshot taken at dispatch would run a check that
// RecordCheckResult then throws away, and the reset or recheck the user asked
// for would wait for yet another tick. Checking the fresh row does what they
// asked for as soon as the topic's turn comes.
func (s *Scheduler) awaitCheckTurn(ctx context.Context, log zerolog.Logger, t *domain.Topic, tr registry.Tracker) *domain.Topic {
	spacing := spacingOf(tr)
	if spacing == 0 {
		return t
	}
	if ctx.Err() != nil {
		return nil
	}
	if !sleepCtx(ctx, s.spacer.reserve(tr.Name(), spacing)) {
		return nil
	}
	fresh, err := s.topics.GetByID(ctx, t.ID, nil)
	switch {
	case errors.Is(err, repo.ErrNotFound):
		log.Info().Msg("check skipped: topic deleted while waiting for its turn")
		return nil
	case err != nil:
		// A read failure is no reason to drop the check: the snapshot still
		// works, and the write guard in RecordCheckResult protects the result.
		log.Warn().Err(err).Msg("re-read topic before check failed; using the dispatch snapshot")
		fresh = t
	case fresh.Status == domain.TopicStatusPaused:
		log.Info().Msg("check skipped: topic paused while waiting for its turn")
		return nil
	}
	// The slot reserved above only orders the lane's goroutines. The re-read
	// sits between it and the start, and a slow read would let the goroutine
	// holding the next slot start first and this one right after it — two
	// starts closer than spacing (found in review). claim is what enforces the
	// gap, against the time checks actually started.
	for {
		wait := s.spacer.claim(tr.Name(), spacing)
		if wait == 0 {
			return fresh
		}
		if !sleepCtx(ctx, wait) {
			return nil
		}
	}
}

// sleepCtx waits for d, or reports false when ctx ends first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// laneQueue is an unbounded FIFO shared by a lane's goroutines.
//
// Unbounded on purpose. A bounded lane has to turn topics away when full, and
// a turned-away topic loses its inflight mark while keeping its old
// next_check_at, so it is again among the oldest due rows on the next tick,
// turned away again, and a large enough backlog fills DueForCheck's LIMIT
// window and keeps every other tracker's topics out (found in review). The
// size is still bounded: the inflight set admits each topic once, so a lane
// never holds more than its tracker's topic count.
type laneQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []*domain.Topic
	closed bool
}

func newLaneQueue() *laneQueue {
	q := &laneQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *laneQueue) push(t *domain.Topic) {
	q.mu.Lock()
	q.items = append(q.items, t)
	q.mu.Unlock()
	q.cond.Signal()
}

// pop blocks until a topic is queued or the queue is closed. Once closed it
// reports false even with topics left: they are still due in the database,
// and the next start picks them up instead of this shutdown working through
// them on a cancelled context.
func (q *laneQueue) pop() (*domain.Topic, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if q.closed {
		return nil, false
	}
	t := q.items[0]
	q.items[0] = nil
	q.items = q.items[1:]
	return t, true
}

func (q *laneQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.cond.Broadcast()
}

// laneFor returns the lane of the topic's tracker when that tracker asks for
// spacing, starting it on first use, or nil for the shared worker queue.
//
// Lanes instead of the shared workers: a worker asleep until its slot is a
// worker no other tracker can use, and a burst of one spaced tracker's topics
// (a restart, a bulk "Check now") once parked all of them and stopped every
// other tracker's checks for every user. A lane has as many goroutines as
// there are workers, so a slow check — a download, a stalled request — does
// not hold back the next check's start; the spacer alone sets the pace.
func (s *Scheduler) laneFor(ctx context.Context, t *domain.Topic) *laneQueue {
	tr := s.lookupTracker(t.TrackerName)
	if tr == nil || spacingOf(tr) == 0 {
		return nil
	}
	name := tr.Name()
	s.lanesMu.Lock()
	defer s.lanesMu.Unlock()
	if q, ok := s.lanes[name]; ok {
		return q
	}
	if s.lanes == nil {
		s.lanes = map[string]*laneQueue{}
	}
	q := newLaneQueue()
	s.lanes[name] = q
	log := s.log.With().Str("lane", name).Logger()
	for range max(s.cfg.SchedulerWorkers, 1) {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			for {
				t, ok := q.pop()
				if !ok {
					return
				}
				s.runCheck(ctx, log, t)
				s.inflight.Delete(t.ID)
			}
		}()
	}
	return q
}

// closeLanes stops every lane goroutine once its current check is over.
func (s *Scheduler) closeLanes() {
	s.lanesMu.Lock()
	defer s.lanesMu.Unlock()
	for _, q := range s.lanes {
		q.close()
	}
	s.lanes = nil
}
