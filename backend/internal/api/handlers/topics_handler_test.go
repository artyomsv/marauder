package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/events"
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

// fakeCreateTracker is a minimal tracker for Create handler tests. It
// matches URLs with the "fake-create://" scheme and returns a stub topic
// from Parse so the Create handler can proceed past URL validation.
type fakeCreateTracker struct{}

func (f *fakeCreateTracker) Name() string        { return "fakecreatetracker" }
func (f *fakeCreateTracker) DisplayName() string { return "Fake Create Tracker" }
func (f *fakeCreateTracker) CanParse(u string) bool {
	return len(u) >= 14 && u[:14] == "fake-create://"
}
func (f *fakeCreateTracker) Parse(_ context.Context, u string) (*domain.Topic, error) {
	return &domain.Topic{DisplayName: "stub", URL: u}, nil
}
func (f *fakeCreateTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (f *fakeCreateTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}

func init() {
	registry.RegisterTracker(&fakeQualityTracker{name: fakeQualityTrackerName})
	registry.RegisterTracker(&fakeCreateTracker{})
}

// fakeTopicStore is an in-memory topicStore for handler tests. It records
// the arguments the handler passed to Update so tests can assert on the
// merged Extra map. It also captures the topic passed to Create so tests
// can verify fields like NotifierID reach the store.
type fakeTopicStore struct {
	getByID    *domain.Topic // returned by GetByID (nil => 404)
	getByIDErr error

	// Captured Create argument.
	created *domain.Topic

	// Captured Update arguments.
	updateCalled            bool
	updateDisplayName       string
	updateClientID          *uuid.UUID
	updateNotifierID        *uuid.UUID
	updateDownloadDir       string
	updateCategory          string
	updateReplaceOnUpdate   bool
	updateReplaceDeleteData bool
	updateExtra             map[string]any
	updateReturn            *domain.Topic

	// Captured ResetCheckState arguments.
	resetCalls [][2]uuid.UUID
	resetErr   error
}

func (s *fakeTopicStore) Create(_ context.Context, t *domain.Topic) (*domain.Topic, error) {
	s.created = t
	out := *t
	out.ID = uuid.New()
	return &out, nil
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

// ResetCheckState records (topicID, userID) per call so tests can assert the
// handler reset the right topic for the right owner.
func (s *fakeTopicStore) ResetCheckState(_ context.Context, id, userID uuid.UUID) error {
	s.resetCalls = append(s.resetCalls, [2]uuid.UUID{id, userID})
	return s.resetErr
}
func (s *fakeTopicStore) Update(_ context.Context, _, _ uuid.UUID, displayName string, clientID, notifierID *uuid.UUID, downloadDir, category string, replaceOnUpdate, replaceDeleteData bool, extra map[string]any) (*domain.Topic, error) {
	s.updateCalled = true
	s.updateDisplayName = displayName
	s.updateClientID = clientID
	s.updateNotifierID = notifierID
	s.updateDownloadDir = downloadDir
	s.updateCategory = category
	s.updateReplaceOnUpdate = replaceOnUpdate
	s.updateReplaceDeleteData = replaceDeleteData
	s.updateExtra = extra
	if s.updateReturn != nil {
		return s.updateReturn, nil
	}
	// Echo back a topic reflecting the edit so the handler can render it.
	return &domain.Topic{
		ID:          uuid.New(),
		DisplayName: displayName,
		ClientID:    clientID,
		NotifierID:  notifierID,
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

func TestTopicsUpdate_PassesReplaceFlags(t *testing.T) {
	store := &fakeTopicStore{
		getByID: &domain.Topic{
			ID:          uuid.New(),
			TrackerName: fakeQualityTrackerName,
			DisplayName: "Show",
		},
	}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	body := updateTopicReq{
		DisplayName:       "Show",
		ReplaceOnUpdate:   boolPtr(true),
		ReplaceDeleteData: boolPtr(false),
	}
	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), body), "id", uuid.New().String())
	h.Update(w, req)

	if w.Code != 200 {
		t.Fatalf("status %d, want 200; body %s", w.Code, w.Body.String())
	}
	if !store.updateReplaceOnUpdate {
		t.Error("replace_on_update should be passed through as true")
	}
	if store.updateReplaceDeleteData {
		t.Error("replace_delete_data should be passed through as false")
	}
}

func TestTopicsUpdate_OmittedReplaceFlags_PreserveExisting(t *testing.T) {
	// An update body without the replace fields must keep the topic's stored
	// values (pointer semantics), not reset them to false.
	store := &fakeTopicStore{
		getByID: &domain.Topic{
			ID:                uuid.New(),
			TrackerName:       fakeQualityTrackerName,
			DisplayName:       "Show",
			ReplaceOnUpdate:   true,
			ReplaceDeleteData: false,
		},
	}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	body := updateTopicReq{DisplayName: "Show"} // no replace fields
	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), body), "id", uuid.New().String())
	h.Update(w, req)

	if w.Code != 200 {
		t.Fatalf("status %d, want 200; body %s", w.Code, w.Body.String())
	}
	if !store.updateReplaceOnUpdate {
		t.Error("omitted replace_on_update should preserve the stored true")
	}
	if store.updateReplaceDeleteData {
		t.Error("omitted replace_delete_data should preserve the stored false")
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

func intPtr(n int) *int    { return &n }
func boolPtr(b bool) *bool { return &b }

// TestTopics_Create_EmitsTopicAdded asserts that a successful POST /topics
// calls h.Emit exactly once with events.TopicAdded.
func TestTopics_Create_EmitsTopicAdded(t *testing.T) {
	var got []events.Event
	store := &fakeTopicStore{}
	h := &Topics{
		Topics:  store,
		BaseURL: "http://test",
		Emit:    func(_ context.Context, ev events.Event) { got = append(got, ev) },
	}

	body := createTopicReq{URL: "fake-create://topic/emit-test"}
	w := httptest.NewRecorder()
	req := authedReq(t, uuid.New(), body)
	h.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	if len(got) != 1 || got[0].Type != events.TopicAdded {
		t.Fatalf("want one topic.added event, got %+v", got)
	}
	if got[0].SourceURL != body.URL {
		t.Errorf("SourceURL = %q, want the topic's tracker URL %q", got[0].SourceURL, body.URL)
	}
}

// ---------------------------------------------------------------------------
// Fake ownership-validation lookups
// ---------------------------------------------------------------------------

// fakeNotifiersOwnershipLookup returns a found Notifier for knownID and
// ErrNotFound for everything else. A nil knownID means all IDs are unknown.
type fakeNotifiersOwnershipLookup struct{ knownID *uuid.UUID }

func (f *fakeNotifiersOwnershipLookup) GetByID(_ context.Context, id uuid.UUID, _ uuid.UUID) (*domain.Notifier, error) {
	if f.knownID != nil && id == *f.knownID {
		return &domain.Notifier{ID: id}, nil
	}
	return nil, repo.ErrNotFound
}

// ---------------------------------------------------------------------------
// Ownership validation tests — Create
// ---------------------------------------------------------------------------

// TestTopics_Create_ValidNotifierID: wired notifiers lookup returns found →
// 201 and captured topic carries the notifier_id.
func TestTopics_Create_ValidNotifierID(t *testing.T) {
	notifierID := uuid.New()
	store := &fakeTopicStore{}
	h := &Topics{
		Topics:    store,
		Notifiers: &fakeNotifiersOwnershipLookup{knownID: &notifierID},
		BaseURL:   "http://x",
	}

	body := createTopicReq{
		URL:        "fake-create://topic/1",
		NotifierID: &notifierID,
	}
	w := httptest.NewRecorder()
	req := authedReq(t, uuid.New(), body)
	h.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	if store.created == nil {
		t.Fatal("handler must call store.Create")
	}
	if store.created.NotifierID == nil || *store.created.NotifierID != notifierID {
		t.Errorf("created.NotifierID = %v, want %s", store.created.NotifierID, notifierID)
	}
}

// TestTopics_Create_UnknownNotifierID: wired notifiers lookup returns ErrNotFound → 422.
func TestTopics_Create_UnknownNotifierID(t *testing.T) {
	knownID := uuid.New()
	unknownID := uuid.New()
	store := &fakeTopicStore{}
	h := &Topics{
		Topics:    store,
		Notifiers: &fakeNotifiersOwnershipLookup{knownID: &knownID},
		BaseURL:   "http://x",
	}

	body := createTopicReq{
		URL:        "fake-create://topic/2",
		NotifierID: &unknownID,
	}
	w := httptest.NewRecorder()
	req := authedReq(t, uuid.New(), body)
	h.Create(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", w.Code, w.Body.String())
	}
	if store.created != nil {
		t.Error("handler must not call store.Create when notifier not found")
	}
}

// ---------------------------------------------------------------------------
// Ownership validation tests — Update
// ---------------------------------------------------------------------------

// TestTopics_Update_UnknownNotifierID: wired notifiers lookup returns ErrNotFound → 422.
func TestTopics_Update_UnknownNotifierID(t *testing.T) {
	knownID := uuid.New()
	unknownID := uuid.New()
	store := &fakeTopicStore{
		getByID: &domain.Topic{ID: uuid.New(), Extra: map[string]any{}},
	}
	h := &Topics{
		Topics:    store,
		Notifiers: &fakeNotifiersOwnershipLookup{knownID: &knownID},
		BaseURL:   "http://x",
	}

	body := updateTopicReq{DisplayName: "X", NotifierID: &unknownID}
	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), body), "id", uuid.New().String())
	h.Update(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", w.Code, w.Body.String())
	}
	if store.updateCalled {
		t.Error("handler must not call store.Update when notifier not found")
	}
}

// TestTopics_Update_PassesNotifierID verifies that notifier_id from the PUT
// body reaches store.Update as the notifierID argument.
func TestTopics_Update_PassesNotifierID(t *testing.T) {
	notifierID := uuid.New()
	store := &fakeTopicStore{
		getByID: &domain.Topic{ID: uuid.New(), Extra: map[string]any{}},
	}
	h := &Topics{Topics: store, BaseURL: "http://x"}

	body := updateTopicReq{DisplayName: "X", NotifierID: &notifierID}
	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), body), "id", uuid.New().String())
	h.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if store.updateNotifierID == nil || *store.updateNotifierID != notifierID {
		t.Errorf("updateNotifierID = %v, want %s", store.updateNotifierID, notifierID)
	}
}
