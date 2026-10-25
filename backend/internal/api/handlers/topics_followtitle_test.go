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

// TestTopics_Create_FollowTrackerTitle pins the precedence of the three
// inputs to a new topic's flag: an explicit request value wins, a supplied
// display name keeps ownership of it, and only a nameless create with no
// explicit choice inherits the instance default (which is materialised onto
// the topic, so editing it later never touches this one).
func TestTopics_Create_FollowTrackerTitle(t *testing.T) {
	tests := []struct {
		name        string
		defaultOn   bool
		settingsErr bool
		noSettings  bool
		reqName     string
		reqFlag     *bool
		want        bool
	}{
		{name: "omitted flag inherits an enabled default", defaultOn: true, want: true},
		{name: "omitted flag inherits a disabled default", defaultOn: false, want: false},
		{name: "an explicit false wins over the default", defaultOn: true, reqFlag: boolPtr(false), want: false},
		{name: "an explicit true wins over the default", defaultOn: false, reqFlag: boolPtr(true), want: true},
		{name: "a supplied name keeps ownership of it", defaultOn: true, reqName: "My Custom Name", want: false},
		{name: "a supplied name with explicit follow honours both", defaultOn: false, reqName: "My Custom Name", reqFlag: boolPtr(true), want: true},
		{name: "a failed settings read falls open to false", defaultOn: true, settingsErr: true, want: false},
		{name: "no settings store falls back to false", noSettings: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeTopicStore{}
			h := &Topics{Topics: store, BaseURL: "http://test"}
			if !tt.noSettings {
				s := &fakeSettingsStore{value: repo.InstanceSettings{DefaultFollowTrackerTitle: tt.defaultOn}}
				if tt.settingsErr {
					s.err = errors.New("db down")
				}
				h.Settings = s
			}

			body := createTopicReq{
				URL:                "fake-create://topic/follow",
				DisplayName:        tt.reqName,
				FollowTrackerTitle: tt.reqFlag,
			}
			w := httptest.NewRecorder()
			h.Create(w, authedReq(t, uuid.New(), body))

			if w.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
			}
			if store.created == nil {
				t.Fatal("topic was not created")
			}
			if store.created.FollowTrackerTitle != tt.want {
				t.Errorf("created.FollowTrackerTitle = %v, want %v", store.created.FollowTrackerTitle, tt.want)
			}
		})
	}
}

// TestTopics_Update_OmittedNameAndFlag_AreNotARename is the M-1 regression:
// an unrelated edit saved after a tracker title change must neither restore
// the stale name nor disable following. The form omits both fields when the
// user left them alone, and the store treats an omitted name as "not
// renaming" — a stale value is not a rename.
func TestTopics_Update_OmittedNameAndFlag_AreNotARename(t *testing.T) {
	store := &fakeTopicStore{
		getByID: &domain.Topic{
			ID: uuid.New(), DisplayName: "Tracker Title B", FollowTrackerTitle: true,
			Extra: map[string]any{},
		},
	}
	h := &Topics{Topics: store, BaseURL: "http://x"}

	body := updateTopicReq{Category: "series"} // name and flag both omitted
	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), body), "id", uuid.New().String())
	h.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if store.updateDisplayName != nil {
		t.Errorf("updateDisplayName = %v, want nil — an omitted name must not be a rename", store.updateDisplayName)
	}
	if store.lastFlags.FollowTrackerTitle != nil {
		t.Errorf("FollowTrackerTitle = %v, want nil — an omitted flag must be preserved at write time", store.lastFlags.FollowTrackerTitle)
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

	body := updateTopicReq{DisplayName: strPtr("Same Name"), FollowTrackerTitle: boolPtr(false)}
	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), body), "id", uuid.New().String())
	h.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if store.lastFlags.FollowTrackerTitle == nil || *store.lastFlags.FollowTrackerTitle {
		t.Errorf("FollowTrackerTitle = %v, want the explicit false to be written", store.lastFlags.FollowTrackerTitle)
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

	body := updateTopicReq{DisplayName: strPtr("New Name"), FollowTrackerTitle: boolPtr(true)}
	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), body), "id", uuid.New().String())
	h.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if store.updateDisplayName == nil || *store.updateDisplayName != "New Name" {
		t.Errorf("updateDisplayName = %v, want New Name", store.updateDisplayName)
	}
	if store.lastFlags.FollowTrackerTitle == nil || !*store.lastFlags.FollowTrackerTitle {
		t.Errorf("FollowTrackerTitle = %v, want the handler to pass the requested flag through", store.lastFlags.FollowTrackerTitle)
	}
}
