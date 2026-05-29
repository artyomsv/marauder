package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// fakeSeasonTracker implements registry.Tracker + registry.WithSeasonCatalog.
type fakeSeasonTracker struct{ name string }

func (f *fakeSeasonTracker) Name() string        { return f.name }
func (f *fakeSeasonTracker) DisplayName() string { return "Fake Season Tracker" }
func (f *fakeSeasonTracker) CanParse(rawURL string) bool {
	return rawURL == "https://fake-season-tracker.test/series/1"
}
func (f *fakeSeasonTracker) Parse(context.Context, string) (*domain.Topic, error) { return nil, nil }
func (f *fakeSeasonTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (f *fakeSeasonTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}
func (f *fakeSeasonTracker) SeasonCatalog(_ context.Context, _ string) ([]registry.Season, error) {
	return []registry.Season{
		{Number: 1, Episodes: []int{1, 2, 3}},
		{Number: 2, Episodes: []int{1, 2}},
	}, nil
}

// fakeNoSeasonTracker implements registry.Tracker but NOT WithSeasonCatalog.
type fakeNoSeasonTracker struct{ name string }

func (f *fakeNoSeasonTracker) Name() string        { return f.name }
func (f *fakeNoSeasonTracker) DisplayName() string { return "Fake No-Season Tracker" }
func (f *fakeNoSeasonTracker) CanParse(rawURL string) bool {
	return rawURL == "https://fake-no-season-tracker.test/series/1"
}
func (f *fakeNoSeasonTracker) Parse(context.Context, string) (*domain.Topic, error) { return nil, nil }
func (f *fakeNoSeasonTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (f *fakeNoSeasonTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}

func init() {
	registry.RegisterTracker(&fakeSeasonTracker{name: "fake-season-tracker-test"})
	registry.RegisterTracker(&fakeNoSeasonTracker{name: "fake-no-season-tracker-test"})
}

func TestTrackers_Seasons_OK(t *testing.T) {
	h := &Trackers{BaseURL: "http://test"}
	req := httptest.NewRequest(http.MethodGet, "/trackers/seasons?url=https://fake-season-tracker.test/series/1", nil)
	w := httptest.NewRecorder()

	h.Seasons(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Seasons []registry.Season `json:"seasons"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Seasons) != 2 {
		t.Fatalf("want 2 seasons, got %d", len(body.Seasons))
	}
	if body.Seasons[0].Number != 1 || len(body.Seasons[0].Episodes) != 3 {
		t.Errorf("season 1 mismatch: %+v", body.Seasons[0])
	}
	if body.Seasons[1].Number != 2 || len(body.Seasons[1].Episodes) != 2 {
		t.Errorf("season 2 mismatch: %+v", body.Seasons[1])
	}
}

func TestTrackers_Seasons_NoCapability_422(t *testing.T) {
	h := &Trackers{BaseURL: "http://test"}
	req := httptest.NewRequest(http.MethodGet, "/trackers/seasons?url=https://fake-no-season-tracker.test/series/1", nil)
	w := httptest.NewRecorder()

	h.Seasons(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w.Code, w.Body.String())
	}
}

func TestTrackers_Seasons_UnknownURL_404(t *testing.T) {
	h := &Trackers{BaseURL: "http://test"}
	req := httptest.NewRequest(http.MethodGet, "/trackers/seasons?url=https://totally-unknown.test/", nil)
	w := httptest.NewRecorder()

	h.Seasons(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestTrackers_Seasons_MissingURL_400(t *testing.T) {
	h := &Trackers{BaseURL: "http://test"}
	req := httptest.NewRequest(http.MethodGet, "/trackers/seasons", nil)
	w := httptest.NewRecorder()

	h.Seasons(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}
