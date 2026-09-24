package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/events"
	"github.com/artyomsv/marauder/backend/internal/metrics"
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

func TestAwaitCheckTurn_TrackerWithoutSpacing_NeverWaitsOrRereads(t *testing.T) {
	topics := &fakeTopics{}
	s := &Scheduler{topics: topics}
	tr := &fakeTracker{name: "plain"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 3 {
		topic := &domain.Topic{ID: uuid.New()}
		if got := s.awaitCheckTurn(ctx, zerolog.Nop(), topic, tr); got != topic {
			t.Fatalf("awaitCheckTurn = %v for a tracker that asks for no spacing, want the topic itself", got)
		}
	}
	// No extra query per check for the trackers that never wait.
	if topics.getCalls != 0 {
		t.Errorf("GetByID calls = %d, want 0", topics.getCalls)
	}
}

// newSpacedFixture is newFixture with the tracker asking for a tiny spacing,
// so awaitCheckTurn re-reads the topic without a noticeable wait.
func newSpacedFixture(t *testing.T) (*fixture, *fakeTracker) {
	t.Helper()
	tr := &fakeTracker{
		name:   "faketracker",
		checks: []checkResult{{check: &domain.Check{Hash: "old-hash"}}},
	}
	f := newFixture(t, tr)
	f.s.lookupTracker = func(string) registry.Tracker { return &spacedTracker{fakeTracker: tr, spacing: time.Millisecond} }
	return f, tr
}

// A reset or recheck while the topic waited in its lane must be honoured when
// its turn comes, not after another tick: the check runs on the fresh row.
func TestRunCheck_ResetWhileWaiting_ChecksTheFreshRow(t *testing.T) {
	f, tr := newSpacedFixture(t)
	observed := time.Now().Add(-time.Hour)
	f.topic.LastCheckedAt = &observed // the snapshot taken at dispatch
	fresh := *f.topic
	fresh.LastCheckedAt = nil // ResetCheckState nulls it
	f.topics.current = &fresh

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if tr.callsCheck != 1 {
		t.Fatalf("Check calls = %d, want 1", tr.callsCheck)
	}
	// RecordCheckResult is guarded on the token the check carries; the
	// snapshot's would no longer match and the result would be discarded.
	if got := f.lastRecord(t).observed; got != nil {
		t.Errorf("result carried last_checked_at %v, want the fresh row's nil", got)
	}
}

func TestRunCheck_ChangedWhileWaiting_SkipsTheCheck(t *testing.T) {
	paused := func(f *fixture) {
		p := *f.topic
		p.Status = domain.TopicStatusPaused
		f.topics.current = &p
	}
	deleted := func(f *fixture) { f.topics.getErr = repo.ErrNotFound }

	for name, stage := range map[string]func(*fixture){"paused": paused, "deleted": deleted} {
		t.Run(name, func(t *testing.T) {
			f, tr := newSpacedFixture(t)
			stage(f)

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
		})
	}
}

// A database blip on the re-read must not strand the topic: the dispatch
// snapshot still works, and the write guard protects its result.
func TestRunCheck_RereadFails_ChecksTheSnapshot(t *testing.T) {
	f, tr := newSpacedFixture(t)
	f.topics.getErr = errors.New("connection reset")

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

// syncTopics serialises calls into a topicsRepo fake, for tests where a
// lane's goroutines run checks concurrently.
type syncTopics struct {
	mu    sync.Mutex
	inner topicsRepo
}

func (s *syncTopics) DueForCheck(ctx context.Context, limit int, exclude []uuid.UUID) ([]*domain.Topic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.DueForCheck(ctx, limit, exclude)
}

func (s *syncTopics) GetByID(ctx context.Context, id uuid.UUID, userID *uuid.UUID) (*domain.Topic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.GetByID(ctx, id, userID)
}

func (s *syncTopics) RecordCheckResult(ctx context.Context, t *domain.Topic, hash string, updated bool, next time.Time, errMsg, errCode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.RecordCheckResult(ctx, t, hash, updated, next, errMsg, errCode)
}

func (s *syncTopics) MarkEpisodeDownloaded(ctx context.Context, t *domain.Topic, packed string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.MarkEpisodeDownloaded(ctx, t, packed)
}

func (s *syncTopics) MarkEpisodesDownloaded(ctx context.Context, t *domain.Topic, packed []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.MarkEpisodesDownloaded(ctx, t, packed)
}

func (s *syncTopics) VerifyCheckState(ctx context.Context, t *domain.Topic) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.VerifyCheckState(ctx, t)
}

// gateTracker is a spaced tracker whose Check reports each start and then
// blocks until release is closed, so a test can hold checks in flight.
type gateTracker struct {
	*spacedTracker
	started chan uuid.UUID
	release chan struct{}
}

func (g *gateTracker) Check(_ context.Context, topic *domain.Topic, _ *domain.TrackerCredential) (*domain.Check, error) {
	g.started <- topic.ID
	<-g.release
	return &domain.Check{Hash: topic.LastHash}, nil
}

func newGateTracker(spacing time.Duration) *gateTracker {
	return &gateTracker{
		spacedTracker: &spacedTracker{fakeTracker: &fakeTracker{name: "spaced"}, spacing: spacing},
		started:       make(chan uuid.UUID, 100),
		release:       make(chan struct{}),
	}
}

func spacedTopics(n int) []*domain.Topic {
	out := make([]*domain.Topic, n)
	for i := range out {
		out[i] = &domain.Topic{ID: uuid.New(), TrackerName: "spaced", LastHash: "h", Extra: map[string]any{}}
	}
	return out
}

// waitStarted receives one check start or fails the test.
func waitStarted(t *testing.T, g *gateTracker) uuid.UUID {
	t.Helper()
	select {
	case id := <-g.started:
		return id
	case <-time.After(5 * time.Second):
		t.Fatal("no check started")
		return uuid.Nil
	}
}

// A spaced tracker's topics go to its own lane, never to the shared workers,
// and the lane runs them and releases them.
func TestDispatchOnce_SpacedTracker_RunsInItsLane(t *testing.T) {
	f := newFixture(t, &fakeTracker{name: "unused"})
	g := newGateTracker(time.Millisecond)
	close(g.release)
	f.s.lookupTracker = func(string) registry.Tracker { return g }
	topic := spacedTopics(1)[0]
	f.s.topics = &dueTopics{fakeTopics: f.topics, due: []*domain.Topic{topic}}
	f.s.jobs = make(chan *domain.Topic, 8)

	f.s.dispatchOnce(context.Background())
	if n := len(f.s.jobs); n != 0 {
		t.Errorf("shared queue holds %d topics, want 0", n)
	}
	if got := waitStarted(t, g); got != topic.ID {
		t.Errorf("lane checked %s, want %s", got, topic.ID)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, marked := f.s.inflight.Load(topic.ID); !marked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("topic still marked in flight after its lane ran it")
		}
		time.Sleep(time.Millisecond)
	}
	f.s.closeLanes()
	f.s.wg.Wait()
}

