package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/config"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// --- Fakes --------------------------------------------------------------

// fakeTracker is a programmable registry.Tracker. Each call to Check
// returns the configured (check, err) at index callsCheck and then
// advances. Same for Download. Out-of-bounds calls return the last
// element so callers can keep polling.
type fakeTracker struct {
	name      string
	checks    []checkResult
	downloads []downloadResult

	callsCheck    int
	callsDownload int
}

type checkResult struct {
	check *domain.Check
	err   error
}

type downloadResult struct {
	payload *domain.Payload
	err     error
}

func (f *fakeTracker) Name() string           { return f.name }
func (f *fakeTracker) DisplayName() string    { return f.name }
func (f *fakeTracker) CanParse(_ string) bool { return true }
func (f *fakeTracker) Parse(_ context.Context, _ string) (*domain.Topic, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeTracker) Check(_ context.Context, _ *domain.Topic, _ *domain.TrackerCredential) (*domain.Check, error) {
	idx := f.callsCheck
	if idx >= len(f.checks) {
		idx = len(f.checks) - 1
	}
	f.callsCheck++
	r := f.checks[idx]
	return r.check, r.err
}

func (f *fakeTracker) Download(_ context.Context, _ *domain.Topic, _ *domain.Check, _ *domain.TrackerCredential) (*domain.Payload, error) {
	idx := f.callsDownload
	if idx >= len(f.downloads) {
		idx = len(f.downloads) - 1
	}
	f.callsDownload++
	r := f.downloads[idx]
	return r.payload, r.err
}

// fakeTopics records every persistence call without touching a DB.
// It satisfies topicsRepo (and optionally markEpisodeDownloader).
type fakeTopics struct {
	recordCalls         []recordCall
	updateExtraCalls    []updateExtraCall
	markCalls           []markCall
	markErr             error
	updateExtraErr      error
	implementMarkAtomic bool // when true, the test exercises the atomic path
}

type recordCall struct {
	id          uuid.UUID
	hash        string
	updated     bool
	nextCheckAt time.Time
	errMsg      string
}

type updateExtraCall struct {
	id    uuid.UUID
	extra map[string]any
}

type markCall struct {
	id     uuid.UUID
	packed string
}

func (f *fakeTopics) DueForCheck(_ context.Context, _ int) ([]*domain.Topic, error) {
	return nil, nil
}

func (f *fakeTopics) RecordCheckResult(_ context.Context, id uuid.UUID, hash string, updated bool, nextCheckAt time.Time, errMsg string) error {
	f.recordCalls = append(f.recordCalls, recordCall{id, hash, updated, nextCheckAt, errMsg})
	return nil
}

func (f *fakeTopics) UpdateExtra(_ context.Context, id uuid.UUID, extra map[string]any) error {
	// Snapshot the map so the test sees the value at the time of the call,
	// not after the scheduler mutates it again on the next iteration.
	snap := make(map[string]any, len(extra))
	for k, v := range extra {
		snap[k] = v
	}
	f.updateExtraCalls = append(f.updateExtraCalls, updateExtraCall{id, snap})
	return f.updateExtraErr
}

// fakeTopicsAtomic is fakeTopics + the optional atomic method, used by
// the persistence-failure test to exercise the markEpisodeDownloader
// branch.
type fakeTopicsAtomic struct {
	fakeTopics
}

func (f *fakeTopicsAtomic) MarkEpisodeDownloaded(_ context.Context, id uuid.UUID, packed string) error {
	f.markCalls = append(f.markCalls, markCall{id, packed})
	return f.markErr
}

// fakeClients records GetByID / GetDefault calls and always returns a
// fixed Client whose ClientName matches the registered fakeClientPlugin.
type fakeClients struct {
	client *domain.Client
}

func (f *fakeClients) GetByID(_ context.Context, _ uuid.UUID, _ uuid.UUID) (*domain.Client, error) {
	return f.client, nil
}

func (f *fakeClients) GetDefault(_ context.Context, _ uuid.UUID) (*domain.Client, error) {
	return f.client, nil
}

// fakeCreds is a no-op credentials repo.
type fakeCreds struct{}

func (f *fakeCreds) GetForTracker(_ context.Context, _ uuid.UUID, _ string) (*domain.TrackerCredential, error) {
	return nil, nil
}

func (f *fakeCreds) MarkSessionExpired(_ context.Context, _, _ uuid.UUID) (bool, error) {
	return false, nil
}

// fakeCredsSession is a credentials repo that returns a configured credential
// and records MarkSessionExpired calls. markExpiredWon controls whether the
// fake reports that THIS call won the NULL->now() transition (the real repo
// derives this from RowsAffected==1).
type fakeCredsSession struct {
	stored           *domain.TrackerCredential
	markExpiredCalls int
	markExpiredWon   bool
	markExpiredErr   error
}

func (f *fakeCredsSession) GetForTracker(_ context.Context, _ uuid.UUID, _ string) (*domain.TrackerCredential, error) {
	return f.stored, nil
}

