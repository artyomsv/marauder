package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/config"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/events"
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
	// episodic makes the tracker advertise registry.WithEpisodeFilter, so the
	// scheduler treats it as a per-episode tracker (replace-on-update is then
	// skipped to protect sibling episodes — issue #101).
	episodic bool

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

// SupportsEpisodeFilter makes *fakeTracker satisfy registry.WithEpisodeFilter.
// It reports true only when the test opted in via the episodic flag.
func (f *fakeTracker) SupportsEpisodeFilter() bool { return f.episodic }

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
	recordCalls            []recordCall
	updateExtraCalls       []updateExtraCall
	updateDisplayNameCalls []updateDisplayNameCall
	markCalls              []markCall
	markErr                error
	updateExtraErr         error
	implementMarkAtomic    bool // when true, the test exercises the atomic path
}

type recordCall struct {
	id          uuid.UUID
	hash        string
	updated     bool
	nextCheckAt time.Time
	errMsg      string
	errCode     string
}

type updateExtraCall struct {
	id    uuid.UUID
	extra map[string]any
}

type markCall struct {
	id     uuid.UUID
	packed string
}

type updateDisplayNameCall struct {
	id   uuid.UUID
	name string
}

func (f *fakeTopics) DueForCheck(_ context.Context, _ int) ([]*domain.Topic, error) {
	return nil, nil
}

