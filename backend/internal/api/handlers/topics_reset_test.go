package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/api/middleware"
	"github.com/artyomsv/marauder/backend/internal/auth"
	"github.com/artyomsv/marauder/backend/internal/clientremove"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/events"
)

// fakeRemover is a torrentRemover that returns canned results and records the
// arguments it was called with.
type fakeRemover struct {
	results   []clientremove.Result
	gotDelete bool
	gotHashes map[uuid.UUID][]string
	called    bool
}

func (f *fakeRemover) Remove(_ context.Context, _ uuid.UUID, byClient map[uuid.UUID][]string, deleteData bool) []clientremove.Result {
	f.called = true
	f.gotDelete = deleteData
	f.gotHashes = byClient
	return f.results
}

// resetWarnings decodes the reset response's warnings array so tests can
// assert on the sentences the user actually sees.
func resetWarnings(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var got struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode reset response: %v", err)
	}
	return got.Warnings
}

// resetRequest builds a POST /topics/{id}/reset request carrying the chi URL
// param and an authenticated user, using the same claims-in-context and
// withURLParam helpers the other handler tests in this package use.
func resetRequest(t *testing.T, topicID, userID uuid.UUID, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/topics/"+topicID.String()+"/reset", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxClaims,
		&auth.Claims{UserID: userID.String()}))
	return withURLParam(req, "id", topicID.String())
}

