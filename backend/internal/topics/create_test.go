package topics

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

// stubDomainsTracker implements registry.WithDomains for CanonicalTopicURL
// tests; the rest of the Tracker interface is unused stubbing.
type stubDomainsTracker struct {
	domains []string
}

func (stubDomainsTracker) Name() string         { return "stub-domains" }
func (stubDomainsTracker) DisplayName() string  { return "Stub Domains Tracker" }
func (stubDomainsTracker) CanParse(string) bool { return true }
func (stubDomainsTracker) Parse(context.Context, string) (*domain.Topic, error) {
	return &domain.Topic{}, nil
}
func (stubDomainsTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (stubDomainsTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}
func (s stubDomainsTracker) Domains() []string { return s.domains }

// stubPlainTracker implements only registry.Tracker (no WithDomains).
type stubPlainTracker struct{}

func (stubPlainTracker) Name() string         { return "stub-plain" }
func (stubPlainTracker) DisplayName() string  { return "Stub Plain Tracker" }
func (stubPlainTracker) CanParse(string) bool { return true }
func (stubPlainTracker) Parse(context.Context, string) (*domain.Topic, error) {
	return &domain.Topic{}, nil
}
func (stubPlainTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (stubPlainTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}

func TestCanonicalTopicURL_RewritesMirrorHost(t *testing.T) {
	tr := &stubDomainsTracker{domains: []string{"kinozal.tv", "kinozal.me"}}
	got := CanonicalTopicURL(tr, "https://kinozal.me/details.php?id=42")
	if got != "https://kinozal.tv/details.php?id=42" {
		t.Errorf("CanonicalTopicURL = %q", got)
	}
	// Same host (modulo www.) → unchanged input returned verbatim.
	if got := CanonicalTopicURL(tr, "https://www.kinozal.tv/details.php?id=42"); got != "https://www.kinozal.tv/details.php?id=42" {
		t.Errorf("same-host URL rewritten: %q", got)
	}
	// Non-WithDomains tracker → unchanged.
	if got := CanonicalTopicURL(&stubPlainTracker{}, "https://x/y"); got != "https://x/y" {
		t.Errorf("plain tracker URL rewritten: %q", got)
	}
}

// canonTracker matches both canon.test and its mirror mirror.test and stores
// whatever URL it is handed, so an integration test can prove BuildAndCreate
// canonicalizes a mirror-host URL to the tracker's default domain before
// persisting (issue #126 dedup identity).
type canonTracker struct{}

func (canonTracker) Name() string        { return "canon-test" }
func (canonTracker) DisplayName() string { return "Canon Tracker" }
func (canonTracker) CanParse(u string) bool {
	return strings.HasPrefix(u, "https://canon.test/") || strings.HasPrefix(u, "https://mirror.test/")
}
func (canonTracker) Parse(_ context.Context, u string) (*domain.Topic, error) {
	return &domain.Topic{DisplayName: "Canon", URL: u, Extra: map[string]any{}}, nil
}
func (canonTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (canonTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}
func (canonTracker) Domains() []string { return []string{"canon.test", "mirror.test"} }

func init() { registry.RegisterTracker(canonTracker{}) }

// TestBuildAndCreate_MirrorURL_CanonicalizesStoredURL proves that adding a
// topic via a mirror host stores it under the canonical (default) domain, so a
// later add of the same topic via the canonical host dedups to one row.
func TestBuildAndCreate_MirrorURL_CanonicalizesStoredURL(t *testing.T) {
	store := &fakeStore{}
	res, err := BuildAndCreate(context.Background(), store, CreateInput{
		UserID: uuid.New(), URL: "https://mirror.test/topic/7",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Created {
		t.Fatalf("want Created")
	}
	if store.created.URL != "https://canon.test/topic/7" {
		t.Errorf("stored URL = %q, want canonical host canon.test", store.created.URL)
	}
}

func TestSafeImageURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"https poster", "https://img.test/p.jpg", "https://img.test/p.jpg"},
		{"http poster", "http://img.test/p.jpg", "http://img.test/p.jpg"},
		{"surrounding whitespace trimmed", "  https://img.test/p.jpg\n", "https://img.test/p.jpg"},
		{"empty stays empty", "", ""},
		{"javascript is dropped", "javascript:alert(1)", ""},
		{"data uri is dropped", "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=", ""},
		{"file is dropped", "file:///etc/passwd", ""},
		{"schemeless path is dropped", "/relative/p.jpg", ""},
		{"hostless https is dropped", "https:///p.jpg", ""},
		{"unparseable is dropped", "https://exa mple.test/\x7f", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SafeImageURL(tc.in); got != tc.want {
				t.Errorf("SafeImageURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// hostileMetaTracker returns a poster URL that would execute in the browser.
// Matches https://hostilemeta.test/*.
type hostileMetaTracker struct{ metaTracker }

func (hostileMetaTracker) Name() string        { return "hostilemeta-test" }
func (hostileMetaTracker) DisplayName() string { return "Hostile Meta Tracker" }
func (hostileMetaTracker) CanParse(u string) bool {
	return strings.HasPrefix(u, "https://hostilemeta.test/")
}
func (hostileMetaTracker) ResolveMetadata(context.Context, string, *domain.TrackerCredential) (*registry.Metadata, error) {
	return &registry.Metadata{Title: "Real Release Name", ImageURL: "javascript:alert(1)"}, nil
}

func init() { registry.RegisterTracker(hostileMetaTracker{}) }

// TestBuildAndCreate_HostileImageURLIsNotStored closes the loop end to end: a
// scraped poster is persisted once and then rendered into an <img src> for
// every later viewer, so the drop has to happen before the row is written.
func TestBuildAndCreate_HostileImageURLIsNotStored(t *testing.T) {
	store := &fakeStore{}
	_, err := BuildAndCreate(context.Background(), store, CreateInput{
		UserID: uuid.New(), URL: "https://hostilemeta.test/topic/1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.created.ImageURL != "" {
		t.Errorf("ImageURL = %q, want it dropped", store.created.ImageURL)
	}
	// The title is unaffected — this is a scheme filter, not a metadata veto.
	if store.created.DisplayName != "Real Release Name" {
		t.Errorf("display name = %q, want Real Release Name", store.created.DisplayName)
	}
}

// slowCredsTracker matches https://slowcreds.test/* and reports the deadline
// its ResolveMetadata was handed.
type slowCredsTracker struct {
	metaTracker
	metaDeadline chan time.Time
}

func (slowCredsTracker) Name() string        { return "slowcreds-test" }
func (slowCredsTracker) DisplayName() string { return "Slow Creds Tracker" }
func (slowCredsTracker) CanParse(u string) bool {
	return strings.HasPrefix(u, "https://slowcreds.test/")
}
func (s *slowCredsTracker) ResolveMetadata(ctx context.Context, _ string, _ *domain.TrackerCredential) (*registry.Metadata, error) {
	dl, _ := ctx.Deadline()
	s.metaDeadline <- dl
	return &registry.Metadata{Title: "Real Release Name"}, nil
}

// TestResolveMetadata_CredentialWarmHasItsOwnBudget pins the split. Sharing one
// deadline let a slow login burn all of it and leave ResolveMetadata to fail
// instantly on a dead context — the exact failure the warm exists to prevent,
// so the fetch must still get its full budget after a warm that runs long.
func TestResolveMetadata_CredentialWarmHasItsOwnBudget(t *testing.T) {
	tr := &slowCredsTracker{metaDeadline: make(chan time.Time, 1)}
	name, resolved := "Placeholder", false
	warmDeadline := make(chan time.Time, 1)
	in := CreateInput{
		URL: "https://slowcreds.test/topic/1",
		Credentials: func(ctx context.Context, _ registry.Tracker) *domain.TrackerCredential {
			dl, ok := ctx.Deadline()
			if !ok {
				t.Error("the credential warm must be bounded")
			}
			warmDeadline <- dl
			return nil
		},
	}
	resolveMetadata(context.Background(), tr, in, &name, &resolved)

	if remaining := time.Until(<-warmDeadline); remaining > credentialWarmTimeout+time.Second {
		t.Errorf("warm got %v of budget, want ~%v", remaining, credentialWarmTimeout)
	}
	// The fetch gets its own full budget, measured AFTER the warm returned —
	// so a warm that spent all of its own leaves this one untouched.
	if remaining := time.Until(<-tr.metaDeadline); remaining < metadataTimeout-time.Second {
		t.Errorf("ResolveMetadata got %v of budget, want ~%v", remaining, metadataTimeout)
	}
}
