package sonarr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

// fakeInstances mimics repo.SonarrInstances: ListEnabled filters by Enabled and
// applies the per-instance cursor; UpdateCursor records the advance.
type fakeInstances struct {
	list    []domain.SonarrInstance
	cursors map[uuid.UUID]*time.Time
}

func (f *fakeInstances) ListEnabled(context.Context, *crypto.MasterKey) ([]domain.SonarrInstance, error) {
	out := []domain.SonarrInstance{}
	for _, inst := range f.list {
		if !inst.Enabled {
			continue
		}
		if c, ok := f.cursors[inst.ID]; ok {
			inst.LastSeenAt = c
		}
		out = append(out, inst)
	}
	return out, nil
}

func (f *fakeInstances) UpdateCursor(_ context.Context, id uuid.UUID, t time.Time) error {
	if f.cursors == nil {
		f.cursors = map[uuid.UUID]*time.Time{}
	}
	tt := t
	f.cursors[id] = &tt
	return nil
}

type fakeAdmin struct{ id uuid.UUID }

func (f fakeAdmin) GetInitialAdmin(context.Context) (*domain.User, error) {
	return &domain.User{ID: f.id}, nil
}

type fakeTopics struct {
	byURL   map[string]*domain.Topic
	created []*domain.Topic
	updated []*domain.Topic
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
func (f *fakeTopics) Update(_ context.Context, id, _ uuid.UUID, displayName string,
	clientID, notifierID *uuid.UUID, downloadDir, category string,
	replaceOnUpdate, replaceDeleteData bool, extra map[string]any,
) (*domain.Topic, error) {
	updated := &domain.Topic{
		ID:                id,
		DisplayName:       displayName,
		ClientID:          clientID,
		NotifierID:        notifierID,
		DownloadDir:       downloadDir,
		Category:          category,
		ReplaceOnUpdate:   replaceOnUpdate,
		ReplaceDeleteData: replaceDeleteData,
		Extra:             extra,
	}
	f.updated = append(f.updated, updated)
	return updated, nil
}

// --- helpers -------------------------------------------------------------

const fakeURL = "https://faketracker.test/forum/viewtopic.php?t=9"

func historyServer(records []HistoryRecord) *httptest.Server {
	// /history/since returns a bare, unpaginated array.
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(records)
	}))
}

// countingHistoryServer also reports how many times it was hit.
func countingHistoryServer(records []HistoryRecord) (*httptest.Server, *int32) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		_ = json.NewEncoder(w).Encode(records)
	}))
	return srv, &hits
}

func baseInstance(serverURL string, owner uuid.UUID) domain.SonarrInstance {
	return domain.SonarrInstance{
		ID: uuid.New(), Name: "TV", Enabled: true, URL: serverURL, APIKey: "k",
		PollIntervalSec: 900, OwnerUserID: &owner, DefaultCategory: "tv-sonarr",
	}
}

func newTestPoller(s instancesStore, a adminResolver, ts topicsStore) *Poller {
	return New(zerolog.Nop(), nil, s, a, ts, 5*time.Second)
}

// --- single-instance behaviour (via pollOnce) ----------------------------

func TestPoller_CreatesTopic(t *testing.T) {
	srv := historyServer([]HistoryRecord{
		{
			ID: 1, Date: time.Now().UTC(), SourceTitle: "Example S01E01 / Example [AVC][1]",
			Data: HistoryData{NzbInfoURL: fakeURL, TorrentInfoHash: "initial-hash"},
		},
	})
	defer srv.Close()

	owner := uuid.New()
	past := time.Now().Add(-time.Hour).UTC()
	inst := baseInstance(srv.URL, owner)
	inst.LastSeenAt = &past
	fi := &fakeInstances{}
	ts := &fakeTopics{}

	newTestPoller(fi, fakeAdmin{}, ts).pollOnce(context.Background(), inst)

	if len(ts.created) != 1 {
		t.Fatalf("want 1 created, got %d", len(ts.created))
	}
	if ts.created[0].Category != "tv-sonarr" {
		t.Errorf("default category not applied: %q", ts.created[0].Category)
	}
	if ts.created[0].Extra["source"] != "sonarr" {
		t.Errorf("topic should be tagged source=sonarr, got %v", ts.created[0].Extra["source"])
	}
	if ts.created[0].Extra["sonarr_infohash"] != "initial-hash" {
		t.Errorf("Sonarr infohash not preserved: %v", ts.created[0].Extra["sonarr_infohash"])
	}
	if ts.created[0].Extra["sonarr_source_title"] != "Example S01E01 / Example [AVC][1]" {
		t.Errorf("Sonarr source title not preserved: %v", ts.created[0].Extra["sonarr_source_title"])
	}
	if c := fi.cursors[inst.ID]; c == nil || !c.After(past) {
		t.Errorf("cursor not advanced")
	}
}

