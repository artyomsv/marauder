package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	pgxmock "github.com/pashagolub/pgxmock/v5"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/audit"
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

func TestSettingsUpdate_WritesTheAuditRow(t *testing.T) {
	// The audit logger drains its buffer asynchronously, so the insert is
	// awaited rather than asserted synchronously.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	mock.ExpectExec(`INSERT INTO audit_log`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), "settings_update", "settings",
			"default_follow_tracker_title", "success", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	h := &Settings{
		Store:   &fakeSettingsStore{},
		BaseURL: "http://x",
		Audit:   audit.NewLogger(ctx, repo.NewAudit(mock), zerolog.Nop()),
	}

	w := httptest.NewRecorder()
	h.Update(w, authedReq(t, uuid.New(), updateSettingsReq{DefaultFollowTrackerTitle: boolPtr(true)}))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := mock.ExpectationsWereMet(); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("the audit row was never written")
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

// TestSettingsUpdate_ExplicitFalseOverwritesTrue: a non-nil false is an
// explicit choice and must be written even over a stored true — the bug this
// guards is the control inventing a value and the save quietly turning a
// default off. (The "omitted field is change nothing" half of the pointer
// semantics cannot reach a successful write while this endpoint has a single
// field: omitting it is the empty body rejected above.)
func TestSettingsUpdate_ExplicitFalseOverwritesTrue(t *testing.T) {
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
