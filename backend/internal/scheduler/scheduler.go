// Package scheduler runs the periodic "check every due topic" loop.
//
// Design:
//   - A single ticker goroutine wakes up every config.SchedulerTick.
//   - It queries repo.Topics.DueForCheck for topics whose next_check_at is
//     past. The number is bounded to at most `workers * 4` so a single tick
//     cannot overload the worker pool.
//   - Each due topic is sent to a worker via a buffered channel. Workers
//     run checks concurrently up to `workers` parallelism.
//   - A worker calls the registered Tracker plugin for the topic, compares
//     the hash, and if the hash changed it calls Download and hands the
//     payload to the assigned client.
//   - After the check, the worker calls repo.Topics.RecordCheckResult to
//     persist the next_check_at and any error.
//
// Errors use exponential backoff capped at config.CheckMaxBackoff. Success
// resets the interval to the topic's configured check_interval_sec.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/config"
	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/events"
	"github.com/artyomsv/marauder/backend/internal/extra"
	"github.com/artyomsv/marauder/backend/internal/infohash"
	"github.com/artyomsv/marauder/backend/internal/metrics"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// --- Consumer-side interfaces ------------------------------------------
//
// The scheduler depends on small interfaces rather than the concrete
// repo types so unit tests can supply in-memory fakes without touching
// a Postgres pool. The concrete repo.Topics / repo.Clients /
// repo.TrackerCredentials types satisfy these interfaces structurally.

// topicsRepo is the subset of *repo.Topics that the scheduler uses.
type topicsRepo interface {
	DueForCheck(ctx context.Context, limit int) ([]*domain.Topic, error)
	RecordCheckResult(ctx context.Context, id uuid.UUID, hash string, updated bool, nextCheckAt time.Time, errMsg string) error
	UpdateExtra(ctx context.Context, id uuid.UUID, extra map[string]any) error
}

// markEpisodeDownloader is an optional capability of topicsRepo.
// Track C is adding *repo.Topics.MarkEpisodeDownloaded as an atomic
// JSONB append; if present, the scheduler uses it instead of the
// non-atomic UpdateExtra(full map) round-trip.
type markEpisodeDownloader interface {
	MarkEpisodeDownloaded(ctx context.Context, id uuid.UUID, packed string) error
}

// displayNamePersister is an optional capability of topicsRepo: it lets the
// scheduler upgrade a topic's stored title to the real one a tracker resolved
// during Check (self-healing placeholder names). Best-effort — a failure here
// never affects the check result.
type displayNamePersister interface {
	UpdateDisplayName(ctx context.Context, id uuid.UUID, displayName string) error
}

// clientsRepo is the subset of *repo.Clients that the scheduler uses.
type clientsRepo interface {
	GetByID(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*domain.Client, error)
	GetDefault(ctx context.Context, userID uuid.UUID) (*domain.Client, error)
}

// credentialsRepo is the subset of *repo.TrackerCredentials that the
// scheduler uses.
type credentialsRepo interface {
	GetForTracker(ctx context.Context, userID uuid.UUID, trackerName string) (*domain.TrackerCredential, error)
	MarkSessionExpired(ctx context.Context, id, userID uuid.UUID) (bool, error)
}

// emitter is the subset of *events.Bus the scheduler uses to publish typed
// lifecycle events. Defined as an interface so the scheduler stays
// unit-testable without the bus, and nil-safe in tests that ignore events.
type emitter interface {
	Emit(ctx context.Context, ev events.Event)
}

// decryptor is the subset of *crypto.MasterKey that the scheduler uses.
type decryptor interface {
	Decrypt(ct, nonce []byte) ([]byte, error)
}

// deliveriesRecorder is the subset of *repo.Deliveries the scheduler uses
// to log what it pushed to a client. Best-effort: a failure here is logged
// and never affects the download/check outcome. Defined as an interface so
// it's nil-safe in unit tests that don't exercise delivery tracking.
type deliveriesRecorder interface {
	Record(ctx context.Context, d *domain.TopicDelivery) (bool, error)
}

