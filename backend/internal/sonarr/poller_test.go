package sonarr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// --- fake tracker registered for the whole sonarr test binary -------------

type pollerFakeTracker struct{}

func (pollerFakeTracker) Name() string        { return "faketracker-test" }
func (pollerFakeTracker) DisplayName() string { return "Fake Tracker" }
func (pollerFakeTracker) CanParse(u string) bool {
	return strings.HasPrefix(u, "https://faketracker.test/")
}
func (pollerFakeTracker) Parse(context.Context, string) (*domain.Topic, error) {
	return &domain.Topic{DisplayName: "Placeholder", Extra: map[string]any{}}, nil
}
func (pollerFakeTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (pollerFakeTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}

func init() { registry.RegisterTracker(pollerFakeTracker{}) }

// --- fakes ---------------------------------------------------------------

type fakeSettings struct {
	cfg    domain.SonarrConfig
	cursor *time.Time
}

func (f *fakeSettings) GetSonarr(context.Context, *crypto.MasterKey) (*domain.SonarrConfig, error) {
	c := f.cfg
	c.LastSeenAt = f.cursor
	return &c, nil
}
func (f *fakeSettings) UpdateSonarrCursor(_ context.Context, t time.Time) error {
	f.cursor = &t
	return nil
}

type fakeAdmin struct{ id uuid.UUID }

func (f fakeAdmin) GetInitialAdmin(context.Context) (*domain.User, error) {
	return &domain.User{ID: f.id}, nil
}

type fakeTopics struct {
	byURL   map[string]*domain.Topic
	created []*domain.Topic
	updated []uuid.UUID
}

func (f *fakeTopics) Create(_ context.Context, t *domain.Topic) (*domain.Topic, error) {
	t.ID = uuid.New()
	f.created = append(f.created, t)
	if f.byURL == nil {
		f.byURL = map[string]*domain.Topic{}
	}
	f.byURL[t.URL] = t
	return t, nil
}
func (f *fakeTopics) GetByURL(_ context.Context, _ uuid.UUID, url string) (*domain.Topic, error) {
	if t, ok := f.byURL[url]; ok {
		return t, nil
	}
	return nil, repo.ErrNotFound
}
func (f *fakeTopics) Update(_ context.Context, id, _ uuid.UUID, _ string, _, _ *uuid.UUID, _, _ string, _ map[string]any) (*domain.Topic, error) {
	f.updated = append(f.updated, id)
	return &domain.Topic{ID: id}, nil
}

// --- helpers -------------------------------------------------------------

const fakeURL = "https://faketracker.test/forum/viewtopic.php?t=9"

func historyServer(records []HistoryRecord) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(historyPage{
			Page: 1, PageSize: 100, TotalRecords: len(records), Records: records,
		})
	}))
}

func baseConfig(serverURL string, owner uuid.UUID) domain.SonarrConfig {
	return domain.SonarrConfig{
		Enabled: true, URL: serverURL, APIKey: "k",
		PollIntervalSec: 900, OwnerUserID: &owner, DefaultCategory: "tv-sonarr",
	}
}

func newTestPoller(s settingsStore, a adminResolver, ts topicsStore) *Poller {
	return New(zerolog.Nop(), nil, s, a, ts, 5*time.Second)
}

// --- tests ---------------------------------------------------------------

func TestPoller_CreatesTopic(t *testing.T) {
	srv := historyServer([]HistoryRecord{
		{ID: 1, Date: time.Now().UTC(), Data: HistoryData{NzbInfoURL: fakeURL}},
	})
	defer srv.Close()

	owner := uuid.New()
	past := time.Now().Add(-time.Hour).UTC()
	s := &fakeSettings{cfg: baseConfig(srv.URL, owner), cursor: &past}
	ts := &fakeTopics{}

	newTestPoller(s, fakeAdmin{}, ts).pollOnce(context.Background())

	if len(ts.created) != 1 {
		t.Fatalf("want 1 created, got %d", len(ts.created))
	}
	if ts.created[0].Category != "tv-sonarr" {
		t.Errorf("default category not applied: %q", ts.created[0].Category)
	}
	if ts.created[0].Extra["source"] != "sonarr" {
		t.Errorf("topic should be tagged source=sonarr, got %v", ts.created[0].Extra["source"])
	}
	if s.cursor == nil || !s.cursor.After(past) {
		t.Errorf("cursor not advanced")
	}
}

func TestPoller_SeasonPackDedup(t *testing.T) {
	now := time.Now().UTC()
	srv := historyServer([]HistoryRecord{
		{ID: 3, Date: now, Data: HistoryData{NzbInfoURL: fakeURL}},
		{ID: 2, Date: now.Add(-time.Minute), Data: HistoryData{NzbInfoURL: fakeURL}},
		{ID: 1, Date: now.Add(-2 * time.Minute), Data: HistoryData{NzbInfoURL: fakeURL}},
	})
	defer srv.Close()

	owner := uuid.New()
	past := now.Add(-time.Hour)
	s := &fakeSettings{cfg: baseConfig(srv.URL, owner), cursor: &past}
	ts := &fakeTopics{}

	newTestPoller(s, fakeAdmin{}, ts).pollOnce(context.Background())

	if len(ts.created) != 1 {
		t.Fatalf("season pack (3 records, 1 url) should create 1 topic, got %d", len(ts.created))
	}
}

