package sonarr

import (
	"context"
	"errors"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/metrics"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/topics"
)

// minPollInterval floors a configured poll interval so a misconfiguration
// can't hammer Sonarr. It is also the manager loop's tick cadence.
const minPollInterval = 60 * time.Second

// defaultPollInterval is used when an instance configures none.
const defaultPollInterval = 15 * time.Minute

// topicSourceSonarr tags topics auto-created by this poller (stored in
// extra["source"]) so the UI can badge them. Mirrored by the frontend.
const topicSourceSonarr = "sonarr"

// instancesStore lists the enabled Sonarr instances and advances per-instance
// poll cursors.
type instancesStore interface {
	ListEnabled(ctx context.Context, master *crypto.MasterKey) ([]domain.SonarrInstance, error)
	UpdateCursor(ctx context.Context, id uuid.UUID, lastSeen time.Time) error
}

// adminResolver resolves the fallback owner when an instance configures none.
type adminResolver interface {
	GetInitialAdmin(ctx context.Context) (*domain.User, error)
}

// topicsStore is the topic persistence the poller needs: BuildAndCreate's
// Store (Create), plus a dedup pre-check and an update path.
type topicsStore interface {
	topics.Store
	GetByURL(ctx context.Context, userID uuid.UUID, url string) (*domain.Topic, error)
	Update(ctx context.Context, id, userID uuid.UUID, displayName string, clientID, notifierID *uuid.UUID, downloadDir, category string, replaceOnUpdate, replaceDeleteData bool, extra map[string]any) (*domain.Topic, error)
}

// Poller periodically reads each enabled Sonarr instance's grab history and
// auto-creates Marauder topics for grabs from supported forum trackers. It
// mirrors the scheduler: a ticker loop, ctx-cancellation, and fail-open
// resilience (a Sonarr blip never crashes the loop or advances a cursor past
// unprocessed records).
type Poller struct {
	log       zerolog.Logger
	master    *crypto.MasterKey
	instances instancesStore
	admin     adminResolver
	topics    topicsStore

	// newClient is injectable so tests can point at an httptest server.
	newClient func(baseURL, apiKey string) *Client
}

// New constructs a Poller.
func New(log zerolog.Logger, master *crypto.MasterKey, instances instancesStore, admin adminResolver, topicsStore topicsStore, httpTimeout time.Duration) *Poller {
	return &Poller{
		log:       log.With().Str("component", "sonarr-poller").Logger(),
		master:    master,
		instances: instances,
		admin:     admin,
		topics:    topicsStore,
		newClient: func(baseURL, apiKey string) *Client {
			return NewClient(baseURL, apiKey, httpTimeout)
		},
	}
}

// Start runs the manager loop until ctx is cancelled. It ticks every
// minPollInterval; each tick lists the enabled instances and polls any instance
// whose own poll interval has elapsed since it was last polled. Adding,
// editing, enabling, disabling, or deleting an instance therefore takes effect
// on the next tick with no goroutine lifecycle to manage.
func (p *Poller) Start(ctx context.Context) error {
	p.log.Info().Msg("sonarr poller starting")
	lastPolled := map[uuid.UUID]time.Time{}
	p.tick(ctx, lastPolled)
	ticker := time.NewTicker(minPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			p.log.Info().Msg("sonarr poller stopping")
			return nil
		case <-ticker.C:
			p.tick(ctx, lastPolled)
		}
	}
}

// tick polls every enabled instance that is due. lastPolled is carried across
// ticks to enforce per-instance intervals; entries for instances no longer
// enabled are pruned so a re-enabled instance polls immediately.
func (p *Poller) tick(ctx context.Context, lastPolled map[uuid.UUID]time.Time) {
	instances, err := p.instances.ListEnabled(ctx, p.master)
	if err != nil {
		p.log.Warn().Err(err).Msg("list sonarr instances failed")
		return
	}
	// A single `now` for the whole tick: stamping lastPolled with the same
	// instant used for the due-check keeps poll duration out of the cadence, so
	// an instance at the 60s floor actually polls every 60s (not 120s).
	now := time.Now()
	present := make(map[uuid.UUID]struct{}, len(instances))
	for _, inst := range instances {
		present[inst.ID] = struct{}{}
		if last, seen := lastPolled[inst.ID]; seen && now.Sub(last) < pollInterval(inst.PollIntervalSec) {
			continue // not due yet
		}
		p.pollOnce(ctx, inst)
		lastPolled[inst.ID] = now
	}
	for id := range lastPolled {
		if _, ok := present[id]; !ok {
			delete(lastPolled, id)
		}
	}
}

