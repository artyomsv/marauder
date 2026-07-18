package topics

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// fakeTracker matches https://faketopics.test/* and declares a quality list.
type fakeTracker struct{}

func (fakeTracker) Name() string        { return "faketopics-test" }
func (fakeTracker) DisplayName() string { return "Fake Topics Tracker" }
func (fakeTracker) CanParse(u string) bool {
	return strings.HasPrefix(u, "https://faketopics.test/")
}
func (fakeTracker) Parse(context.Context, string) (*domain.Topic, error) {
	return &domain.Topic{DisplayName: "Placeholder", Extra: map[string]any{"seed": "v"}}, nil
}
func (fakeTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (fakeTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}
func (fakeTracker) Qualities() []string    { return []string{"1080p", "720p"} }
func (fakeTracker) DefaultQuality() string { return "1080p" }

func init() { registry.RegisterTracker(fakeTracker{}) }

type fakeStore struct {
	createErr error
	created   *domain.Topic
}

func (f *fakeStore) Create(_ context.Context, t *domain.Topic) (*domain.Topic, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	t.ID = uuid.New()
	f.created = t
	return t, nil
}

const goodURL = "https://faketopics.test/topic/1"

func TestBuildAndCreate_Created(t *testing.T) {
	store := &fakeStore{}
	res, err := BuildAndCreate(context.Background(), store, CreateInput{
		UserID: uuid.New(), URL: goodURL, Category: "tv-sonarr", DownloadDir: "/data", Source: "sonarr",
		Extra: map[string]any{domain.TopicExtraSonarrInfoHash: "abc", "source": "caller-value"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Created || res.Topic == nil {
		t.Fatalf("want Created with topic, got %+v", res)
	}
	if store.created.Extra["source"] != "sonarr" {
		t.Errorf("source tag = %v, want sonarr", store.created.Extra["source"])
	}
	if store.created.Extra[domain.TopicExtraSonarrInfoHash] != "abc" {
		t.Errorf("creator extra not merged: %v", store.created.Extra[domain.TopicExtraSonarrInfoHash])
	}
	if store.created.TrackerName != "faketopics-test" {
		t.Errorf("tracker = %q", store.created.TrackerName)
	}
	if store.created.Category != "tv-sonarr" || store.created.DownloadDir != "/data" {
		t.Errorf("defaults not applied: category=%q dir=%q", store.created.Category, store.created.DownloadDir)
	}
	if store.created.Status != domain.TopicStatusActive {
		t.Errorf("status = %q", store.created.Status)
	}
}

func TestBuildAndCreate_DuplicateIsIdempotent(t *testing.T) {
	store := &fakeStore{createErr: &pgconn.PgError{Code: "23505"}}
	res, err := BuildAndCreate(context.Background(), store, CreateInput{UserID: uuid.New(), URL: goodURL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Created {
		t.Errorf("want Created=false on duplicate")
	}
}

func TestBuildAndCreate_NoTracker(t *testing.T) {
	_, err := BuildAndCreate(context.Background(), &fakeStore{}, CreateInput{URL: "https://unknown.example/x"})
	if !errors.Is(err, ErrNoTracker) {
		t.Fatalf("want ErrNoTracker, got %v", err)
	}
}

func TestBuildAndCreate_QualityUnsupported(t *testing.T) {
	_, err := BuildAndCreate(context.Background(), &fakeStore{}, CreateInput{URL: goodURL, Quality: "4k"})
	if !errors.Is(err, ErrQualityUnsupported) {
		t.Fatalf("want ErrQualityUnsupported, got %v", err)
	}
}

func TestBuildAndCreate_OtherCreateErrorPropagates(t *testing.T) {
	store := &fakeStore{createErr: errors.New("db down")}
	_, err := BuildAndCreate(context.Background(), store, CreateInput{URL: goodURL})
	if err == nil || errors.Is(err, ErrNoTracker) {
		t.Fatalf("want raw db error, got %v", err)
	}
}

// metaTracker is a tracker that also resolves real metadata. Matches
// https://metatopics.test/* and returns a fixed title + image.
type metaTracker struct{}

func (metaTracker) Name() string        { return "metatopics-test" }
func (metaTracker) DisplayName() string { return "Meta Topics Tracker" }
func (metaTracker) CanParse(u string) bool {
	return strings.HasPrefix(u, "https://metatopics.test/")
}
func (metaTracker) Parse(context.Context, string) (*domain.Topic, error) {
	return &domain.Topic{DisplayName: "Meta topic 1", Extra: map[string]any{}}, nil
}
func (metaTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (metaTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}
func (metaTracker) ResolveMetadata(_ context.Context, _ string, _ *domain.TrackerCredential) (*registry.Metadata, error) {
	return &registry.Metadata{Title: "Real Release Name", ImageURL: "https://img.test/p.jpg"}, nil
}

func init() { registry.RegisterTracker(metaTracker{}) }

func TestBuildAndCreate_PlaceholderName_FlagsPlaceholder(t *testing.T) {
	// fakeTracker has no metadata; name falls back to Parse's "Placeholder".
	store := &fakeStore{}
	_, err := BuildAndCreate(context.Background(), store, CreateInput{UserID: uuid.New(), URL: goodURL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !store.created.DisplayNameIsPlaceholder {
		t.Errorf("want DisplayNameIsPlaceholder=true for Parse fallback name")
	}
	if store.created.DisplayName != "Placeholder" {
		t.Errorf("display name = %q, want Placeholder", store.created.DisplayName)
	}
}

func TestBuildAndCreate_ResolvedMetadata_FlagsResolved(t *testing.T) {
	store := &fakeStore{}
	_, err := BuildAndCreate(context.Background(), store, CreateInput{
		UserID: uuid.New(), URL: "https://metatopics.test/topic/1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.created.DisplayNameIsPlaceholder {
		t.Errorf("want DisplayNameIsPlaceholder=false when metadata resolved a title")
	}
	if store.created.DisplayName != "Real Release Name" {
		t.Errorf("display name = %q, want Real Release Name", store.created.DisplayName)
	}
}

func TestBuildAndCreate_CallerSuppliedName_FlagsResolved(t *testing.T) {
	store := &fakeStore{}
	_, err := BuildAndCreate(context.Background(), store, CreateInput{
		UserID: uuid.New(), URL: goodURL, DisplayName: "User Chosen",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.created.DisplayNameIsPlaceholder {
		t.Errorf("want DisplayNameIsPlaceholder=false for caller-supplied name")
	}
}
