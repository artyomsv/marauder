package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/db/repo"
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

func TestAwaitCheckTurn_TrackerWithoutSpacing_NeverWaitsOrVerifies(t *testing.T) {
	topics := &fakeTopics{}
	s := &Scheduler{topics: topics}
	tr := &fakeTracker{name: "plain"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 3 {
		if !s.awaitCheckTurn(ctx, zerolog.Nop(), &domain.Topic{ID: uuid.New()}, tr) {
			t.Fatal("awaitCheckTurn = false for a tracker that asks for no spacing")
		}
	}
	// No extra query per check for the trackers that never wait.
	if len(topics.verifyCalls) != 0 {
		t.Errorf("VerifyCheckState calls = %d, want 0", len(topics.verifyCalls))
	}
}

// A reset, recheck or delete while the topic waited moves its token. The
// check must then not run: its result would be thrown away after spending
// requests on the rate-limited site.
func TestRunCheck_StateChangedWhileWaiting_SkipsTheCheck(t *testing.T) {
	tr := &fakeTracker{
		name:   "faketracker",
		checks: []checkResult{{check: &domain.Check{Hash: "old-hash"}}},
	}
	f := newFixture(t, tr)
	f.s.lookupTracker = func(string) registry.Tracker { return &spacedTracker{fakeTracker: tr, spacing: time.Millisecond} }
	f.topics.verifyErr = repo.ErrStaleCheckResult

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if tr.callsCheck != 0 {
		t.Errorf("Check calls = %d, want 0", tr.callsCheck)
	}
	if len(f.topics.recordCalls) != 0 {
		t.Errorf("RecordCheckResult calls = %d, want 0", len(f.topics.recordCalls))
	}
	if n := len(f.emitter.ofType(events.CheckStarted)); n != 0 {
		t.Errorf("check.started events = %d, want 0", n)
	}
}

// Only staleness drops a check. A database blip must not strand the topic.
func TestRunCheck_VerifyFailsWhileWaiting_ChecksAnyway(t *testing.T) {
	tr := &fakeTracker{
		name:   "faketracker",
		checks: []checkResult{{check: &domain.Check{Hash: "old-hash"}}},
	}
	f := newFixture(t, tr)
	f.s.lookupTracker = func(string) registry.Tracker { return &spacedTracker{fakeTracker: tr, spacing: time.Millisecond} }
	f.topics.verifyErr = errors.New("connection reset")

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if tr.callsCheck != 1 {
		t.Errorf("Check calls = %d, want 1", tr.callsCheck)
	}
}

// deadlineTracker is a spacedTracker that records the time its Check had
// left, to show the budget is not spent on the wait.
type deadlineTracker struct {
	*spacedTracker
	left []time.Duration
}

func (d *deadlineTracker) Check(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential) (*domain.Check, error) {
	if dl, ok := ctx.Deadline(); ok {
		d.left = append(d.left, time.Until(dl))
	}
	return d.spacedTracker.Check(ctx, topic, creds)
}

func TestRunCheck_SpacedTracker_BudgetStartsAfterTheWait(t *testing.T) {
	tr := &fakeTracker{
		name:   "faketracker",
		checks: []checkResult{{check: &domain.Check{Hash: "old-hash"}}},
	}
	f := newFixture(t, tr)
	const gap = 500 * time.Millisecond
	dt := &deadlineTracker{spacedTracker: &spacedTracker{fakeTracker: tr, spacing: gap}}
	f.s.lookupTracker = func(string) registry.Tracker { return dt }

	f.s.runCheck(context.Background(), f.s.log, f.topic) // takes the free slot
	f.s.runCheck(context.Background(), f.s.log, f.topic) // waits ~gap

	if len(dt.left) != 2 {
		t.Fatalf("Check calls with a deadline = %d, want 2", len(dt.left))
	}
	budget := f.s.cfg.TrackerHTTPTimeout + 5*time.Second
	// Had checkCtx been created before the wait, the second check would
	// have at most budget-gap left.
	if floor := budget - gap/2; dt.left[1] < floor {
		t.Errorf("second check had %v left, want more than %v", dt.left[1], floor)
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
	due      []*domain.Topic
	excluded [][]uuid.UUID // the exclude list of each call
}

func (d *dueTopics) DueForCheck(_ context.Context, _ int, exclude []uuid.UUID) ([]*domain.Topic, error) {
	d.excluded = append(d.excluded, exclude)
	return d.due, nil
}

// Topics already held must not take rows of DueForCheck's LIMIT window, or a
// backlog of them keeps every other topic from being selected.
func TestDispatchOnce_PassesInflightTopicsAsExclusions(t *testing.T) {
	f := newFixture(t, &fakeTracker{name: "faketracker"})
	held := uuid.New()
	f.s.inflight.Store(held, struct{}{})
	due := &dueTopics{fakeTopics: f.topics}
	f.s.topics = due

	f.s.dispatchOnce(context.Background())

	if len(due.excluded) != 1 || len(due.excluded[0]) != 1 || due.excluded[0][0] != held {
		t.Errorf("exclude = %v, want [[%s]]", due.excluded, held)
	}
}

// The worker keeps a topic marked for the whole check and releases it after,
// so it can be dispatched again next time it falls due.
func TestWorker_HoldsInflightDuringCheck_ReleasesAfter(t *testing.T) {
	tr := &fakeTracker{name: "faketracker", checks: []checkResult{{check: &domain.Check{Hash: "old-hash"}}}}
	f := newFixture(t, tr)
	var markedDuringCheck bool
	tr.onCheck = func() { _, markedDuringCheck = f.s.inflight.Load(f.topic.ID) }

	f.s.inflight.Store(f.topic.ID, struct{}{}) // as dispatchOnce does
	f.s.jobs <- f.topic
	close(f.s.jobs)
	f.s.wg.Add(1)
	f.s.worker(context.Background(), 0)

	if tr.callsCheck != 1 {
		t.Fatalf("Check calls = %d, want 1", tr.callsCheck)
	}
	if !markedDuringCheck {
		t.Error("topic was not marked in flight while its check ran")
	}
	if _, marked := f.s.inflight.Load(f.topic.ID); marked {
		t.Error("topic still marked in flight after the worker finished it")
	}
}

// A spaced tracker's topics go to its own lane, never to the shared workers,
// and the lane runs them and releases them.
func TestDispatchOnce_SpacedTracker_RunsInItsLane(t *testing.T) {
	tr := &fakeTracker{name: "faketracker", checks: []checkResult{{check: &domain.Check{Hash: "old-hash"}}}}
	f := newFixture(t, tr)
	f.s.lookupTracker = func(string) registry.Tracker { return &spacedTracker{fakeTracker: tr, spacing: time.Millisecond} }
	f.s.topics = &dueTopics{fakeTopics: f.topics, due: []*domain.Topic{f.topic}}
	f.s.jobs = make(chan *domain.Topic, 8)

	f.s.dispatchOnce(context.Background())
	if n := len(f.s.jobs); n != 0 {
		t.Errorf("shared queue holds %d topics, want 0", n)
	}
	f.s.closeLanes()
	f.s.wg.Wait()

	if tr.callsCheck != 1 {
		t.Errorf("Check calls = %d, want 1", tr.callsCheck)
	}
	if _, marked := f.s.inflight.Load(f.topic.ID); marked {
		t.Error("topic still marked in flight after its lane ran it")
	}
}

// The failure the lanes exist to prevent: a backlog of one spaced tracker
// must not keep another tracker's topics from being dispatched.
func TestDispatchOnce_FullLane_OtherTrackersStillDispatched(t *testing.T) {
	spaced := &fakeTracker{name: "spaced", checks: []checkResult{{check: &domain.Check{Hash: "old-hash"}}}}
	release := make(chan struct{})
	spaced.onCheck = func() { <-release } // the lane is stuck on its first check
	plain := &fakeTracker{name: "plain"}
	f := newFixture(t, spaced)
	f.s.lookupTracker = func(name string) registry.Tracker {
		if name == "spaced" {
			return &spacedTracker{fakeTracker: spaced, spacing: time.Millisecond}
		}
		return plain
	}
	f.s.jobs = make(chan *domain.Topic, 8)

	// Lane capacity is SchedulerWorkers*4 = 4; seven spaced topics overflow
	// it whether or not the lane has taken its first one yet.
	var due []*domain.Topic
	for range 7 {
		due = append(due, &domain.Topic{ID: uuid.New(), TrackerName: "spaced", LastHash: "old-hash", Extra: map[string]any{}})
	}
	other := &domain.Topic{ID: uuid.New(), TrackerName: "plain"}
	due = append(due, other)
	f.s.topics = &dueTopics{fakeTopics: f.topics, due: due}

	f.s.dispatchOnce(context.Background())

	select {
	case got := <-f.s.jobs:
		if got.ID != other.ID {
			t.Errorf("shared queue got topic %s, want the other tracker's %s", got.ID, other.ID)
		}
	default:
		t.Error("other tracker's topic was not dispatched behind a full lane")
	}
	var turnedAway int
	for _, d := range due[:7] {
		if _, marked := f.s.inflight.Load(d.ID); !marked {
			turnedAway++
		}
	}
	if turnedAway == 0 {
		t.Error("no spaced topic was released; the lane never filled")
	}

	close(release)
	f.s.closeLanes()
	f.s.wg.Wait()
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
