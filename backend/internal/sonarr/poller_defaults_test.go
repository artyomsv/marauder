package sonarr

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
)

type defaultsSettings struct {
	value repo.InstanceSettings
	err   error
	reads int
}

func (s *defaultsSettings) Get(context.Context) (*repo.InstanceSettings, error) {
	s.reads++
	return &s.value, s.err
}

func TestPoller_Create_ReplaceDefaultsPrecedence(t *testing.T) {
	on, off := true, false
	choices := []struct {
		name  string
		value *bool
	}{{"inherit", nil}, {"on", &on}, {"off", &off}}
	for _, global := range []struct {
		name                string
		replace, deleteData bool
		fail, absent        bool
	}{
		{name: "built-ins", absent: true},
		{name: "settings error", fail: true, replace: true},
		{name: "off/off"},
		{name: "off/on", deleteData: true},
		{name: "on/off", replace: true},
		{name: "on/on", replace: true, deleteData: true},
	} {
		for _, replace := range choices {
			for _, deleteData := range choices {
				t.Run(fmt.Sprintf("%s/replace=%s/delete=%s", global.name, replace.name, deleteData.name), func(t *testing.T) {
					settings := &defaultsSettings{value: repo.InstanceSettings{
						DefaultFollowTrackerTitle: true, DefaultReplaceOnUpdate: global.replace, DefaultReplaceDeleteData: global.deleteData,
					}}
					if global.fail {
						settings.err = errors.New("db down")
					}
					store := &fakeTopics{}
					p := newTestPoller(&fakeInstances{}, fakeAdmin{}, store)
					if !global.absent {
						p.settings = settings
					}
					p.processURL(context.Background(), domain.SonarrInstance{
						DefaultReplaceOnUpdate: replace.value, DefaultReplaceDeleteData: deleteData.value,
					}, uuid.New(), HistoryRecord{Data: HistoryData{NzbInfoURL: fakeURL}})
					if len(store.created) != 1 {
						t.Fatalf("created = %d, want 1", len(store.created))
					}
					wantReplace, wantDelete := global.replace, global.deleteData
					if global.absent || global.fail {
						wantReplace, wantDelete = false, true
					}
					if replace.value != nil {
						wantReplace = *replace.value
					}
					if deleteData.value != nil {
						wantDelete = *deleteData.value
					}
					got := store.created[0]
					if got.ReplaceOnUpdate != wantReplace || got.ReplaceDeleteData != wantDelete {
						t.Errorf("stored replace/delete = %v/%v, want %v/%v", got.ReplaceOnUpdate, got.ReplaceDeleteData, wantReplace, wantDelete)
					}
					if got.FollowTrackerTitle != (!global.absent && !global.fail) {
						t.Errorf("follow title = %v", got.FollowTrackerTitle)
					}
					if !global.absent && settings.reads != 1 {
						t.Errorf("settings reads = %d, want 1", settings.reads)
					}
				})
			}
		}
	}
}

func TestPoller_ExistingTopicKeepsReplacePolicyWithoutReadingDefaults(t *testing.T) {
	for _, realign := range []bool{false, true} {
		for _, replace := range []bool{false, true} {
			t.Run(fmt.Sprintf("realign=%v/replace=%v", realign, replace), func(t *testing.T) {
				deleteData := !replace
				existing := &domain.Topic{ID: uuid.New(), URL: fakeURL, Category: "old",
					ReplaceOnUpdate: replace, ReplaceDeleteData: deleteData}
				store := &fakeTopics{byURL: map[string]*domain.Topic{fakeURL: existing}}
				settings := &defaultsSettings{}
				p := newTestPoller(&fakeInstances{}, fakeAdmin{}, store)
				p.settings = settings
				p.processURL(context.Background(), domain.SonarrInstance{
					UpdateExisting: realign, DefaultCategory: "new",
					DefaultReplaceOnUpdate: &deleteData, DefaultReplaceDeleteData: &replace,
				}, uuid.New(), HistoryRecord{Data: HistoryData{NzbInfoURL: fakeURL}})
				if settings.reads != 0 {
					t.Errorf("settings reads = %d, want 0", settings.reads)
				}
				if len(store.created) != 0 {
					t.Fatal("duplicate created a topic")
				}
				if !realign {
					if len(store.updated) != 0 {
						t.Fatal("duplicate changed a topic")
					}
					return
				}
				if len(store.updated) != 1 {
					t.Fatalf("updated = %d, want 1", len(store.updated))
				}
				got := store.updated[0]
				if got.ReplaceOnUpdate != replace || got.ReplaceDeleteData != deleteData {
					t.Errorf("existing policy changed: %v/%v", got.ReplaceOnUpdate, got.ReplaceDeleteData)
				}
			})
		}
	}
}

// Issue #250: a Sonarr-created topic takes the global default interval, and
// the built-in 900 s when settings are absent or unreadable.
func TestPoller_Create_CheckIntervalDefault(t *testing.T) {
	for _, tc := range []struct {
		name         string
		global       int
		fail, absent bool
		want         int
	}{
		{name: "global", global: 3600, want: 3600},
		{name: "no settings", absent: true, want: 900},
		{name: "settings error", global: 3600, fail: true, want: 900},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := &defaultsSettings{value: repo.InstanceSettings{DefaultCheckIntervalSec: tc.global}}
			if tc.fail {
				settings.err = errors.New("db down")
			}
			store := &fakeTopics{}
			p := newTestPoller(&fakeInstances{}, fakeAdmin{}, store)
			if !tc.absent {
				p.settings = settings
			}
			p.processURL(context.Background(), domain.SonarrInstance{}, uuid.New(),
				HistoryRecord{Data: HistoryData{NzbInfoURL: fakeURL}})
			if len(store.created) != 1 {
				t.Fatalf("created = %d, want 1", len(store.created))
			}
			if got := store.created[0].CheckIntervalSec; got != tc.want {
				t.Errorf("stored interval = %d, want %d", got, tc.want)
			}
		})
	}
}
