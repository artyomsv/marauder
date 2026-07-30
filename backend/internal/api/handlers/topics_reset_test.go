package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestTopicsReset_DeliveryDeleteFailureAborts(t *testing.T) {
	topicID, userID := uuid.New(), uuid.New()
	store := &fakeTopicStore{getByID: &domain.Topic{ID: topicID, UserID: userID}}
	deliveries := &fakeDeliveriesStore{deleteErr: errors.New("connection refused")}
	h := &Topics{Topics: store, Deliveries: deliveries, Remover: &fakeRemover{}}

	rec := httptest.NewRecorder()
	h.Reset(rec, resetRequest(t, topicID, userID, `{}`))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(store.resetCalls) != 0 {
		t.Error("topic state must not be reset when the delivery rows survived")
	}
}