func (f *fakeCredsSession) MarkSessionExpired(_ context.Context, _, _ uuid.UUID) (bool, error) {
	f.markExpiredCalls++
	return f.markExpiredWon, f.markExpiredErr
}

// fakeNotifier records Send / SendVia calls.
type fakeNotifier struct {
	calls          int
	lastID         uuid.UUID
	lastEvent      string
	lastMsg        domain.Message
	lastNotifierID *uuid.UUID
}

func (f *fakeNotifier) Send(_ context.Context, userID uuid.UUID, event string, msg domain.Message) int {
	f.calls++
	f.lastID = userID
	f.lastEvent = event
	f.lastMsg = msg
	f.lastNotifierID = nil
	return 1
}

func (f *fakeNotifier) SendVia(_ context.Context, userID uuid.UUID, notifierID *uuid.UUID, event string, msg domain.Message) int {
	f.calls++
	f.lastID = userID
	f.lastEvent = event
	f.lastMsg = msg
	f.lastNotifierID = notifierID
	return 1
}

// fakeDecryptor returns its input unchanged.
type fakeDecryptor struct{}

func (f *fakeDecryptor) Decrypt(ct, _ []byte) ([]byte, error) { return ct, nil }

// fakeDeliveries records every delivery the scheduler logs.
type fakeDeliveries struct {
	recorded []*domain.TopicDelivery
	err      error
}

func (f *fakeDeliveries) Record(_ context.Context, d *domain.TopicDelivery) (bool, error) {
	f.recorded = append(f.recorded, d)
	return f.err == nil, f.err
}

// fakeClientPlugin satisfies registry.Client and records every Add call.
type fakeClientPlugin struct {
	name     string
	addCalls int
	addErr   error
	lastOpts domain.AddOptions
}

func (f *fakeClientPlugin) Name() string                 { return f.name }
func (f *fakeClientPlugin) DisplayName() string          { return f.name }
func (f *fakeClientPlugin) ConfigSchema() map[string]any { return nil }
func (f *fakeClientPlugin) Test(_ context.Context, _ []byte) error {
	return nil
}
func (f *fakeClientPlugin) Add(_ context.Context, _ []byte, _ *domain.Payload, opts domain.AddOptions) error {
	f.addCalls++
	f.lastOpts = opts
	return f.addErr
}

// --- Test setup helpers ------------------------------------------------

type fixture struct {
	s            *Scheduler
	topics       *fakeTopics
	atomicTopics *fakeTopicsAtomic
	clientPlugin *fakeClientPlugin
	deliveries   *fakeDeliveries
	notifier     *fakeNotifier
	tracker      *fakeTracker
	topic        *domain.Topic
}

// newFixture wires a scheduler with all-fakes dependencies. If atomic
// is true, the topics repo also implements markEpisodeDownloader.
func newFixture(t *testing.T, tracker *fakeTracker, atomic bool) *fixture {
	t.Helper()

	cfg := &config.Config{
		SchedulerEnabled:            true,
		SchedulerWorkers:            1,
		SchedulerTick:               time.Second,
		SchedulerMaxEpisodesPerTick: 25,
		TrackerHTTPTimeout:          5 * time.Second,
		CheckMaxBackoff:             time.Hour,
	}

	clientID := uuid.New()
	client := &domain.Client{
		ID:         clientID,
		ClientName: "fakeclient",
	}
	clientPlugin := &fakeClientPlugin{name: "fakeclient"}

	var topicsImpl topicsRepo
	var plain *fakeTopics
	var atomicImpl *fakeTopicsAtomic
	if atomic {
		atomicImpl = &fakeTopicsAtomic{}
		plain = &atomicImpl.fakeTopics
		topicsImpl = atomicImpl
	} else {
		plain = &fakeTopics{}
		topicsImpl = plain
	}

	deliveries := &fakeDeliveries{}
	notifier := &fakeNotifier{}
	s := &Scheduler{
		cfg:           cfg,
		log:           zerolog.New(io.Discard),
		topics:        topicsImpl,
		clients:       &fakeClients{client: client},
		creds:         &fakeCreds{},
		deliveries:    deliveries,
		notifier:      notifier,
		master:        &fakeDecryptor{},
		lookupTracker: func(name string) registry.Tracker { return tracker },
		lookupClient:  func(name string) registry.Client { return clientPlugin },
		jobs:          make(chan *domain.Topic, 1),
		stop:          make(chan struct{}),
		ready:         make(chan struct{}),
	}

	cid := clientID
	topic := &domain.Topic{
		ID:               uuid.New(),
		UserID:           uuid.New(),
		TrackerName:      "faketracker",
		URL:              "https://example.com/topic/1",
		DisplayName:      "Fake Topic",
		ClientID:         &cid,
		CheckIntervalSec: 900,
		Status:           domain.TopicStatusActive,
		LastHash:         "old-hash",
		Extra:            map[string]any{},
	}

	return &fixture{
		s:            s,
		topics:       plain,
		atomicTopics: atomicImpl,
		clientPlugin: clientPlugin,
		deliveries:   deliveries,
		notifier:     notifier,
		tracker:      tracker,
		topic:        topic,
	}
}