// What the lanes are for: however long one spaced tracker's backlog is, it
// never turns its topics away — a turned-away topic keeps its old
// next_check_at and crowds the LIMIT window again on the next tick — and
// never keeps another tracker's topics from being dispatched.
func TestDispatchOnce_LaneBacklog_HoldsEveryTopicAndOthersStillDispatch(t *testing.T) {
	f := newFixture(t, &fakeTracker{name: "unused"})
	g := newGateTracker(time.Millisecond) // release stays open: checks hang
	plain := &fakeTracker{name: "plain"}
	f.s.lookupTracker = func(name string) registry.Tracker {
		if name == "spaced" {
			return g
		}
		return plain
	}
	f.s.jobs = make(chan *domain.Topic, 8)
	backlog := spacedTopics(40) // far above the shared queue's workers*4
	other := &domain.Topic{ID: uuid.New(), TrackerName: "plain"}
	f.s.topics = &syncTopics{inner: &dueTopics{fakeTopics: f.topics, due: append(append([]*domain.Topic{}, backlog...), other)}}

	f.s.dispatchOnce(context.Background())

	for _, b := range backlog {
		if _, marked := f.s.inflight.Load(b.ID); !marked {
			t.Fatalf("spaced topic %s was turned away", b.ID)
		}
	}
	select {
	case got := <-f.s.jobs:
		if got.ID != other.ID {
			t.Errorf("shared queue got %s, want the other tracker's %s", got.ID, other.ID)
		}
	default:
		t.Error("other tracker's topic was not dispatched behind the backlog")
	}

	// Every queued topic is checked; none was dropped on the way.
	close(g.release)
	checked := map[uuid.UUID]bool{}
	for range backlog {
		checked[waitStarted(t, g)] = true
	}
	if len(checked) != len(backlog) {
		t.Errorf("checked %d distinct topics, want %d", len(checked), len(backlog))
	}
	f.s.closeLanes()
	f.s.wg.Wait()
}

