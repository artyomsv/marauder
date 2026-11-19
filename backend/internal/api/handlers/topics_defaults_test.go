package handlers

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/db/repo"
)

func TestTopics_Create_ReplaceDefaults(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		globalReplace, globalDelete bool
		replace, deleteData         *bool
		onlyNew, notifyOnly         bool
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
		{name: "notify-only does not inherit replace", globalReplace: true, globalDelete: true, notifyOnly: true, wantDelete: true},
		{name: "notify-only keeps explicit replace", globalDelete: true, notifyOnly: true, replace: boolPtr(true), wantReplace: true, wantDelete: true},
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
			var logs bytes.Buffer
			h := &Topics{Topics: store, Settings: settings, BaseURL: "http://test", Log: zerolog.New(&logs)}
			if tc.noSettings {
				h.Settings = nil
			}
			w := httptest.NewRecorder()
			h.Create(w, authedReq(t, uuid.New(), createTopicReq{
				URL: "fake-create://topic/defaults", ReplaceOnUpdate: tc.replace, ReplaceDeleteData: tc.deleteData,
				OnlyNewFiles: tc.onlyNew, NotifyOnly: tc.notifyOnly, FolderNameMode: tc.folder,
			}))
			if logged := strings.Contains(logs.String(), "settings read failed"); logged != tc.settingsErr {
				t.Errorf("settings failure logged = %v, want %v: %s", logged, tc.settingsErr, logs.String())
			}
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

// Issue #250: a create without an interval takes the instance default; an
// explicit interval wins; an out-of-range explicit one is still refused; and
// a failed settings read falls back to the built-in 900 s.
func TestTopics_Create_CheckIntervalDefault(t *testing.T) {
	for _, tc := range []struct {
		name        string
		global      int
		explicit    int
		settingsErr bool
		want        int
		wantStatus  int
	}{
		{name: "inherits global", global: 3600, want: 3600},
		{name: "explicit wins", global: 3600, explicit: 1800, want: 1800},
		{name: "explicit out of range refused", global: 3600, explicit: 10, wantStatus: http.StatusUnprocessableEntity},
		{name: "failed settings uses built-in", global: 3600, settingsErr: true, want: 900},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := &fakeSettingsStore{value: repo.InstanceSettings{DefaultCheckIntervalSec: tc.global}}
			if tc.settingsErr {
				settings.err = errors.New("db down")
			}
			store := &fakeTopicStore{}
			h := &Topics{Topics: store, Settings: settings, BaseURL: "http://test", Log: zerolog.Nop()}
			w := httptest.NewRecorder()
			h.Create(w, authedReq(t, uuid.New(), createTopicReq{
				URL: "fake-create://topic/interval", CheckIntervalSec: tc.explicit,
			}))
			status := tc.wantStatus
			if status == 0 {
				status = http.StatusCreated
			}
			if w.Code != status {
				t.Fatalf("status = %d, want %d: %s", w.Code, status, w.Body.String())
			}
			if status != http.StatusCreated {
				return
			}
			if got := store.created.CheckIntervalSec; got != tc.want {
				t.Errorf("stored interval = %d, want %d", got, tc.want)
			}
		})
	}
}