// lastRecord returns the most recent recordCall, or fails the test if
// none was made.
func (f *fixture) lastRecord(t *testing.T) recordCall {
	t.Helper()
	if len(f.topics.recordCalls) == 0 {
		t.Fatal("expected RecordCheckResult to be called, but it was not")
	}
	return f.topics.recordCalls[len(f.topics.recordCalls)-1]
}

// --- Tests --------------------------------------------------------------

func TestRunCheck_HashUnchanged(t *testing.T) {
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{check: &domain.Check{Hash: "old-hash"}, err: nil},
		},
	}
	f := newFixture(t, tr, false)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if tr.callsDownload != 0 {
		t.Errorf("expected 0 Download calls, got %d", tr.callsDownload)
	}
	if f.clientPlugin.addCalls != 0 {
		t.Errorf("expected 0 client Add calls, got %d", f.clientPlugin.addCalls)
	}
	rec := f.lastRecord(t)
	if rec.updated {
		t.Errorf("expected updated=false, got true")
	}
	if rec.errMsg != "" {
		t.Errorf("expected empty errMsg, got %q", rec.errMsg)
	}
	if rec.hash != "old-hash" {
		t.Errorf("expected hash=old-hash, got %q", rec.hash)
	}
}

func TestRunCheck_SinglePayload_HappyPath(t *testing.T) {
	// Plugin that returns one payload, then signals "no pending" — the
	// shape of every non-LostFilm tracker.
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{check: &domain.Check{Hash: "new-hash"}, err: nil},
		},
		downloads: []downloadResult{
			{payload: &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:abc"}, err: nil},
			{err: registry.ErrNoPendingEpisodes},
		},
	}
	f := newFixture(t, tr, false)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if got := f.clientPlugin.addCalls; got != 1 {
		t.Errorf("expected 1 client Add call, got %d", got)
	}
	rec := f.lastRecord(t)
	if !rec.updated {
		t.Errorf("expected updated=true, got false")
	}
	if rec.errMsg != "" {
		t.Errorf("expected empty errMsg, got %q", rec.errMsg)
	}
	if rec.hash != "new-hash" {
		t.Errorf("expected hash=new-hash, got %q", rec.hash)
	}
}

func TestRunCheck_SinglePayload_RecordsDelivery(t *testing.T) {
	const hash = "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{check: &domain.Check{Hash: "new-hash"}, err: nil},
		},
		downloads: []downloadResult{
			{payload: &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:" + hash}, err: nil},
			{err: registry.ErrNoPendingEpisodes},
		},
	}
	f := newFixture(t, tr, false)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if got := len(f.deliveries.recorded); got != 1 {
		t.Fatalf("expected 1 delivery recorded, got %d", got)
	}
	d := f.deliveries.recorded[0]
	if d.Infohash != hash {
		t.Errorf("infohash = %q, want %q", d.Infohash, hash)
	}
	// Single-payload topics label the delivery with the topic display name.
	if d.Label != "Fake Topic" {
		t.Errorf("label = %q, want the topic display name", d.Label)
	}
	if d.TopicID != f.topic.ID {
		t.Errorf("topic id mismatch")
	}
}

func TestRunCheck_SinglePayload_NotifiesUpdated(t *testing.T) {
	const hash = "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{check: &domain.Check{Hash: "new-hash"}, err: nil},
		},
		downloads: []downloadResult{
			{payload: &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:" + hash}, err: nil},
			{err: registry.ErrNoPendingEpisodes},
		},
	}
	f := newFixture(t, tr, false)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if f.notifier.calls != 1 {
		t.Fatalf("expected 1 notification, got %d", f.notifier.calls)
	}
	if f.notifier.lastEvent != "updated" {
		t.Errorf("event = %q, want updated", f.notifier.lastEvent)
	}
	if f.notifier.lastID != f.topic.UserID {
		t.Errorf("notification sent to wrong user")
	}
	// Single-payload topics summarise with the topic display name.
	if !strings.Contains(f.notifier.lastMsg.Body, "Fake Topic") {
		t.Errorf("body = %q, want it to mention the topic name", f.notifier.lastMsg.Body)
	}
	// The notification fires when the torrent is handed to the client (download
	// START), not when it finishes. The body must not claim completion.
	if strings.Contains(f.notifier.lastMsg.Body, "Downloaded") {
		t.Errorf("body = %q, must not claim the torrent finished downloading", f.notifier.lastMsg.Body)
	}
	if !strings.Contains(f.notifier.lastMsg.Body, "Sent to client") {
		t.Errorf("body = %q, want it to say the release was sent to the client", f.notifier.lastMsg.Body)
	}
}

