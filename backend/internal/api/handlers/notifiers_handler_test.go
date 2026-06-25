package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// hnotifier is an offline test notifier plugin: Test/Send never touch the
// network, so handler tests for Create/Update (which call plugin.Test) are
// deterministic. Registered once under a unique name.
type hnotifier struct{}

func (hnotifier) Name() string                                       { return "test-handler-notifier" }
func (hnotifier) DisplayName() string                                { return "Test Handler Notifier" }
func (hnotifier) ConfigSchema() map[string]any                       { return nil }
func (hnotifier) Test(context.Context, []byte) error                 { return nil }
func (hnotifier) Send(context.Context, []byte, domain.Message) error { return nil }

func init() { registry.RegisterNotifier(hnotifier{}) }

func newNotifierTestMaster(t *testing.T) *crypto.MasterKey {
	t.Helper()
	mk, err := crypto.LoadMasterKey(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatalf("LoadMasterKey: %v", err)
	}
	return mk
}

type fakeNotifierStore struct {
	got          *domain.Notifier
	updateCalled bool
	updateIsDef  bool
	getByID      *domain.Notifier
}

func (s *fakeNotifierStore) ListForUser(context.Context, uuid.UUID) ([]*domain.Notifier, error) {
	return nil, nil
}
func (s *fakeNotifierStore) Create(_ context.Context, n *domain.Notifier) (*domain.Notifier, error) {
	s.got = n
	n.ID = uuid.New()
	return n, nil
}
func (s *fakeNotifierStore) GetByID(context.Context, uuid.UUID, uuid.UUID) (*domain.Notifier, error) {
	if s.getByID == nil {
		return nil, repo.ErrNotFound
	}
	return s.getByID, nil
}
func (s *fakeNotifierStore) Delete(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (s *fakeNotifierStore) Update(_ context.Context, _, _ uuid.UUID, _, _ string, _ []string, isDefault bool, _, _ []byte) error {
	s.updateCalled = true
	s.updateIsDef = isDefault
	return nil
}

func TestNotifiers_Create_PassesIsDefault(t *testing.T) {
	store := &fakeNotifierStore{}
	h := &Notifiers{Notifiers: store, Master: newNotifierTestMaster(t), BaseURL: "http://x"}

	body := createNotifierReq{
		NotifierName: "test-handler-notifier",
		DisplayName:  "W",
		IsDefault:    true,
		Config:       json.RawMessage(`{"k":"v"}`),
	}
	rr := httptest.NewRecorder()
	h.Create(rr, authedReq(t, uuid.New(), body))

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if store.got == nil || !store.got.IsDefault {
		t.Errorf("Create did not forward is_default=true")
	}
}

func TestNotifiers_Update_PassesIsDefault(t *testing.T) {
	id := uuid.New()
	store := &fakeNotifierStore{getByID: &domain.Notifier{ID: id, NotifierName: "test-handler-notifier"}}
	h := &Notifiers{Notifiers: store, Master: newNotifierTestMaster(t), BaseURL: "http://x"}

	body := updateNotifierReq{
		DisplayName: "W2",
		Events:      []string{"updated"},
		IsDefault:   true,
		Config:      json.RawMessage(`{"k":"v"}`),
	}
	rr := httptest.NewRecorder()
	h.Update(rr, withURLParam(authedReq(t, uuid.New(), body), "id", id.String()))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if !store.updateCalled || !store.updateIsDef {
		t.Errorf("Update not called with is_default=true (called=%v def=%v)", store.updateCalled, store.updateIsDef)
	}
}

func TestValidNotifierEvents_FiltersAndDefaults(t *testing.T) {
	// empty -> full canonical notifiable set (5 entries)
	if got := validNotifierEvents(nil); len(got) != 5 {
		t.Errorf("default set size = %d, want 5", len(got))
	}
	// drops legacy 'updated' is allowed-through (kept for back-compat) but
	// unknown junk is dropped
	got := validNotifierEvents([]string{"release.found", "bogus.event", "download.completed"})
	for _, e := range got {
		if e == "bogus.event" {
			t.Errorf("bogus event should be dropped: %v", got)
		}
	}
	if len(got) != 2 {
		t.Errorf("got %v, want [release.found download.completed]", got)
	}
}