func TestPoller_SeasonPackDedup(t *testing.T) {
	now := time.Now().UTC()
	srv := historyServer([]HistoryRecord{
		{
			ID: 3, Date: now, SourceTitle: "newest AVC grab",
			Data: HistoryData{NzbInfoURL: fakeURL, TorrentInfoHash: "newest-hash"},
		},
		{
			ID: 2, Date: now.Add(-time.Minute), SourceTitle: "older HEVC grab",
			Data: HistoryData{NzbInfoURL: fakeURL, TorrentInfoHash: "older-hash"},
		},
		{
			ID: 1, Date: now.Add(-2 * time.Minute), SourceTitle: "oldest HEVC grab",
			Data: HistoryData{NzbInfoURL: fakeURL, TorrentInfoHash: "oldest-hash"},
		},
	})
	defer srv.Close()

	owner := uuid.New()
	past := now.Add(-time.Hour)
	inst := baseInstance(srv.URL, owner)
	inst.LastSeenAt = &past
	ts := &fakeTopics{}

	newTestPoller(&fakeInstances{}, fakeAdmin{}, ts).pollOnce(context.Background(), inst)

	if len(ts.created) != 1 {
		t.Fatalf("season pack (3 records, 1 url) should create 1 topic, got %d", len(ts.created))
	}
	if ts.created[0].Extra["sonarr_infohash"] != "newest-hash" ||
		ts.created[0].Extra["sonarr_source_title"] != "newest AVC grab" {
		t.Errorf("dedup should retain newest grab metadata, got %#v", ts.created[0].Extra)
	}
}

func TestPoller_DuplicateSkipped(t *testing.T) {
	srv := historyServer([]HistoryRecord{
		{ID: 1, Date: time.Now().UTC(), Data: HistoryData{NzbInfoURL: fakeURL}},
	})
	defer srv.Close()

	owner := uuid.New()
	past := time.Now().Add(-time.Hour).UTC()
	inst := baseInstance(srv.URL, owner)
	inst.LastSeenAt = &past
	ts := &fakeTopics{byURL: map[string]*domain.Topic{
		fakeURL: {ID: uuid.New(), URL: fakeURL, Category: "tv-sonarr"},
	}}

	newTestPoller(&fakeInstances{}, fakeAdmin{}, ts).pollOnce(context.Background(), inst)

	if len(ts.created) != 0 || len(ts.updated) != 0 {
		t.Errorf("existing topic must be left alone: created=%d updated=%d", len(ts.created), len(ts.updated))
	}
}

func TestPoller_ExistingTopicRefreshesVariantMetadata(t *testing.T) {
	srv := historyServer([]HistoryRecord{
		{
			ID: 1, Date: time.Now().UTC(), SourceTitle: "new AVC grab",
			Data: HistoryData{NzbInfoURL: fakeURL, TorrentInfoHash: "new-hash"},
		},
	})
	defer srv.Close()

	owner := uuid.New()
	past := time.Now().Add(-time.Hour).UTC()
	inst := baseInstance(srv.URL, owner)
	inst.LastSeenAt = &past
	ts := &fakeTopics{byURL: map[string]*domain.Topic{
		fakeURL: {
			ID: uuid.New(), URL: fakeURL, DisplayName: "Existing",
			Category: "manual-category", DownloadDir: "/manual",
			Extra: map[string]any{
				"provider":            "aniliberty",
				"alias":               "example",
				"sonarr_infohash":     "old-hash",
				"sonarr_source_title": "old HEVC grab",
			},
		},
	}}

	newTestPoller(&fakeInstances{}, fakeAdmin{}, ts).pollOnce(context.Background(), inst)

	if len(ts.updated) != 1 {
		t.Fatalf("want 1 metadata update, got %d", len(ts.updated))
	}
	updated := ts.updated[0]
	if updated.Extra["sonarr_infohash"] != "new-hash" ||
		updated.Extra["sonarr_source_title"] != "new AVC grab" {
		t.Errorf("variant metadata not refreshed: %#v", updated.Extra)
	}
	if updated.Extra["provider"] != "aniliberty" || updated.Extra["alias"] != "example" {
		t.Errorf("tracker metadata not preserved: %#v", updated.Extra)
	}
	if updated.Category != "manual-category" || updated.DownloadDir != "/manual" {
		t.Errorf("UpdateExisting=false must preserve routing, got category=%q dir=%q",
			updated.Category, updated.DownloadDir)
	}
}