func TestRunCheck_NoDownload_DoesNotNotify(t *testing.T) {
	tr := &fakeTracker{
		name:   "faketracker",
		checks: []checkResult{{check: &domain.Check{Hash: "old-hash"}, err: nil}}, // unchanged
	}
	f := newFixture(t, tr, false)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if f.notifier.calls != 0 {
		t.Errorf("expected no notification when nothing downloaded, got %d", f.notifier.calls)
	}
}

func TestRunCheck_Episodes_NotifiesWithEpisodeLabels(t *testing.T) {
	const h1 = "1111111111111111111111111111111111111111"
	const h2 = "2222222222222222222222222222222222222222"
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{
				check: &domain.Check{
					Hash: "new-hash",
					Extra: map[string]any{
						"pending_episodes": []string{"791001001", "791001002"},
						"pending_human":    []string{"s01e01", "s01e02"},
					},
				},
			},
		},
		downloads: []downloadResult{
			{payload: &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:" + h1}},
			{payload: &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:" + h2}},
			{err: registry.ErrNoPendingEpisodes},
		},
	}
	f := newFixture(t, tr, true)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if f.notifier.calls != 1 {
		t.Fatalf("expected 1 summary notification, got %d", f.notifier.calls)
	}
	body := f.notifier.lastMsg.Body
	if !strings.Contains(body, "s01e01") || !strings.Contains(body, "s01e02") {
		t.Errorf("body = %q, want both episode labels", body)
	}
}

func TestRunCheck_Episodes_RecordsDeliveryWithEpisodeLabel(t *testing.T) {
	const h1 = "1111111111111111111111111111111111111111"
	const h2 = "2222222222222222222222222222222222222222"
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{
				check: &domain.Check{
					Hash: "new-hash",
					Extra: map[string]any{
						"pending_episodes": []string{"791001001", "791001002"},
						"pending_human":    []string{"s01e01", "s01e02"},
					},
				},
			},
		},
		downloads: []downloadResult{
			{payload: &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:" + h1}},
			{payload: &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:" + h2}},
			{err: registry.ErrNoPendingEpisodes},
		},
	}
	f := newFixture(t, tr, true)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if got := len(f.deliveries.recorded); got != 2 {
		t.Fatalf("expected 2 deliveries recorded, got %d", got)
	}
	// Labels come from pending_human, aligned with the packed list.
	want := []struct{ hash, label string }{
		{h1, "s01e01"},
		{h2, "s01e02"},
	}
	for i, w := range want {
		d := f.deliveries.recorded[i]
		if d.Infohash != w.hash {
			t.Errorf("delivery %d infohash = %q, want %q", i, d.Infohash, w.hash)
		}
		if d.Label != w.label {
			t.Errorf("delivery %d label = %q, want %q", i, d.Label, w.label)
		}
	}
}

func TestRunCheck_ThreePendingEpisodes(t *testing.T) {
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{
				check: &domain.Check{
					Hash: "new-hash",
					Extra: map[string]any{
						"pending_episodes": []string{"S01E01", "S01E02", "S01E03"},
					},
				},
			},
		},
		downloads: []downloadResult{
			{payload: &domain.Payload{MagnetURI: "magnet:1"}},
			{payload: &domain.Payload{MagnetURI: "magnet:2"}},
			{payload: &domain.Payload{MagnetURI: "magnet:3"}},
			{err: registry.ErrNoPendingEpisodes},
		},
	}
	f := newFixture(t, tr, true)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if got := f.clientPlugin.addCalls; got != 3 {
		t.Errorf("expected 3 client Add calls, got %d", got)
	}
	if got := tr.callsDownload; got != 3 {
		t.Errorf("expected 3 Download calls, got %d", got)
	}
	if got := len(f.atomicTopics.markCalls); got != 3 {
		t.Errorf("expected 3 MarkEpisodeDownloaded calls, got %d", got)
	}
	wantPacked := []string{"S01E01", "S01E02", "S01E03"}
	for i, w := range wantPacked {
		if f.atomicTopics.markCalls[i].packed != w {
			t.Errorf("mark call %d: got %q, want %q", i, f.atomicTopics.markCalls[i].packed, w)
		}
	}
	rec := f.lastRecord(t)
	if !rec.updated {
		t.Errorf("expected updated=true, got false")
	}
	if rec.errMsg != "" {
		t.Errorf("expected empty errMsg, got %q", rec.errMsg)
	}
}

func TestRunCheck_FirstDownloadError(t *testing.T) {
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{check: &domain.Check{Hash: "new-hash"}, err: nil},
		},
		downloads: []downloadResult{
			{err: errors.New("connection refused")},
		},
	}
	f := newFixture(t, tr, false)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if f.clientPlugin.addCalls != 0 {
		t.Errorf("expected 0 client Add calls, got %d", f.clientPlugin.addCalls)
	}
	rec := f.lastRecord(t)
	if rec.updated {
		t.Errorf("expected updated=false (no progress), got true")
	}
	if rec.errMsg == "" {
		t.Errorf("expected non-empty errMsg")
	}
	// The hash must NOT advance on a failed download: a transient failure
	// would otherwise strand the topic permanently (next check sees no
	// change and never retries).
	if rec.hash != "old-hash" {
		t.Errorf("expected hash to stay old-hash (not advance on failure), got %q", rec.hash)
	}
}