// pollOnce performs one poll for a single instance. It is fail-open: every
// error path logs and returns without advancing the cursor, so the same history
// window is retried on the next due tick.
func (p *Poller) pollOnce(ctx context.Context, inst domain.SonarrInstance) {
	if inst.URL == "" || inst.APIKey == "" {
		p.log.Warn().Str("instance", inst.Name).Msg("sonarr instance enabled but url/api key not set")
		return
	}

	ownerID, ok := p.resolveOwner(ctx, inst)
	if !ok {
		return
	}

	// First run (no cursor): go-forward only. Stamp the cursor to now and
	// import nothing, so enabling an instance can never flood Marauder with a
	// topic per historical grab. New grabs flow from here on.
	if inst.LastSeenAt == nil {
		if err := p.instances.UpdateCursor(ctx, inst.ID, time.Now().UTC()); err != nil {
			p.log.Warn().Err(err).Str("instance", inst.Name).Msg("init sonarr cursor failed")
		}
		p.log.Info().Str("instance", inst.Name).Msg("sonarr cursor initialised (go-forward); no historical import")
		return
	}

	client := p.newClient(inst.URL, inst.APIKey)
	records, err := client.GrabHistorySince(ctx, *inst.LastSeenAt)
	if err != nil {
		p.log.Warn().Err(err).Str("instance", inst.Name).Msg("fetch sonarr history failed")
		metrics.SonarrPollsTotal.WithLabelValues("error").Inc()
		return // do NOT advance cursor — retry next due tick
	}

	maxDate := p.processRecords(ctx, inst, ownerID, records)
	if maxDate.After(*inst.LastSeenAt) {
		if err := p.instances.UpdateCursor(ctx, inst.ID, maxDate); err != nil {
			p.log.Warn().Err(err).Str("instance", inst.Name).Msg("advance sonarr cursor failed")
		}
	}
	metrics.SonarrPollsTotal.WithLabelValues("ok").Inc()
}

// processRecords dedupes the batch by topic URL and processes each unique URL
// in chronological order. Returns the newest record Date seen (the cursor).
func (p *Poller) processRecords(ctx context.Context, inst domain.SonarrInstance, ownerID uuid.UUID, records []HistoryRecord) time.Time {
	// maxDate advances over EVERY record — including ones skipped below (empty
	// URL, duplicate, or unmatched/disallowed tracker) — so the cursor moves
	// past records we deliberately don't act on instead of re-fetching them on
	// every tick.
	var maxDate time.Time
	latestByURL := make(map[string]HistoryRecord, len(records))
	for _, rec := range records {
		if rec.Date.After(maxDate) {
			maxDate = rec.Date
		}
		url := rec.Data.NzbInfoURL
		if url == "" {
			continue
		}
		current, exists := latestByURL[url]
		if !exists || rec.Date.After(current.Date) || (rec.Date.Equal(current.Date) && rec.ID > current.ID) {
			latestByURL[url] = rec
		}
	}

	// A season pack fans out into one history row per episode. Keep one row per
	// URL, but retain the newest grab so a later quality/codec grab for the same
	// AniLiberty release becomes the monitored variant.
	unique := make([]HistoryRecord, 0, len(latestByURL))
	for _, rec := range latestByURL {
		unique = append(unique, rec)
	}
	sort.Slice(unique, func(i, j int) bool {
		if !unique[i].Date.Equal(unique[j].Date) {
			return unique[i].Date.Before(unique[j].Date)
		}
		return unique[i].ID < unique[j].ID
	})
	for _, rec := range unique {
		p.processURL(ctx, inst, ownerID, rec)
	}
	return maxDate
}