func TestPoller_UpdateExistingRealigns(t *testing.T) {
	srv := historyServer([]HistoryRecord{
		{ID: 1, Date: time.Now().UTC(), Data: HistoryData{NzbInfoURL: fakeURL}},
	})
	defer srv.Close()

	owner := uuid.New()
	past := time.Now().Add(-time.Hour).UTC()
	inst := baseInstance(srv.URL, owner)
	inst.LastSeenAt = &past
	inst.UpdateExisting = true
	inst.DefaultCategory = "tv-sonarr"
	ts := &fakeTopics{byURL: map[string]*domain.Topic{
		fakeURL: {ID: uuid.New(), URL: fakeURL, Category: "old-category"}, // differs
	}}

	newTestPoller(&fakeInstances{}, fakeAdmin{}, ts).pollOnce(context.Background(), inst)

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
	inst := baseInstance(srv.URL, owner)
	inst.LastSeenAt = &past
	ts := &fakeTopics{}

	newTestPoller(&fakeInstances{}, fakeAdmin{}, ts).pollOnce(context.Background(), inst)

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
	inst := baseInstance(srv.URL, owner)
	inst.LastSeenAt = &past
	inst.AllowedTrackers = []string{"some-other-tracker"}
	ts := &fakeTopics{}

	newTestPoller(&fakeInstances{}, fakeAdmin{}, ts).pollOnce(context.Background(), inst)

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
	inst := baseInstance(srv.URL, owner)
	inst.LastSeenAt = &past
	fi := &fakeInstances{}
	ts := &fakeTopics{}

	newTestPoller(fi, fakeAdmin{}, ts).pollOnce(context.Background(), inst)

	if fi.cursors[inst.ID] != nil {
		t.Errorf("cursor must NOT advance when Sonarr errors")
	}
	if len(ts.created) != 0 {
		t.Errorf("no topics on Sonarr error")
	}
}

func TestPoller_FirstRunGoesForward(t *testing.T) {
	srv := historyServer([]HistoryRecord{
		{ID: 1, Date: time.Now().UTC(), Data: HistoryData{NzbInfoURL: fakeURL}},
	})
	defer srv.Close()

	owner := uuid.New()
	inst := baseInstance(srv.URL, owner)
	inst.LastSeenAt = nil // no cursor yet
	fi := &fakeInstances{}
	ts := &fakeTopics{}

	newTestPoller(fi, fakeAdmin{}, ts).pollOnce(context.Background(), inst)

	if len(ts.created) != 0 {
		t.Errorf("first run must go-forward (import nothing), got %d created", len(ts.created))
	}
	if fi.cursors[inst.ID] == nil {
		t.Errorf("first run must initialise the cursor")
	}
}

// --- multi-instance behaviour (via tick) ---------------------------------