// trackerLookupFn is a test seam: the scheduler resolves a tracker by
// name through this function so tests can inject fakes without touching
// the global registry.
type trackerLookupFn func(name string) registry.Tracker

// clientLookupFn is the analogous seam for client plugins.
type clientLookupFn func(name string) registry.Client

// RunSummary captures one tick's outcome for the system status endpoint.
type RunSummary struct {
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Checked   int       `json:"checked"`
	Updated   int       `json:"updated"`
	Errors    int       `json:"errors"`
}

// Scheduler is the running scheduler service.
type Scheduler struct {
	cfg        *config.Config
	log        zerolog.Logger
	topics     topicsRepo
	clients    clientsRepo
	creds      credentialsRepo
	deliveries deliveriesRecorder // nil-safe; records what was pushed to a client
	master     decryptor
	emit       emitter // nil-safe; publishes typed lifecycle events

	// Test seams (default to registry.GetTracker / registry.GetClient).
	lookupTracker trackerLookupFn
	lookupClient  clientLookupFn

	jobs  chan *domain.Topic
	wg    sync.WaitGroup
	stop  chan struct{}
	ready chan struct{}

	// Lightweight in-memory ring buffer of recent run summaries.
	historyMu sync.Mutex
	history   []RunSummary

	// Live counters for the in-flight run.
	currentMu sync.Mutex
	current   *RunSummary
}

// New constructs a scheduler.
func New(cfg *config.Config, log zerolog.Logger, topics *repo.Topics, clients *repo.Clients, creds *repo.TrackerCredentials, deliveries *repo.Deliveries, master *crypto.MasterKey, emit emitter) *Scheduler {
	return &Scheduler{
		cfg:           cfg,
		log:           log.With().Str("component", "scheduler").Logger(),
		topics:        topics,
		clients:       clients,
		creds:         creds,
		deliveries:    deliveries,
		master:        master,
		emit:          emit,
		lookupTracker: registry.GetTracker,
		lookupClient:  registry.GetClient,
		jobs:          make(chan *domain.Topic, cfg.SchedulerWorkers*4),
		stop:          make(chan struct{}),
		ready:         make(chan struct{}),
	}
}

// Start launches the scheduler. It blocks until the passed ctx is cancelled,
// at which point it drains in-flight work and returns.
func (s *Scheduler) Start(ctx context.Context) error {
	s.log.Info().
		Int("workers", s.cfg.SchedulerWorkers).
		Dur("tick", s.cfg.SchedulerTick).
		Msg("scheduler starting")

	for i := 0; i < s.cfg.SchedulerWorkers; i++ {
		s.wg.Add(1)
		go s.worker(ctx, i)
	}

	close(s.ready)
	ticker := time.NewTicker(s.cfg.SchedulerTick)
	defer ticker.Stop()

	// Kick off immediately
	s.dispatchOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			s.log.Info().Msg("scheduler stopping")
			close(s.jobs)
			s.wg.Wait()
			return nil
		case <-ticker.C:
			s.dispatchOnce(ctx)
		}
	}
}

// Ready blocks until workers are up. Useful in tests.
func (s *Scheduler) Ready() <-chan struct{} { return s.ready }

func (s *Scheduler) dispatchOnce(ctx context.Context) {
	if !s.cfg.SchedulerEnabled {
		return
	}
	limit := s.cfg.SchedulerWorkers * 4
	due, err := s.topics.DueForCheck(ctx, limit)
	if err != nil {
		s.log.Error().Err(err).Msg("DueForCheck failed")
		metrics.SchedulerRunsTotal.WithLabelValues("error").Inc()
		return
	}

	if len(due) == 0 {
		metrics.SchedulerRunsTotal.WithLabelValues("ok").Inc()
		return
	}

	// Open a new run summary that workers will increment.
	s.beginRun()
	defer s.endRun()
	metrics.SchedulerRunsTotal.WithLabelValues("ok").Inc()

	for _, t := range due {
		select {
		case s.jobs <- t:
		case <-ctx.Done():
			return
		default:
			s.log.Warn().Msg("job queue full; will retry next tick")
			return
		}
	}
}

