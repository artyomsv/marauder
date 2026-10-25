package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
)

// fakeSettingsStore is an in-memory settingsStore for handler tests.
type fakeSettingsStore struct {
	value repo.InstanceSettings
	err   error
	set   []bool
}

func (f *fakeSettingsStore) Get(context.Context) (*repo.InstanceSettings, error) {
	if f.err != nil {
		return nil, f.err
	}
	s := f.value
	return &s, nil
}

func (f *fakeSettingsStore) SetDefaultFollowTrackerTitle(_ context.Context, v bool) error {
	if f.err != nil {
		return f.err
	}
	f.value.DefaultFollowTrackerTitle = v
	f.set = append(f.set, v)
	return nil
}

func TestTopics_Create_FollowTrackerTitle_OmittedUsesInstanceDefault(t *testing.T) {
	store := &fakeTopicStore{}
	h := &Topics{
		Topics:   store,
		Settings: &fakeSettingsStore{value: repo.InstanceSettings{DefaultFollowTrackerTitle: true}},
		BaseURL:  "http://test",
	}

	body := createTopicReq{URL: "fake-create://topic/follow-default"}
	w := httptest.NewRecorder()
	h.Create(w, authedReq(t, uuid.New(), body))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	if store.created == nil || !store.created.FollowTrackerTitle {
		t.Errorf("created.FollowTrackerTitle = %v, want true (the instance default)",
			store.created != nil && store.created.FollowTrackerTitle)
	}
}

func TestTopics_Create_FollowTrackerTitle_ExplicitFalseOverridesDefault(t *testing.T) {
	store := &fakeTopicStore{}
	h := &Topics{
		Topics:   store,
		Settings: &fakeSettingsStore{value: repo.InstanceSettings{DefaultFollowTrackerTitle: true}},
		BaseURL:  "http://test",
	}

	// A non-nil false is an explicit choice, not "unspecified".
	body := createTopicReq{URL: "fake-create://topic/follow-off", FollowTrackerTitle: boolPtr(false)}
	w := httptest.NewRecorder()
	h.Create(w, authedReq(t, uuid.New(), body))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	if store.created == nil || store.created.FollowTrackerTitle {
		t.Error("created.FollowTrackerTitle = true, want false (explicit value wins over the default)")
	}
}

func TestTopics_Create_FollowTrackerTitle_ExplicitTrue(t *testing.T) {
	store := &fakeTopicStore{}
	h := &Topics{
		Topics:   store,
		Settings: &fakeSettingsStore{value: repo.InstanceSettings{DefaultFollowTrackerTitle: false}},
		BaseURL:  "http://test",
	}

	body := createTopicReq{URL: "fake-create://topic/follow-on", FollowTrackerTitle: boolPtr(true)}
	w := httptest.NewRecorder()
	h.Create(w, authedReq(t, uuid.New(), body))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	if store.created == nil || !store.created.FollowTrackerTitle {
		t.Error("created.FollowTrackerTitle = false, want true")
	}
}

func TestTopics_Create_FollowTrackerTitle_SettingsReadFailsOpen(t *testing.T) {
	store := &fakeTopicStore{}
	h := &Topics{
		Topics:   store,
		Settings: &fakeSettingsStore{err: errors.New("db down")},
		BaseURL:  "http://test",
	}

	body := createTopicReq{URL: "fake-create://topic/follow-failopen"}
	w := httptest.NewRecorder()
	h.Create(w, authedReq(t, uuid.New(), body))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	if store.created == nil || store.created.FollowTrackerTitle {
		t.Error("created.FollowTrackerTitle = true, want false when the settings read fails")
	}
}

func TestTopics_Create_FollowTrackerTitle_NoSettingsStoreFallsBackToFalse(t *testing.T) {
	store := &fakeTopicStore{}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	body := createTopicReq{URL: "fake-create://topic/follow-nosettings"}
	w := httptest.NewRecorder()
	h.Create(w, authedReq(t, uuid.New(), body))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	if store.created == nil || store.created.FollowTrackerTitle {
		t.Error("created.FollowTrackerTitle = true, want false without a settings store")
	}
}

func TestTopics_Update_FollowTrackerTitle_OmittedKeepsExisting(t *testing.T) {
	store := &fakeTopicStore{
		getByID: &domain.Topic{
			ID: uuid.New(), DisplayName: "Same Name", FollowTrackerTitle: true,
			Extra: map[string]any{},
		},
	}
	h := &Topics{Topics: store, BaseURL: "http://x"}

	body := updateTopicReq{DisplayName: "Same Name"} // flag omitted
	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), body), "id", uuid.New().String())
	h.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !store.lastFlags.FollowTrackerTitle {
		t.Error("FollowTrackerTitle = false, want the stored value preserved on an omitted field")
	}
}

func TestTopics_Update_FollowTrackerTitle_ExplicitFalse(t *testing.T) {
	store := &fakeTopicStore{
		getByID: &domain.Topic{
			ID: uuid.New(), DisplayName: "Same Name", FollowTrackerTitle: true,
			Extra: map[string]any{},
		},
	}
	h := &Topics{Topics: store, BaseURL: "http://x"}

	body := updateTopicReq{DisplayName: "Same Name", FollowTrackerTitle: boolPtr(false)}
	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), body), "id", uuid.New().String())
	h.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if store.lastFlags.FollowTrackerTitle {
		t.Error("FollowTrackerTitle = true, want the explicit false to be written")
	}
}

func TestTopics_Update_FollowTrackerTitle_ExplicitTrueOnRename(t *testing.T) {
	// The handler passes the requested flag through; the store is what turns
	// it off when the name changes (mirrored in the API response the caller
	// gets back). Pinned here so the pass-through itself can't silently drop
	// the flag — the clear-on-rename clause is pinned in the repo tests.
	store := &fakeTopicStore{
		getByID: &domain.Topic{
			ID: uuid.New(), DisplayName: "Old Name", FollowTrackerTitle: false,
			Extra: map[string]any{},
		},
	}
	h := &Topics{Topics: store, BaseURL: "http://x"}

	body := updateTopicReq{DisplayName: "New Name", FollowTrackerTitle: boolPtr(true)}
	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), body), "id", uuid.New().String())
	h.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if store.updateDisplayName != "New Name" {
		t.Errorf("updateDisplayName = %q, want New Name", store.updateDisplayName)
	}
	if !store.lastFlags.FollowTrackerTitle {
		t.Error("FollowTrackerTitle = false, want the handler to pass the requested flag through")
	}
}
