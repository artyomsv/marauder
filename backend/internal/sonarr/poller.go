package sonarr

import (
	"context"
	"errors"
	"slices"
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

// minPollInterval floors the configured poll interval so a misconfiguration
// can't hammer Sonarr.
const minPollInterval = 60 * time.Second

// defaultPollInterval is used when none is configured.
const defaultPollInterval = 15 * time.Minute

// topicSourceSonarr tags topics auto-created by this poller (stored in
// extra["source"]) so the UI can badge them. Mirrored by the frontend.
const topicSourceSonarr = "sonarr"

// settingsStore reads the integration config and advances the poll cursor.
type settingsStore interface {
	GetSonarr(ctx context.Context, master *crypto.MasterKey) (*domain.SonarrConfig, error)
	UpdateSonarrCursor(ctx context.Context, lastSeen time.Time) error
}

// adminResolver resolves the fallback owner when none is configured.
type adminResolver interface {
	GetInitialAdmin(ctx context.Context) (*domain.User, error)
}

// topicsStore is the topic persistence the poller needs: BuildAndCreate's
// Store (Create), plus a dedup pre-check and an update path.
type topicsStore interface {
	topics.Store
	GetByURL(ctx context.Context, userID uuid.UUID, url string) (*domain.Topic, error)
	Update(ctx context.Context, id, userID uuid.UUID, displayName string, clientID, notifierID *uuid.UUID, downloadDir, category string, extra map[string]any) (*domain.Topic, error)
}

// Poller periodically reads Sonarr grab history and auto-creates Marauder
// topics for grabs from supported forum trackers. It mirrors the scheduler:
// a ticker loop, ctx-cancellation, and fail-open resilience (a Sonarr blip
// never crashes the loop or advances the cursor past unprocessed records).
type Poller struct {
	log      zerolog.Logger
	master   *crypto.MasterKey
	settings settingsStore
	admin    adminResolver
	topics   topicsStore

	// newClient is injectable so tests can point at an httptest server.
	newClient func(baseURL, apiKey string) *Client
}

// New constructs a Poller.
func New(log zerolog.Logger, master *crypto.MasterKey, settings settingsStore, admin adminResolver, topicsStore topicsStore, httpTimeout time.Duration) *Poller {
	return &Poller{
		log:      log.With().Str("component", "sonarr-poller").Logger(),
		master:   master,
		settings: settings,
		admin:    admin,
		topics:   topicsStore,
		newClient: func(baseURL, apiKey string) *Client {
			return NewClient(baseURL, apiKey, httpTimeout)
		},
	}
}

// Start runs the poll loop until ctx is cancelled. It polls once immediately,
// then on the configured interval, re-reading settings each tick. Config
// changes (enable/disable, interval) are picked up on the NEXT tick, not
// instantly: a just-enabled integration with a long interval waits up to one
// interval before its first poll, and an interval change applies from the
// following tick (the ticker is reset only after a tick observes the new
// value). This is acceptable for a background poller; it is not a restart-free
// instant-apply.
func (p *Poller) Start(ctx context.Context) error {
	p.log.Info().Msg("sonarr poller starting")
	interval := p.pollOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			p.log.Info().Msg("sonarr poller stopping")
			return nil
		case <-ticker.C:
			next := p.pollOnce(ctx)
			if next != interval {
				interval = next
				ticker.Reset(interval)
			}
		}
	}
}

// pollOnce performs one poll and returns the interval to use until the next
// one. It is fail-open: every error path logs and returns the interval
// without advancing the cursor, so the same history window is retried.
func (p *Poller) pollOnce(ctx context.Context) time.Duration {
	cfg, err := p.settings.GetSonarr(ctx, p.master)
	if err != nil {
		p.log.Warn().Err(err).Msg("read sonarr config failed")
		return defaultPollInterval
	}
	interval := pollInterval(cfg.PollIntervalSec)
	if !cfg.Enabled {
		return interval
	}
	if cfg.URL == "" || cfg.APIKey == "" {
		p.log.Warn().Msg("sonarr enabled but url/api key not set")
		return interval
	}

	ownerID, ok := p.resolveOwner(ctx, cfg)
	if !ok {
		return interval
	}

	// First run (no cursor): go-forward only. Stamp the cursor to now and
	// import nothing, so enabling the integration can never flood Marauder
	// with a topic per historical grab. New grabs flow from here on.
	if cfg.LastSeenAt == nil {
		if err := p.settings.UpdateSonarrCursor(ctx, time.Now().UTC()); err != nil {
			p.log.Warn().Err(err).Msg("init sonarr cursor failed")
		}
		p.log.Info().Msg("sonarr cursor initialised (go-forward); no historical import")
		return interval
	}

	client := p.newClient(cfg.URL, cfg.APIKey)
	records, err := client.GrabHistorySince(ctx, *cfg.LastSeenAt)
	if err != nil {
		p.log.Warn().Err(err).Msg("fetch sonarr history failed")
		metrics.SonarrPollsTotal.WithLabelValues("error").Inc()
		return interval // do NOT advance cursor — retry next tick
	}

	maxDate := p.processRecords(ctx, cfg, ownerID, records)
	if maxDate.After(*cfg.LastSeenAt) {
		if err := p.settings.UpdateSonarrCursor(ctx, maxDate); err != nil {
			p.log.Warn().Err(err).Msg("advance sonarr cursor failed")
		}
	}
	metrics.SonarrPollsTotal.WithLabelValues("ok").Inc()
	return interval
}