func (s *Scheduler) beginRun() {
	s.currentMu.Lock()
	defer s.currentMu.Unlock()
	now := time.Now().UTC()
	s.current = &RunSummary{StartedAt: now}
}

func (s *Scheduler) endRun() {
	s.currentMu.Lock()
	if s.current == nil {
		s.currentMu.Unlock()
		return
	}
	s.current.EndedAt = time.Now().UTC()
	finished := *s.current
	s.current = nil
	s.currentMu.Unlock()

	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	s.history = append(s.history, finished)
	const maxHistory = 50
	if len(s.history) > maxHistory {
		s.history = s.history[len(s.history)-maxHistory:]
	}
}

// History returns a snapshot of the most-recent run summaries (newest last).
func (s *Scheduler) History() []RunSummary {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	out := make([]RunSummary, len(s.history))
	copy(out, s.history)
	return out
}

// Paused reports whether the scheduler is currently paused via config.
func (s *Scheduler) Paused() bool {
	return !s.cfg.SchedulerEnabled
}

func (s *Scheduler) recordChecked(updated bool, errored bool) {
	s.currentMu.Lock()
	defer s.currentMu.Unlock()
	if s.current == nil {
		return
	}
	s.current.Checked++
	if updated {
		s.current.Updated++
	}
	if errored {
		s.current.Errors++
	}
}

func (s *Scheduler) worker(ctx context.Context, id int) {
	defer s.wg.Done()
	log := s.log.With().Int("worker", id).Logger()
	for t := range s.jobs {
		s.runCheck(ctx, log, t)
	}
}

// recordResult is a tiny wrapper around RecordCheckResult that logs the
// (rare) persistence failure rather than discarding it. Persistence
// errors are non-fatal here — the next tick re-evaluates the topic.
func (s *Scheduler) recordResult(ctx context.Context, log zerolog.Logger, id uuid.UUID, hash string, updated bool, nextCheckAt time.Time, errMsg string) {
	if err := s.topics.RecordCheckResult(ctx, id, hash, updated, nextCheckAt, errMsg); err != nil {
		log.Warn().Err(err).Msg("RecordCheckResult failed")
	}
}

