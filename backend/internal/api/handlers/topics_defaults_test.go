package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/db/repo"
)

func TestTopics_Create_ReplaceDefaults(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		globalReplace, globalDelete bool
		replace, deleteData         *bool
		onlyNew                     bool
		folder                      string
		settingsErr, noSettings     bool
		wantReplace, wantDelete     bool
		wantStatus                  int
	}{
		{name: "built-ins", globalDelete: true, wantDelete: true},
		{name: "global season pack defaults", globalReplace: true, wantReplace: true},
		{name: "explicit false", globalReplace: true, globalDelete: true, replace: boolPtr(false), deleteData: boolPtr(false)},
		{name: "explicit true", replace: boolPtr(true), deleteData: boolPtr(true), wantReplace: true, wantDelete: true},
		{name: "only new guards inherited true", globalReplace: true, globalDelete: true, onlyNew: true, wantReplace: true},
		{name: "folder guards inherited true", globalReplace: true, globalDelete: true, folder: "keep", wantReplace: true},
		{name: "explicit unsafe true rejected", globalReplace: true, deleteData: boolPtr(true), onlyNew: true, wantStatus: http.StatusUnprocessableEntity},
		{name: "failed settings", globalReplace: true, settingsErr: true, wantDelete: true},
		{name: "no settings", noSettings: true, wantDelete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := &fakeSettingsStore{value: repo.InstanceSettings{
				DefaultFollowTrackerTitle: true,
				DefaultReplaceOnUpdate:    tc.globalReplace, DefaultReplaceDeleteData: tc.globalDelete,
			}}
			if tc.settingsErr {
				settings.err = errors.New("db down")
			}
			store := &fakeTopicStore{}
			h := &Topics{Topics: store, Settings: settings, BaseURL: "http://test"}
			if tc.noSettings {
				h.Settings = nil
			}
			w := httptest.NewRecorder()
			h.Create(w, authedReq(t, uuid.New(), createTopicReq{
				URL: "fake-create://topic/defaults", ReplaceOnUpdate: tc.replace, ReplaceDeleteData: tc.deleteData,
				OnlyNewFiles: tc.onlyNew, FolderNameMode: tc.folder,
			}))
			status := tc.wantStatus
			if status == 0 {
				status = http.StatusCreated
			}
			if w.Code != status {
				t.Fatalf("status = %d, want %d: %s", w.Code, status, w.Body.String())
			}
			if !tc.noSettings && settings.reads != 1 {
				t.Errorf("settings reads = %d, want 1", settings.reads)
			}
			if status != http.StatusCreated {
				return
			}
			got := store.created
			if got.ReplaceOnUpdate != tc.wantReplace || got.ReplaceDeleteData != tc.wantDelete {
				t.Errorf("stored replace/delete = %v/%v, want %v/%v", got.ReplaceOnUpdate, got.ReplaceDeleteData, tc.wantReplace, tc.wantDelete)
			}
			if got.FollowTrackerTitle != (!tc.settingsErr && !tc.noSettings) {
				t.Errorf("follow title = %v", got.FollowTrackerTitle)
			}
		})
	}
}