// processRecords dedupes the batch by topic URL and processes each unique URL
// in chronological order. Returns the newest record Date seen (the cursor).
func (p *Poller) processRecords(ctx context.Context, cfg *domain.SonarrConfig, ownerID uuid.UUID, records []HistoryRecord) time.Time {
	// maxDate advances over EVERY record — including ones skipped below (empty
	// URL, duplicate, or unmatched/disallowed tracker) — so the cursor moves
	// past records we deliberately don't act on instead of re-fetching them on
	// every tick.
	var maxDate time.Time
	seen := make(map[string]struct{}, len(records))
	for _, rec := range records {
		if rec.Date.After(maxDate) {
			maxDate = rec.Date
		}
		url := rec.Data.NzbInfoURL
		if url == "" {
			continue
		}
		if _, dup := seen[url]; dup { // season pack: N records, one topic
			continue
		}
		seen[url] = struct{}{}
		p.processURL(ctx, cfg, ownerID, url)
	}
	return maxDate
}

// processURL handles a single topic URL: match → allowed-filter → dedup →
// create or (optionally) update.
func (p *Poller) processURL(ctx context.Context, cfg *domain.SonarrConfig, ownerID uuid.UUID, url string) {
	tracker := registry.FindTrackerForURL(url)
	if tracker == nil {
		metrics.SonarrRecordsProcessedTotal.WithLabelValues("no_tracker").Inc()
		return
	}
	if len(cfg.AllowedTrackers) > 0 && !slices.Contains(cfg.AllowedTrackers, tracker.Name()) {
		metrics.SonarrRecordsProcessedTotal.WithLabelValues("disallowed").Inc()
		return
	}

	existing, err := p.topics.GetByURL(ctx, ownerID, url)
	switch {
	case err == nil:
		p.handleExisting(ctx, cfg, ownerID, existing)
		return
	case !errors.Is(err, repo.ErrNotFound):
		p.log.Warn().Err(err).Str("url", url).Msg("topic lookup failed")
		metrics.SonarrRecordsProcessedTotal.WithLabelValues("error").Inc()
		return
	}

	res, err := topics.BuildAndCreate(ctx, p.topics, topics.CreateInput{
		UserID:      ownerID,
		URL:         url,
		ClientID:    cfg.DefaultClientID,
		Category:    cfg.DefaultCategory,
		DownloadDir: cfg.DefaultDownloadDir,
		Source:      topicSourceSonarr,
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
		Str("topic_id", res.Topic.ID.String()).Msg("auto-created topic from sonarr grab")
	metrics.SonarrTopicsCreatedTotal.Inc()
	metrics.SonarrRecordsProcessedTotal.WithLabelValues("created").Inc()
}

// handleExisting optionally realigns an already-monitored topic's client,
// category, and download dir with the configured Sonarr defaults.
func (p *Poller) handleExisting(ctx context.Context, cfg *domain.SonarrConfig, ownerID uuid.UUID, existing *domain.Topic) {
	if !cfg.UpdateExisting || !needsRealign(existing, cfg) {
		metrics.SonarrRecordsProcessedTotal.WithLabelValues("duplicate").Inc()
		return
	}
	if _, err := p.topics.Update(ctx, existing.ID, ownerID, existing.DisplayName,
		cfg.DefaultClientID, existing.NotifierID, cfg.DefaultDownloadDir, cfg.DefaultCategory,
		existing.Extra); err != nil {
		p.log.Warn().Err(err).Str("url", existing.URL).Msg("realign existing topic failed")
		metrics.SonarrRecordsProcessedTotal.WithLabelValues("error").Inc()
		return
	}
	p.log.Info().Str("url", existing.URL).Msg("realigned existing topic to sonarr defaults")
	metrics.SonarrRecordsProcessedTotal.WithLabelValues("updated").Inc()
}

// resolveOwner returns the configured owner, falling back to the first admin.
func (p *Poller) resolveOwner(ctx context.Context, cfg *domain.SonarrConfig) (uuid.UUID, bool) {
	if cfg.OwnerUserID != nil {
		return *cfg.OwnerUserID, true
	}
	admin, err := p.admin.GetInitialAdmin(ctx)
	if err != nil {
		p.log.Warn().Err(err).Msg("no owner configured and no admin found; skipping poll")
		return uuid.Nil, false
	}
	return admin.ID, true
}

func needsRealign(t *domain.Topic, cfg *domain.SonarrConfig) bool {
	return !ptrUUIDEqual(t.ClientID, cfg.DefaultClientID) ||
		t.Category != cfg.DefaultCategory ||
		t.DownloadDir != cfg.DefaultDownloadDir
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