// runCheck is the per-topic orchestrator. It loads credentials, runs
// the tracker's Check, and — if the hash changed — hands off to
// downloadAllPending which drains every queued episode in one tick.
func (s *Scheduler) runCheck(ctx context.Context, log zerolog.Logger, t *domain.Topic) {
	log = log.With().
		Str("topic_id", t.ID.String()).
		Str("tracker", t.TrackerName).
		Str("url", t.URL).
		Logger()

	start := time.Now()
	defer func() {
		metrics.SchedulerTopicCheckDurationSeconds.
			WithLabelValues(t.TrackerName).
			Observe(time.Since(start).Seconds())
	}()

	tr := s.lookupTracker(t.TrackerName)
	if tr == nil {
		log.Error().Msg("no registered tracker")
		metrics.SchedulerTopicChecksTotal.WithLabelValues(t.TrackerName, "no_plugin").Inc()
		s.recordResult(ctx, log, t.ID, "", false, s.backoff(t, true, nil), "tracker plugin not installed")
		s.notifyError(ctx, t, "tracker plugin not installed")
		s.recordChecked(false, true)
		return
	}

	// Emit check.started once the tracker plugin is confirmed present.
	if s.emit != nil {
		s.emit.Emit(ctx, events.Event{UserID: t.UserID, TopicID: &t.ID, Type: events.CheckStarted})
	}

	// checkCtx covers credential decryption, login, and the initial
	// Check call. The per-episode Download loop allocates its own
	// per-iteration context with the same TrackerHTTPTimeout so each
	// download has its own clock.
	checkCtx, cancel := context.WithTimeout(ctx, s.cfg.TrackerHTTPTimeout+5*time.Second)
	defer cancel()

	creds, ok := s.loadCredentials(ctx, checkCtx, log, t, tr)
	if !ok {
		// loadCredentials already recorded the result + metric.
		return
	}

	check, err := tr.Check(checkCtx, t, creds)
	if err != nil {
		log.Warn().Err(err).Msg("check failed")
		metrics.SchedulerTopicChecksTotal.WithLabelValues(t.TrackerName, "error").Inc()
		s.recordResult(ctx, log, t.ID, "", false, s.backoff(t, true, err), err.Error())
		s.notifyError(ctx, t, err.Error())
		s.recordChecked(false, true)
		return
	}

	updated := check.Hash != "" && check.Hash != t.LastHash
	var anySubmitted bool
	var delivered []string
	if updated {
		log.Info().Str("old_hash", t.LastHash).Str("new_hash", check.Hash).Msg("topic updated")
		metrics.TrackerUpdatesTotal.WithLabelValues(t.TrackerName).Inc()

		// Emit release.found once per new hash, before draining episodes.
		if s.emit != nil {
			s.emit.Emit(ctx, events.Event{
				UserID: t.UserID, TopicID: &t.ID, NotifierID: t.NotifierID,
				Type: events.ReleaseFound, Severity: "info",
				Title: t.DisplayName, Body: "New release detected",
				Link: s.cfg.PublicBaseURL + "/topics",
			})
		}

		var dlErr error
		delivered, dlErr = s.downloadAllPending(ctx, log, t, tr, check, creds)
		anySubmitted = len(delivered) > 0
		if dlErr != nil {
			// A failed download loop must NOT advance the persisted hash.
			// If it did, the next check would see check.Hash == LastHash,
			// treat the topic as unchanged, skip the download forever, and
			// a later no-op check would even clear the error — leaving the
			// topic "active, no error, never updated" while silently never
			// downloading. Persist the OLD hash so the change is re-detected
			// and retried next tick. Any progress made before the failure
			// was already persisted via MarkEpisodeDownloaded and is encoded
			// into the recomputed hash, so keeping the old hash still
			// re-triggers without losing that progress.
			if anySubmitted {
				log.Warn().Err(dlErr).Msg("download loop failed mid-progress")
			} else {
				log.Warn().Err(dlErr).Msg("download failed")
				metrics.SchedulerTopicChecksTotal.WithLabelValues(t.TrackerName, "download_error").Inc()
			}
			s.recordResult(ctx, log, t.ID, t.LastHash, anySubmitted, s.backoff(t, true, dlErr), dlErr.Error())
			s.notifyError(ctx, t, dlErr.Error())
			s.recordChecked(true, true)
			return
		}
	}

	// Self-heal the display name: a tracker's Check often resolves the real
	// title (e.g. RuTracker's <title>) that wasn't available at add time.
	// Persist it when it changed so placeholder names upgrade themselves.
	if check.DisplayName != "" && check.DisplayName != t.DisplayName {
		if p, ok := s.topics.(displayNamePersister); ok {
			if err := p.UpdateDisplayName(ctx, t.ID, check.DisplayName); err != nil {
				log.Warn().Err(err).Msg("UpdateDisplayName failed")
			}
		}
	}

	// New releases were pushed to a client this tick — notify the user's
	// notifiers subscribed to the "updated" event (best-effort).
	if anySubmitted {
		s.notifyUpdated(ctx, t, delivered)
	}

	metrics.SchedulerTopicChecksTotal.WithLabelValues(t.TrackerName, "ok").Inc()
	nextCheckAt := s.backoff(t, false, nil)
	if s.emit != nil {
		s.emit.Emit(ctx, events.Event{
			UserID: t.UserID, TopicID: &t.ID, Type: events.CheckCompleted,
			Data: map[string]any{"next_check_at": nextCheckAt.UTC().Format(time.RFC3339)},
		})
	}
	s.recordResult(ctx, log, t.ID, check.Hash, updated || anySubmitted, nextCheckAt, "")
	s.recordChecked(updated || anySubmitted, false)
}