func (f *fakeTopics) RecordCheckResult(_ context.Context, id uuid.UUID, hash string, updated bool, nextCheckAt time.Time, errMsg, errCode string) error {
	f.recordCalls = append(f.recordCalls, recordCall{id, hash, updated, nextCheckAt, errMsg, errCode})
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

func (f *fakeTopics) UpdateDisplayName(_ context.Context, id uuid.UUID, name string) error {
	f.updateDisplayNameCalls = append(f.updateDisplayNameCalls, updateDisplayNameCall{id, name})
	return nil
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
	client       *domain.Client
	getByIDCalls []uuid.UUID
}

func (f *fakeClients) GetByID(_ context.Context, id uuid.UUID, _ uuid.UUID) (*domain.Client, error) {
	f.getByIDCalls = append(f.getByIDCalls, id)
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

// fakeEmitter records every Emit call for assertions.
type fakeEmitter struct {
	mu     sync.Mutex
	events []events.Event
}

func (f *fakeEmitter) Emit(_ context.Context, ev events.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
}

func (f *fakeEmitter) types() []events.Type {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []events.Type
	for _, e := range f.events {
		out = append(out, e.Type)
	}
	return out
}

func (f *fakeEmitter) ofType(tp events.Type) []events.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []events.Event
	for _, e := range f.events {
		if e.Type == tp {
			out = append(out, e)
		}
	}
	return out
}

func (f *fakeEmitter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

// fakeDecryptor returns its input unchanged.
type fakeDecryptor struct{}

func (f *fakeDecryptor) Decrypt(ct, _ []byte) ([]byte, error) { return ct, nil }

// fakeDeliveries records every delivery the scheduler logs and serves the
// configured prior deliveries for the replace-on-update path.
type fakeDeliveries struct {
	recorded []*domain.TopicDelivery
	err      error

	// prior is returned by ListForTopic (the pre-update snapshot).
	prior   []*domain.TopicDelivery
	listErr error
	// deletedHashes captures the hashes passed to DeleteByInfohashes.
	deletedHashes []string
}

func (f *fakeDeliveries) Record(_ context.Context, d *domain.TopicDelivery) (bool, error) {
	f.recorded = append(f.recorded, d)
	return f.err == nil, f.err
}

func (f *fakeDeliveries) ListForTopic(_ context.Context, _ uuid.UUID) ([]*domain.TopicDelivery, error) {
	return f.prior, f.listErr
}

func (f *fakeDeliveries) DeleteByInfohashes(_ context.Context, _ uuid.UUID, hashes []string) (int64, error) {
	f.deletedHashes = append(f.deletedHashes, hashes...)
	return int64(len(hashes)), nil
}

// fakeClientPlugin satisfies registry.Client (and registry.WithRemoval) and
// records every Add / Remove call.
type fakeClientPlugin struct {
	name     string
	addCalls int
	addErr   error
	lastOpts domain.AddOptions

	// Remove tracking for the replace-on-update path.
	removeCalls      int
	removeHashes     []string
	removeDeleteData bool
	removeErr        error
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

func (f *fakeClientPlugin) Remove(_ context.Context, _ []byte, hashes []string, deleteData bool) error {
	f.removeCalls++
	f.removeHashes = append(f.removeHashes, hashes...)
	f.removeDeleteData = deleteData
	return f.removeErr
}

// --- Test setup helpers ------------------------------------------------

type fixture struct {
	s            *Scheduler
	topics       *fakeTopics
	atomicTopics *fakeTopicsAtomic
	clientPlugin *fakeClientPlugin
	deliveries   *fakeDeliveries
	emitter      *fakeEmitter
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
	emit := &fakeEmitter{}
	s := &Scheduler{
		cfg:           cfg,
		log:           zerolog.New(io.Discard),
		topics:        topicsImpl,
		clients:       &fakeClients{client: client},
		creds:         &fakeCreds{},
		deliveries:    deliveries,
		emit:          emit,
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
		emitter:      emit,
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

	evs := f.emitter.ofType(events.DownloadSubmitted)
	if len(evs) != 1 {
		t.Fatalf("expected 1 download.submitted event, got %d", len(evs))
	}
	ev := evs[0]
	if ev.UserID != f.topic.UserID {
		t.Errorf("event UserID mismatch")
	}
	// The topic name lives in the event Title; the body must NOT repeat it
	// (single-release labels equal the display name, and the repetition
	// made notifications a wall of text).
	if ev.Title != "Fake Topic" {
		t.Errorf("title = %q, want the topic display name", ev.Title)
	}
	if strings.Contains(ev.Body, "Fake Topic") {
		t.Errorf("body = %q, must not repeat the topic name", ev.Body)
	}
	// The notification fires when the torrent is handed to the client (download
	// START), not when it finishes. The body must not claim completion.
	if strings.Contains(ev.Body, "Downloaded") {
		t.Errorf("body = %q, must not claim the torrent finished downloading", ev.Body)
	}
	if !strings.Contains(ev.Body, "Sent to client") {
		t.Errorf("body = %q, want it to say the release was sent to the client", ev.Body)
	}
}

func TestRunCheck_NoDownload_DoesNotNotify(t *testing.T) {
	tr := &fakeTracker{
		name:   "faketracker",
		checks: []checkResult{{check: &domain.Check{Hash: "old-hash"}, err: nil}}, // unchanged
	}
	f := newFixture(t, tr, false)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if evs := f.emitter.ofType(events.DownloadSubmitted); len(evs) != 0 {
		t.Errorf("expected no download.submitted event when nothing downloaded, got %d", len(evs))
	}
}

func TestRunCheck_CheckError_NotifiesError(t *testing.T) {
	tr := &fakeTracker{
		name:   "faketracker",
		checks: []checkResult{{err: errors.New("tracker boom")}},
	}
	f := newFixture(t, tr, false)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	evs := f.emitter.ofType(events.CheckFailed)
	if len(evs) != 1 {
		t.Fatalf("expected 1 check.failed event on first failure, got %d", len(evs))
	}
	ev := evs[0]
	if ev.UserID != f.topic.UserID {
		t.Errorf("check.failed event UserID mismatch")
	}
	if ev.NotifierID != f.topic.NotifierID {
		t.Errorf("check.failed event did not carry the topic's notifier override")
	}
	if !strings.Contains(ev.Body, "tracker boom") {
		t.Errorf("body = %q, want it to include the underlying error", ev.Body)
	}
	if ev.SourceURL != f.topic.URL {
		t.Errorf("SourceURL = %q, want the topic's tracker URL %q", ev.SourceURL, f.topic.URL)
	}
}

// TestRunCheck_NewRelease_EventsCarrySourceURL asserts that release.found and
// download.submitted carry the topic's original tracker URL so notifiers can
// render a "Source:" link next to the Marauder one (issue #109).
func TestRunCheck_NewRelease_EventsCarrySourceURL(t *testing.T) {
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

	for _, typ := range []events.Type{events.ReleaseFound, events.DownloadSubmitted} {
		evs := f.emitter.ofType(typ)
		if len(evs) != 1 {
			t.Fatalf("expected 1 %s event, got %d", typ, len(evs))
		}
		if evs[0].SourceURL != f.topic.URL {
			t.Errorf("%s SourceURL = %q, want the topic's tracker URL %q", typ, evs[0].SourceURL, f.topic.URL)
		}
	}
}

// fakeTrackerWithComment is a fakeTracker that also implements the
// author-comment capability (issue #110).
type fakeTrackerWithComment struct {
	fakeTracker
	comment      string
	commentErr   error
	commentCalls int
	commentURL   string
}

func (f *fakeTrackerWithComment) AuthorComment(_ context.Context, rawURL string, _ *domain.TrackerCredential) (string, error) {
	f.commentCalls++
	f.commentURL = rawURL
	return f.comment, f.commentErr
}

// newCommentFixture wires a fixture whose tracker also serves author comments.
func newCommentFixture(t *testing.T, comment string, commentErr error) (*fixture, *fakeTrackerWithComment) {
	t.Helper()
	const hash = "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	tr := &fakeTrackerWithComment{
		fakeTracker: fakeTracker{
			name:   "faketracker",
			checks: []checkResult{{check: &domain.Check{Hash: "new-hash"}}},
			downloads: []downloadResult{
				{payload: &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:" + hash}},
				{err: registry.ErrNoPendingEpisodes},
			},
		},
		comment:    comment,
		commentErr: commentErr,
	}
	f := newFixture(t, &tr.fakeTracker, false)
	f.s.lookupTracker = func(string) registry.Tracker { return tr }
	return f, tr
}

// TestRunCheck_NewRelease_EventsCarryAuthorComment asserts the author's
// latest tracker comment is fetched once per detected update and stamped
// onto both notifiable update events (issue #110).
func TestRunCheck_NewRelease_EventsCarryAuthorComment(t *testing.T) {
	f, tr := newCommentFixture(t, "Added episode 8.", nil)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if tr.commentCalls != 1 {
		t.Errorf("AuthorComment calls = %d, want exactly 1 per update", tr.commentCalls)
	}
	if tr.commentURL != f.topic.URL {
		t.Errorf("AuthorComment URL = %q, want the topic URL %q", tr.commentURL, f.topic.URL)
	}
	for _, typ := range []events.Type{events.ReleaseFound, events.DownloadSubmitted} {
		evs := f.emitter.ofType(typ)
		if len(evs) != 1 {
			t.Fatalf("expected 1 %s event, got %d", typ, len(evs))
		}
		if evs[0].AuthorComment != "Added episode 8." {
			t.Errorf("%s AuthorComment = %q, want the author's comment", typ, evs[0].AuthorComment)
		}
	}
}

// TestRunCheck_AuthorCommentError_FailOpen asserts a comment-fetch failure
// never fails the check and never blocks the base notification.
func TestRunCheck_AuthorCommentError_FailOpen(t *testing.T) {
	f, _ := newCommentFixture(t, "", errors.New("forum page boom"))

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	rec := f.lastRecord(t)
	if rec.errMsg != "" {
		t.Errorf("check errMsg = %q, want success despite comment-fetch failure", rec.errMsg)
	}
	evs := f.emitter.ofType(events.DownloadSubmitted)
	if len(evs) != 1 {
		t.Fatalf("expected the base download.submitted event, got %d", len(evs))
	}
	if evs[0].AuthorComment != "" {
		t.Errorf("AuthorComment = %q, want empty on fetch failure", evs[0].AuthorComment)
	}
}

// TestRunCheck_AuthorComment_CappedAtMaxRunes asserts overlong comments are
// rune-truncated with an ellipsis before entering notification events.
func TestRunCheck_AuthorComment_CappedAtMaxRunes(t *testing.T) {
	long := strings.Repeat("я", 400) // multibyte on purpose: cap must count runes
	f, _ := newCommentFixture(t, long, nil)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	evs := f.emitter.ofType(events.ReleaseFound)
	if len(evs) != 1 {
		t.Fatalf("expected 1 release.found event, got %d", len(evs))
	}
	want := strings.Repeat("я", 299) + "…"
	if evs[0].AuthorComment != want {
		t.Errorf("AuthorComment length = %d runes, want 300 (299 + ellipsis)", len([]rune(evs[0].AuthorComment)))
	}
}

// TestCapExcerpt_Boundary pins the exact truncation contract: at most max
// runes INCLUDING the ellipsis, whitespace trimmed first, multibyte-safe.
func TestCapExcerpt_Boundary(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"under the cap passes through", strings.Repeat("я", 299), 300, strings.Repeat("я", 299)},
		{"exactly at the cap passes through", strings.Repeat("я", 300), 300, strings.Repeat("я", 300)},
		{"one over truncates to cap with ellipsis", strings.Repeat("я", 301), 300, strings.Repeat("я", 299) + "…"},
		{"surrounding whitespace trimmed before measuring", "  hi  ", 300, "hi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := capExcerpt(tt.in, tt.max); got != tt.want {
				t.Errorf("capExcerpt() = %d runes, want %d (%q)", len([]rune(got)), len([]rune(tt.want)), tt.want[:min(20, len(tt.want))])
			}
		})
	}
}

