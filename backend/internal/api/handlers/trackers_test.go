package handlers

import (
	"context"
	"encoding/json"
	"errors"
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

// fakeErrSeasonTracker implements registry.Tracker + WithSeasonCatalog but
// its SeasonCatalog always fails, exercising the 502 path.
type fakeErrSeasonTracker struct{ name string }

func (f *fakeErrSeasonTracker) Name() string        { return f.name }
func (f *fakeErrSeasonTracker) DisplayName() string { return "Fake Err Season Tracker" }
func (f *fakeErrSeasonTracker) CanParse(rawURL string) bool {
	return rawURL == "https://fake-err-season-tracker.test/series/1"
}
func (f *fakeErrSeasonTracker) Parse(context.Context, string) (*domain.Topic, error) { return nil, nil }
func (f *fakeErrSeasonTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (f *fakeErrSeasonTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}
func (f *fakeErrSeasonTracker) SeasonCatalog(_ context.Context, _ string) ([]registry.Season, error) {
	return nil, errors.New("boom")
}

// fakeRequiredCredsTracker implements registry.WithCredentials but NOT
// WithAnonymousDownload — credentials are required.
type fakeRequiredCredsTracker struct{ name string }

func (f *fakeRequiredCredsTracker) Name() string        { return f.name }
func (f *fakeRequiredCredsTracker) DisplayName() string { return "Fake Required-Creds Tracker" }
func (f *fakeRequiredCredsTracker) CanParse(rawURL string) bool {
	return rawURL == "https://fake-required-creds.test/topic/1"
}
func (f *fakeRequiredCredsTracker) Parse(context.Context, string) (*domain.Topic, error) {
	return nil, nil
}
func (f *fakeRequiredCredsTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (f *fakeRequiredCredsTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}
func (f *fakeRequiredCredsTracker) Login(context.Context, *domain.TrackerCredential) error {
	return nil
}
func (f *fakeRequiredCredsTracker) Verify(context.Context, *domain.TrackerCredential) (bool, error) {
	return true, nil
}

// fakeOptionalCredsTracker implements registry.WithCredentials AND
// WithAnonymousDownload — credentials are optional.
type fakeOptionalCredsTracker struct{ name string }

func (f *fakeOptionalCredsTracker) Name() string        { return f.name }
func (f *fakeOptionalCredsTracker) DisplayName() string { return "Fake Optional-Creds Tracker" }
func (f *fakeOptionalCredsTracker) CanParse(rawURL string) bool {
	return rawURL == "https://fake-optional-creds.test/topic/1"
}
func (f *fakeOptionalCredsTracker) Parse(context.Context, string) (*domain.Topic, error) {
	return nil, nil
}
func (f *fakeOptionalCredsTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (f *fakeOptionalCredsTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}
func (f *fakeOptionalCredsTracker) Login(context.Context, *domain.TrackerCredential) error {
	return nil
}
func (f *fakeOptionalCredsTracker) Verify(context.Context, *domain.TrackerCredential) (bool, error) {
	return true, nil
}
func (f *fakeOptionalCredsTracker) SupportsAnonymousDownload() bool { return true }

func init() {
	registry.RegisterTracker(&fakeSeasonTracker{name: "fake-season-tracker-test"})
	registry.RegisterTracker(&fakeNoSeasonTracker{name: "fake-no-season-tracker-test"})
	registry.RegisterTracker(&fakeErrSeasonTracker{name: "fake-err-season-tracker-test"})
	registry.RegisterTracker(&fakeRequiredCredsTracker{name: "fake-required-creds-test"})
	registry.RegisterTracker(&fakeOptionalCredsTracker{name: "fake-optional-creds-test"})
}

func TestTrackers_Match_RequiredCredentials(t *testing.T) {
	h := &Trackers{BaseURL: "http://test"}
	req := httptest.NewRequest(http.MethodGet, "/trackers/match?url=https://fake-required-creds.test/topic/1", nil)
	w := httptest.NewRecorder()

	h.Match(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		RequiresCredentials bool `json:"requires_credentials"`
		CredentialsOptional bool `json:"credentials_optional"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.RequiresCredentials {
		t.Error("want requires_credentials=true for a WithCredentials-only tracker")
	}
	if body.CredentialsOptional {
		t.Error("want credentials_optional=false for a WithCredentials-only tracker")
	}
}

func TestTrackers_Match_OptionalCredentials(t *testing.T) {
	h := &Trackers{BaseURL: "http://test"}
	req := httptest.NewRequest(http.MethodGet, "/trackers/match?url=https://fake-optional-creds.test/topic/1", nil)
	w := httptest.NewRecorder()

	h.Match(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		RequiresCredentials bool `json:"requires_credentials"`
		CredentialsOptional bool `json:"credentials_optional"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.RequiresCredentials {
		t.Error("want requires_credentials=false for an anonymous-capable tracker")
	}
	if !body.CredentialsOptional {
		t.Error("want credentials_optional=true for an anonymous-capable tracker")
	}
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

func TestTrackers_Seasons_UpstreamError_502(t *testing.T) {
	h := &Trackers{BaseURL: "http://test"}
	req := httptest.NewRequest(http.MethodGet, "/trackers/seasons?url=https://fake-err-season-tracker.test/series/1", nil)
	w := httptest.NewRecorder()

	h.Seasons(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d: %s", w.Code, w.Body.String())
	}
}