func TestTopicsReset_WipesStateAndForwardsDeleteData(t *testing.T) {
	topicID, userID, clientID := uuid.New(), uuid.New(), uuid.New()
	store := &fakeTopicStore{
		getByID: &domain.Topic{ID: topicID, UserID: userID, DisplayName: "Show", URL: "https://tracker/1"},
	}
	deliveries := &fakeDeliveriesStore{items: []*domain.TopicDelivery{
		{Infohash: "aaa", ClientID: &clientID},
	}}
	remover := &fakeRemover{results: []clientremove.Result{
		{ClientID: clientID, ClientName: "transmission", Hashes: []string{"aaa"}, OK: true},
	}}
	var emitted []events.Event
	h := &Topics{
		Topics:     store,
		Deliveries: deliveries,
		Remover:    remover,
		Emit:       func(_ context.Context, ev events.Event) { emitted = append(emitted, ev) },
	}

	rec := httptest.NewRecorder()
	h.Reset(rec, resetRequest(t, topicID, userID, `{"delete_data":true}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Removed  int      `json:"removed"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Removed != 1 {
		t.Fatalf("want 1 removed, got %d", got.Removed)
	}
	if len(got.Warnings) != 0 {
		t.Fatalf("want no warnings, got %v", got.Warnings)
	}
	if !remover.gotDelete {
		t.Error("delete_data was not forwarded to the remover")
	}
	if !deliveries.deleted {
		t.Error("delivery rows were not deleted")
	}
	if len(store.resetCalls) != 1 || store.resetCalls[0] != [2]uuid.UUID{topicID, userID} {
		t.Errorf("ResetCheckState called wrong: %v", store.resetCalls)
	}
	// The delivery delete must be scoped to the owner, not just the topic, so
	// the repo's ownership join can do its job.
	if deliveries.deletedFor != [2]uuid.UUID{topicID, userID} {
		t.Errorf("DeleteForTopic not scoped to the owner: %v", deliveries.deletedFor)
	}
	if len(emitted) != 1 || emitted[0].Type != events.TopicReset {
		t.Errorf("want one topic.reset event, got %v", emitted)
	}
}

func TestTopicsReset_OmittedDeleteDataDefaultsToFalse(t *testing.T) {
	topicID, userID, clientID := uuid.New(), uuid.New(), uuid.New()
	store := &fakeTopicStore{getByID: &domain.Topic{ID: topicID, UserID: userID}}
	deliveries := &fakeDeliveriesStore{items: []*domain.TopicDelivery{
		{Infohash: "aaa", ClientID: &clientID},
	}}
	remover := &fakeRemover{results: []clientremove.Result{
		{ClientID: clientID, ClientName: "qbittorrent", Hashes: []string{"aaa"}, OK: true},
	}}
	h := &Topics{Topics: store, Deliveries: deliveries, Remover: remover}

	rec := httptest.NewRecorder()
	h.Reset(rec, resetRequest(t, topicID, userID, ``))

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if remover.gotDelete {
		t.Error("delete_data must default to false when the field is omitted")
	}
}

func TestTopicsReset_RemovalFailureStillWipesStateAndWarns(t *testing.T) {
	topicID, userID, clientID := uuid.New(), uuid.New(), uuid.New()
	store := &fakeTopicStore{getByID: &domain.Topic{ID: topicID, UserID: userID}}
	deliveries := &fakeDeliveriesStore{items: []*domain.TopicDelivery{
		{Infohash: "aaa", ClientID: &clientID},
	}}
	remover := &fakeRemover{results: []clientremove.Result{{
		ClientID:   clientID,
		ClientName: "transmission",
		Hashes:     []string{"aaa"},
		Reason:     clientremove.ReasonError,
		Err:        errors.New("connection refused"),
	}}}
	h := &Topics{Topics: store, Deliveries: deliveries, Remover: remover}

	rec := httptest.NewRecorder()
	h.Reset(rec, resetRequest(t, topicID, userID, `{"delete_data":false}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("reset must be fail-open on removal failure, got %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Removed  int      `json:"removed"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Removed != 0 {
		t.Errorf("want 0 removed, got %d", got.Removed)
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "transmission") {
		t.Errorf("want a warning naming the client, got %v", got.Warnings)
	}
	if !deliveries.deleted {
		t.Error("delivery rows must be deleted even when removal failed")
	}
	if len(store.resetCalls) != 1 {
		t.Error("topic state must be reset even when removal failed")
	}
}

// TestTopicsReset_DeliveriesWithoutAClientAreWarnedAbout covers the rows
// GroupByClient drops: a delivery with no client id cannot be addressed, but
// the reset deletes its row anyway, so the torrent survives in whatever client
// holds it — now untracked. Silence there reads as "Removed 0 torrent(s)" with
// nothing to explain it.
func TestTopicsReset_DeliveriesWithoutAClientAreWarnedAbout(t *testing.T) {
	topicID, userID, clientID := uuid.New(), uuid.New(), uuid.New()
	store := &fakeTopicStore{getByID: &domain.Topic{ID: topicID, UserID: userID}}
	deliveries := &fakeDeliveriesStore{items: []*domain.TopicDelivery{
		{Infohash: "aaa", ClientID: &clientID},
		{Infohash: "bbb", ClientID: nil},
		{Infohash: "ccc", ClientID: nil},
	}}
	remover := &fakeRemover{results: []clientremove.Result{
		{ClientID: clientID, ClientName: "qbittorrent", Hashes: []string{"aaa"}, OK: true},
	}}
	h := &Topics{Topics: store, Deliveries: deliveries, Remover: remover}

	rec := httptest.NewRecorder()
	h.Reset(rec, resetRequest(t, topicID, userID, `{"delete_data":false}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Removed  int      `json:"removed"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Removed != 1 {
		t.Errorf("want 1 removed (the addressable row), got %d", got.Removed)
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "2 torrent(s) had no recorded client") {
		t.Fatalf("want one warning naming the 2 unaddressable rows, got %v", got.Warnings)
	}
}

// TestTopicsReset_OnlyClientlessDeliveries_WarnsRatherThanSilentZero is the
// degenerate case of the above: nothing is addressable, so the remover is never
// called and the early return must still carry the warning out.
func TestTopicsReset_OnlyClientlessDeliveries_WarnsRatherThanSilentZero(t *testing.T) {
	topicID, userID := uuid.New(), uuid.New()
	store := &fakeTopicStore{getByID: &domain.Topic{ID: topicID, UserID: userID}}
	deliveries := &fakeDeliveriesStore{items: []*domain.TopicDelivery{
		{Infohash: "aaa", ClientID: nil},
	}}
	remover := &fakeRemover{}
	h := &Topics{Topics: store, Deliveries: deliveries, Remover: remover}

	rec := httptest.NewRecorder()
	h.Reset(rec, resetRequest(t, topicID, userID, ``))

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if remover.called {
		t.Error("remover must not be called when nothing is addressable")
	}
	var got struct {
		Removed  int      `json:"removed"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "1 torrent(s) had no recorded client") {
		t.Fatalf("want one warning, got %v", got.Warnings)
	}
}

// TestTopicsReset_ListFailureIsWarnedAbout covers the branch where the
// delivery list itself fails. Removal is fail-open, so the reset proceeds —
// but silently returning "Removed 0 torrent(s)" would tell the user their old
// torrents are handled when nothing was even looked at.
func TestTopicsReset_ListFailureIsWarnedAbout(t *testing.T) {
	topicID, userID := uuid.New(), uuid.New()
	store := &fakeTopicStore{getByID: &domain.Topic{ID: topicID, UserID: userID}}
	deliveries := &fakeDeliveriesStore{listErr: errors.New("connection refused")}
	remover := &fakeRemover{}
	h := &Topics{Topics: store, Deliveries: deliveries, Remover: remover}

	rec := httptest.NewRecorder()
	h.Reset(rec, resetRequest(t, topicID, userID, `{}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("listing is fail-open, want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	warnings := resetWarnings(t, rec)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "could not list delivered torrents") {
		t.Fatalf("want a warning naming the list failure, got %v", warnings)
	}
	if remover.called {
		t.Error("nothing was listed, so there is nothing to remove")
	}
	if len(store.resetCalls) != 1 {
		t.Error("the topic must still be reset — client removal is fail-open")
	}
}

// TestTopicsReset_NoRemoverConfigured_Warns covers the nil Remover seam: the
// torrents stay in the client, so the reset must say so rather than report a
// clean zero.
func TestTopicsReset_NoRemoverConfigured_Warns(t *testing.T) {
	topicID, userID, clientID := uuid.New(), uuid.New(), uuid.New()
	store := &fakeTopicStore{getByID: &domain.Topic{ID: topicID, UserID: userID}}
	deliveries := &fakeDeliveriesStore{items: []*domain.TopicDelivery{
		{Infohash: "aaa", ClientID: &clientID},
	}}
	h := &Topics{Topics: store, Deliveries: deliveries} // Remover nil

	rec := httptest.NewRecorder()
	h.Reset(rec, resetRequest(t, topicID, userID, `{}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	warnings := resetWarnings(t, rec)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "torrent removal is not configured") {
		t.Fatalf("want a warning about the missing remover, got %v", warnings)
	}
}

// TestTopicsReset_NoDeliveriesStoreConfigured_Warns covers the other nil seam.
// Without a Deliveries store the fail-closed row delete is skipped entirely,
// so the scheduler gets armed with every delivery row intact — the exact state
// that step exists to prevent. Returning a clean 200 there would report the
// failure as a success, so it must warn like its Remover sibling does.
func TestTopicsReset_NoDeliveriesStoreConfigured_Warns(t *testing.T) {
	topicID, userID := uuid.New(), uuid.New()
	store := &fakeTopicStore{getByID: &domain.Topic{ID: topicID, UserID: userID}}
	h := &Topics{Topics: store, Remover: &fakeRemover{}} // Deliveries nil

	rec := httptest.NewRecorder()
	h.Reset(rec, resetRequest(t, topicID, userID, `{}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	warnings := resetWarnings(t, rec)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "delivery tracking is not configured") {
		t.Fatalf("want a warning about the missing delivery store, got %v", warnings)
	}
}

// TestTopicsReset_DisconnectedClientStillFinishesFailClosedSteps covers the
// user closing the tab mid-reset. Go cancels r.Context() the moment the
// connection drops, so running the fail-closed steps on it would abandon the
// reset exactly half-done: torrents already removed and their files already
// deleted (step 1), but delivery rows intact, hash unchanged, scheduler not
// armed, nothing re-downloaded — and the 500 explaining it written into a
// socket nobody is reading. That is the precise state those steps exist to
// prevent, so they must run on a context detached from the request.
func TestTopicsReset_DisconnectedClientStillFinishesFailClosedSteps(t *testing.T) {
	topicID, userID, clientID := uuid.New(), uuid.New(), uuid.New()
	store := &fakeTopicStore{getByID: &domain.Topic{ID: topicID, UserID: userID}}
	deliveries := &fakeDeliveriesStore{items: []*domain.TopicDelivery{
		{Infohash: "aaa", ClientID: &clientID},
	}}
	remover := &fakeRemover{results: []clientremove.Result{
		{ClientID: clientID, ClientName: "transmission", Hashes: []string{"aaa"}, OK: true},
	}}
	h := &Topics{Topics: store, Deliveries: deliveries, Remover: remover}

	req := resetRequest(t, topicID, userID, `{"delete_data":true}`)
	// The client is already gone by the time the handler runs — the same state
	// net/http leaves the request in once the connection closes.
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.Reset(rec, req)

	if !deliveries.deleted {
		t.Error("delivery rows must still be deleted after the client disconnects")
	}
	if len(store.resetCalls) != 1 {
		t.Errorf("check state must still be reset after the client disconnects, got %d calls", len(store.resetCalls))
	}
	if rec.Code != http.StatusOK {
		t.Errorf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTopicsReset_ForeignTopicIsNotFound(t *testing.T) {
	topicID, intruder := uuid.New(), uuid.New()
	// GetByID is user-scoped in the real repo, so a foreign topic surfaces as
	// ErrNotFound rather than as a topic with a different UserID.
	store := &fakeTopicStore{getByIDErr: repo.ErrNotFound}
	deliveries := &fakeDeliveriesStore{}
	remover := &fakeRemover{}
	h := &Topics{Topics: store, Deliveries: deliveries, Remover: remover}

	rec := httptest.NewRecorder()
	h.Reset(rec, resetRequest(t, topicID, intruder, `{}`))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if remover.called {
		t.Error("must not touch the client for a topic the caller does not own")
	}
	if deliveries.deleted {
		t.Error("must not delete delivery rows for a topic the caller does not own")
	}
	if len(store.resetCalls) != 0 {
		t.Error("must not reset a topic the caller does not own")
	}
}

// problemDetail decodes the RFC-7807 body's "detail" field so tests can
// assert on the actionable text, not just the status code.
func problemDetail(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var got struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode problem response: %v", err)
	}
	return got.Detail
}

// TestTopicsReset_DeliveryDeleteFailureAborts covers the case where step 1
// (client removal) already succeeded before step 2 (delivery-row delete)
// fails: the torrent is gone from the client, but nothing has told the user
// that, and the topic's hash is unchanged so the scheduler will not
// re-download on its own. The 500 body must carry the removed count and say
// to retry, or the removal is silently lost.
func TestTopicsReset_DeliveryDeleteFailureAborts(t *testing.T) {
	topicID, userID, clientID := uuid.New(), uuid.New(), uuid.New()
	store := &fakeTopicStore{getByID: &domain.Topic{ID: topicID, UserID: userID}}
	deliveries := &fakeDeliveriesStore{
		items:     []*domain.TopicDelivery{{Infohash: "aaa", ClientID: &clientID}},
		deleteErr: errors.New("connection refused"),
	}
	remover := &fakeRemover{results: []clientremove.Result{
		{ClientID: clientID, ClientName: "transmission", Hashes: []string{"aaa"}, OK: true},
	}}
	h := &Topics{Topics: store, Deliveries: deliveries, Remover: remover}

	rec := httptest.NewRecorder()
	h.Reset(rec, resetRequest(t, topicID, userID, `{}`))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", rec.Code, rec.Body.String())
	}
	detail := problemDetail(t, rec)
	if !strings.Contains(detail, "1") || !strings.Contains(strings.ToLower(detail), "retry") {
		t.Errorf("500 body must report the removed count and tell the user to retry, got %q", detail)
	}
	if len(store.resetCalls) != 0 {
		t.Error("topic state must not be reset when the delivery rows survived")
	}
}

// TestTopicsReset_CheckStateResetFailure_ReportsRemovedCountAndRetry covers
// the last fail-closed step: by this point client removal AND the delivery
// delete have already succeeded, so a failure here must not report a bare
// generic error — the user needs to know torrents are already gone and to
// retry the reset to finish arming the scheduler.
func TestTopicsReset_CheckStateResetFailure_ReportsRemovedCountAndRetry(t *testing.T) {
	topicID, userID, clientID := uuid.New(), uuid.New(), uuid.New()
	store := &fakeTopicStore{
		getByID:  &domain.Topic{ID: topicID, UserID: userID},
		resetErr: errors.New("connection refused"),
	}
	deliveries := &fakeDeliveriesStore{items: []*domain.TopicDelivery{
		{Infohash: "aaa", ClientID: &clientID},
	}}
	remover := &fakeRemover{results: []clientremove.Result{
		{ClientID: clientID, ClientName: "transmission", Hashes: []string{"aaa"}, OK: true},
	}}
	h := &Topics{Topics: store, Deliveries: deliveries, Remover: remover}

	rec := httptest.NewRecorder()
	h.Reset(rec, resetRequest(t, topicID, userID, `{}`))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", rec.Code, rec.Body.String())
	}
	detail := problemDetail(t, rec)
	if !strings.Contains(detail, "1") || !strings.Contains(strings.ToLower(detail), "retry") {
		t.Errorf("500 body must report the removed count and tell the user to retry, got %q", detail)
	}
	if !deliveries.deleted {
		t.Error("delivery rows must already be deleted by the time the check-state reset runs")
	}
}

// TestTopicsReset_CheckStateResetNotFound_404 covers ResetCheckState
// surfacing repo.ErrNotFound (e.g. the topic was deleted concurrently) —
// this must map to 404, not the generic 500 path.
func TestTopicsReset_CheckStateResetNotFound_404(t *testing.T) {
	topicID, userID := uuid.New(), uuid.New()
	store := &fakeTopicStore{
		getByID:  &domain.Topic{ID: topicID, UserID: userID},
		resetErr: repo.ErrNotFound,
	}
	deliveries := &fakeDeliveriesStore{}
	h := &Topics{Topics: store, Deliveries: deliveries, Remover: &fakeRemover{}}

	rec := httptest.NewRecorder()
	h.Reset(rec, resetRequest(t, topicID, userID, `{}`))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------- per-topic single-flight ----------

// concurrentResetStore is a topicStore that is safe under concurrent use and
// whose GetByID echoes back the requested id, so one handler can serve resets
// of two different topics at the same time.
type concurrentResetStore struct {
	userID uuid.UUID

	mu    sync.Mutex
	reset []uuid.UUID
	err   error // returned by ResetCheckState
}

func (s *concurrentResetStore) Create(context.Context, *domain.Topic) (*domain.Topic, error) {
	return nil, nil
}
func (s *concurrentResetStore) GetByID(_ context.Context, id uuid.UUID, _ *uuid.UUID) (*domain.Topic, error) {
	return &domain.Topic{ID: id, UserID: s.userID, DisplayName: "Show", URL: "https://tracker/x"}, nil
}
func (s *concurrentResetStore) ListForUser(context.Context, uuid.UUID) ([]*domain.Topic, error) {
	return nil, nil
}
func (s *concurrentResetStore) Delete(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (s *concurrentResetStore) UpdateStatus(context.Context, uuid.UUID, uuid.UUID, domain.TopicStatus) error {
	return nil
}
func (s *concurrentResetStore) Update(context.Context, uuid.UUID, uuid.UUID, string, *uuid.UUID, *uuid.UUID, string, string, bool, bool, map[string]any) (*domain.Topic, error) {
	return nil, nil
}
func (s *concurrentResetStore) ResetCheckState(_ context.Context, id, _ uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.reset = append(s.reset, id)
	return nil
}
func (s *concurrentResetStore) QueueRecheck(context.Context, uuid.UUID, uuid.UUID) (repo.RecheckOutcome, error) {
	return repo.RecheckOutcome{}, nil
}

func (s *concurrentResetStore) resetIDs() []uuid.UUID {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]uuid.UUID(nil), s.reset...)
}

// concurrentDeliveries is a deliveriesStore safe under concurrent use. Every
// topic reports one delivery carrying a client id, so the reset always reaches
// the remover — removeDeliveredTorrents short-circuits before it otherwise.
type concurrentDeliveries struct {
	clientID uuid.UUID
	mu       sync.Mutex
}

func (d *concurrentDeliveries) ListForTopic(_ context.Context, topicID uuid.UUID) ([]*domain.TopicDelivery, error) {
	return []*domain.TopicDelivery{{TopicID: topicID, Infohash: "aaa", ClientID: &d.clientID}}, nil
}

func (d *concurrentDeliveries) DeleteForTopic(context.Context, uuid.UUID, uuid.UUID) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return 1, nil
}

// blockingRemover parks its FIRST Remove call until the test releases it, so a
// reset can be held open mid-flight while later resets still run to
// completion. A sync.Once would not do: it blocks every other caller inside
// Do until the first returns, which is the thing under test.
type blockingRemover struct {
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func newBlockingRemover() *blockingRemover {
	return &blockingRemover{entered: make(chan struct{}, 8), release: make(chan struct{})}
}

func (b *blockingRemover) Remove(context.Context, uuid.UUID, map[uuid.UUID][]string, bool) []clientremove.Result {
	if b.calls.Add(1) == 1 {
		b.entered <- struct{}{}
		<-b.release
	}
	return nil
}

// TestTopicsReset_ConcurrentSameTopicRejectedOtherTopicRuns is the throttle on
// this destructive endpoint: a second reset of a topic that already has one in
// flight is refused with 429, while a reset of a *different* topic is
// untouched — bulk reset deliberately fires one concurrent request per
// selected topic and must keep working.
func TestTopicsReset_ConcurrentSameTopicRejectedOtherTopicRuns(t *testing.T) {
	userID := uuid.New()
	held, other := uuid.New(), uuid.New()
	store := &concurrentResetStore{userID: userID}
	remover := newBlockingRemover()
	h := &Topics{
		Topics:     store,
		Deliveries: &concurrentDeliveries{clientID: uuid.New()},
		Remover:    remover,
	}

	firstRec := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		h.Reset(firstRec, resetRequest(t, held, userID, `{}`))
	}()
	// Park until the first reset is inside the remover — i.e. past the gate
	// and mid-way through its destructive work.
	<-remover.entered

	sameRec := httptest.NewRecorder()
	h.Reset(sameRec, resetRequest(t, held, userID, `{}`))
	if sameRec.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429 for a concurrent reset of the same topic, got %d: %s",
			sameRec.Code, sameRec.Body.String())
	}

	otherRec := httptest.NewRecorder()
	h.Reset(otherRec, resetRequest(t, other, userID, `{}`))
	if otherRec.Code != http.StatusOK {
		t.Fatalf("a concurrent reset of a different topic must succeed, got %d: %s",
			otherRec.Code, otherRec.Body.String())
	}

	close(remover.release)
	<-firstDone
	if firstRec.Code != http.StatusOK {
		t.Fatalf("held reset should have finished 200, got %d: %s", firstRec.Code, firstRec.Body.String())
	}

	// The rejected attempt must not have reset anything, and the gate must
	// have been released once the held reset finished.
	got := store.resetIDs()
	if len(got) != 2 {
		t.Fatalf("want exactly 2 state resets (held + other), got %v", got)
	}
	againRec := httptest.NewRecorder()
	h.Reset(againRec, resetRequest(t, held, userID, `{}`))
	if againRec.Code != http.StatusOK {
		t.Fatalf("gate not released after the first reset finished, got %d: %s",
			againRec.Code, againRec.Body.String())
	}
}

// TestTopicsReset_GateReleasedOnErrorPath proves the in-flight entry is
// released when the reset fails, not just when it succeeds. Without this the
// 500's own advice — "retry the reset" — would be impossible to follow: every
// retry would come back 429 for the life of the process.
func TestTopicsReset_GateReleasedOnErrorPath(t *testing.T) {
	userID, topicID := uuid.New(), uuid.New()
	store := &concurrentResetStore{userID: userID, err: errors.New("db down")}
	h := &Topics{
		Topics:     store,
		Deliveries: &concurrentDeliveries{clientID: uuid.New()},
		Remover:    &fakeRemover{},
	}

	first := httptest.NewRecorder()
	h.Reset(first, resetRequest(t, topicID, userID, `{}`))
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", first.Code, first.Body.String())
	}

	store.mu.Lock()
	store.err = nil
	store.mu.Unlock()

	retry := httptest.NewRecorder()
	h.Reset(retry, resetRequest(t, topicID, userID, `{}`))
	if retry.Code != http.StatusOK {
		t.Fatalf("retry after a failed reset must be admitted, got %d: %s", retry.Code, retry.Body.String())
	}
}
