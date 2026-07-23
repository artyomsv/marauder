package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/api/middleware"
	"github.com/artyomsv/marauder/backend/internal/auth"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// fakeSearchTracker implements registry.Tracker + registry.WithSearch with a
// canned public result set.
type fakeSearchTracker struct {
	name    string
	results []registry.SearchResult
	err     error
}

func (f *fakeSearchTracker) Name() string                                         { return f.name }
func (f *fakeSearchTracker) DisplayName() string                                  { return "Fake Search " + f.name }
func (f *fakeSearchTracker) CanParse(string) bool                                 { return false }
func (f *fakeSearchTracker) Parse(context.Context, string) (*domain.Topic, error) { return nil, nil }
func (f *fakeSearchTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (f *fakeSearchTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}
func (f *fakeSearchTracker) Search(context.Context, string, *domain.TrackerCredential) ([]registry.SearchResult, error) {
	return f.results, f.err
}

// fakeCredsSearchTracker is login-gated: WithCredentials + WithSearch, and
// Search demands a credential.
type fakeCredsSearchTracker struct{ fakeSearchTracker }

func (f *fakeCredsSearchTracker) Login(context.Context, *domain.TrackerCredential) error {
	return nil
}
func (f *fakeCredsSearchTracker) Verify(context.Context, *domain.TrackerCredential) (bool, error) {
	return true, nil
}
func (f *fakeCredsSearchTracker) Search(_ context.Context, _ string, creds *domain.TrackerCredential) ([]registry.SearchResult, error) {
	if creds == nil {
		return nil, registry.ErrSearchRequiresCredentials
	}
	return f.results, nil
}

// fakeSlowSearchTracker blocks until released, for the single-flight test.
type fakeSlowSearchTracker struct {
	fakeSearchTracker
	started chan struct{}
	release chan struct{}
}

func (f *fakeSlowSearchTracker) Search(ctx context.Context, _ string, _ *domain.TrackerCredential) ([]registry.SearchResult, error) {
	close(f.started)
	select {
	case <-f.release:
	case <-ctx.Done():
	}
	return nil, nil
}

// fakeSearchCredStore satisfies credentialStore; only GetForTracker matters here.
type fakeSearchCredStore struct{ cred *domain.TrackerCredential }

func (s *fakeSearchCredStore) Create(context.Context, *domain.TrackerCredential) (*domain.TrackerCredential, error) {
	return nil, nil
}
func (s *fakeSearchCredStore) GetByID(context.Context, uuid.UUID, uuid.UUID) (*domain.TrackerCredential, error) {
	return nil, nil
}
func (s *fakeSearchCredStore) GetForTracker(context.Context, uuid.UUID, string) (*domain.TrackerCredential, error) {
	if s.cred == nil {
		return nil, context.Canceled // any error means "no credential"
	}
	return s.cred, nil
}
func (s *fakeSearchCredStore) ListForUser(context.Context, uuid.UUID) ([]*domain.TrackerCredential, error) {
	return nil, nil
}
func (s *fakeSearchCredStore) Update(context.Context, uuid.UUID, uuid.UUID, string, []byte, []byte) error {
	return nil
}
func (s *fakeSearchCredStore) SetSession(context.Context, uuid.UUID, uuid.UUID, []byte, []byte) error {
	return nil
}
func (s *fakeSearchCredStore) Delete(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func searchRequest(t *testing.T, uid uuid.UUID, rawQuery string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/trackers/search?"+rawQuery, nil)
	return req.WithContext(context.WithValue(req.Context(), middleware.CtxClaims,
		&auth.Claims{UserID: uid.String()}))
}

type searchResponse struct {
	Results []struct {
		TrackerName string `json:"tracker_name"`
		Title       string `json:"title"`
		URL         string `json:"url"`
		Seeders     int    `json:"seeders"`
	} `json:"results"`
	Errors []struct {
		TrackerName string `json:"tracker_name"`
		Error       string `json:"error"`
	} `json:"errors"`
}

func TestSearch_MissingQuery_BadRequest(t *testing.T) {
	h := &Trackers{BaseURL: "http://test"}
	w := httptest.NewRecorder()
	h.Search(w, searchRequest(t, uuid.New(), ""))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestSearch_QueryTooLong_BadRequest(t *testing.T) {
	h := &Trackers{BaseURL: "http://test"}
	w := httptest.NewRecorder()
	h.Search(w, searchRequest(t, uuid.New(), "q="+strings.Repeat("x", 201)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestSearch_AggregatesAndSortsBySeeders(t *testing.T) {
	registry.RegisterTracker(&fakeSearchTracker{name: "fake-search-a", results: []registry.SearchResult{
		{Title: "low", URL: "https://a.test/1", Seeders: 5},
		{Title: "unknown", URL: "https://a.test/2", Seeders: -1},
	}})
	registry.RegisterTracker(&fakeSearchTracker{name: "fake-search-b", results: []registry.SearchResult{
		{Title: "high", URL: "https://b.test/1", Seeders: 12},
	}})

	h := &Trackers{BaseURL: "http://test"}
	w := httptest.NewRecorder()
	h.Search(w, searchRequest(t, uuid.New(), "q=test&trackers=fake-search-a,fake-search-b"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var resp searchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("results = %d, want 3", len(resp.Results))
	}
	if resp.Results[0].Title != "high" || resp.Results[1].Title != "low" || resp.Results[2].Title != "unknown" {
		t.Errorf("sort order = %s,%s,%s", resp.Results[0].Title, resp.Results[1].Title, resp.Results[2].Title)
	}
	if len(resp.Errors) != 0 {
		t.Errorf("errors = %+v, want none", resp.Errors)
	}
}

func TestSearch_TrackerFilter_Restricts(t *testing.T) {
	registry.RegisterTracker(&fakeSearchTracker{name: "fake-search-only", results: []registry.SearchResult{
		{Title: "kept", URL: "https://only.test/1", Seeders: 1},
	}})
	registry.RegisterTracker(&fakeSearchTracker{name: "fake-search-excluded", results: []registry.SearchResult{
		{Title: "dropped", URL: "https://ex.test/1", Seeders: 9},
	}})

	h := &Trackers{BaseURL: "http://test"}
	w := httptest.NewRecorder()
	h.Search(w, searchRequest(t, uuid.New(), "q=test&trackers=fake-search-only"))
	var resp searchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Title != "kept" {
		t.Fatalf("results = %+v, want only 'kept'", resp.Results)
	}
}

func TestSearch_CredsTrackerWithoutCredential_ReportsError(t *testing.T) {
	registry.RegisterTracker(&fakeCredsSearchTracker{fakeSearchTracker{name: "fake-search-gated"}})
	registry.RegisterTracker(&fakeSearchTracker{name: "fake-search-open", results: []registry.SearchResult{
		{Title: "public", URL: "https://open.test/1", Seeders: 3},
	}})

	h := &Trackers{BaseURL: "http://test", Creds: &fakeSearchCredStore{}} // store has no credential
	w := httptest.NewRecorder()
	h.Search(w, searchRequest(t, uuid.New(), "q=test&trackers=fake-search-gated,fake-search-open"))
	var resp searchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Title != "public" {
		t.Fatalf("results = %+v, want only 'public'", resp.Results)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].TrackerName != "fake-search-gated" ||
		resp.Errors[0].Error != "search requires credentials" {
		t.Fatalf("errors = %+v, want gated needs-credentials entry", resp.Errors)
	}
}

func TestSearch_SecondConcurrentRequest_TooManyRequests(t *testing.T) {
	slow := &fakeSlowSearchTracker{
		fakeSearchTracker: fakeSearchTracker{name: "fake-search-slow"},
		started:           make(chan struct{}),
		release:           make(chan struct{}),
	}
	registry.RegisterTracker(slow)

	h := &Trackers{BaseURL: "http://test"}
	uid := uuid.New()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w := httptest.NewRecorder()
		h.Search(w, searchRequest(t, uid, "q=test&trackers=fake-search-slow"))
	}()

	select {
	case <-slow.started:
	case <-time.After(5 * time.Second):
		t.Fatal("slow search never started")
	}

	w2 := httptest.NewRecorder()
	h.Search(w2, searchRequest(t, uid, "q=test&trackers=fake-search-slow"))
	if w2.Code != http.StatusTooManyRequests {
		t.Errorf("concurrent search status = %d, want 429", w2.Code)
	}

	close(slow.release)
	wg.Wait()
}

func TestListTrackerInfos_SupportsSearchFlag(t *testing.T) {
	infos := listTrackerInfos([]registry.Tracker{
		&fakeSearchTracker{name: "fake-info-search"},
		&fakeNoSeasonTracker{name: "fake-info-plain"},
	})
	if len(infos) != 2 {
		t.Fatalf("infos = %d, want 2", len(infos))
	}
	if infos[0]["supports_search"] != true {
		t.Errorf("search-capable tracker: supports_search = %v, want true", infos[0]["supports_search"])
	}
	if infos[1]["supports_search"] != false {
		t.Errorf("plain tracker: supports_search = %v, want false", infos[1]["supports_search"])
	}
}