// notifyUpdated emits a download.submitted event summarising what was
// delivered this tick. Best-effort: a nil emitter or zero deliveries is a
// no-op.
func (s *Scheduler) notifyUpdated(ctx context.Context, t *domain.Topic, labels []string) {
	if s.emit == nil || len(labels) == 0 {
		return
	}
	const maxList = 10
	shown := labels
	overflow := 0
	if len(shown) > maxList {
		overflow = len(shown) - maxList
		shown = shown[:maxList]
	}
	body := "Sent to client: " + strings.Join(shown, ", ")
	if overflow > 0 {
		body += fmt.Sprintf(" (+%d more)", overflow)
	}
	s.emit.Emit(ctx, events.Event{
		UserID: t.UserID, TopicID: &t.ID, NotifierID: t.NotifierID,
		Type: events.DownloadSubmitted, Severity: "info",
		Title: t.DisplayName, Body: body, Link: s.cfg.PublicBaseURL + "/topics",
	})
}

// notifyError emits a check.failed event when a topic first enters the error
// state (tracker check, download/client, or missing-plugin failure). Deduped
// by the pre-check ConsecutiveErrors snapshot: only the first failure (count
// 0) emits, so a topic retrying on its backoff schedule doesn't spam every
// tick.
func (s *Scheduler) notifyError(ctx context.Context, t *domain.Topic, errMsg string) {
	if s.emit == nil || t.ConsecutiveErrors > 0 {
		return
	}
	s.emit.Emit(ctx, events.Event{
		UserID: t.UserID, TopicID: &t.ID, NotifierID: t.NotifierID,
		Type: events.CheckFailed, Severity: "error",
		Title: "Topic check failed: " + t.DisplayName, Body: errMsg,
		Link: s.cfg.PublicBaseURL + "/topics",
	})
}

// loadCredentials fetches and decrypts the per-user tracker credential
// for trackers that implement WithCredentials, then performs the
// plugin's Login. Returns (nil, true) if the tracker doesn't need
// credentials at all. Returns (_, false) on any failure, having
// already persisted the error result and recorded metrics.
func (s *Scheduler) loadCredentials(ctx context.Context, checkCtx context.Context, log zerolog.Logger, t *domain.Topic, tr registry.Tracker) (*domain.TrackerCredential, bool) {
	wc, isWC := tr.(registry.WithCredentials)
	if !isWC || s.creds == nil {
		return nil, true
	}
	stored, lerr := s.creds.GetForTracker(ctx, t.UserID, t.TrackerName)
	if lerr != nil || stored == nil {
		// No credentials stored — the plugin will run anonymously.
		// Plugins that require auth will fail their own Check() with
		// a clear error.
		return nil, true
	}
	plain, derr := s.master.Decrypt(stored.SecretEnc, stored.SecretNonce)
	if derr != nil {
		log.Warn().Err(derr).Msg("decrypt credential failed")
		return nil, true
	}
	stored.SecretEnc = plain
	// Rehydrate the persisted session cookie (cookie-session plugins read
	// plaintext JSON from SessionEnc, mirroring the SecretEnc convention).
	if len(stored.SessionEnc) > 0 {
		sessPlain, serr := s.master.Decrypt(stored.SessionEnc, stored.SessionNonce)
		if serr != nil {
			log.Warn().Err(serr).Msg("decrypt session failed")
		} else {
			stored.SessionEnc = sessPlain
		}
	}
	if loginErr := wc.Login(checkCtx, stored); loginErr != nil {
		if errors.Is(loginErr, registry.ErrSessionExpired) && stored.SessionExpiredAt == nil {
			// The atomic UPDATE...WHERE session_expired_at IS NULL is the
			// real dedup gate: when many topics share one credential and
			// all see the stale nil snapshot in the same tick, only the
			// check whose UPDATE actually transitioned NULL->now() gets
			// transitioned==true and fires the single notification.
			transitioned, merr := s.creds.MarkSessionExpired(ctx, stored.ID, stored.UserID)
			if merr != nil {
				log.Warn().Err(merr).Msg("mark session expired failed")
			}
			if transitioned && s.emit != nil {
				s.emit.Emit(ctx, events.Event{
					UserID: stored.UserID, TopicID: &t.ID, NotifierID: t.NotifierID,
					Type: events.SessionExpired, Severity: "error",
					Title: "Tracker session expired",
					Body:  t.TrackerName + " needs re-authentication",
					Link:  s.cfg.PublicBaseURL + "/credentials",
				})
			}
		}
		log.Warn().Err(loginErr).Msg("tracker login failed")
		metrics.SchedulerTopicChecksTotal.WithLabelValues(t.TrackerName, "auth_error").Inc()
		s.recordResult(ctx, log, t.ID, "", false, s.backoff(t, true, loginErr), "auth failed: "+loginErr.Error())
		s.recordChecked(false, true)
		return nil, false
	}
	return stored, true
}