func TestPoller_DuplicateSkipped(t *testing.T) {
	srv := historyServer([]HistoryRecord{
		{ID: 1, Date: time.Now().UTC(), Data: HistoryData{NzbInfoURL: fakeURL}},
	})
	defer srv.Close()

	owner := uuid.New()
	past := time.Now().Add(-time.Hour).UTC()
	s := &fakeSettings{cfg: baseConfig(srv.URL, owner), cursor: &past}
	ts := &fakeTopics{byURL: map[string]*domain.Topic{
		fakeURL: {ID: uuid.New(), URL: fakeURL, Category: "tv-sonarr"},
	}}

	newTestPoller(s, fakeAdmin{}, ts).pollOnce(context.Background())

	if len(ts.created) != 0 || len(ts.updated) != 0 {
		t.Errorf("existing topic must be left alone: created=%d updated=%d", len(ts.created), len(ts.updated))
	}
}

func TestPoller_UpdateExistingRealigns(t *testing.T) {
	srv := historyServer([]HistoryRecord{
		{ID: 1, Date: time.Now().UTC(), Data: HistoryData{NzbInfoURL: fakeURL}},
	})
	defer srv.Close()

	owner := uuid.New()
	past := time.Now().Add(-time.Hour).UTC()
	cfg := baseConfig(srv.URL, owner)
	cfg.UpdateExisting = true
	cfg.DefaultCategory = "tv-sonarr"
	s := &fakeSettings{cfg: cfg, cursor: &past}
	ts := &fakeTopics{byURL: map[string]*domain.Topic{
		fakeURL: {ID: uuid.New(), URL: fakeURL, Category: "old-category"}, // differs
	}}

	newTestPoller(s, fakeAdmin{}, ts).pollOnce(context.Background())

	if len(ts.updated) != 1 {
		t.Errorf("want 1 realign update, got %d", len(ts.updated))
	}
}

func TestPoller_NonMatchingURLIgnored(t *testing.T) {
	srv := historyServer([]HistoryRecord{
		{ID: 1, Date: time.Now().UTC(), Data: HistoryData{NzbInfoURL: "https://unknown.example/x"}},
	})
	defer srv.Close()

	owner := uuid.New()
	past := time.Now().Add(-time.Hour).UTC()
	s := &fakeSettings{cfg: baseConfig(srv.URL, owner), cursor: &past}
	ts := &fakeTopics{}

	newTestPoller(s, fakeAdmin{}, ts).pollOnce(context.Background())

	if len(ts.created) != 0 {
		t.Errorf("unmatched URL must not create a topic, got %d", len(ts.created))
	}
}

func TestPoller_DisallowedTrackerSkipped(t *testing.T) {
	srv := historyServer([]HistoryRecord{
		{ID: 1, Date: time.Now().UTC(), Data: HistoryData{NzbInfoURL: fakeURL}},
	})
	defer srv.Close()

	owner := uuid.New()
	past := time.Now().Add(-time.Hour).UTC()
	cfg := baseConfig(srv.URL, owner)
	cfg.AllowedTrackers = []string{"some-other-tracker"}
	s := &fakeSettings{cfg: cfg, cursor: &past}
	ts := &fakeTopics{}

	newTestPoller(s, fakeAdmin{}, ts).pollOnce(context.Background())

	if len(ts.created) != 0 {
		t.Errorf("disallowed tracker must be skipped, got %d created", len(ts.created))
	}
}

func TestPoller_FailOpenOnSonarrError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	owner := uuid.New()
	past := time.Now().Add(-time.Hour).UTC()
	s := &fakeSettings{cfg: baseConfig(srv.URL, owner), cursor: &past}
	ts := &fakeTopics{}

	newTestPoller(s, fakeAdmin{}, ts).pollOnce(context.Background())

	if s.cursor == nil || !s.cursor.Equal(past) {
		t.Errorf("cursor must NOT advance when Sonarr errors")
	}
	if len(ts.created) != 0 {
		t.Errorf("no topics on Sonarr error")
	}
}

func TestPoller_DisabledNoOp(t *testing.T) {
	owner := uuid.New()
	past := time.Now().Add(-time.Hour).UTC()
	cfg := baseConfig("http://unused.invalid", owner)
	cfg.Enabled = false
	s := &fakeSettings{cfg: cfg, cursor: &past}
	ts := &fakeTopics{}

	newTestPoller(s, fakeAdmin{}, ts).pollOnce(context.Background())

	if len(ts.created) != 0 {
		t.Errorf("disabled poller must do nothing")
	}
	if !s.cursor.Equal(past) {
		t.Errorf("disabled poller must not touch cursor")
	}
}

func TestPoller_FirstRunGoesForward(t *testing.T) {
	srv := historyServer([]HistoryRecord{
		{ID: 1, Date: time.Now().UTC(), Data: HistoryData{NzbInfoURL: fakeURL}},
	})
	defer srv.Close()

	owner := uuid.New()
	s := &fakeSettings{cfg: baseConfig(srv.URL, owner), cursor: nil} // no cursor yet
	ts := &fakeTopics{}

	newTestPoller(s, fakeAdmin{}, ts).pollOnce(context.Background())

	if len(ts.created) != 0 {
		t.Errorf("first run must go-forward (import nothing), got %d created", len(ts.created))
	}
	if s.cursor == nil {
		t.Errorf("first run must initialise the cursor")
	}
}
