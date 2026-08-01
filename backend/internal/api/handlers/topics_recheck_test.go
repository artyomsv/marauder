package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

func TestRecheck_QueuesTheTopicAndReturns204(t *testing.T) {
	store := &fakeTopicStore{recheckOK: true}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	uid := uuid.New()
	id := uuid.New()
	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uid, nil), "id", id.String())
	h.Recheck(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204; body %s", w.Code, w.Body.String())
	}
	if len(store.recheckCalls) != 1 {
		t.Fatalf("QueueRecheck called %d times, want 1", len(store.recheckCalls))
	}
	if store.recheckCalls[0] != [2]uuid.UUID{id, uid} {
		t.Errorf("QueueRecheck got %v, want {topicID, userID} = {%s, %s}",
			store.recheckCalls[0], id, uid)
	}
}

// A paused topic must NOT be reported as success: the scheduler ignores paused
// rows, so a 204 would promise a check that never happens. It must also not
// retry the queue — the topic is genuinely paused, so a second QueueRecheck
// call would just observe the same ineligible row again.
func TestRecheck_PausedTopic_Returns409(t *testing.T) {
	store := &fakeTopicStore{
		recheckOK: false,
		getByID:   &domain.Topic{ID: uuid.New(), Status: domain.TopicStatusPaused},
	}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), nil), "id", uuid.New().String())
	h.Recheck(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409; body %s", w.Code, w.Body.String())
	}
	if len(store.recheckCalls) != 1 {
		t.Fatalf("QueueRecheck called %d times, want 1 (must not retry a genuinely paused topic)", len(store.recheckCalls))
	}
}

// TestRecheck_ResumedBetweenQueueAndLookup_Returns204 covers the TOCTOU race:
// QueueRecheck's UPDATE matches nothing (topic was paused at that instant),
// but by the time GetByID runs, the topic has been resumed by another
// request. Reporting "paused" here would describe a state that no longer
// holds and send the user to resume an already-active topic, so the handler
// must retry the queue instead — and that retry must succeed (204), not 409.
func TestRecheck_ResumedBetweenQueueAndLookup_Returns204(t *testing.T) {
	store := &fakeTopicStore{
		recheckResults: []bool{false, true},
		getByID:        &domain.Topic{ID: uuid.New(), Status: domain.TopicStatusActive},
	}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	uid := uuid.New()
	id := uuid.New()
	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uid, nil), "id", id.String())
	h.Recheck(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204; body %s", w.Code, w.Body.String())
	}
	if len(store.recheckCalls) != 2 {
		t.Fatalf("QueueRecheck called %d times, want 2 (initial + retry)", len(store.recheckCalls))
	}
	for i, call := range store.recheckCalls {
		if call != [2]uuid.UUID{id, uid} {
			t.Errorf("QueueRecheck call %d got %v, want {topicID, userID} = {%s, %s}", i, call, id, uid)
		}
	}
}

// Not found and not-yours are the same answer, so the response cannot be used
// to probe for another user's topics.
func TestRecheck_UnknownTopic_Returns404(t *testing.T) {
	store := &fakeTopicStore{recheckOK: false, getByID: nil}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), nil), "id", uuid.New().String())
	h.Recheck(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body %s", w.Code, w.Body.String())
	}
}

func TestRecheck_MalformedID_Returns400(t *testing.T) {
	store := &fakeTopicStore{}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), nil), "id", "not-a-uuid")
	h.Recheck(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}
	if len(store.recheckCalls) != 0 {
		t.Error("store must not be touched for a malformed id")
	}
}

func TestRecheck_StoreError_Returns500(t *testing.T) {
	store := &fakeTopicStore{recheckErr: errors.New("db down")}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), nil), "id", uuid.New().String())
	h.Recheck(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", w.Code)
	}
}