// downloadAllPending drains every pending episode for a topic in one
// tick. The loop runs at most cfg.SchedulerMaxEpisodesPerTick times.
//
// Returns (delivered, error) where delivered is the human label of every
// payload successfully handed off to the client this tick (episodic:
// "s05e01"; single-torrent: the topic display name). error is non-nil if
// the loop terminated abnormally. The caller uses len(delivered) > 0 to
// decide whether to record an "updated" timestamp and notify even when an
// error occurred mid-loop.
//
// Each iteration uses its own context derived from ctx with a
// TrackerHTTPTimeout deadline so a slow download cannot starve the
// remaining iterations. Persistence calls (MarkEpisodeDownloaded) use
// the parent ctx so they survive a per-iteration deadline expiry.
func (s *Scheduler) downloadAllPending(ctx context.Context, log zerolog.Logger, t *domain.Topic, tr registry.Tracker, check *domain.Check, creds *domain.TrackerCredential) ([]string, error) {
	maxPerTick := s.cfg.SchedulerMaxEpisodesPerTick
	if maxPerTick <= 0 {
		maxPerTick = 25
	}

	var delivered []string
	var i int
	for i = 0; i < maxPerTick; i++ {
		// The human label for the episode about to be downloaded. The
		// plugin keeps pending_human aligned with pending_episodes (oldest
		// first), so human[0] names pending_episodes[0] — the one Download
		// fetches this iteration. Single-payload trackers have no pending
		// list; their label falls back to the topic display name.
		human := extra.StringSlice(check.Extra, "pending_human")
		label := ""
		if len(human) > 0 {
			label = human[0]
		}

		// Per-iteration ctx so each download has its own clock.
		iterCtx, cancel := context.WithTimeout(ctx, s.cfg.TrackerHTTPTimeout)
		payload, derr := tr.Download(iterCtx, t, check, creds)
		cancel()

		if derr != nil {
			if isNoPendingError(derr) {
				// ErrNoPendingEpisodes is the plugin's graceful "nothing
				// (more) to download" signal — valid even on the first
				// iteration. A hash change that yields zero pending (the
				// user caught up, or the start filter excludes every
				// episode) is a legitimate no-op, not a failure; returning
				// nil lets runCheck advance the hash to the now-current
				// state instead of erroring and stranding the topic.
				return delivered, nil
			}
			return delivered, derr
		}

		if err := s.submitToClient(ctx, log, t, payload, label); err != nil {
			metrics.SchedulerTopicChecksTotal.WithLabelValues(t.TrackerName, "submit_error").Inc()
			return delivered, fmt.Errorf("submit: %w", err)
		}
		// Record the human label of what was just delivered. Episodic
		// trackers supply it via pending_human; single-torrent trackers fall
		// back to the topic display name (same label recordDelivery uses).
		if label != "" {
			delivered = append(delivered, label)
		} else {
			delivered = append(delivered, t.DisplayName)
		}

		// Mark this episode downloaded. Use the parent ctx (not the
		// per-iteration one) so persistence survives even if the
		// download timeout fires moments later.
		pending := extra.StringSlice(check.Extra, "pending_episodes")
		if len(pending) == 0 {
			// Single-payload plugin (most trackers) — done.
			return delivered, nil
		}
		if err := s.markDownloaded(ctx, t, pending[0]); err != nil {
			return delivered, fmt.Errorf("persist downloaded: %w", err)
		}
		log.Info().Str("packed", pending[0]).Msg("marked episode downloaded")

		// Derive remaining locally; no second tr.Check call needed.
		if len(pending) <= 1 {
			return delivered, nil
		}
		check.Extra["pending_episodes"] = pending[1:]
		// Keep the human labels aligned with the packed list as we consume.
		if len(human) > 1 {
			check.Extra["pending_human"] = human[1:]
		} else {
			check.Extra["pending_human"] = []string{}
		}
	}

	if i >= maxPerTick {
		log.Warn().Int("max_per_tick", maxPerTick).Msg("scheduler hit per-tick episode cap")
		metrics.SchedulerEpisodesPerTickCappedTotal.WithLabelValues(t.TrackerName).Inc()
	}
	return delivered, nil
}

