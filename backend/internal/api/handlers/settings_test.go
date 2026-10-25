package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/db/repo"
)

// The instance-wide defaults endpoint: every signed-in user reads it (the
// add-topic form seeds its toggle from the response), only admins write it.
func TestSettingsGet_ReturnsTheStoredDefault(t *testing.T) {
	h := &Settings{
		Store:   &fakeSettingsStore{value: repo.InstanceSettings{DefaultFollowTrackerTitle: true}},
		BaseURL: "http://x",
	}
	w := httptest.NewRecorder()
	h.Get(w, authedReq(t, uuid.New(), nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"default_follow_tracker_title":true`) {
		t.Errorf("body = %s, want the stored default", w.Body.String())
	}
}

func TestSettingsGet_StoreErrorIsInternal(t *testing.T) {
	h := &Settings{
		Store:   &fakeSettingsStore{err: errors.New("db down")},
		BaseURL: "http://x",
	}
	w := httptest.NewRecorder()
	h.Get(w, authedReq(t, uuid.New(), nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", w.Code, w.Body.String())
	}
}

func TestSettingsUpdate_WritesAndAudits(t *testing.T) {
	store := &fakeSettingsStore{}
	h := &Settings{Store: store, BaseURL: "http://x"}

	body := updateSettingsReq{DefaultFollowTrackerTitle: boolPtr(true)}
	w := httptest.NewRecorder()
	h.Update(w, authedReq(t, uuid.New(), body))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if len(store.set) != 1 || !store.set[0] {
		t.Errorf("set = %v, want [true]", store.set)
	}
	if !strings.Contains(w.Body.String(), `"default_follow_tracker_title":true`) {
		t.Errorf("body = %s, want the written value", w.Body.String())
	}
}

func TestSettingsUpdate_EmptyBodyIsRejected(t *testing.T) {
	store := &fakeSettingsStore{}
	h := &Settings{Store: store, BaseURL: "http://x"}

	w := httptest.NewRecorder()
	h.Update(w, authedReq(t, uuid.New(), updateSettingsReq{}))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	if len(store.set) != 0 {
		t.Errorf("set = %v, want no write", store.set)
	}
}

func TestSettingsUpdate_OmittedFieldPreservesTheStoredValue(t *testing.T) {
	// Pointer semantics, matching every other settings-bearing endpoint: an
	// omitted field is "change nothing", not "write false".
	store := &fakeSettingsStore{value: repo.InstanceSettings{DefaultFollowTrackerTitle: true}}
	h := &Settings{Store: store, BaseURL: "http://x"}

	body := updateSettingsReq{DefaultFollowTrackerTitle: boolPtr(false)}
	w := httptest.NewRecorder()
	h.Update(w, authedReq(t, uuid.New(), body))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if len(store.set) != 1 || store.set[0] {
		t.Errorf("set = %v, want [false]", store.set)
	}
}

func TestSettingsUpdate_StoreErrorIsInternal(t *testing.T) {
	h := &Settings{Store: &fakeSettingsStore{err: errors.New("db down")}, BaseURL: "http://x"}

	body := updateSettingsReq{DefaultFollowTrackerTitle: boolPtr(true)}
	w := httptest.NewRecorder()
	h.Update(w, authedReq(t, uuid.New(), body))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", w.Code, w.Body.String())
	}
}