func TestRunCheck_MidLoopDownloadError(t *testing.T) {
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{
				check: &domain.Check{
					Hash: "new-hash",
					Extra: map[string]any{
						"pending_episodes": []string{"S01E01", "S01E02", "S01E03"},
					},
				},
			},
		},
		downloads: []downloadResult{
			{payload: &domain.Payload{MagnetURI: "magnet:1"}},
			{payload: &domain.Payload{MagnetURI: "magnet:2"}},
			{err: errors.New("network blip")},
		},
	}
	f := newFixture(t, tr, true)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if got := f.clientPlugin.addCalls; got != 2 {
		t.Errorf("expected 2 client Add calls, got %d", got)
	}
	if got := len(f.atomicTopics.markCalls); got != 2 {
		t.Errorf("expected 2 MarkEpisodeDownloaded calls, got %d", got)
	}
	rec := f.lastRecord(t)
	if !rec.updated {
		t.Errorf("expected updated=true (mid-loop progress preserved), got false")
	}
	if rec.errMsg == "" {
		t.Errorf("expected non-empty errMsg")
	}
	// Hash stays at the old value so the remaining (undownloaded) episode
	// is re-detected and retried on the next check rather than stranded.
	if rec.hash != "old-hash" {
		t.Errorf("expected hash to stay old-hash so the remaining episode retries, got %q", rec.hash)
	}
}

// TestRunCheck_CaughtUpNoPending covers a legitimate no-op: the hash
// changed (so the topic looks "updated") but the very first Download
// returns ErrNoPendingEpisodes because every episode is already
// downloaded or excluded by the start filter. This must be treated as a
// graceful no-op — no error, and the hash advances to the now-current
// state — not as a first-iteration download failure.
func TestRunCheck_CaughtUpNoPending(t *testing.T) {
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{check: &domain.Check{Hash: "new-hash", Extra: map[string]any{}}, err: nil},
		},
		downloads: []downloadResult{
			{err: registry.ErrNoPendingEpisodes},
		},
	}
	f := newFixture(t, tr, false)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if f.clientPlugin.addCalls != 0 {
		t.Errorf("expected 0 client Add calls, got %d", f.clientPlugin.addCalls)
	}
	rec := f.lastRecord(t)
	if rec.errMsg != "" {
		t.Errorf("expected empty errMsg (graceful no-op, not a download failure), got %q", rec.errMsg)
	}
	// The hash DOES advance here: there is genuinely nothing pending, so
	// the new state is fully processed and must not be re-checked as "new".
	if rec.hash != "new-hash" {
		t.Errorf("expected hash to advance to new-hash, got %q", rec.hash)
	}
}

func TestRunCheck_PersistFailureMidLoop(t *testing.T) {
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{
				check: &domain.Check{
					Hash: "new-hash",
					Extra: map[string]any{
						"pending_episodes": []string{"S01E01", "S01E02", "S01E03"},
					},
				},
			},
		},
		downloads: []downloadResult{
			{payload: &domain.Payload{MagnetURI: "magnet:1"}},
			{payload: &domain.Payload{MagnetURI: "magnet:2"}},
			{payload: &domain.Payload{MagnetURI: "magnet:3"}},
		},
	}
	f := newFixture(t, tr, true)
	// Fail the SECOND mark call. The submit succeeded, so anySubmitted
	// should be true and the recorded result should reflect "updated".
	f.atomicTopics.markErr = errors.New("db down")

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if got := f.clientPlugin.addCalls; got != 1 {
		t.Errorf("expected 1 client Add call before persist failure, got %d", got)
	}
	rec := f.lastRecord(t)
	if !rec.updated {
		t.Errorf("expected updated=true (1 successful submit before persist failure), got false")
	}
	if rec.errMsg == "" {
		t.Errorf("expected non-empty errMsg from persist failure")
	}
}

func TestRunCheck_HitsMaxPerTick(t *testing.T) {
	// Build a downloads slice big enough to exceed the cap.
	const cap = 25
	downloads := make([]downloadResult, cap+5)
	for i := range downloads {
		downloads[i] = downloadResult{payload: &domain.Payload{MagnetURI: "magnet"}}
	}

	pending := make([]string, cap+5)
	for i := range pending {
		pending[i] = "S01E01"
	}

	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{
				check: &domain.Check{
					Hash: "new-hash",
					Extra: map[string]any{
						"pending_episodes": pending,
					},
				},
			},
		},
		downloads: downloads,
	}
	f := newFixture(t, tr, true)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if got := f.clientPlugin.addCalls; got != cap {
		t.Errorf("expected exactly %d client Add calls (capped), got %d", cap, got)
	}
	if got := len(f.atomicTopics.markCalls); got != cap {
		t.Errorf("expected exactly %d MarkEpisodeDownloaded calls, got %d", cap, got)
	}
	rec := f.lastRecord(t)
	if !rec.updated {
		t.Errorf("expected updated=true after capped run, got false")
	}
	if rec.errMsg != "" {
		t.Errorf("expected empty errMsg on capped run, got %q", rec.errMsg)
	}
}