// markDownloaded persists the fact that one packed episode id was
// successfully handed to a torrent client. If the underlying topics
// repo implements the atomic MarkEpisodeDownloaded method (Track C),
// it is used; otherwise this falls back to a read-modify-write through
// UpdateExtra. The fallback path mutates t.Extra in place.
func (s *Scheduler) markDownloaded(ctx context.Context, t *domain.Topic, packed string) error {
	if med, ok := s.topics.(markEpisodeDownloader); ok {
		return med.MarkEpisodeDownloaded(ctx, t.ID, packed)
	}
	// TODO Track C: drop this fallback once *repo.Topics.MarkEpisodeDownloaded ships.
	if t.Extra == nil {
		t.Extra = map[string]any{}
	}
	already := extra.StringSlice(t.Extra, "downloaded_episodes")
	already = append(already, packed)
	t.Extra["downloaded_episodes"] = already
	return s.topics.UpdateExtra(ctx, t.ID, t.Extra)
}

// isNoPendingError reports whether err signals that a per-episode
// tracker has nothing left to download this tick. Per-episode plugins
// (currently only LostFilm) wrap registry.ErrNoPendingEpisodes via
// fmt.Errorf("...: %w", ...) so errors.Is matches at any depth.
func isNoPendingError(err error) bool {
	return errors.Is(err, registry.ErrNoPendingEpisodes)
}

func (s *Scheduler) submitToClient(ctx context.Context, log zerolog.Logger, t *domain.Topic, payload *domain.Payload, label string) error {
	if t.ClientID == nil {
		// No explicit client — fall back to the user's default client,
		// if any.
		def, err := s.clients.GetDefault(ctx, t.UserID)
		if err != nil {
			return errors.New("no client configured for this topic and no default client")
		}
		return s.sendViaClient(ctx, log, def, t, payload, label)
	}
	cfg, err := s.clients.GetByID(ctx, *t.ClientID, t.UserID)
	if err != nil {
		return fmt.Errorf("load client: %w", err)
	}
	return s.sendViaClient(ctx, log, cfg, t, payload, label)
}

