package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
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

func (f *fakeTracker) Name() string                     { return f.name }
func (f *fakeTracker) DisplayName() string              { return f.name }
func (f *fakeTracker) CanParse(_ string) bool           { return true }
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
	recordCalls          []recordCall
	updateExtraCalls     []updateExtraCall
	markCalls            []markCall
	markErr              error
	updateExtraErr       error
	implementMarkAtomic  bool // when true, the test exercises the atomic path
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

// fakeDecryptor returns its input unchanged.
type fakeDecryptor struct{}

func (f *fakeDecryptor) Decrypt(ct, _ []byte) ([]byte, error) { return ct, nil }

// fakeClientPlugin satisfies registry.Client and records every Add call.
type fakeClientPlugin struct {
	name      string
	addCalls  int
	addErr    error
	lastOpts  domain.AddOptions
}

func (f *fakeClientPlugin) Name() string                       { return f.name }
func (f *fakeClientPlugin) DisplayName() string                { return f.name }
func (f *fakeClientPlugin) ConfigSchema() map[string]any       { return nil }
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

	s := &Scheduler{
		cfg:           cfg,
		log:           zerolog.New(io.Discard),
		topics:        topicsImpl,
		clients:       &fakeClients{client: client},
		creds:         &fakeCreds{},
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
			got := s.backoff(topic, tt.failure)
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