func TestRunCheck_NonAtomicFallback(t *testing.T) {
	// Verifies the fallback path: when topicsRepo does NOT implement
	// markEpisodeDownloader, the scheduler should call UpdateExtra
	// with downloaded_episodes appended.
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{
				check: &domain.Check{
					Hash: "new-hash",
					Extra: map[string]any{
						"pending_episodes": []string{"S01E01", "S01E02"},
					},
				},
			},
		},
		downloads: []downloadResult{
			{payload: &domain.Payload{MagnetURI: "magnet:1"}},
			{payload: &domain.Payload{MagnetURI: "magnet:2"}},
		},
	}
	f := newFixture(t, tr, false)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if got := f.clientPlugin.addCalls; got != 2 {
		t.Errorf("expected 2 client Add calls, got %d", got)
	}
	if got := len(f.topics.updateExtraCalls); got != 2 {
		t.Errorf("expected 2 UpdateExtra calls in fallback path, got %d", got)
	}
}

func TestBackoff_TableTest(t *testing.T) {
	cfg := &config.Config{
		CheckMaxBackoff: 6 * time.Hour,
	}
	s := &Scheduler{cfg: cfg}

	const interval = 60 // seconds
	base := time.Duration(interval) * time.Second
	tests := []struct {
		name              string
		consecutiveErrors int
		failure           bool
		cause             error
		minBackoff        time.Duration
		maxBackoff        time.Duration
		expectCapped      bool
	}{
		{
			name:              "success resets to interval",
			consecutiveErrors: 5,
			failure:           false,
			minBackoff:        base,
			maxBackoff:        base + 50*time.Millisecond,
		},
		{
			name:              "transient error retries fast (60s), not exponential",
			consecutiveErrors: 3,
			failure:           true,
			cause:             errors.New(`Get "https://x/t": context deadline exceeded`),
			minBackoff:        transientRetryDelay,
			maxBackoff:        transientRetryDelay + 50*time.Millisecond,
		},
		{
			// At transientRetryMax consecutive failures the fast-retry stops
			// and exponential backoff resumes: 2^(5+1) * 60s = 64m (< 6h cap).
			name:              "transient error falls back to exponential once persistent",
			consecutiveErrors: transientRetryMax,
			failure:           true,
			cause:             errors.New("dial tcp: i/o timeout"),
			minBackoff:        64 * base,
			maxBackoff:        64*base + 50*time.Millisecond,
		},
		{
			name:              "first failure: 2x base",
			consecutiveErrors: 0,
			failure:           true,
			minBackoff:        2 * base,
			maxBackoff:        2*base + 50*time.Millisecond,
		},
		{
			name:              "second failure: 4x base",
			consecutiveErrors: 1,
			failure:           true,
			minBackoff:        4 * base,
			maxBackoff:        4*base + 50*time.Millisecond,
		},
		{
			name:              "third failure: 8x base",
			consecutiveErrors: 2,
			failure:           true,
			minBackoff:        8 * base,
			maxBackoff:        8*base + 50*time.Millisecond,
		},
		{
			name:              "fourth failure: 16x base",
			consecutiveErrors: 3,
			failure:           true,
			minBackoff:        16 * base,
			maxBackoff:        16*base + 50*time.Millisecond,
		},
		{
			name:              "fifth failure: 32x base",
			consecutiveErrors: 4,
			failure:           true,
			minBackoff:        32 * base,
			maxBackoff:        32*base + 50*time.Millisecond,
		},
		{
			name:              "many failures: capped at 6h",
			consecutiveErrors: 20,
			failure:           true,
			minBackoff:        6 * time.Hour,
			maxBackoff:        6*time.Hour + 50*time.Millisecond,
			expectCapped:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			topic := &domain.Topic{
				CheckIntervalSec:  interval,
				ConsecutiveErrors: tt.consecutiveErrors,
			}
			before := time.Now().UTC()
			got := s.backoff(topic, tt.failure, tt.cause)
			delta := got.Sub(before)
			if delta < tt.minBackoff {
				t.Errorf("backoff = %v, want >= %v", delta, tt.minBackoff)
			}
			if delta > tt.maxBackoff {
				t.Errorf("backoff = %v, want <= %v", delta, tt.maxBackoff)
			}
			if tt.expectCapped && delta != 6*time.Hour && delta < 6*time.Hour {
				t.Errorf("expected backoff to be capped at 6h, got %v", delta)
			}
		})
	}
}

// fakeTrackerWithCreds wraps fakeTracker and additionally implements
// registry.WithCredentials so that loadCredentials calls Login.
type fakeTrackerWithCreds struct {
	fakeTracker
	loginErr error
}