// processURL handles a single topic URL: match → allowed-filter → dedup →
// create or (optionally) update.
func (p *Poller) processURL(ctx context.Context, inst domain.SonarrInstance, ownerID uuid.UUID, rec HistoryRecord) {
	url := rec.Data.NzbInfoURL
	tracker := registry.FindTrackerForURL(url)
	if tracker == nil {
		metrics.SonarrRecordsProcessedTotal.WithLabelValues("no_tracker").Inc()
		return
	}
	if len(inst.AllowedTrackers) > 0 && !slices.Contains(inst.AllowedTrackers, tracker.Name()) {
		metrics.SonarrRecordsProcessedTotal.WithLabelValues("disallowed").Inc()
		return
	}

	existing, err := p.topics.GetByURL(ctx, ownerID, url)
	switch {
	case err == nil:
		p.handleExisting(ctx, inst, ownerID, existing)
		return
	case !errors.Is(err, repo.ErrNotFound):
		p.log.Warn().Err(err).Str("url", url).Msg("topic lookup failed")
		metrics.SonarrRecordsProcessedTotal.WithLabelValues("error").Inc()
		return
	}

	res, err := topics.BuildAndCreate(ctx, p.topics, topics.CreateInput{
		UserID:      ownerID,
		URL:         url,
		ClientID:    inst.DefaultClientID,
		Category:    inst.DefaultCategory,
		DownloadDir: inst.DefaultDownloadDir,
		Source:      topicSourceSonarr,
		Extra:       sonarrTopicExtra(rec),
	})
	if err != nil {
		p.log.Warn().Err(err).Str("url", url).Msg("auto-create topic failed")
		metrics.SonarrRecordsProcessedTotal.WithLabelValues("error").Inc()
		return
	}
	if !res.Created { // lost a create race; another path already has it
		metrics.SonarrRecordsProcessedTotal.WithLabelValues("duplicate").Inc()
		return
	}
	p.log.Info().Str("url", url).Str("tracker", tracker.Name()).
		Str("instance", inst.Name).Str("topic_id", res.Topic.ID.String()).
		Msg("auto-created topic from sonarr grab")
	metrics.SonarrTopicsCreatedTotal.Inc()
	metrics.SonarrRecordsProcessedTotal.WithLabelValues("created").Inc()
}

func sonarrTopicExtra(rec HistoryRecord) map[string]any {
	extra := map[string]any{}
	if value := rec.Data.TorrentInfoHash; value != "" {
		extra["sonarr_infohash"] = value
	}
	if value := rec.SourceTitle; value != "" {
		extra["sonarr_source_title"] = value
	}
	return extra
}

// handleExisting optionally realigns an already-monitored topic's client,
// category, and download dir with the instance's configured defaults.
func (p *Poller) handleExisting(ctx context.Context, inst domain.SonarrInstance, ownerID uuid.UUID, existing *domain.Topic) {
	if !inst.UpdateExisting || !needsRealign(existing, inst) {
		metrics.SonarrRecordsProcessedTotal.WithLabelValues("duplicate").Inc()
		return
	}
	if _, err := p.topics.Update(ctx, existing.ID, ownerID, existing.DisplayName,
		inst.DefaultClientID, existing.NotifierID, inst.DefaultDownloadDir, inst.DefaultCategory,
		existing.ReplaceOnUpdate, existing.ReplaceDeleteData, existing.Extra); err != nil {
		p.log.Warn().Err(err).Str("url", existing.URL).Msg("realign existing topic failed")
		metrics.SonarrRecordsProcessedTotal.WithLabelValues("error").Inc()
		return
	}
	p.log.Info().Str("url", existing.URL).Str("instance", inst.Name).Msg("realigned existing topic to sonarr defaults")
	metrics.SonarrRecordsProcessedTotal.WithLabelValues("updated").Inc()
}

// resolveOwner returns the instance's configured owner, falling back to the
// first admin.
func (p *Poller) resolveOwner(ctx context.Context, inst domain.SonarrInstance) (uuid.UUID, bool) {
	if inst.OwnerUserID != nil {
		return *inst.OwnerUserID, true
	}
	admin, err := p.admin.GetInitialAdmin(ctx)
	if err != nil {
		p.log.Warn().Err(err).Msg("no owner configured and no admin found; skipping poll")
		return uuid.Nil, false
	}
	return admin.ID, true
}

func needsRealign(t *domain.Topic, inst domain.SonarrInstance) bool {
	return !ptrUUIDEqual(t.ClientID, inst.DefaultClientID) ||
		t.Category != inst.DefaultCategory ||
		t.DownloadDir != inst.DefaultDownloadDir
}

func pollInterval(sec int) time.Duration {
	if sec <= 0 {
		return defaultPollInterval
	}
	d := time.Duration(sec) * time.Second
	if d < minPollInterval {
		return minPollInterval
	}
	return d
}

func ptrUUIDEqual(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
