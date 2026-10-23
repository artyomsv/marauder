package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
)

// Folder name settings on the topic API (issue #225).

func strPtr(s string) *string { return &s }

func TestTopicsCreate_FolderName_Stored(t *testing.T) {
	store := &fakeTopicStore{}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	body := createTopicReq{URL: "fake-create://topic/folder", ReplaceOnUpdate: true, FolderNameMode: "custom", FolderName: " My Show "}
	w := httptest.NewRecorder()
	h.Create(w, authedReq(t, uuid.New(), body))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	if store.created.FolderNameMode != domain.FolderNameCustom || store.created.FolderName != "My Show" {
		t.Errorf("created (%q, %q), want (custom, My Show)", store.created.FolderNameMode, store.created.FolderName)
	}
	if store.created.ReplaceDeleteData {
		t.Error("omitted replace_delete_data must default to false with a folder name")
	}
}

func TestTopicsCreate_InvalidFolderName_422(t *testing.T) {
	store := &fakeTopicStore{}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	body := createTopicReq{URL: "fake-create://topic/bad-folder", FolderNameMode: "custom", FolderName: "a/b"}
	w := httptest.NewRecorder()
	h.Create(w, authedReq(t, uuid.New(), body))

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", w.Code, w.Body.String())
	}
	if store.created != nil {
		t.Error("an invalid folder name must not reach the store")
	}
}

func TestTopicsCreate_FolderNameWithDeleteData_422(t *testing.T) {
	store := &fakeTopicStore{}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	body := createTopicReq{URL: "fake-create://topic/folder-delete", ReplaceOnUpdate: true, ReplaceDeleteData: boolPtr(true), FolderNameMode: "keep"}
	w := httptest.NewRecorder()
	h.Create(w, authedReq(t, uuid.New(), body))

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", w.Code, w.Body.String())
	}
}

// An edit that leaves the folder fields out must not touch the setting: the
// store gets nil, not the value read before the edit.
func TestTopicsUpdate_OmittedFolderFields_PassesNilPolicy(t *testing.T) {
	store := &fakeTopicStore{getByID: &domain.Topic{
		ID: uuid.New(), TrackerName: fakeQualityTrackerName, DisplayName: "Show",
		FolderNameMode: domain.FolderNameCustom, FolderName: "X",
	}}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	w := httptest.NewRecorder()
	h.Update(w, withURLParam(authedReq(t, uuid.New(), updateTopicReq{DisplayName: "Show"}), "id", uuid.New().String()))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body %s", w.Code, w.Body.String())
	}
	if store.updateFolder != nil {
		t.Errorf("folder policy = %+v, want nil", *store.updateFolder)
	}
}

func TestTopicsUpdate_ModeOnly_KeepsStoredName(t *testing.T) {
	store := &fakeTopicStore{getByID: &domain.Topic{
		ID: uuid.New(), TrackerName: fakeQualityTrackerName, DisplayName: "Show",
		FolderNameMode: domain.FolderNameCustom, FolderName: "X",
	}}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	body := updateTopicReq{DisplayName: "Show", FolderNameMode: strPtr("custom")}
	w := httptest.NewRecorder()
	h.Update(w, withURLParam(authedReq(t, uuid.New(), body), "id", uuid.New().String()))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body %s", w.Code, w.Body.String())
	}
	want := repo.FolderNamePolicy{Mode: domain.FolderNameCustom, Name: "X"}
	if store.updateFolder == nil || *store.updateFolder != want {
		t.Errorf("folder policy = %+v, want %+v", store.updateFolder, want)
	}
}

func TestTopicsUpdate_NameOnly_ValidatedAgainstStoredMode(t *testing.T) {
	store := &fakeTopicStore{getByID: &domain.Topic{
		ID: uuid.New(), TrackerName: fakeQualityTrackerName, DisplayName: "Show",
		FolderNameMode: domain.FolderNameCustom, FolderName: "X",
	}}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	body := updateTopicReq{DisplayName: "Show", FolderName: strPtr("CON")}
	w := httptest.NewRecorder()
	h.Update(w, withURLParam(authedReq(t, uuid.New(), body), "id", uuid.New().String()))

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422; body %s", w.Code, w.Body.String())
	}
	if store.updateCalled {
		t.Error("store.Update was called for an invalid name")
	}
}

// Turning a folder name on for a topic that already deletes the previous
// version's files must be refused, on the values the topic would store.
func TestTopicsUpdate_TurnOnFolderName_WithStoredDeleteData_422(t *testing.T) {
	store := &fakeTopicStore{getByID: &domain.Topic{
		ID: uuid.New(), TrackerName: fakeQualityTrackerName, DisplayName: "Show",
		ReplaceOnUpdate: true, ReplaceDeleteData: true,
	}}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	body := updateTopicReq{DisplayName: "Show", FolderNameMode: strPtr("keep")}
	w := httptest.NewRecorder()
	h.Update(w, withURLParam(authedReq(t, uuid.New(), body), "id", uuid.New().String()))

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422; body %s", w.Code, w.Body.String())
	}
	if store.updateCalled {
		t.Error("store.Update was called for a rejected policy")
	}
}