func (f *fakeTrackerWithCreds) Login(_ context.Context, _ *domain.TrackerCredential) error {
	return f.loginErr
}

func (f *fakeTrackerWithCreds) Verify(_ context.Context, _ *domain.TrackerCredential) (bool, error) {
	return true, nil
}

// newSessionFixture builds a scheduler wired for session-expiry tests.
// creds is injected as the credentials repo; notifier is the fake notifier.
func newSessionFixture(t *testing.T, creds credentialsRepo, notifier eventNotifier) (*Scheduler, *domain.Topic) {
	t.Helper()
	cfg := &config.Config{
		SchedulerEnabled:            true,
		SchedulerWorkers:            1,
		SchedulerTick:               time.Second,
		SchedulerMaxEpisodesPerTick: 25,
		TrackerHTTPTimeout:          5 * time.Second,
		CheckMaxBackoff:             time.Hour,
		PublicBaseURL:               "http://localhost:34080",
	}
	s := &Scheduler{
		cfg:    cfg,
		log:    zerolog.New(io.Discard),
		topics: &fakeTopics{},
		clients: &fakeClients{client: &domain.Client{
			ID:         uuid.New(),
			ClientName: "fakeclient",
		}},
		creds:         creds,
		master:        &fakeDecryptor{},
		notifier:      notifier,
		lookupTracker: func(_ string) registry.Tracker { return nil },
		lookupClient:  func(_ string) registry.Client { return nil },
		jobs:          make(chan *domain.Topic, 1),
		stop:          make(chan struct{}),
		ready:         make(chan struct{}),
	}
	topic := &domain.Topic{
		ID:               uuid.New(),
		UserID:           uuid.New(),
		TrackerName:      "lostfilm",
		URL:              "https://example.com/topic/1",
		CheckIntervalSec: 900,
		Status:           domain.TopicStatusActive,
		LastHash:         "old-hash",
		Extra:            map[string]any{},
	}
	return s, topic
}

// TestLoadCredentials_SessionExpired_WonTransition verifies that when
// Login returns ErrSessionExpired, the credential is not yet flagged
// (SessionExpiredAt == nil), and the atomic UPDATE wins the NULL->now()
// transition (MarkSessionExpired returns true), MarkSessionExpired and
// Send are each called exactly once.
func TestLoadCredentials_SessionExpired_WonTransition(t *testing.T) {
	tr := &fakeTrackerWithCreds{
		fakeTracker: fakeTracker{name: "lostfilm"},
		loginErr:    fmt.Errorf("lostfilm: %w", registry.ErrSessionExpired),
	}

	storedCred := &domain.TrackerCredential{
		ID:               uuid.New(),
		UserID:           uuid.New(),
		TrackerName:      "lostfilm",
		SessionExpiredAt: nil, // not yet flagged
		SecretEnc:        []byte("secret"),
	}
	creds := &fakeCredsSession{stored: storedCred, markExpiredWon: true}
	notifier := &fakeNotifier{}

	s, topic := newSessionFixture(t, creds, notifier)
	topic.UserID = storedCred.UserID
	s.lookupTracker = func(_ string) registry.Tracker { return tr }

	ctx := context.Background()
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	log := zerolog.New(io.Discard)

	_, ok := s.loadCredentials(ctx, checkCtx, log, topic, tr)

	if ok {
		t.Fatal("expected loadCredentials to return ok=false on login error")
	}
	if got := creds.markExpiredCalls; got != 1 {
		t.Errorf("MarkSessionExpired: want 1 call, got %d", got)
	}
	if got := notifier.calls; got != 1 {
		t.Errorf("notifier.Send: want 1 call, got %d", got)
	}
	if notifier.lastID != storedCred.UserID {
		t.Errorf("notifier.Send userID: want %s, got %s", storedCred.UserID, notifier.lastID)
	}
}

// TestLoadCredentials_SessionExpired_LostRace simulates the concurrent-dedup
// case: the cheap fast-path snapshot still shows SessionExpiredAt == nil (a
// stale read shared across topics on one credential), so the check attempts
// the UPDATE — but another concurrent check already transitioned the row, so
// the atomic UPDATE...WHERE IS NULL affects 0 rows and MarkSessionExpired
// returns false. This check must NOT notify even though it saw a nil snapshot
// and Login returned ErrSessionExpired.
func TestLoadCredentials_SessionExpired_LostRace(t *testing.T) {
	tr := &fakeTrackerWithCreds{
		fakeTracker: fakeTracker{name: "lostfilm"},
		loginErr:    fmt.Errorf("lostfilm: %w", registry.ErrSessionExpired),
	}

	storedCred := &domain.TrackerCredential{
		ID:               uuid.New(),
		UserID:           uuid.New(),
		TrackerName:      "lostfilm",
		SessionExpiredAt: nil, // stale snapshot — looked nil to this check
		SecretEnc:        []byte("secret"),
	}
	creds := &fakeCredsSession{stored: storedCred, markExpiredWon: false}
	notifier := &fakeNotifier{}

	s, topic := newSessionFixture(t, creds, notifier)
	topic.UserID = storedCred.UserID
	s.lookupTracker = func(_ string) registry.Tracker { return tr }

	ctx := context.Background()
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	log := zerolog.New(io.Discard)

	_, ok := s.loadCredentials(ctx, checkCtx, log, topic, tr)

	if ok {
		t.Fatal("expected loadCredentials to return ok=false on login error")
	}
	if got := creds.markExpiredCalls; got != 1 {
		t.Errorf("MarkSessionExpired: want 1 attempt (the UPDATE is the gate), got %d", got)
	}
	if got := notifier.calls; got != 0 {
		t.Errorf("notifier.Send: want 0 calls (lost the transition race), got %d", got)
	}
}

