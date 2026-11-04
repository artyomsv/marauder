package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
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

func TestSettingsUpdate_PartialFieldsReturnStoredRowAndAuditOnlySentKeys(t *testing.T) {
	for _, key := range []string{"default_follow_tracker_title", "default_replace_on_update", "default_replace_delete_data"} {
		t.Run(key, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			mock, err := pgxmock.NewPool()
			if err != nil {
				t.Fatal(err)
			}
			defer mock.Close()
			body := map[string]bool{key: false}
			metadata, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			resourceID := "topic_defaults"
			if key == "default_follow_tracker_title" {
				resourceID = key
			}
			mock.ExpectExec(`INSERT INTO audit_log`).
				WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), "settings_update", "settings",
					resourceID, "success", pgxmock.AnyArg(), pgxmock.AnyArg(), metadata).
				WillReturnResult(pgxmock.NewResult("INSERT", 1))
			store := &fakeSettingsStore{value: repo.InstanceSettings{
				DefaultFollowTrackerTitle: true, DefaultReplaceOnUpdate: true, DefaultReplaceDeleteData: true,
			}}
			h := &Settings{Store: store, BaseURL: "http://test", Audit: audit.NewLogger(ctx, repo.NewAudit(mock), zerolog.Nop())}
			w := httptest.NewRecorder()
			h.Update(w, authedReq(t, uuid.New(), body))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			var got map[string]bool
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			want := map[string]bool{"default_follow_tracker_title": true, "default_replace_on_update": true, "default_replace_delete_data": true}
			want[key] = false
			if !reflect.DeepEqual(got, want) {
				t.Errorf("response = %v, want %v", got, want)
			}
			if len(store.patches) != 1 || store.reads != 0 {
				t.Errorf("writes/reads = %d/%d, want 1/0", len(store.patches), store.reads)
			}
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if err := mock.ExpectationsWereMet(); err == nil {
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
			t.Error("audit metadata did not match the sent field")
		})
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
	if len(store.patches) != 1 || !*store.patches[0].DefaultFollowTrackerTitle {
		t.Errorf("set = %v, want [true]", store.patches)
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
	if len(store.patches) != 0 {
		t.Errorf("set = %v, want no write", store.patches)
	}
}

// TestSettingsUpdate_ExplicitFalseOverwritesTrue: a non-nil false is an
// explicit choice and must be written even over a stored true — the bug this
// guards is the control inventing a value and the save quietly turning a
// default off.
func TestSettingsUpdate_ExplicitFalseOverwritesTrue(t *testing.T) {
	store := &fakeSettingsStore{value: repo.InstanceSettings{DefaultFollowTrackerTitle: true}}
	h := &Settings{Store: store, BaseURL: "http://x"}

	body := updateSettingsReq{DefaultFollowTrackerTitle: boolPtr(false)}
	w := httptest.NewRecorder()
	h.Update(w, authedReq(t, uuid.New(), body))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if len(store.patches) != 1 || *store.patches[0].DefaultFollowTrackerTitle {
		t.Errorf("set = %v, want [false]", store.patches)
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
