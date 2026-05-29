package handlers

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// fakeQualityTracker implements registry.Tracker + WithQuality so the
// PUT /topics/{id} handler's quality-validation branch has a real
// Qualities() list to check against. CanParse returns false on purpose:
// the handler resolves the tracker via registry.GetTracker(trackerName),
// not by URL, so parseability is irrelevant here.
type fakeQualityTracker struct{ name string }

func (f *fakeQualityTracker) Name() string         { return f.name }
func (f *fakeQualityTracker) DisplayName() string  { return "Fake Quality Tracker" }
func (f *fakeQualityTracker) CanParse(string) bool { return false }
func (f *fakeQualityTracker) Parse(context.Context, string) (*domain.Topic, error) {
	return nil, nil
}
func (f *fakeQualityTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (f *fakeQualityTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}
func (f *fakeQualityTracker) Qualities() []string    { return []string{"720p", "1080p"} }
func (f *fakeQualityTracker) DefaultQuality() string { return "1080p" }

const fakeQualityTrackerName = "fakequalitytracker"

func init() {
	registry.RegisterTracker(&fakeQualityTracker{name: fakeQualityTrackerName})
}

// fakeTopicStore is an in-memory topicStore for handler tests. It records
// the arguments the handler passed to Update so tests can assert on the
// merged Extra map.
type fakeTopicStore struct {
	getByID    *domain.Topic // returned by GetByID (nil => 404)
	getByIDErr error

	// Captured Update arguments.
	updateCalled      bool
	updateDisplayName string
	updateClientID    *uuid.UUID
	updateDownloadDir string
	updateCategory    string
	updateExtra       map[string]any
	updateReturn      *domain.Topic
}

func (s *fakeTopicStore) Create(context.Context, *domain.Topic) (*domain.Topic, error) {
	return nil, nil
}
func (s *fakeTopicStore) GetByID(context.Context, uuid.UUID, *uuid.UUID) (*domain.Topic, error) {
	return s.getByID, s.getByIDErr
}
func (s *fakeTopicStore) ListForUser(context.Context, uuid.UUID) ([]*domain.Topic, error) {
	return nil, nil
}
func (s *fakeTopicStore) Delete(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (s *fakeTopicStore) UpdateStatus(context.Context, uuid.UUID, uuid.UUID, domain.TopicStatus) error {
	return nil
}
func (s *fakeTopicStore) Update(_ context.Context, _, _ uuid.UUID, displayName string, clientID *uuid.UUID, downloadDir, category string, extra map[string]any) (*domain.Topic, error) {
	s.updateCalled = true
	s.updateDisplayName = displayName
	s.updateClientID = clientID
	s.updateDownloadDir = downloadDir
	s.updateCategory = category
	s.updateExtra = extra
	if s.updateReturn != nil {
		return s.updateReturn, nil
	}
	// Echo back a topic reflecting the edit so the handler can render it.
	return &domain.Topic{
		ID:          uuid.New(),
		DisplayName: displayName,
		ClientID:    clientID,
		DownloadDir: downloadDir,
		Category:    category,
		Extra:       extra,
	}, nil
}

// TestTopicsUpdate_PreservesDownloadedEpisodes is the critical property:
// editing a topic must NOT wipe extra["downloaded_episodes"]. Otherwise the
// scheduler would re-download every episode the user already has.
func TestTopicsUpdate_PreservesDownloadedEpisodes(t *testing.T) {
	store := &fakeTopicStore{
		getByID: &domain.Topic{
			ID:          uuid.New(),
			TrackerName: fakeQualityTrackerName,
			DisplayName: "Old Name",
			Extra: map[string]any{
				"downloaded_episodes": []any{"791001005"},
				"quality":             "1080p",
			},
		},
	}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	body := updateTopicReq{
		DisplayName:  "New Name",
		Quality:      "720p",
		StartEpisode: intPtr(3),
	}
	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), body), "id", uuid.New().String())
	h.Update(w, req)

	if w.Code != 200 {
		t.Fatalf("status %d, want 200; body %s", w.Code, w.Body.String())
	}
	if !store.updateCalled {
		t.Fatal("handler must call store.Update")
	}

	// downloaded_episodes must survive the merge untouched.
	de, ok := store.updateExtra["downloaded_episodes"].([]any)
	if !ok {
		t.Fatalf("downloaded_episodes missing or wrong type after merge: %#v", store.updateExtra["downloaded_episodes"])
	}
	if len(de) != 1 || de[0] != "791001005" {
		t.Errorf("downloaded_episodes = %#v, want [791001005]", de)
	}

	// Capability fields must be updated.
	if store.updateExtra["quality"] != "720p" {
		t.Errorf("quality = %#v, want 720p", store.updateExtra["quality"])
	}
	if store.updateExtra["start_episode"] != 3 {
		t.Errorf("start_episode = %#v, want 3", store.updateExtra["start_episode"])
	}
	if store.updateDisplayName != "New Name" {
		t.Errorf("display_name = %q, want New Name", store.updateDisplayName)
	}
}

// TestTopicsUpdate_NotFound: GetByID returns (nil, nil) => 404.
func TestTopicsUpdate_NotFound(t *testing.T) {
	store := &fakeTopicStore{getByID: nil}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), updateTopicReq{DisplayName: "X"}), "id", uuid.New().String())
	h.Update(w, req)

	if w.Code != 404 {
		t.Fatalf("status %d, want 404; body %s", w.Code, w.Body.String())
	}
	if store.updateCalled {
		t.Error("handler must not call Update when topic is not found")
	}
}

// TestTopicsUpdate_BadQuality: a quality not in the tracker's Qualities()
// list yields 422 and never reaches the store.
func TestTopicsUpdate_BadQuality(t *testing.T) {
	store := &fakeTopicStore{
		getByID: &domain.Topic{
			ID:          uuid.New(),
			TrackerName: fakeQualityTrackerName,
			Extra:       map[string]any{},
		},
	}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), updateTopicReq{Quality: "9999p"}), "id", uuid.New().String())
	h.Update(w, req)

	if w.Code != 422 {
		t.Fatalf("status %d, want 422; body %s", w.Code, w.Body.String())
	}
	if store.updateCalled {
		t.Error("handler must not call Update on invalid quality")
	}
}

func intPtr(n int) *int { return &n }