// TestLoadCredentials_SessionExpired_AlreadyFlagged verifies that when
// Login returns ErrSessionExpired but the credential is already flagged
// (SessionExpiredAt != nil), neither MarkSessionExpired nor Send is called
// again.
func TestLoadCredentials_SessionExpired_AlreadyFlagged(t *testing.T) {
	tr := &fakeTrackerWithCreds{
		fakeTracker: fakeTracker{name: "lostfilm"},
		loginErr:    fmt.Errorf("lostfilm: %w", registry.ErrSessionExpired),
	}

	now := time.Now()
	storedCred := &domain.TrackerCredential{
		ID:               uuid.New(),
		UserID:           uuid.New(),
		TrackerName:      "lostfilm",
		SessionExpiredAt: &now, // already flagged — must not re-notify
		SecretEnc:        []byte("secret"),
	}
	creds := &fakeCredsSession{stored: storedCred}
	notifier := &fakeNotifier{}

	s, topic := newSessionFixture(t, creds, notifier)
	topic.UserID = storedCred.UserID
	s.lookupTracker = func(_ string) registry.Tracker { return tr }

	ctx := context.Background()
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	log := zerolog.New(io.Discard)

	_, ok := s.loadCredentials(ctx, checkCtx, log, topic, tr)

	if ok {
		t.Fatal("expected loadCredentials to return ok=false on login error")
	}
	if got := creds.markExpiredCalls; got != 0 {
		t.Errorf("MarkSessionExpired: want 0 calls (already flagged), got %d", got)
	}
	if got := notifier.calls; got != 0 {
		t.Errorf("notifier.Send: want 0 calls (already flagged), got %d", got)
	}
}

// TestNotifyUpdated_RoutesToTopicNotifier verifies notifyUpdated forwards
// the topic's NotifierID to the dispatcher so a per-topic override is honoured.
func TestNotifyUpdated_RoutesToTopicNotifier(t *testing.T) {
	notifier := &fakeNotifier{}
	s := &Scheduler{cfg: &config.Config{PublicBaseURL: "http://x"}, notifier: notifier}

	notifierID := uuid.New()
	topic := &domain.Topic{UserID: uuid.New(), DisplayName: "My Show", NotifierID: &notifierID}

	s.notifyUpdated(context.Background(), topic, []string{"s01e01"})

	if notifier.calls != 1 {
		t.Fatalf("want 1 notification, got %d", notifier.calls)
	}
	if notifier.lastEvent != "updated" {
		t.Errorf("event = %q, want updated", notifier.lastEvent)
	}
	if notifier.lastNotifierID == nil || *notifier.lastNotifierID != notifierID {
		t.Errorf("notifierID = %v, want %s", notifier.lastNotifierID, notifierID)
	}
}

// TestNotifyUpdated_NilNotifierID_GlobalFanOut verifies a topic without an
// override passes nil through (global fan-out, unchanged behaviour).
func TestNotifyUpdated_NilNotifierID_GlobalFanOut(t *testing.T) {
	notifier := &fakeNotifier{}
	s := &Scheduler{cfg: &config.Config{PublicBaseURL: "http://x"}, notifier: notifier}

	topic := &domain.Topic{UserID: uuid.New(), DisplayName: "My Show", NotifierID: nil}

	s.notifyUpdated(context.Background(), topic, []string{"s01e01"})

	if notifier.calls != 1 {
		t.Fatalf("want 1 notification, got %d", notifier.calls)
	}
	if notifier.lastNotifierID != nil {
		t.Errorf("notifierID = %v, want nil", notifier.lastNotifierID)
	}
}

func TestIsNoPendingError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unrelated error", errors.New("oops"), false},
		{"typed sentinel", registry.ErrNoPendingEpisodes, true},
		{"wrapped sentinel", errors.Join(errors.New("ctx"), registry.ErrNoPendingEpisodes), true},
		{"fmt-wrapped sentinel", fmt.Errorf("lostfilm: %w", registry.ErrNoPendingEpisodes), true},
		{"untyped substring no longer matches", errors.New("foo: no pending episodes bar"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNoPendingError(tt.err); got != tt.want {
				t.Errorf("isNoPendingError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
