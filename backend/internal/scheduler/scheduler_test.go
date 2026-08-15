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
	"github.com/artyomsv/marauder/backend/internal/db/repo"
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
	// onCheck runs at the top of Check, before the configured result is
	// returned. It is the seam for simulating something happening to the topic
	// while the worker is out at the tracker — a reset, say.
	onCheck func()

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
	if f.onCheck != nil {
		f.onCheck()
	}
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
// It satisfies topicsRepo.
type fakeTopics struct {
	recordCalls            []recordCall
	updateDisplayNameCalls []updateDisplayNameCall
	markCalls              []markCall
	markErr                error
	verifyCalls            []uuid.UUID
	verifyErr              error
}

type recordCall struct {
	id          uuid.UUID
	observed    *time.Time // the last_checked_at the worker carried in
	hash        string
	updated     bool
	nextCheckAt time.Time
	errMsg      string
	errCode     string
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

func (f *fakeTopics) RecordCheckResult(_ context.Context, t *domain.Topic, hash string, updated bool, nextCheckAt time.Time, errMsg, errCode string) error {
	f.recordCalls = append(f.recordCalls, recordCall{t.ID, t.LastCheckedAt, hash, updated, nextCheckAt, errMsg, errCode})
	return nil
}

func (f *fakeTopics) UpdateDisplayName(_ context.Context, id uuid.UUID, name string) error {
	f.updateDisplayNameCalls = append(f.updateDisplayNameCalls, updateDisplayNameCall{id, name})
	return nil
}

func (f *fakeTopics) MarkEpisodeDownloaded(_ context.Context, t *domain.Topic, packed string) error {
	f.markCalls = append(f.markCalls, markCall{t.ID, packed})
	return f.markErr
}

// VerifyCheckState is the pre-submit read-only guard. verifyErr lets a test
// stage a reset landing between Check and Add; the zero value means "token
// still valid", so every existing test keeps submitting as before.
func (f *fakeTopics) VerifyCheckState(_ context.Context, t *domain.Topic) error {
	f.verifyCalls = append(f.verifyCalls, t.ID)
	return f.verifyErr
}

// fakeClients records GetByID / GetDefault calls and always returns a
// fixed Client whose ClientName matches the registered fakeClientPlugin.
type fakeClients struct {
	client       *domain.Client
	getByIDCalls []uuid.UUID
	getByIDErr   error
}

func (f *fakeClients) GetByID(_ context.Context, id uuid.UUID, _ uuid.UUID) (*domain.Client, error) {
	f.getByIDCalls = append(f.getByIDCalls, id)
	if f.getByIDErr != nil {
		return nil, f.getByIDErr
	}
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
	// onAdd runs after the call is recorded, so a test can land a reset in
	// the narrow gap the pre-submit guard cannot cover: between Add returning
	// and the episode mark that follows it.
	onAdd func()

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
	if f.onAdd != nil {
		f.onAdd()
	}
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
	clients      *fakeClients
	clientPlugin *fakeClientPlugin
	deliveries   *fakeDeliveries
	emitter      *fakeEmitter
	tracker      *fakeTracker
	topic        *domain.Topic
}

// newFixture wires a scheduler with all-fakes dependencies.
func newFixture(t *testing.T, tracker *fakeTracker) *fixture {
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

	topicsImpl := &fakeTopics{}
	clientsImpl := &fakeClients{client: client}

	deliveries := &fakeDeliveries{}
	emit := &fakeEmitter{}
	s := &Scheduler{
		cfg:           cfg,
		log:           zerolog.New(io.Discard),
		topics:        topicsImpl,
		clients:       clientsImpl,
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
		topics:       topicsImpl,
		clients:      clientsImpl,
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
	f := newFixture(t, tr)

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
	f := newFixture(t, tr)

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
	f := newFixture(t, tr)

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
	f := newFixture(t, tr)

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
	f := newFixture(t, tr)

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
	f := newFixture(t, tr)

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
	f := newFixture(t, tr)

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
	f := newFixture(t, &tr.fakeTracker)
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
	f := newFixture(t, tr)
	f.topic.ConsecutiveErrors = 1 // already in the error state — must not re-notify

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if evs := f.emitter.ofType(events.CheckFailed); len(evs) != 0 {
		t.Errorf("expected no check.failed event on a repeat failure, got %d", len(evs))
	}
}

// TestRunCheck_DownloadFails_AlreadyErrored_NoReleaseFound pins the dedup for
// the stuck-download loop. A failed download deliberately persists the OLD
// hash so the change is re-detected next tick — which means every retry
// re-enters the `updated` branch. Without a guard that re-emits release.found
// (persisted AND notifiable) on every retry, so one unreachable client turns
// into an unbounded stream of "New release detected" rows and notifications.
func TestRunCheck_DownloadFails_AlreadyErrored_NoReleaseFound(t *testing.T) {
	const hash = "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{check: &domain.Check{Hash: "new-hash"}, err: nil},
		},
		downloads: []downloadResult{
			{payload: &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:" + hash}, err: nil},
		},
	}
	f := newFixture(t, tr)
	// The client is unreachable, so the submit fails for this tick.
	f.clientPlugin.addErr = errors.New("dial tcp 192.0.2.1:8083: connect: connection refused")
	// This is a RETRY: the topic already failed at least once, so the release
	// was announced back then and must not be announced again.
	f.topic.ConsecutiveErrors = 1

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if evs := f.emitter.ofType(events.ReleaseFound); len(evs) != 0 {
		t.Errorf("expected no release.found event on a retry of an already-failing topic, got %d", len(evs))
	}
}

// TestRunCheck_DownloadFails_FirstFailure_EmitsReleaseFound guards the other
// side of the dedup: the FIRST detection must still announce, even though its
// download fails. Suppressing it would hide the release entirely.
func TestRunCheck_DownloadFails_FirstFailure_EmitsReleaseFound(t *testing.T) {
	const hash = "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{check: &domain.Check{Hash: "new-hash"}, err: nil},
		},
		downloads: []downloadResult{
			{payload: &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:" + hash}, err: nil},
		},
	}
	f := newFixture(t, tr)
	f.clientPlugin.addErr = errors.New("dial tcp 192.0.2.1:8083: connect: connection refused")
	f.topic.ConsecutiveErrors = 0 // healthy until this tick

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if evs := f.emitter.ofType(events.ReleaseFound); len(evs) != 1 {
		t.Errorf("expected 1 release.found event on the first failing detection, got %d", len(evs))
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
	f := newFixture(t, tr)

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
	f := newFixture(t, tr)

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
	f := newFixture(t, tr)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if got := f.clientPlugin.addCalls; got != 3 {
		t.Errorf("expected 3 client Add calls, got %d", got)
	}
	if got := tr.callsDownload; got != 3 {
		t.Errorf("expected 3 Download calls, got %d", got)
	}
	if got := len(f.topics.markCalls); got != 3 {
		t.Errorf("expected 3 MarkEpisodeDownloaded calls, got %d", got)
	}
	wantPacked := []string{"S01E01", "S01E02", "S01E03"}
	for i, w := range wantPacked {
		if f.topics.markCalls[i].packed != w {
			t.Errorf("mark call %d: got %q, want %q", i, f.topics.markCalls[i].packed, w)
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
	f := newFixture(t, tr)

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
	f := newFixture(t, tr)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if got := f.clientPlugin.addCalls; got != 2 {
		t.Errorf("expected 2 client Add calls, got %d", got)
	}
	if got := len(f.topics.markCalls); got != 2 {
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
	f := newFixture(t, tr)

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
	f := newFixture(t, tr)
	// Fail the SECOND mark call. The submit succeeded, so anySubmitted
	// should be true and the recorded result should reflect "updated".
	f.topics.markErr = errors.New("db down")

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
	f := newFixture(t, tr)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if got := f.clientPlugin.addCalls; got != cap {
		t.Errorf("expected exactly %d client Add calls (capped), got %d", cap, got)
	}
	if got := len(f.topics.markCalls); got != cap {
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
	f := newFixture(t, singlePayloadTracker(newHash))
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
	f := newFixture(t, singlePayloadTracker(newHash))
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
	f := newFixture(t, singlePayloadTracker(sameHash))
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
	f := newFixture(t, singlePayloadTracker(newHash))
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
	f := newFixture(t, singlePayloadTracker(newHash))
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
	f := newFixture(t, tr)
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
	f := newFixture(t, singlePayloadTracker(newHash))
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
			// A solver that cannot mint is infrastructure, not a verdict about
			// the tracker, and infrastructure comes back. On 2026-08-05 this
			// case took the exponential path and parked two topics for 30
			// minutes over a 9-second FlareSolverr startup.
			name:              "clearance unavailable retries fast, not exponential",
			consecutiveErrors: 1,
			failure:           true,
			cause:             fmt.Errorf("rutracker login: %w: solver said no", registry.ErrClearanceUnavailable),
			minBackoff:        transientRetryDelay,
			maxBackoff:        transientRetryDelay + 50*time.Millisecond,
		},
		{
			// Transient does not mean infinite: a solver that stays down still
			// falls back to exponential after transientRetryMax attempts.
			name:              "clearance unavailable still gives up on fast retry once persistent",
			consecutiveErrors: transientRetryMax,
			failure:           true,
			cause:             fmt.Errorf("rutracker login: %w: solver said no", registry.ErrClearanceUnavailable),
			minBackoff:        64 * base,
			maxBackoff:        64*base + 50*time.Millisecond,
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
	f := newFixture(t, tr)

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
	f := newFixture(t, tr)
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
	f := newFixture(t, tr)
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
		// Cloudflare must win over BOTH later passes. The HTTP-status pass maps
		// 403 -> auth, and the keyword pass matches "invalid credentials" and
		// "login failed" — either would swallow these back into `auth`, which
		// is exactly the misdiagnosis this code exists to prevent.
		{"cloudflare challenge with a 403 in the message", `rutracker GET https://rutracker.org/forum/index.php -> 403: cloudflare challenge`, errCodeCloudflare},
		{"cloudflare challenge wrapped by the login path", "auth failed: rutracker login failed: cloudflare challenge", errCodeCloudflare},
		{"cloudflare wording from the sentinel", "tracker is behind a cloudflare challenge", errCodeCloudflare},
		// Solver failures must also outrank the timeout/unreachable passes:
		// their messages routinely carry "context deadline exceeded", and
		// letting that win would classify them as network errors and trigger
		// domain rotation on evidence that says nothing about the domain.
		{"solver timeout keeps the solver code", `flaresolverr: call: Post "http://flaresolverr:8191/v1": context deadline exceeded`, errCodeSolver},
		{"solver disabled", "flaresolverr is not configured", errCodeSolver},
		// An unsolved challenge is NOT a solver malfunction — the solver
		// answered promptly and the tracker is genuinely gated. Bucketing it as
		// `solver` told the user "the tracker itself is probably fine,
		// retrying", which is the opposite of the truth.
		{"unsolved challenge is a cloudflare problem", "flaresolverr: request.get: Challenge not solved!", errCodeCloudflare},
		// The 2026-08-05 boot race: the solver was still launching Chrome when
		// the scheduler started checking, the mint was refused, and the bare
		// request hit the tracker's challenge. Both halves of that story are in
		// the message, and the SOLVER half is the actionable one — the tracker
		// was never the problem. This must outrank the challenge wording it
		// necessarily arrives with, and the "connection refused" that would
		// otherwise read as a dead tracker domain and trigger rotation.
		{"clearance unavailable outranks the challenge it caused", `rutracker GET https://rutracker.org/forum/viewtopic.php?t=1: cloudflare clearance unavailable: flaresolverr: sessions.create: dial tcp 172.24.0.2:8191: connect: connection refused`, errCodeSolver},
		{"clearance unavailable wrapped by the login path", "auth failed: rutracker login: cloudflare clearance unavailable: flaresolverr is not configured", errCodeSolver},
		// Matched on our own wording, not the product name, so a provider that
		// is not FlareSolverr classifies identically.
		{"clearance unavailable from a non-flaresolverr provider", "rutracker login: cloudflare clearance unavailable: solver returned 503", errCodeSolver},
		// A tracker or custom-mirror hostname containing the solver's name must
		// not hijack the classification; only our own wrapper prefix counts.
		{"tracker url mentioning the solver is still a network error", `kinozal GET https://flaresolverr.example.com/details.php?id=1 -> 522`, errCodeUnreachable},
		{"auth failed prefix", "auth failed: invalid credentials", errCodeAuth},
		{"session expired", "lostfilm: session expired", errCodeAuth},
		// A network failure inside the login path must outrank the
		// "auth failed: " prefix that loadCredentials stamps onto every error
		// it reports, for the same reason the Cloudflare and solver blocks
		// above outrank it. On 2026-08-15 a LostFilm custom mirror stopped
		// resolving; every check recorded `auth`, which told the user their
		// credentials were wrong AND — because `auth` is not in the set
		// recordResult rotates on — meant the tracker could never step off the
		// dead domain. 13-22 consecutive errors with no path to recovery.
		{"dns failure wrapped by the login path", `auth failed: lostfilm: session validation: lostfilm verify: Get "https://mirror.lostfilm.tv/my": dial tcp: lookup mirror.lostfilm.tv on 127.0.0.11:53: no such host`, errCodeUnreachable},
		{"login timeout wrapped by the login path", "auth failed: rutracker login: context deadline exceeded", errCodeTimeout},
		{"connection refused wrapped by the login path", "auth failed: kinozal login: dial tcp 1.2.3.4:443: connect: connection refused", errCodeUnreachable},
		// The other half of that ordering: a genuine credential rejection
		// carries no network marker and must still classify as auth.
		{"genuine credential rejection stays auth", "auth failed: lostfilm login: invalid credentials", errCodeAuth},
		{"genuine session expiry stays auth", "auth failed: lostfilm: session expired", errCodeAuth},
		// The reorder applies to every check error, not only login-wrapped
		// ones, so its one deliberate semantic change is pinned here rather
		// than left implicit: a message carrying BOTH an auth keyword and a
		// network marker now reads as network. That is the correct reading —
		// the request never got an answer to reject — and unlike `auth` it
		// lets rotation try another domain instead of parking forever.
		{"auth wording with a network marker reads as network", "rutracker login failed: read tcp 1.2.3.4:443: connection reset by peer", errCodeUnreachable},
		// A bare network error on the non-login path is untouched by the
		// reorder: it matched unreachable before and still does.
		{"bare network error unaffected by the reorder", `Get "https://rutracker.org/forum/index.php": dial tcp: lookup rutracker.org: no such host`, errCodeUnreachable},
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
		{"not-found 404 falls through to unknown", `tapochek GET https://x.invalid/?id=1 -> 404`, errCodeUnknown},
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
	f := newFixture(t, &fakeTracker{})
	topic := &domain.Topic{ID: uuid.New(), TrackerName: "kinozal"}
	f.s.recordResult(
		context.Background(), zerolog.Nop(), topic, "", false,
		time.Now(), `kinozal GET: Get "https://kinozal.tv/details.php?id=1": context deadline exceeded`,
		nil,
	)
	if len(f.topics.recordCalls) != 1 {
		t.Fatalf("want 1 record call, got %d", len(f.topics.recordCalls))
	}
	if got := f.topics.recordCalls[0].errCode; got != errCodeTimeout {
		t.Errorf("errCode = %q, want %q", got, errCodeTimeout)
	}
}

func TestRecordResult_SuccessLeavesErrorCodeEmpty(t *testing.T) {
	f := newFixture(t, &fakeTracker{})
	topic := &domain.Topic{ID: uuid.New(), TrackerName: "faketracker"}
	f.s.recordResult(context.Background(), zerolog.Nop(), topic, "abc", true, time.Now(), "", nil)
	if len(f.topics.recordCalls) != 1 {
		t.Fatalf("want 1 record call, got %d", len(f.topics.recordCalls))
	}
	if got := f.topics.recordCalls[0].errCode; got != "" {
		t.Errorf("errCode = %q, want empty on success", got)
	}
}

// --- domain rotation (issue #126 Phase 2) --------------------------------

// fakeRotator is a programmable domainRotator that records every tracker
// name it was asked to report a failure for.
type fakeRotator struct{ calls []string }

func (f *fakeRotator) ReportFailure(_ context.Context, name string) {
	f.calls = append(f.calls, name)
}

func TestRecordResult_NetworkError_ReportsDomainFailure(t *testing.T) {
	tests := []struct {
		name      string
		errMsg    string
		wantCalls int
	}{
		{"unreachable rotates", "kinozal GET: dial tcp: connection refused", 1},
		{"timeout rotates", "context deadline exceeded", 1},
		{"auth error does not rotate", "kinozal login failed: invalid credentials", 0},
		{"success does not rotate", "", 0},
		// A slow or failing challenge solver says nothing about the tracker's
		// domain, and rotation is one-directional — it never migrates back. On
		// 2026-07-30 a FlareSolverr queue backlog rotated RuTracker from .org
		// onto a mirror that serves only a "Redirecting..." stub, breaking
		// every check until the active domain was reset by hand. Note the
		// message also contains "context deadline exceeded", which the row
		// above proves rotates — so this is specifically about the solver
		// prefix winning.
		{
			"solver timeout does not rotate",
			`Get "https://rutracker.org/forum/viewtopic.php?t=1": flaresolverr: call: Post "http://flaresolverr:8191/v1": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`,
			0,
		},
		{"solver refusal does not rotate", "flaresolverr: Challenge not solved!", 0},
		// The deadlock this fix removes: while a DNS failure in the login path
		// classified as `auth`, rotation never fired, so a tracker parked on a
		// custom mirror that had stopped resolving stayed there forever. It
		// must now rotate so the ring can step to a domain that resolves.
		{
			"dns failure in the login path rotates",
			`auth failed: lostfilm verify: Get "https://mirror.lostfilm.tv/my": dial tcp: lookup mirror.lostfilm.tv: no such host`,
			1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rot := &fakeRotator{}
			f := newFixture(t, &fakeTracker{})
			f.s.domains = rot
			topic := &domain.Topic{ID: uuid.New(), TrackerName: "kinozal"}
			f.s.recordResult(context.Background(), zerolog.Nop(), topic,
				"", false, time.Now(), tt.errMsg, nil)
			if len(rot.calls) != tt.wantCalls {
				t.Errorf("ReportFailure calls = %d, want %d", len(rot.calls), tt.wantCalls)
			}
			if tt.wantCalls == 1 && rot.calls[0] != "kinozal" {
				t.Errorf("ReportFailure tracker = %q, want kinozal", rot.calls[0])
			}
		})
	}
}

// --- Typed causes: failures that are not the tracker's ------------------

// A failure handing the payload to the user's torrent client says nothing
// about the tracker. Classified by message it landed in the same bucket as a
// tracker timeout, which both told the user "the tracker didn't respond" and
// rotated the tracker's domain. Observed 2026-08-15: an unreachable
// Transmission rotated LostFilm off www.lostfilm.tv 8ms after the submit timed
// out, on a tracker that had just authenticated and detected new episodes.
func TestRecordResult_ClientDeliveryFailure_DoesNotBlameTracker(t *testing.T) {
	rot := &fakeRotator{}
	f := newFixture(t, &fakeTracker{})
	f.s.domains = rot
	topic := &domain.Topic{ID: uuid.New(), TrackerName: "lostfilm"}

	cause := fmt.Errorf("%w: %w", errClientDelivery,
		errors.New(`Post "http://transmission:9091/transmission/rpc": context deadline exceeded`))
	f.s.recordResult(context.Background(), zerolog.Nop(), topic,
		"", false, time.Now(), cause.Error(), cause)

	if len(f.topics.recordCalls) != 1 {
		t.Fatalf("want 1 record call, got %d", len(f.topics.recordCalls))
	}
	if got := f.topics.recordCalls[0].errCode; got != errCodeClient {
		t.Errorf("errCode = %q, want %q", got, errCodeClient)
	}
	if len(rot.calls) != 0 {
		t.Errorf("ReportFailure calls = %d, want 0 — a client outage must not rotate the tracker's domain", len(rot.calls))
	}
}

// A failure writing our own episode-progress state is the same shape with the
// database in place of the client: "context deadline exceeded", classified
// `timeout`, rotating the tracker's domain on evidence about our DB.
func TestRecordResult_StatePersistFailure_DoesNotBlameTracker(t *testing.T) {
	rot := &fakeRotator{}
	f := newFixture(t, &fakeTracker{})
	f.s.domains = rot
	topic := &domain.Topic{ID: uuid.New(), TrackerName: "lostfilm"}

	cause := fmt.Errorf("%w: %w", errStatePersist, context.DeadlineExceeded)
	f.s.recordResult(context.Background(), zerolog.Nop(), topic,
		"", false, time.Now(), cause.Error(), cause)

	if got := f.lastRecord(t).errCode; got != errCodeInternal {
		t.Errorf("errCode = %q, want %q", got, errCodeInternal)
	}
	if len(rot.calls) != 0 {
		t.Errorf("ReportFailure calls = %d, want 0 — a database failure must not rotate the tracker's domain", len(rot.calls))
	}
}

// The sentinel decides, not the wording: the same "context deadline exceeded"
// text with no sentinel is still a tracker timeout and still rotates. This
// pairing is what proves the typed check is doing the separating, since
// classifyError cannot tell the two apart.
func TestRecordResult_TrackerTimeout_StillRotates(t *testing.T) {
	rot := &fakeRotator{}
	f := newFixture(t, &fakeTracker{})
	f.s.domains = rot
	topic := &domain.Topic{ID: uuid.New(), TrackerName: "lostfilm"}

	cause := errors.New(`Post "https://www.lostfilm.tv/v_search.php": context deadline exceeded`)
	f.s.recordResult(context.Background(), zerolog.Nop(), topic,
		"", false, time.Now(), cause.Error(), cause)

	if got := f.lastRecord(t).errCode; got != errCodeTimeout {
		t.Errorf("errCode = %q, want %q", got, errCodeTimeout)
	}
	if len(rot.calls) != 1 {
		t.Fatalf("ReportFailure calls = %d, want 1", len(rot.calls))
	}
}

// --- Wrap sites, end to end -------------------------------------------
//
// The tests above hand recordResult a cause they built themselves, so they
// pin the CLASSIFIER but not the code that produces the sentinel. That gap is
// not hypothetical: the errClientDelivery wrap was moved from downloadAllPending
// into sendViaClient and every one of those tests stayed green. These drive
// runCheck so the wrap sites themselves are load-bearing — delete either wrap
// and these fail.

func TestRunCheck_ClientAddFails_RecordsClientCodeWithoutRotating(t *testing.T) {
	tr := &fakeTracker{
		name:   "faketracker",
		checks: []checkResult{{check: &domain.Check{Hash: "new-hash"}}},
		downloads: []downloadResult{
			{payload: &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:c12fe1c06bba254a9dc9f519b335aa7c1367a88a"}},
		},
	}
	rot := &fakeRotator{}
	f := newFixture(t, tr)
	f.s.domains = rot
	// Wording deliberately identical to a tracker timeout: only the sentinel
	// applied at the Add call separates the two.
	f.clientPlugin.addErr = errors.New(`Post "http://transmission:9091/transmission/rpc": context deadline exceeded`)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if got := f.lastRecord(t).errCode; got != errCodeClient {
		t.Errorf("errCode = %q, want %q", got, errCodeClient)
	}
	if len(rot.calls) != 0 {
		t.Errorf("ReportFailure calls = %d, want 0 — a client outage must not rotate the tracker's domain", len(rot.calls))
	}
}

func TestRunCheck_EpisodeMarkFails_RecordsInternalCodeWithoutRotating(t *testing.T) {
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{{
			check: &domain.Check{
				Hash:  "new-hash",
				Extra: map[string]any{"pending_episodes": []string{"S01E01", "S01E02"}},
			},
		}},
		downloads: []downloadResult{
			{payload: &domain.Payload{MagnetURI: "magnet:1"}},
			{payload: &domain.Payload{MagnetURI: "magnet:2"}},
		},
	}
	rot := &fakeRotator{}
	f := newFixture(t, tr)
	f.s.domains = rot
	f.topics.markErr = errors.New("context deadline exceeded")

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if got := f.lastRecord(t).errCode; got != errCodeInternal {
		t.Errorf("errCode = %q, want %q", got, errCodeInternal)
	}
	if len(rot.calls) != 0 {
		t.Errorf("ReportFailure calls = %d, want 0 — a database failure must not rotate the tracker's domain", len(rot.calls))
	}
}

// The client row is read from Postgres on every submit, so a DB timeout there
// renders as "context deadline exceeded" just like a tracker timeout. Left
// unmarked it rotated the TRACKER's domain on evidence about our database —
// reachable, since RotateFailureThreshold is 2 within 5m and rotation mutates
// the in-memory active domain before it tries to persist, so it lands even
// while the DB that would record it is down.
func TestRunCheck_ClientLookupFails_RecordsInternalCodeWithoutRotating(t *testing.T) {
	tr := &fakeTracker{
		name:   "faketracker",
		checks: []checkResult{{check: &domain.Check{Hash: "new-hash"}}},
		downloads: []downloadResult{
			{payload: &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:c12fe1c06bba254a9dc9f519b335aa7c1367a88a"}},
		},
	}
	rot := &fakeRotator{}
	f := newFixture(t, tr)
	f.s.domains = rot
	f.clients.getByIDErr = errors.New("context deadline exceeded")

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if got := f.lastRecord(t).errCode; got != errCodeInternal {
		t.Errorf("errCode = %q, want %q", got, errCodeInternal)
	}
	if len(rot.calls) != 0 {
		t.Errorf("ReportFailure calls = %d, want 0 — a database failure must not rotate the tracker's domain", len(rot.calls))
	}
}

func TestClassifyCause_SentinelOutranksMessage(t *testing.T) {
	tests := []struct {
		name  string
		msg   string
		cause error
		want  string
	}{
		{
			"client sentinel wins over timeout wording",
			`submit to torrent client: Post "http://qbit:6611/api/v2/torrents/add": context deadline exceeded`,
			fmt.Errorf("%w: %w", errClientDelivery, context.DeadlineExceeded),
			errCodeClient,
		},
		{
			"client sentinel wins over connection-refused wording",
			"submit to torrent client: dial tcp 203.0.113.10:9091: connect: connection refused",
			fmt.Errorf("%w: %w", errClientDelivery, errors.New("connect: connection refused")),
			errCodeClient,
		},
		{
			"state-persist sentinel wins over timeout wording",
			"marauder storage: context deadline exceeded",
			fmt.Errorf("%w: %w", errStatePersist, context.DeadlineExceeded),
			errCodeInternal,
		},
		{
			"nil cause falls back to the message",
			"tracker plugin not installed",
			nil,
			errCodePluginMissing,
		},
		// sendViaClient can fail three ways and only one of them means "your
		// torrent client is unreachable". The sentinel is applied at the Add
		// call for exactly this reason: wrapping the whole function made a
		// missing plugin and an undecryptable config both render as
		// "check it's running", which is wrong and unactionable. Both fall to
		// `unknown`, which shows the raw text — accurate and already actionable
		// ("client plugin %q not installed" says precisely what is wrong).
		// Note the client-plugin message does NOT match the "plugin not
		// installed" substring that yields errCodePluginMissing, because the
		// quoted client name sits between the two halves; that code belongs to
		// the tracker-plugin path, whose copy is tracker-specific anyway.
		// Neither code is in the rotation set, so nothing here rotates.
		{
			"missing client plugin is not a client outage",
			`client plugin "qbittorrent" not installed`,
			errors.New(`client plugin "qbittorrent" not installed`),
			errCodeUnknown,
		},
		{
			"undecryptable client config is not a client outage",
			"decrypt client config: cipher: message authentication failed",
			errors.New("decrypt client config: cipher: message authentication failed"),
			errCodeUnknown,
		},
		{
			"unrelated cause falls back to the message",
			`kinozal GET: Get "https://kinozal.me/details.php?id=1": context deadline exceeded`,
			errors.New("boom"),
			errCodeTimeout,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyCause(tt.msg, tt.cause); got != tt.want {
				t.Errorf("classifyCause(%q, %v) = %q, want %q", tt.msg, tt.cause, got, tt.want)
			}
		})
	}
}

// --- Stale-write guard: a reset landing mid-check ------------------------

// fakeTopicsGuarded models repo.Topics' optimistic-concurrency guard on
// last_checked_at in memory: it holds the persisted state and accepts a
// RecordCheckResult only while the token the caller observed still matches.
//
// The guard itself is SQL (`WHERE ... last_checked_at IS NOT DISTINCT FROM $7`)
// and is pinned by repo's pgxmock tests. This fake exists to prove the other
// half — that the scheduler carries the observed token through to the write and
// survives a rejection without undoing the reset.
type fakeTopicsGuarded struct {
	fakeTopics

	// The version token, as persisted. BOTH columns form it: last_checked_at
	// alone is NULL after every reset and so cannot tell two resets apart.
	lastCheckedAt  *time.Time
	nextCheckAt    time.Time
	lastHash       string
	downloaded     []string
	resets         int
	accepted       int
	rejected       int
	markRejected   int
	verifyCalls    int
	verifyRejected int
	// afterAcceptedMark runs once an episode mark has been accepted, so a test
	// can land a reset exactly between two loop iterations.
	afterAcceptedMark func()
}

// tokenMatches is the Go equivalent of the SQL guard
// `last_checked_at IS NOT DISTINCT FROM $n AND next_check_at = $n+1`.
func (f *fakeTopicsGuarded) tokenMatches(t *domain.Topic) bool {
	return sameInstant(f.lastCheckedAt, t.LastCheckedAt) && f.nextCheckAt.Equal(t.NextCheckAt)
}

func (f *fakeTopicsGuarded) RecordCheckResult(_ context.Context, t *domain.Topic, hash string, updated bool, nextCheckAt time.Time, errMsg, errCode string) error {
	f.recordCalls = append(f.recordCalls, recordCall{t.ID, t.LastCheckedAt, hash, updated, nextCheckAt, errMsg, errCode})
	if !f.tokenMatches(t) {
		f.rejected++
		return repo.ErrStaleCheckResult
	}
	f.accepted++
	now := time.Now()
	f.lastCheckedAt = &now
	f.nextCheckAt = nextCheckAt
	if hash != "" {
		f.lastHash = hash
	}
	return nil
}

// VerifyCheckState is the read-only form of the same token guard, applied
// before a submission instead of after one.
func (f *fakeTopicsGuarded) VerifyCheckState(_ context.Context, t *domain.Topic) error {
	f.verifyCalls++
	if !f.tokenMatches(t) {
		f.verifyRejected++
		return repo.ErrStaleCheckResult
	}
	return nil
}

func (f *fakeTopicsGuarded) MarkEpisodeDownloaded(_ context.Context, t *domain.Topic, packed string) error {
	f.markCalls = append(f.markCalls, markCall{t.ID, packed})
	if !f.tokenMatches(t) {
		f.markRejected++
		return repo.ErrStaleCheckResult
	}
	f.downloaded = append(f.downloaded, packed)
	if f.afterAcceptedMark != nil {
		f.afterAcceptedMark()
	}
	return nil
}

// resetClock stands in for Postgres now() as ResetCheckState writes it. Only
// one property matters — consecutive resets stamp distinct, increasing
// next_check_at values — so the fake advances a counter rather than depending
// on the host clock's resolution.
var resetClock = time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

// reset mirrors what repo.Topics.ResetCheckState does to the guard's inputs:
// last_checked_at cleared, downloaded_episodes dropped, and — crucially — a
// fresh next_check_at stamped, which is what makes consecutive resets
// distinguishable.
func (f *fakeTopicsGuarded) reset() {
	f.resets++
	f.lastCheckedAt = nil
	f.nextCheckAt = resetClock.Add(time.Duration(f.resets) * time.Microsecond)
	f.lastHash = ""
	f.downloaded = nil
}

// sameInstant is the Go equivalent of SQL's IS NOT DISTINCT FROM over a
// nullable timestamp: two NULLs match, a NULL and a value do not.
func sameInstant(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// TestRunCheck_ResetMidCheck_DoesNotClobberReset reproduces the race the
// last_checked_at guard exists for. A worker is out at the tracker when the
// user resets the topic; the reset removes the delivered torrents, drops the
// hash and queues an immediate re-check. Without the guard the worker's write
// lands afterwards and restores the pre-reset hash plus a backoff
// next_check_at — the topic then looks like it was never reset, except its
// torrents are gone from the client, and nothing re-downloads until the
// tracker's own hash changes.
func TestRunCheck_ResetMidCheck_DoesNotClobberReset(t *testing.T) {
	observed := time.Now().Add(-time.Hour)
	observedNext := time.Now().Add(-time.Minute)
	guarded := &fakeTopicsGuarded{
		lastCheckedAt: &observed,
		nextCheckAt:   observedNext,
		lastHash:      "old-hash",
	}

	tr := &fakeTracker{
		name:      "faketracker",
		checks:    []checkResult{{check: &domain.Check{Hash: "new-hash"}}},
		downloads: []downloadResult{{payload: &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:abc"}}},
	}
	f := newFixture(t, tr)
	f.s.topics = guarded
	f.topic.LastCheckedAt = &observed
	f.topic.NextCheckAt = observedNext
	// The reset lands while the worker is inside tr.Check.
	tr.onCheck = guarded.reset

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	// f.topics is the fixture's default fake; the guarded one replaced it on
	// s.topics, so its own call log is the authoritative record here.
	if len(guarded.recordCalls) != 1 {
		t.Fatalf("want 1 RecordCheckResult call, got %d", len(guarded.recordCalls))
	}
	rec := guarded.recordCalls[0]
	if rec.observed == nil || !rec.observed.Equal(observed) {
		t.Fatalf("worker carried observed last_checked_at %v, want %v", rec.observed, observed)
	}
	if guarded.accepted != 0 || guarded.rejected != 1 {
		t.Fatalf("stale write not rejected: accepted=%d rejected=%d", guarded.accepted, guarded.rejected)
	}
	if guarded.lastHash != "" {
		t.Errorf("reset clobbered: last_hash = %q, want empty", guarded.lastHash)
	}
	if guarded.lastCheckedAt != nil {
		t.Errorf("reset clobbered: last_checked_at = %v, want nil", guarded.lastCheckedAt)
	}
}

// TestRunCheck_AfterReset_WritesResult is the other half of the guard. The
// fresh check that a reset queues observes last_checked_at = NULL, and NULL IS
// NOT DISTINCT FROM NULL is true, so its result must land — a `= $7` guard
// would silently drop every post-reset check instead.
func TestRunCheck_AfterReset_WritesResult(t *testing.T) {
	guarded := &fakeTopicsGuarded{}
	guarded.reset() // freshly reset: no token, no hash, fresh next_check_at

	tr := &fakeTracker{
		name:      "faketracker",
		checks:    []checkResult{{check: &domain.Check{Hash: "new-hash"}}},
		downloads: []downloadResult{{payload: &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:abc"}}},
	}
	f := newFixture(t, tr)
	f.s.topics = guarded
	f.topic.LastCheckedAt = nil
	f.topic.NextCheckAt = guarded.nextCheckAt
	f.topic.LastHash = ""

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if guarded.accepted != 1 || guarded.rejected != 0 {
		t.Fatalf("post-reset write not accepted: accepted=%d rejected=%d", guarded.accepted, guarded.rejected)
	}
	if guarded.lastHash != "new-hash" {
		t.Errorf("last_hash = %q, want new-hash", guarded.lastHash)
	}
	if f.clientPlugin.addCalls != 1 {
		t.Errorf("client Add calls = %d, want 1 (the re-download)", f.clientPlugin.addCalls)
	}
}

// TestRunCheck_SecondReset_SurvivesWorkerDispatchedByFirstReset is the case a
// last_checked_at-only token could never catch, because NULL is not monotonic.
//
// Reset #1 clears last_checked_at and sets next_check_at = now(), so a worker
// is dispatched almost immediately and observes the NULL token. While it is
// out at the tracker — 10-20s for a Cloudflare-gated tracker going through
// FlareSolverr — the user resets again, and reset #2 removes the torrents from
// the client and deletes their on-disk data. Under the single-column guard the
// worker's write then MATCHED (NULL IS NOT DISTINCT FROM NULL) and restored the
// pre-reset hash: reset #2 silently undone after its irreversible half had
// already run, with nothing left to re-download it.
//
// This is reachable through the workflow the reset handler itself prescribes —
// its fail-closed 500s tell the user to retry the reset, and reset #1 has
// already armed the scheduler. next_check_at differs per reset, so the pair
// rejects the write.
func TestRunCheck_SecondReset_SurvivesWorkerDispatchedByFirstReset(t *testing.T) {
	guarded := &fakeTopicsGuarded{}
	guarded.reset() // reset #1

	tr := &fakeTracker{
		name:      "faketracker",
		checks:    []checkResult{{check: &domain.Check{Hash: "new-hash"}}},
		downloads: []downloadResult{{payload: &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:abc"}}},
	}
	f := newFixture(t, tr)
	f.s.topics = guarded
	// The worker carries the token reset #1 left behind: a NULL
	// last_checked_at, and reset #1's next_check_at.
	f.topic.LastCheckedAt = nil
	f.topic.NextCheckAt = guarded.nextCheckAt
	f.topic.LastHash = ""
	// Reset #2 lands while the worker is inside tr.Check.
	tr.onCheck = guarded.reset

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if guarded.accepted != 0 || guarded.rejected != 1 {
		t.Fatalf("reset #1's worker overwrote reset #2: accepted=%d rejected=%d", guarded.accepted, guarded.rejected)
	}
	if guarded.lastHash != "" {
		t.Errorf("reset #2 clobbered: last_hash = %q, want empty", guarded.lastHash)
	}
	if guarded.lastCheckedAt != nil {
		t.Errorf("reset #2 clobbered: last_checked_at = %v, want nil", guarded.lastCheckedAt)
	}
}

// TestRunCheck_ResetBeforeSubmit_DoesNotSubmit covers the pre-submit token
// check: a reset that lands between Check and Add must stop the worker BEFORE
// it hands anything to the client.
//
// Handing over a payload is the one step of a tick a reset cannot undo
// afterwards. The reset removes exactly the torrents its delivery snapshot
// listed, so a torrent added after that snapshot survives it — with
// delete_data set the reset reports success while that torrent's files stay on
// disk, and the re-delivered release then rechecks and resumes against them.
//
// Aborting is not an error: the check ends cleanly, and the check the reset
// queued re-downloads from scratch, which is what the reset asked for.
func TestRunCheck_ResetBeforeSubmit_DoesNotSubmit(t *testing.T) {
	guarded := &fakeTopicsGuarded{}
	guarded.reset()

	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{{check: &domain.Check{
			Hash: "new-hash",
			Extra: map[string]any{
				"pending_episodes": []string{"S01E01", "S01E02"},
				"pending_human":    []string{"s01e01", "s01e02"},
			},
		}}},
		downloads: []downloadResult{
			{payload: &domain.Payload{MagnetURI: "magnet:1"}},
			{payload: &domain.Payload{MagnetURI: "magnet:2"}},
		},
	}
	f := newFixture(t, tr)
	f.s.topics = guarded
	f.topic.LastCheckedAt = nil
	f.topic.NextCheckAt = guarded.nextCheckAt
	f.topic.LastHash = ""
	// The reset lands while the worker is still at the tracker, so the token
	// has already moved on by the time the worker reaches the submit.
	tr.onCheck = guarded.reset

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if f.clientPlugin.addCalls != 0 {
		t.Fatalf("client Add calls = %d, want 0 — nothing may reach the client after the reset", f.clientPlugin.addCalls)
	}
	if guarded.verifyRejected != 1 {
		t.Errorf("want the pre-submit guard to reject once, got %d (verifies attempted: %d)",
			guarded.verifyRejected, guarded.verifyCalls)
	}
	// Nothing was submitted, so nothing may be marked downloaded either.
	if len(guarded.markCalls) != 0 {
		t.Errorf("episode mark attempted after an aborted submit: %v", guarded.markCalls)
	}
	if len(guarded.downloaded) != 0 {
		t.Errorf("episode stranded: downloaded_episodes = %v, want empty", guarded.downloaded)
	}
	// The check's own result is discarded too, so the reset stands whole.
	if guarded.accepted != 0 || guarded.rejected != 1 {
		t.Errorf("check result not discarded: accepted=%d rejected=%d", guarded.accepted, guarded.rejected)
	}
}

// TestRunCheck_ResetMidEpisodeLoop_DoesNotStrandEpisode covers the residual
// gap the pre-submit check cannot cover: a reset landing after Add returns but
// before the episode mark. Only per-episode trackers (WithEpisodeFilter —
// LostFilm today) can hit it, since downloadAllPending returns before the mark
// for single-payload plugins.
//
// Unguarded, the mark fires later in the check than the reset: the worker
// delivers episode E, the reset then removes E from the client, deletes its
// data and its delivery row, and clears downloaded_episodes — and only then
// does the append land, re-marking E as downloaded. E ends up absent from the
// client, absent from disk, and skipped by every future check.
//
// Dropping the mark is the correct outcome: the post-reset check re-downloads
// the episode, which is what the reset asked for.
func TestRunCheck_ResetMidEpisodeLoop_DoesNotStrandEpisode(t *testing.T) {
	guarded := &fakeTopicsGuarded{}
	guarded.reset()

	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{{check: &domain.Check{
			Hash: "new-hash",
			Extra: map[string]any{
				"pending_episodes": []string{"S01E01", "S01E02"},
				"pending_human":    []string{"s01e01", "s01e02"},
			},
		}}},
		downloads: []downloadResult{
			{payload: &domain.Payload{MagnetURI: "magnet:1"}},
			{payload: &domain.Payload{MagnetURI: "magnet:2"}},
		},
	}
	f := newFixture(t, tr)
	f.s.topics = guarded
	f.topic.LastCheckedAt = nil
	f.topic.NextCheckAt = guarded.nextCheckAt
	f.topic.LastHash = ""
	// The reset lands in the one gap the pre-submit check leaves open: after
	// the payload has reached the client, before the mark is written. Fires
	// once so the second iteration is not double-reset.
	f.clientPlugin.onAdd = func() {
		f.clientPlugin.onAdd = nil
		guarded.reset()
	}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if guarded.markRejected != 1 {
		t.Fatalf("want the episode mark rejected once, got %d (marks attempted: %d)",
			guarded.markRejected, len(guarded.markCalls))
	}
	if len(guarded.downloaded) != 0 {
		t.Errorf("episode stranded: downloaded_episodes = %v, want empty so the reset re-downloads it",
			guarded.downloaded)
	}
	// The loop must stop on the rejection rather than keep draining episodes
	// into state that no longer exists.
	if f.clientPlugin.addCalls != 1 {
		t.Errorf("client Add calls = %d, want 1 — the loop should stop once the guard rejects", f.clientPlugin.addCalls)
	}
	// And the check's own result is discarded too, so the reset stands whole.
	if guarded.accepted != 0 || guarded.rejected != 1 {
		t.Errorf("check result not discarded: accepted=%d rejected=%d", guarded.accepted, guarded.rejected)
	}
}

// TestRunCheck_ResetMidEpisodeLoop_PreservesEarlierProgress proves the abort
// keeps partial progress. A reset landing after the first episode is fully
// delivered and marked must stop the second submission without discarding the
// first: the episode is in the client, its mark persisted, and the tick reports
// it as delivered rather than failing.
func TestRunCheck_ResetMidEpisodeLoop_PreservesEarlierProgress(t *testing.T) {
	guarded := &fakeTopicsGuarded{}
	guarded.reset()

	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{{check: &domain.Check{
			Hash: "new-hash",
			Extra: map[string]any{
				"pending_episodes": []string{"S01E01", "S01E02", "S01E03"},
				"pending_human":    []string{"s01e01", "s01e02", "s01e03"},
			},
		}}},
		downloads: []downloadResult{
			{payload: &domain.Payload{MagnetURI: "magnet:1"}},
			{payload: &domain.Payload{MagnetURI: "magnet:2"}},
			{payload: &domain.Payload{MagnetURI: "magnet:3"}},
		},
	}
	f := newFixture(t, tr)
	f.s.topics = guarded
	f.topic.LastCheckedAt = nil
	f.topic.NextCheckAt = guarded.nextCheckAt
	f.topic.LastHash = ""

	// Let episode 1 complete end to end, then land the reset so episode 2's
	// pre-submit check is the one that rejects.
	guardedAfterFirstMark(guarded)

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if f.clientPlugin.addCalls != 1 {
		t.Fatalf("client Add calls = %d, want 1 — episode 1 delivered, episode 2 refused", f.clientPlugin.addCalls)
	}
	if guarded.verifyRejected != 1 {
		t.Errorf("want exactly one pre-submit rejection, got %d", guarded.verifyRejected)
	}
	// Episode 1's mark was written and accepted before the reset landed. (The
	// reset then clears downloaded_episodes, which is correct and is why this
	// asserts on the mark rather than on the surviving list — the reset asked
	// for a re-download.)
	if len(guarded.markCalls) != 1 || guarded.markRejected != 0 {
		t.Errorf("episode 1's mark: %d attempted, %d rejected — want 1 and 0",
			len(guarded.markCalls), guarded.markRejected)
	}
	// The tick still reports what it delivered, so the user is told episode 1
	// landed rather than the whole tick vanishing.
	submitted := f.emitter.ofType(events.DownloadSubmitted)
	if len(submitted) != 1 {
		t.Fatalf("want the delivered episode reported once, got %d submitted events", len(submitted))
	}
	if !strings.Contains(submitted[0].Body, "s01e01") {
		t.Errorf("submitted event does not name the delivered episode: %q", submitted[0].Body)
	}
	// The abort is not a failure: the check records a clean result (which the
	// token guard then discards, as it should).
	if len(guarded.recordCalls) != 1 {
		t.Fatalf("want one recorded result, got %d", len(guarded.recordCalls))
	}
	if guarded.recordCalls[0].errMsg != "" {
		t.Errorf("abort reported as an error: %q", guarded.recordCalls[0].errMsg)
	}
}

// guardedAfterFirstMark makes the fake reset itself the moment the first
// episode mark is accepted — the point at which episode 1 is fully done and
// episode 2 has not yet been submitted.
func guardedAfterFirstMark(g *fakeTopicsGuarded) {
	g.afterAcceptedMark = func() {
		g.afterAcceptedMark = nil
		g.reset()
	}
}

// TestRunCheck_NoReset_StillSubmits is the other half of the pre-submit check:
// with an untouched token the guard must wave the submission through. Without
// it, a guard that rejected everything would look identical to a working one in
// every abort test above.
func TestRunCheck_NoReset_StillSubmits(t *testing.T) {
	guarded := &fakeTopicsGuarded{}
	guarded.reset()

	tr := &fakeTracker{
		name:      "faketracker",
		checks:    []checkResult{{check: &domain.Check{Hash: "new-hash"}}},
		downloads: []downloadResult{{payload: &domain.Payload{MagnetURI: "magnet:1"}}},
	}
	f := newFixture(t, tr)
	f.s.topics = guarded
	f.topic.LastCheckedAt = nil
	f.topic.NextCheckAt = guarded.nextCheckAt
	f.topic.LastHash = ""

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if f.clientPlugin.addCalls != 1 {
		t.Fatalf("client Add calls = %d, want 1 — a valid token must not block the submit", f.clientPlugin.addCalls)
	}
	if guarded.verifyCalls != 1 || guarded.verifyRejected != 0 {
		t.Errorf("guard consulted %d time(s), rejected %d — want 1 and 0",
			guarded.verifyCalls, guarded.verifyRejected)
	}
	// The check completes normally and its result is persisted.
	if guarded.accepted != 1 || guarded.rejected != 0 {
		t.Errorf("check result not persisted: accepted=%d rejected=%d", guarded.accepted, guarded.rejected)
	}
	if guarded.lastHash != "new-hash" {
		t.Errorf("last_hash = %q, want %q", guarded.lastHash, "new-hash")
	}
}
