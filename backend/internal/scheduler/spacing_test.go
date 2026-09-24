package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/events"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// spacedTracker is a fakeTracker that asks for spacing between its checks.
type spacedTracker struct {
	*fakeTracker
	spacing time.Duration
}

func (s *spacedTracker) CheckSpacing() time.Duration { return s.spacing }

var _ registry.WithCheckSpacing = (*spacedTracker)(nil)

func TestCheckSpacer_Reserve_HandsOutSpacedSlots(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	c := &checkSpacer{now: func() time.Time { return now }}
	const gap = 5 * time.Second

	for i, want := range []time.Duration{0, gap, 2 * gap} {
		if got := c.reserve("tapochek", gap); got != want {
			t.Errorf("reserve #%d = %v, want %v", i, got, want)
		}
	}
	// Trackers are spaced independently.
	if got := c.reserve("rutracker", gap); got != 0 {
		t.Errorf("other tracker = %v, want 0", got)
	}
	// Once the reserved slots have passed, the next check starts at once.
	now = now.Add(time.Minute)
	if got := c.reserve("tapochek", gap); got != 0 {
		t.Errorf("after the slots passed = %v, want 0", got)
	}
}

func TestCheckSpacer_Reserve_PartlyElapsedSlot_WaitsTheRest(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	c := &checkSpacer{now: func() time.Time { return now }}
	c.reserve("tapochek", 5*time.Second)
	now = now.Add(2 * time.Second)
	if got := c.reserve("tapochek", 5*time.Second); got != 3*time.Second {
		t.Errorf("reserve = %v, want 3s", got)
	}
}

func TestWaitForCheckSlot_TrackerWithoutSpacing_NeverWaits(t *testing.T) {
	s := &Scheduler{}
	tr := &fakeTracker{name: "plain"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 3 {
		if !s.waitForCheckSlot(ctx, tr) {
			t.Fatal("waitForCheckSlot = false for a tracker that asks for no spacing")
		}
	}
}

func TestRunCheck_SpacedTracker_SecondCheckStartsAfterTheGap(t *testing.T) {
	tr := &fakeTracker{
		name:   "faketracker",
		checks: []checkResult{{check: &domain.Check{Hash: "old-hash"}}},
	}
	f := newFixture(t, tr)
	const gap = 40 * time.Millisecond
	f.s.lookupTracker = func(string) registry.Tracker { return &spacedTracker{fakeTracker: tr, spacing: gap} }

	start := time.Now()
	f.s.runCheck(context.Background(), f.s.log, f.topic)
	f.s.runCheck(context.Background(), f.s.log, f.topic)
	if elapsed := time.Since(start); elapsed < gap {
		t.Errorf("two checks took %v, want at least the %v gap", elapsed, gap)
	}
	if tr.callsCheck != 2 {
		t.Errorf("Check calls = %d, want 2", tr.callsCheck)
	}
}

// A check still waiting for its slot at shutdown must leave no trace: no
// check.started pulse in the UI, no request, and no result that would move
// next_check_at — the topic stays due for the next start.
func TestRunCheck_ContextEndsWhileWaiting_LeavesTopicUntouched(t *testing.T) {
	tr := &fakeTracker{
		name:   "faketracker",
		checks: []checkResult{{check: &domain.Check{Hash: "old-hash"}}},
	}
	f := newFixture(t, tr)
	f.s.lookupTracker = func(string) registry.Tracker { return &spacedTracker{fakeTracker: tr, spacing: time.Hour} }

	f.s.runCheck(context.Background(), f.s.log, f.topic) // takes the free slot
	records, started := len(f.topics.recordCalls), len(f.emitter.ofType(events.CheckStarted))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	f.s.runCheck(ctx, f.s.log, f.topic)

	if tr.callsCheck != 1 {
		t.Errorf("Check calls = %d, want 1", tr.callsCheck)
	}
	if got := len(f.topics.recordCalls); got != records {
		t.Errorf("RecordCheckResult calls = %d, want %d", got, records)
	}
	if got := len(f.emitter.ofType(events.CheckStarted)); got != started {
		t.Errorf("check.started events = %d, want %d", got, started)
	}
}

// dueTopics is a fakeTopics whose DueForCheck returns a fixed list.
type dueTopics struct {
	*fakeTopics
	due []*domain.Topic
}

func (d *dueTopics) DueForCheck(context.Context, int) ([]*domain.Topic, error) {
	return d.due, nil
}

// A topic waiting for its spacing slot is still due by next_check_at, so
// without the inflight set every tick would queue it again.
func TestDispatchOnce_TopicStillQueued_NotDispatchedAgain(t *testing.T) {
	f := newFixture(t, &fakeTracker{name: "faketracker"})
	a := &domain.Topic{ID: uuid.New()}
	b := &domain.Topic{ID: uuid.New()}
	f.s.topics = &dueTopics{fakeTopics: f.topics, due: []*domain.Topic{a, b}}
	f.s.jobs = make(chan *domain.Topic, 8)

	f.s.dispatchOnce(context.Background())
	f.s.dispatchOnce(context.Background())
	if got := len(f.s.jobs); got != 2 {
		t.Fatalf("queued = %d after two ticks, want 2", got)
	}

	// Once a worker is done with a topic, a later tick may queue it again.
	<-f.s.jobs
	f.s.inflight.Delete(a.ID)
	f.s.dispatchOnce(context.Background())
	if got := len(f.s.jobs); got != 2 {
		t.Errorf("queued = %d after a finished topic fell due again, want 2", got)
	}
}

// A topic the full queue turned away must not stay marked, or it would
// never be dispatched again.
func TestDispatchOnce_QueueFull_ReleasesTheTopic(t *testing.T) {
	f := newFixture(t, &fakeTracker{name: "faketracker"})
	a := &domain.Topic{ID: uuid.New()}
	b := &domain.Topic{ID: uuid.New()}
	f.s.topics = &dueTopics{fakeTopics: f.topics, due: []*domain.Topic{a, b}}
	f.s.jobs = make(chan *domain.Topic, 1)

	f.s.dispatchOnce(context.Background())
	if _, marked := f.s.inflight.Load(b.ID); marked {
		t.Error("topic the full queue refused is still marked in flight")
	}
	if _, marked := f.s.inflight.Load(a.ID); !marked {
		t.Error("queued topic is not marked in flight")
	}
}