func TestPoller_PollsEachEnabledInstanceWithOwnCursor(t *testing.T) {
	now := time.Now().UTC()
	srv := historyServer([]HistoryRecord{
		{ID: 1, Date: now, Data: HistoryData{NzbInfoURL: fakeURL}},
	})
	defer srv.Close()

	owner := uuid.New()
	past := now.Add(-time.Hour)
	tv := baseInstance(srv.URL, owner)
	tv.Name = "TV"
	anime := baseInstance(srv.URL, owner)
	anime.Name = "Anime"
	fi := &fakeInstances{
		list:    []domain.SonarrInstance{tv, anime},
		cursors: map[uuid.UUID]*time.Time{tv.ID: &past, anime.ID: &past},
	}
	ts := &fakeTopics{}

	newTestPoller(fi, fakeAdmin{}, ts).tick(context.Background(), map[uuid.UUID]time.Time{})

	if c := fi.cursors[tv.ID]; c == nil || !c.After(past) {
		t.Errorf("TV cursor not advanced independently")
	}
	if c := fi.cursors[anime.ID]; c == nil || !c.After(past) {
		t.Errorf("Anime cursor not advanced independently")
	}
}

func TestPoller_SkipsDisabledInstance(t *testing.T) {
	now := time.Now().UTC()
	srv := historyServer([]HistoryRecord{
		{ID: 1, Date: now, Data: HistoryData{NzbInfoURL: fakeURL}},
	})
	defer srv.Close()

	owner := uuid.New()
	past := now.Add(-time.Hour)
	on := baseInstance(srv.URL, owner)
	off := baseInstance(srv.URL, owner)
	off.Enabled = false
	fi := &fakeInstances{
		list:    []domain.SonarrInstance{on, off},
		cursors: map[uuid.UUID]*time.Time{on.ID: &past, off.ID: &past},
	}
	ts := &fakeTopics{}

	newTestPoller(fi, fakeAdmin{}, ts).tick(context.Background(), map[uuid.UUID]time.Time{})

	if c := fi.cursors[on.ID]; c == nil || !c.After(past) {
		t.Errorf("enabled instance should have polled and advanced cursor")
	}
	if c := fi.cursors[off.ID]; c == nil || !c.Equal(past) {
		t.Errorf("disabled instance cursor must be untouched")
	}
}

func TestPoller_ReenabledInstancePollsImmediately(t *testing.T) {
	now := time.Now().UTC()
	srv, hits := countingHistoryServer([]HistoryRecord{
		{ID: 1, Date: now, Data: HistoryData{NzbInfoURL: fakeURL}},
	})
	defer srv.Close()

	owner := uuid.New()
	past := now.Add(-time.Hour)
	inst := baseInstance(srv.URL, owner) // 900s interval
	fi := &fakeInstances{
		list:    []domain.SonarrInstance{inst},
		cursors: map[uuid.UUID]*time.Time{inst.ID: &past},
	}
	p := newTestPoller(fi, fakeAdmin{}, &fakeTopics{})

	lastPolled := map[uuid.UUID]time.Time{}
	p.tick(context.Background(), lastPolled) // enabled → polled (hit 1)
	fi.list[0].Enabled = false
	p.tick(context.Background(), lastPolled) // disabled → pruned from lastPolled
	fi.list[0].Enabled = true
	p.tick(context.Background(), lastPolled) // re-enabled → pruned, so polls now (hit 2)

	if got := atomic.LoadInt32(hits); got != 2 {
		t.Errorf("re-enabled instance must poll immediately (lastPolled pruned): got %d hits, want 2", got)
	}
}

func TestPoller_PerInstanceIntervalDue(t *testing.T) {
	now := time.Now().UTC()
	srv, hits := countingHistoryServer([]HistoryRecord{
		{ID: 1, Date: now, Data: HistoryData{NzbInfoURL: fakeURL}},
	})
	defer srv.Close()

	owner := uuid.New()
	past := now.Add(-time.Hour)
	inst := baseInstance(srv.URL, owner) // 900s interval
	fi := &fakeInstances{
		list:    []domain.SonarrInstance{inst},
		cursors: map[uuid.UUID]*time.Time{inst.ID: &past},
	}
	ts := &fakeTopics{}
	p := newTestPoller(fi, fakeAdmin{}, ts)

	lastPolled := map[uuid.UUID]time.Time{}
	p.tick(context.Background(), lastPolled) // due (never polled) → 1 hit
	p.tick(context.Background(), lastPolled) // within interval → skipped

	if got := atomic.LoadInt32(hits); got != 1 {
		t.Errorf("instance within its interval must not be re-polled: got %d hits, want 1", got)
	}
}