// TestRunCheck_HashUnchanged_AuthorCommentNotFetched asserts no extra forum
// round-trip happens on a no-update tick.
func TestRunCheck_HashUnchanged_AuthorCommentNotFetched(t *testing.T) {
	f, tr := newCommentFixture(t, "Added episode 8.", nil)
	tr.checks = []checkResult{{check: &domain.Check{Hash: "old-hash"}}}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if tr.commentCalls != 0 {
		t.Errorf("AuthorComment calls = %d, want 0 when the hash is unchanged", tr.commentCalls)
	}
}

func TestRunCheck_CheckError_AlreadyErrored_NoNotify(t *testing.T) {
	tr := &fakeTracker{
		name:   "faketracker",
		checks: []checkResult{{err: errors.New("tracker boom")}},
	}
	f := newFixture(t, tr, false)
	f.topic.ConsecutiveErrors = 1 // already in the error state — must not re-notify

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if evs := f.emitter.ofType(events.CheckFailed); len(evs) != 0 {
		t.Errorf("expected no check.failed event on a repeat failure, got %d", len(evs))
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

	evs := f.emitter.ofType(events.DownloadSubmitted)
	if len(evs) != 1 {
		t.Fatalf("expected 1 download.submitted event, got %d", len(evs))
	}
	body := evs[0].Body
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

// --- replace-on-update (issue #101) -------------------------------------

// singlePayloadTracker returns one new release then signals "no pending" — the
// shape of every single-release (non-episodic) tracker.
func singlePayloadTracker(newHash string) *fakeTracker {
	return &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{check: &domain.Check{Hash: "new-hash"}},
		},
		downloads: []downloadResult{
			{payload: &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:" + newHash}},
			{err: registry.ErrNoPendingEpisodes},
		},
	}
}

func TestRunCheck_ReplaceOnUpdate_RemovesPreviousAndPrunes(t *testing.T) {
	const oldHash = "1111111111111111111111111111111111111111"
	const newHash = "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	f := newFixture(t, singlePayloadTracker(newHash), false)
	f.topic.ReplaceOnUpdate = true
	f.topic.ReplaceDeleteData = true
	f.deliveries.prior = []*domain.TopicDelivery{
		{Infohash: oldHash, ClientID: f.topic.ClientID},
	}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if f.clientPlugin.removeCalls != 1 {
		t.Fatalf("remove calls = %d, want 1", f.clientPlugin.removeCalls)
	}
	if len(f.clientPlugin.removeHashes) != 1 || f.clientPlugin.removeHashes[0] != oldHash {
		t.Errorf("removed hashes = %v, want [%s]", f.clientPlugin.removeHashes, oldHash)
	}
	if !f.clientPlugin.removeDeleteData {
		t.Errorf("deleteData = false, want true")
	}
	if len(f.deliveries.deletedHashes) != 1 || f.deliveries.deletedHashes[0] != oldHash {
		t.Errorf("pruned delivery rows = %v, want [%s]", f.deliveries.deletedHashes, oldHash)
	}
	// The new release must still be delivered and recorded as updated.
	if f.clientPlugin.addCalls != 1 {
		t.Errorf("add calls = %d, want 1", f.clientPlugin.addCalls)
	}
	if rec := f.lastRecord(t); !rec.updated || rec.errMsg != "" {
		t.Errorf("record = %+v, want updated with no error", rec)
	}
}

func TestRunCheck_ReplaceOnUpdate_KeepData(t *testing.T) {
	const oldHash = "1111111111111111111111111111111111111111"
	const newHash = "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	f := newFixture(t, singlePayloadTracker(newHash), false)
	f.topic.ReplaceOnUpdate = true
	f.topic.ReplaceDeleteData = false
	f.deliveries.prior = []*domain.TopicDelivery{{Infohash: oldHash, ClientID: f.topic.ClientID}}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if f.clientPlugin.removeCalls != 1 {
		t.Fatalf("remove calls = %d, want 1", f.clientPlugin.removeCalls)
	}
	if f.clientPlugin.removeDeleteData {
		t.Errorf("deleteData = true, want false (keep-data sub-option)")
	}
	// The torrent is removed from the client regardless of the file-deletion
	// flag, so its delivery row must still be pruned.
	if len(f.deliveries.deletedHashes) != 1 || f.deliveries.deletedHashes[0] != oldHash {
		t.Errorf("pruned rows = %v, want [%s] even when keeping data", f.deliveries.deletedHashes, oldHash)
	}
}

func TestRunCheck_ReplaceOnUpdate_SkipsCurrentInfohash(t *testing.T) {
	// Edge: the tracker bumped its opaque check hash but the torrent (infohash)
	// is unchanged, so the "previous" snapshot contains the SAME infohash that
	// was just (re)delivered. The guard must never remove it.
	const sameHash = "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	f := newFixture(t, singlePayloadTracker(sameHash), false)
	f.topic.ReplaceOnUpdate = true
	f.deliveries.prior = []*domain.TopicDelivery{{Infohash: sameHash, ClientID: f.topic.ClientID}}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if f.clientPlugin.removeCalls != 0 {
		t.Errorf("remove calls = %d, want 0 — the just-delivered torrent must not be removed", f.clientPlugin.removeCalls)
	}
	if len(f.deliveries.deletedHashes) != 0 {
		t.Errorf("pruned rows = %v, want none for the current infohash", f.deliveries.deletedHashes)
	}
}

func TestRunCheck_ReplaceOnUpdate_RemovesFromEachHoldingClient(t *testing.T) {
	// Prior deliveries can live on different clients than the topic's current
	// one (the user reassigned the client). Each must be removed from the client
	// that actually holds it — i.e. GetByID is called per distinct client_id.
	const h1 = "1111111111111111111111111111111111111111"
	const h2 = "2222222222222222222222222222222222222222"
	const newHash = "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	clientA := uuid.New()
	clientB := uuid.New()
	f := newFixture(t, singlePayloadTracker(newHash), false)
	f.topic.ReplaceOnUpdate = true
	f.deliveries.prior = []*domain.TopicDelivery{
		{Infohash: h1, ClientID: &clientA},
		{Infohash: h2, ClientID: &clientB},
	}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	calls := f.s.clients.(*fakeClients).getByIDCalls
	seen := map[uuid.UUID]bool{}
	for _, id := range calls {
		seen[id] = true
	}
	if !seen[clientA] || !seen[clientB] {
		t.Errorf("GetByID called for %v, want both holding clients %s and %s", calls, clientA, clientB)
	}
	if f.clientPlugin.removeCalls != 2 {
		t.Errorf("remove calls = %d, want 2 (one per holding client)", f.clientPlugin.removeCalls)
	}
}

func TestRunCheck_ReplaceDisabled_NoRemoval(t *testing.T) {
	const oldHash = "1111111111111111111111111111111111111111"
	const newHash = "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	f := newFixture(t, singlePayloadTracker(newHash), false)
	f.topic.ReplaceOnUpdate = false // default — keep all versions
	f.deliveries.prior = []*domain.TopicDelivery{{Infohash: oldHash, ClientID: f.topic.ClientID}}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if f.clientPlugin.removeCalls != 0 {
		t.Errorf("remove calls = %d, want 0 when replace is disabled", f.clientPlugin.removeCalls)
	}
	if len(f.deliveries.deletedHashes) != 0 {
		t.Errorf("pruned rows = %v, want none when replace is disabled", f.deliveries.deletedHashes)
	}
}

func TestRunCheck_ReplaceOnUpdate_Episodic_NoRemoval(t *testing.T) {
	// A per-episode tracker accumulates episodes; replacing would delete
	// siblings. The scheduler must skip removal even with replace enabled.
	const oldHash = "1111111111111111111111111111111111111111"
	const newHash = "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	tr := singlePayloadTracker(newHash)
	tr.episodic = true
	f := newFixture(t, tr, false)
	f.topic.ReplaceOnUpdate = true
	f.deliveries.prior = []*domain.TopicDelivery{{Infohash: oldHash, ClientID: f.topic.ClientID}}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if f.clientPlugin.removeCalls != 0 {
		t.Errorf("remove calls = %d, want 0 for an episodic tracker", f.clientPlugin.removeCalls)
	}
}

func TestRunCheck_ReplaceOnUpdate_RemoveFailsOpen(t *testing.T) {
	// A removal failure must not fail the check: the new release was delivered.
	const oldHash = "1111111111111111111111111111111111111111"
	const newHash = "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	f := newFixture(t, singlePayloadTracker(newHash), false)
	f.topic.ReplaceOnUpdate = true
	f.deliveries.prior = []*domain.TopicDelivery{{Infohash: oldHash, ClientID: f.topic.ClientID}}
	f.clientPlugin.removeErr = errors.New("qbit unreachable")

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	rec := f.lastRecord(t)
	if !rec.updated {
		t.Errorf("expected updated=true despite removal failure")
	}
	if rec.errMsg != "" {
		t.Errorf("expected no error recorded on a fail-open removal, got %q", rec.errMsg)
	}
	// A failed removal must NOT prune the delivery row (the torrent is still there).
	if len(f.deliveries.deletedHashes) != 0 {
		t.Errorf("pruned rows = %v, want none when removal failed", f.deliveries.deletedHashes)
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
// creds is injected as the credentials repo; emit is the fake emitter.
func newSessionFixture(t *testing.T, creds credentialsRepo, emit emitter) (*Scheduler, *domain.Topic) {
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
		emit:          emit,
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
	emit := &fakeEmitter{}

	s, topic := newSessionFixture(t, creds, emit)
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
	evs := emit.ofType(events.SessionExpired)
	if len(evs) != 1 {
		t.Errorf("session.expired emit: want 1, got %d", len(evs))
	} else if evs[0].UserID != storedCred.UserID {
		t.Errorf("session.expired UserID: want %s, got %s", storedCred.UserID, evs[0].UserID)
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
	emit := &fakeEmitter{}

	s, topic := newSessionFixture(t, creds, emit)
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
	if evs := emit.ofType(events.SessionExpired); len(evs) != 0 {
		t.Errorf("session.expired emit: want 0 (lost the transition race), got %d", len(evs))
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
	emit := &fakeEmitter{}

	s, topic := newSessionFixture(t, creds, emit)
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
	if evs := emit.ofType(events.SessionExpired); len(evs) != 0 {
		t.Errorf("session.expired emit: want 0 (already flagged), got %d", len(evs))
	}
}

// TestNotifyUpdated_RoutesToTopicNotifier verifies notifyUpdated carries
// the topic's NotifierID in the emitted event so the bus can route via it.
func TestNotifyUpdated_RoutesToTopicNotifier(t *testing.T) {
	emit := &fakeEmitter{}
	topicID := uuid.New()
	s := &Scheduler{cfg: &config.Config{PublicBaseURL: "http://x"}, emit: emit}

	notifierID := uuid.New()
	topic := &domain.Topic{ID: topicID, UserID: uuid.New(), DisplayName: "My Show", NotifierID: &notifierID}

	s.notifyUpdated(context.Background(), topic, []string{"s01e01"}, "")

	evs := emit.ofType(events.DownloadSubmitted)
	if len(evs) != 1 {
		t.Fatalf("want 1 download.submitted event, got %d", len(evs))
	}
	ev := evs[0]
	if ev.NotifierID == nil || *ev.NotifierID != notifierID {
		t.Errorf("NotifierID = %v, want %s", ev.NotifierID, notifierID)
	}
}

// TestNotifyUpdated_SingleLabelEqualsDisplayName_NoTitleDuplication: for a
// single-release topic the delivery label IS the display name, and
// repeating it after the bold title made the notification a wall of text.
// The body must collapse to a plain "Sent to client".
func TestNotifyUpdated_SingleLabelEqualsDisplayName_NoTitleDuplication(t *testing.T) {
	emit := &fakeEmitter{}
	s := &Scheduler{cfg: &config.Config{PublicBaseURL: "http://x"}, emit: emit}
	topic := &domain.Topic{ID: uuid.New(), UserID: uuid.New(), DisplayName: "My Show"}

	s.notifyUpdated(context.Background(), topic, []string{"My Show"}, "")

	evs := emit.ofType(events.DownloadSubmitted)
	if len(evs) != 1 {
		t.Fatalf("want 1 download.submitted event, got %d", len(evs))
	}
	if evs[0].Body != "Sent to client" {
		t.Errorf("Body = %q, want %q (no title repetition)", evs[0].Body, "Sent to client")
	}
}

// TestNotifyUpdated_NilNotifierID_GlobalFanOut verifies a topic without an
// override emits nil NotifierID (global fan-out, unchanged behaviour).
func TestNotifyUpdated_NilNotifierID_GlobalFanOut(t *testing.T) {
	emit := &fakeEmitter{}
	topicID := uuid.New()
	s := &Scheduler{cfg: &config.Config{PublicBaseURL: "http://x"}, emit: emit}

	topic := &domain.Topic{ID: topicID, UserID: uuid.New(), DisplayName: "My Show", NotifierID: nil}

	s.notifyUpdated(context.Background(), topic, []string{"s01e01"}, "")

	evs := emit.ofType(events.DownloadSubmitted)
	if len(evs) != 1 {
		t.Fatalf("want 1 download.submitted event, got %d", len(evs))
	}
	if evs[0].NotifierID != nil {
		t.Errorf("NotifierID = %v, want nil", evs[0].NotifierID)
	}
}

// TestRunCheck_NewRelease_EmitsReleaseFoundAndSubmitted drives one check
// that detects a new hash and submits one payload, asserting the typed
// events fire in the expected order with no check.failed emitted.
func TestRunCheck_NewRelease_EmitsReleaseFoundAndSubmitted(t *testing.T) {
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

	typs := f.emitter.types()

	// Must contain release.found and download.submitted.
	found := func(tp events.Type) bool {
		for _, ty := range typs {
			if ty == tp {
				return true
			}
		}
		return false
	}
	if !found(events.ReleaseFound) {
		t.Errorf("expected release.found in emitted events, got %v", typs)
	}
	if !found(events.DownloadSubmitted) {
		t.Errorf("expected download.submitted in emitted events, got %v", typs)
	}
	// Must NOT emit check.failed on success.
	if found(events.CheckFailed) {
		t.Errorf("unexpected check.failed in emitted events, got %v", typs)
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

func TestRunCheck_SelfHeal_PlaceholderName_Persists(t *testing.T) {
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{check: &domain.Check{Hash: "old-hash", DisplayName: "Real Title"}, err: nil},
		},
	}
	f := newFixture(t, tr, false)
	f.topic.DisplayName = "Fake topic 1"
	f.topic.DisplayNameIsPlaceholder = true

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if len(f.topics.updateDisplayNameCalls) != 1 {
		t.Fatalf("want 1 UpdateDisplayName call, got %d", len(f.topics.updateDisplayNameCalls))
	}
	if got := f.topics.updateDisplayNameCalls[0].name; got != "Real Title" {
		t.Errorf("healed name = %q, want Real Title", got)
	}
}

func TestRunCheck_SelfHeal_ResolvedName_DoesNotDowngrade(t *testing.T) {
	// Regression for #90: a resolved title must NOT be overwritten even when
	// Check reports a different (e.g. main-page) DisplayName.
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{check: &domain.Check{Hash: "old-hash", DisplayName: "Kinozal.TV Main Page"}, err: nil},
		},
	}
	f := newFixture(t, tr, false)
	f.topic.DisplayName = "Real Release Name"
	f.topic.DisplayNameIsPlaceholder = false

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if len(f.topics.updateDisplayNameCalls) != 0 {
		t.Errorf("want 0 UpdateDisplayName calls for a resolved title, got %d",
			len(f.topics.updateDisplayNameCalls))
	}
}

// --- classifyError ------------------------------------------------------

func TestClassifyError_MapsKnownPatternsToCodes(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want string
	}{
		{"kinozal timeout (the #86 case)", `kinozal GET: Get "https://kinozal.tv/details.php?id=1": context deadline exceeded`, errCodeTimeout},
		{"http client timeout", `Get "https://x": net/http: request canceled (Client.Timeout exceeded)`, errCodeTimeout},
		{"i/o timeout", "read tcp 1.2.3.4:443: i/o timeout", errCodeTimeout},
		{"connection refused", "dial tcp 127.0.0.1:80: connect: connection refused", errCodeUnreachable},
		{"dns no such host", `Get "https://nope": dial tcp: lookup nope: no such host`, errCodeUnreachable},
		{"cloudflare 522 origin down", "rutracker GET: unexpected status 522", errCodeUnreachable},
		{"tracker GET arrow status 522", `kinozal GET https://kinozal.tv/details.php?id=1805143 -> 522`, errCodeUnreachable},
		{"server 500 via unexpected status", "generictorrentfile: unexpected status 500", errCodeUnreachable},
		{"rate limited 429", `rutracker GET https://rutracker.org/forum/viewtopic.php?t=1 -> 429`, errCodeUnreachable},
		{"tls handshake timeout", "net/http: TLS handshake timeout", errCodeTimeout},
		{"tls handshake error (no timeout word)", "net/http: TLS handshake error from 1.2.3.4", errCodeUnreachable},
		{"connection reset", "read: connection reset by peer", errCodeUnreachable},
		{"auth failed prefix", "auth failed: invalid credentials", errCodeAuth},
		{"session expired", "lostfilm: session expired", errCodeAuth},
		{"unauthorized 401", "unexpected status 401 unauthorized", errCodeAuth},
		{"forbidden 403 status", `lostfilm GET https://lostfilm.tv/series/x -> 403`, errCodeAuth},
		{"captcha required", "captcha required to log in", errCodeAuth},
		{"plugin missing", "tracker plugin not installed", errCodePluginMissing},
		{"parse failure", "parse: could not find magnet link", errCodeParse},
		{"unexpected token", "invalid character '<' looking for beginning of value", errCodeParse},
		// Regression for the URL-id false positive (review HIGH): a real
		// timeout whose topic id embeds "522"/"403" must NOT be misread as a
		// status code — the URL is stripped before matching.
		{"timeout, url id contains 522", `kinozal GET: Get "https://kinozal.tv/details.php?id=15221": context deadline exceeded`, errCodeTimeout},
		{"timeout, url id contains 403", `kinozal GET: Get "https://kinozal.tv/details.php?id=40312": context deadline exceeded`, errCodeTimeout},
		{"refused, url id contains 401", `nnm-club GET: Get "https://nnmclub.to/forum/viewtopic.php?t=4012": dial tcp 1.2.3.4:80: connect: connection refused`, errCodeUnreachable},
		// Other 4xx (e.g. 404) is neither auth nor a 5xx: falls through to
		// unknown so the UI shows the raw detail instead of a wrong phrase.
		{"not-found 404 falls through to unknown", `freetorrents GET https://x.invalid/?id=1 -> 404`, errCodeUnknown},
		{"unrecognised", "something completely different happened", errCodeUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyError(tt.msg); got != tt.want {
				t.Errorf("classifyError(%q) = %q, want %q", tt.msg, got, tt.want)
			}
		})
	}
}

func TestRecordResult_ClassifiesAndPersistsErrorCode(t *testing.T) {
	f := newFixture(t, &fakeTracker{}, false)
	id := uuid.New()
	f.s.recordResult(
		context.Background(), zerolog.Nop(), id, "", false,
		time.Now(), `kinozal GET: Get "https://kinozal.tv/details.php?id=1": context deadline exceeded`,
	)
	if len(f.topics.recordCalls) != 1 {
		t.Fatalf("want 1 record call, got %d", len(f.topics.recordCalls))
	}
	if got := f.topics.recordCalls[0].errCode; got != errCodeTimeout {
		t.Errorf("errCode = %q, want %q", got, errCodeTimeout)
	}
}

func TestRecordResult_SuccessLeavesErrorCodeEmpty(t *testing.T) {
	f := newFixture(t, &fakeTracker{}, false)
	id := uuid.New()
	f.s.recordResult(context.Background(), zerolog.Nop(), id, "abc", true, time.Now(), "")
	if len(f.topics.recordCalls) != 1 {
		t.Fatalf("want 1 record call, got %d", len(f.topics.recordCalls))
	}
	if got := f.topics.recordCalls[0].errCode; got != "" {
		t.Errorf("errCode = %q, want empty on success", got)
	}
}