func (s *Scheduler) sendViaClient(ctx context.Context, log zerolog.Logger, cfg *domain.Client, t *domain.Topic, payload *domain.Payload, label string) error {
	clientPlugin := s.lookupClient(cfg.ClientName)
	if clientPlugin == nil {
		metrics.ClientSubmitTotal.WithLabelValues(cfg.ClientName, "no_plugin").Inc()
		return fmt.Errorf("client plugin %q not installed", cfg.ClientName)
	}
	rawConfig, err := s.master.Decrypt(cfg.ConfigEnc, cfg.ConfigNonce)
	if err != nil {
		metrics.ClientSubmitTotal.WithLabelValues(cfg.ClientName, "decrypt_error").Inc()
		return fmt.Errorf("decrypt client config: %w", err)
	}
	if err := clientPlugin.Add(ctx, rawConfig, payload, domain.AddOptions{
		DownloadDir: t.DownloadDir,
		Category:    t.Category,
	}); err != nil {
		metrics.ClientSubmitTotal.WithLabelValues(cfg.ClientName, "error").Inc()
		return err
	}
	metrics.ClientSubmitTotal.WithLabelValues(cfg.ClientName, "ok").Inc()
	s.recordDelivery(ctx, log, t, cfg, payload, label)
	return nil
}

// recordDelivery logs what was just pushed to a client into
// topic_deliveries so the UI can show delivered items and (where the
// client supports it) live download status. The BitTorrent infohash of the
// payload is the key that links this record to the client's own torrent
// list. This is best-effort Tier-1 tracking: any failure (no recorder
// wired, an undecodable payload, a DB error) is logged and swallowed — it
// must never turn a successful download into a failed check.
func (s *Scheduler) recordDelivery(ctx context.Context, log zerolog.Logger, t *domain.Topic, cfg *domain.Client, payload *domain.Payload, label string) {
	if s.deliveries == nil {
		return
	}
	hash, err := infohash.FromPayload(payload.MagnetURI, payload.TorrentFile)
	if err != nil {
		log.Debug().Err(err).Msg("could not derive infohash; skipping delivery record")
		return
	}
	if label == "" {
		label = t.DisplayName
	}
	clientID := cfg.ID
	if _, err := s.deliveries.Record(ctx, &domain.TopicDelivery{
		TopicID:  t.ID,
		Infohash: hash,
		Label:    label,
		ClientID: &clientID,
	}); err != nil {
		log.Warn().Err(err).Msg("record delivery failed")
	}
}

// transientRetryDelay is how soon a topic is re-checked after a transient
// network failure (timeout, DNS blip, Cloudflare challenge hang). Short, so a
// momentary glitch self-heals in about a minute instead of parking the topic
// on the long exponential backoff meant for durable errors.
const transientRetryDelay = 60 * time.Second

// transientRetryMax bounds how many consecutive transient failures keep the
// fast-retry behaviour before falling back to exponential backoff, so a tracker
// that is genuinely down isn't hammered once a minute forever.
const transientRetryMax = 5

// backoff computes the next_check_at timestamp. On success we use the topic's
// configured interval. On a *transient* failure (network blip) we retry quickly
// so it auto-recovers; on a durable failure we exponentially back off to the cap.
func (s *Scheduler) backoff(t *domain.Topic, failure bool, cause error) time.Time {
	if !failure {
		return time.Now().UTC().Add(time.Duration(t.CheckIntervalSec) * time.Second)
	}
	if isTransientError(cause) && t.ConsecutiveErrors < transientRetryMax {
		return time.Now().UTC().Add(transientRetryDelay)
	}
	attempt := t.ConsecutiveErrors + 1
	base := time.Duration(t.CheckIntervalSec) * time.Second
	mult := math.Pow(2, float64(attempt))
	d := time.Duration(float64(base) * mult)
	if d > s.cfg.CheckMaxBackoff {
		d = s.cfg.CheckMaxBackoff
	}
	return time.Now().UTC().Add(d)
}

// isTransientError reports whether err is a transient network/infra failure —
// a timeout, DNS blip, refused/reset connection, or a Cloudflare challenge that
// hung the request — as opposed to a durable error (bad credentials, an
// unparseable page, a missing plugin). Transient failures clear on their own,
// so the scheduler retries them quickly instead of backing off.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"context deadline exceeded",
		"timeout",
		"i/o timeout",
		"connection refused",
		"connection reset",
		"server misbehaving", // docker embedded-DNS blip
		"no such host",
		"temporary failure",
		"eof",
		"tls handshake",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