// A slow check must not hold back the next start: the spacer sets the pace,
// not the duration of the check in front.
func TestLane_SlowCheck_DoesNotHoldBackTheNextStart(t *testing.T) {
	f := newFixture(t, &fakeTracker{name: "unused"})
	f.s.cfg.SchedulerWorkers = 2
	g := newGateTracker(10 * time.Millisecond)
	f.s.lookupTracker = func(string) registry.Tracker { return g }
	topics := spacedTopics(2)
	f.s.topics = &syncTopics{inner: &dueTopics{fakeTopics: f.topics, due: topics}}

	f.s.dispatchOnce(context.Background())
	waitStarted(t, g) // the first check now hangs
	waitStarted(t, g) // and the second starts anyway

	close(g.release)
	f.s.closeLanes()
	f.s.wg.Wait()
}

// Shutdown must not work through a lane's backlog on a cancelled context:
// those topics are still due and the next start picks them up.
func TestLaneQueue_Closed_StopsEvenWithTopicsLeft(t *testing.T) {
	q := newLaneQueue()
	q.push(&domain.Topic{ID: uuid.New()})
	q.close()
	if _, ok := q.pop(); ok {
		t.Error("pop handed out a topic after close")
	}
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

// histogramCount reads how many observations one histogram child holds.
func histogramCount(t *testing.T, o prometheus.Observer) uint64 {
	t.Helper()
	m, ok := o.(prometheus.Metric)
	if !ok {
		t.Fatal("observer is not a metric")
	}
	var d dto.Metric
	if err := m.Write(&d); err != nil {
		t.Fatalf("read histogram: %v", err)
	}
	return d.GetHistogram().GetSampleCount()
}

// A check skipped at its turn never ran, so it must not add its wait to the
// check-duration histogram as if it were check time.
func TestRunCheck_SkippedAtItsTurn_RecordsNoDuration(t *testing.T) {
	f, _ := newSpacedFixture(t)
	f.topic.TrackerName = "metric-" + uuid.NewString() // a histogram child of its own
	hist := metrics.SchedulerTopicCheckDurationSeconds.WithLabelValues(f.topic.TrackerName)

	f.topics.getErr = repo.ErrNotFound
	f.s.runCheck(context.Background(), f.s.log, f.topic)
	if n := histogramCount(t, hist); n != 0 {
		t.Errorf("skipped check recorded %d durations, want 0", n)
	}

	f.topics.getErr = nil
	f.topics.current = f.topic
	f.s.runCheck(context.Background(), f.s.log, f.topic)
	if n := histogramCount(t, hist); n != 1 {
		t.Errorf("check that ran recorded %d durations, want 1", n)
	}
}
