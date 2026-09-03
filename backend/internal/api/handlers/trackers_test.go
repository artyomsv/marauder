package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/api/middleware"
	"github.com/artyomsv/marauder/backend/internal/auth"
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

// --- Preview -----------------------------------------------------------

// fakePreviewTracker is a login-gated WithMetadata tracker: it resolves a real
// title only when handed a credential, exactly like Toloka, which serves a
// guest a stub with an empty <title>. It records what it was given.
type fakePreviewTracker struct {
	name       string
	imageURL   string
	gotCreds   *domain.TrackerCredential
	resolveErr error
}

func (f *fakePreviewTracker) Name() string        { return f.name }
func (f *fakePreviewTracker) DisplayName() string { return "Fake Preview Tracker" }
func (f *fakePreviewTracker) CanParse(rawURL string) bool {
	return rawURL == "https://"+f.name+".test/t1"
}
func (f *fakePreviewTracker) Parse(context.Context, string) (*domain.Topic, error) { return nil, nil }
func (f *fakePreviewTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (f *fakePreviewTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}
func (f *fakePreviewTracker) Login(context.Context, *domain.TrackerCredential) error { return nil }
func (f *fakePreviewTracker) Verify(context.Context, *domain.TrackerCredential) (bool, error) {
	return true, nil
}
func (f *fakePreviewTracker) ResolveMetadata(_ context.Context, _ string, creds *domain.TrackerCredential) (*registry.Metadata, error) {
	f.gotCreds = creds
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	if creds == nil {
		// The guest stub: an empty title, which is what made a Toloka topic
		// store a placeholder name and no poster.
		return &registry.Metadata{}, nil
	}
	return &registry.Metadata{Title: "Real Release Name", ImageURL: f.imageURL}, nil
}

type previewResponse struct {
	Title    string `json:"title"`
	ImageURL string `json:"image_url"`
}

func previewRequest(t *testing.T, uid uuid.UUID, rawURL string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/trackers/preview?url="+url.QueryEscape(rawURL), nil)
	return req.WithContext(context.WithValue(req.Context(), middleware.CtxClaims,
		&auth.Claims{UserID: uid.String()}))
}

// TestTrackers_Preview_UsesTheStoredCredential is the point of the change: a
// login-gated tracker answers an anonymous caller with a stub, so resolving
// as nobody stored topics with a placeholder name and no image — and while
// the scheduler self-heals a placeholder name on the first check, nothing
// backfills an image.
func TestTrackers_Preview_UsesTheStoredCredential(t *testing.T) {
	mk := testMasterKey(t)
	tr := &fakePreviewTracker{name: "fake-preview-gated", imageURL: "https://img.test/p.jpg"}
	registry.RegisterTracker(tr)
	cred := encryptedCred(t, mk, tr.name)

	h := &Trackers{BaseURL: "http://test", Creds: &fakeSearchCredStore{cred: cred}, Master: mk}
	w := httptest.NewRecorder()
	h.Preview(w, previewRequest(t, cred.UserID, "https://"+tr.name+".test/t1"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if tr.gotCreds == nil {
		t.Fatal("ResolveMetadata was called anonymously; a gated tracker returns a stub to a guest")
	}
	var got previewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Title != "Real Release Name" || got.ImageURL != "https://img.test/p.jpg" {
		t.Errorf("preview = %+v, want the resolved title and poster", got)
	}
}

// TestTrackers_Preview_DropsANonHTTPPoster: the poster is scraped off a
// tracker page and the browser renders it into an <img src>, so a compromised
// or hostile page must not be able to hand the viewer a javascript: URL.
func TestTrackers_Preview_DropsANonHTTPPoster(t *testing.T) {
	mk := testMasterKey(t)
	tr := &fakePreviewTracker{name: "fake-preview-hostile", imageURL: "javascript:alert(1)"}
	registry.RegisterTracker(tr)
	cred := encryptedCred(t, mk, tr.name)

	h := &Trackers{BaseURL: "http://test", Creds: &fakeSearchCredStore{cred: cred}, Master: mk}
	w := httptest.NewRecorder()
	h.Preview(w, previewRequest(t, cred.UserID, "https://"+tr.name+".test/t1"))

	var got previewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ImageURL != "" {
		t.Errorf("image_url = %q, want it dropped", got.ImageURL)
	}
	// A scheme filter, not a metadata veto — the title still comes through.
	if got.Title != "Real Release Name" {
		t.Errorf("title = %q, want it unaffected", got.Title)
	}
}

// TestTrackers_Preview_ResolveFailure_IsEmptyNot500: the preview is cosmetic.
// Failing it would block the add-topic form over artwork.
func TestTrackers_Preview_ResolveFailure_IsEmptyNot500(t *testing.T) {
	tr := &fakePreviewTracker{name: "fake-preview-broken", resolveErr: errors.New("tracker down")}
	registry.RegisterTracker(tr)

	h := &Trackers{BaseURL: "http://test"}
	w := httptest.NewRecorder()
	h.Preview(w, previewRequest(t, uuid.New(), "https://"+tr.name+".test/t1"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the preview must fail open", w.Code)
	}
	var got previewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Title != "" || got.ImageURL != "" {
		t.Errorf("preview = %+v, want empty", got)
	}
}

// TestTrackers_Preview_SecondConcurrentRequest_429. The form fires a preview
// on every settled URL edit and each one may log in on a gated tracker;
// Toloka 429s at six requests in three seconds. Same per-user gate as search.
func TestTrackers_Preview_SecondConcurrentRequest_429(t *testing.T) {
	mk := testMasterKey(t)
	release := make(chan struct{})
	tr := &blockingPreviewTracker{
		fakePreviewTracker: fakePreviewTracker{name: "fake-preview-slow"},
		release:            release,
		entered:            make(chan struct{}),
	}
	registry.RegisterTracker(tr)
	cred := encryptedCred(t, mk, tr.name)

	h := &Trackers{BaseURL: "http://test", Creds: &fakeSearchCredStore{cred: cred}, Master: mk}
	target := "https://" + tr.name + ".test/t1"

	started := make(chan struct{})
	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		close(started)
		h.Preview(w, previewRequest(t, cred.UserID, target))
		done <- w.Code
	}()
	<-started
	<-tr.entered // the first request is inside ResolveMetadata, holding the gate

	w2 := httptest.NewRecorder()
	h.Preview(w2, previewRequest(t, cred.UserID, target))
	if w2.Code != http.StatusTooManyRequests {
		t.Errorf("concurrent second preview = %d, want 429", w2.Code)
	}

	close(release)
	if code := <-done; code != http.StatusOK {
		t.Errorf("first preview = %d, want 200", code)
	}
}

// blockingPreviewTracker parks inside ResolveMetadata until released, so the
// gate can be observed while a request genuinely holds it.
type blockingPreviewTracker struct {
	fakePreviewTracker
	release chan struct{}
	entered chan struct{}
}

func (f *blockingPreviewTracker) ResolveMetadata(ctx context.Context, u string, creds *domain.TrackerCredential) (*registry.Metadata, error) {
	close(f.entered)
	<-f.release
	return f.fakePreviewTracker.ResolveMetadata(ctx, u, creds)
}
