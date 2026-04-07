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
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/config"
	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/extra"
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

// clientsRepo is the subset of *repo.Clients that the scheduler uses.
type clientsRepo interface {
	GetByID(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*domain.Client, error)
	GetDefault(ctx context.Context, userID uuid.UUID) (*domain.Client, error)
}

// credentialsRepo is the subset of *repo.TrackerCredentials that the
// scheduler uses.
type credentialsRepo interface {
	GetForTracker(ctx context.Context, userID uuid.UUID, trackerName string) (*domain.TrackerCredential, error)
}

// decryptor is the subset of *crypto.MasterKey that the scheduler uses.
type decryptor interface {
	Decrypt(ct, nonce []byte) ([]byte, error)
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
	cfg     *config.Config
	log     zerolog.Logger
	topics  topicsRepo
	clients clientsRepo
	creds   credentialsRepo
	master  decryptor

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
func New(cfg *config.Config, log zerolog.Logger, topics *repo.Topics, clients *repo.Clients, creds *repo.TrackerCredentials, master *crypto.MasterKey) *Scheduler {
	return &Scheduler{
		cfg:           cfg,
		log:           log.With().Str("component", "scheduler").Logger(),
		topics:        topics,
		clients:       clients,
		creds:         creds,
		master:        master,
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
		s.recordResult(ctx, log, t.ID, "", false, s.backoff(t, true), "tracker plugin not installed")
		s.recordChecked(false, true)
		return
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
		s.recordResult(ctx, log, t.ID, "", false, s.backoff(t, true), err.Error())
		s.recordChecked(false, true)
		return
	}

	updated := check.Hash != "" && check.Hash != t.LastHash
	var anySubmitted bool
	if updated {
		log.Info().Str("old_hash", t.LastHash).Str("new_hash", check.Hash).Msg("topic updated")
		metrics.TrackerUpdatesTotal.WithLabelValues(t.TrackerName).Inc()

		var dlErr error
		anySubmitted, dlErr = s.downloadAllPending(ctx, log, t, tr, check, creds)
		if dlErr != nil {
			// Mid-loop and first-iteration failures both arrive here.
			// anySubmitted distinguishes "we made progress" from "we
			// got nothing", which controls whether RecordCheckResult
			// persists the new hash + last_updated_at timestamp.
			if anySubmitted {
				log.Warn().Err(dlErr).Msg("download loop failed mid-progress")
			} else {
				log.Warn().Err(dlErr).Msg("download failed")
				metrics.SchedulerTopicChecksTotal.WithLabelValues(t.TrackerName, "download_error").Inc()
			}
			s.recordResult(ctx, log, t.ID, check.Hash, anySubmitted, s.backoff(t, true), dlErr.Error())
			s.recordChecked(true, true)
			return
		}
	}

	metrics.SchedulerTopicChecksTotal.WithLabelValues(t.TrackerName, "ok").Inc()
	s.recordResult(ctx, log, t.ID, check.Hash, updated || anySubmitted, s.backoff(t, false), "")
	s.recordChecked(updated || anySubmitted, false)
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
	if loginErr := wc.Login(checkCtx, stored); loginErr != nil {
		log.Warn().Err(loginErr).Msg("tracker login failed")
		metrics.SchedulerTopicChecksTotal.WithLabelValues(t.TrackerName, "auth_error").Inc()
		s.recordResult(ctx, log, t.ID, "", false, s.backoff(t, true), "auth failed: "+loginErr.Error())
		s.recordChecked(false, true)
		return nil, false
	}
	return stored, true
}

// downloadAllPending drains every pending episode for a topic in one
// tick. The loop runs at most cfg.SchedulerMaxEpisodesPerTick times.
//
// Returns (anySubmitted, error). error is non-nil if the loop
// terminated abnormally; anySubmitted reports whether at least one
// payload was successfully handed off to the client. The caller uses
// the pair to decide whether to record an "updated" timestamp even
// when an error occurred mid-loop.
//
// Each iteration uses its own context derived from ctx with a
// TrackerHTTPTimeout deadline so a slow download cannot starve the
// remaining iterations. Persistence calls (MarkEpisodeDownloaded) use
// the parent ctx so they survive a per-iteration deadline expiry.
func (s *Scheduler) downloadAllPending(ctx context.Context, log zerolog.Logger, t *domain.Topic, tr registry.Tracker, check *domain.Check, creds *domain.TrackerCredential) (bool, error) {
	maxPerTick := s.cfg.SchedulerMaxEpisodesPerTick
	if maxPerTick <= 0 {
		maxPerTick = 25
	}

	var anySubmitted bool
	var i int
	for i = 0; i < maxPerTick; i++ {
		// Per-iteration ctx so each download has its own clock.
		iterCtx, cancel := context.WithTimeout(ctx, s.cfg.TrackerHTTPTimeout)
		payload, derr := tr.Download(iterCtx, t, check, creds)
		cancel()

		if derr != nil {
			if i > 0 && isNoPendingError(derr) {
				// Graceful loop end — natural exit signal from the plugin.
				return anySubmitted, nil
			}
			return anySubmitted, derr
		}

		if err := s.submitToClient(ctx, log, t, payload); err != nil {
			metrics.SchedulerTopicChecksTotal.WithLabelValues(t.TrackerName, "submit_error").Inc()
			return anySubmitted, fmt.Errorf("submit: %w", err)
		}
		anySubmitted = true

		// Mark this episode downloaded. Use the parent ctx (not the
		// per-iteration one) so persistence survives even if the
		// download timeout fires moments later.
		pending := extra.StringSlice(check.Extra, "pending_episodes")
		if len(pending) == 0 {
			// Single-payload plugin (most trackers) — done.
			return anySubmitted, nil
		}
		if err := s.markDownloaded(ctx, t, pending[0]); err != nil {
			return anySubmitted, fmt.Errorf("persist downloaded: %w", err)
		}
		log.Info().Str("packed", pending[0]).Msg("marked episode downloaded")

		// Derive remaining locally; no second tr.Check call needed.
		if len(pending) <= 1 {
			return anySubmitted, nil
		}
		check.Extra["pending_episodes"] = pending[1:]
	}

	if i >= maxPerTick {
		log.Warn().Int("max_per_tick", maxPerTick).Msg("scheduler hit per-tick episode cap")
		metrics.SchedulerEpisodesPerTickCappedTotal.WithLabelValues(t.TrackerName).Inc()
	}
	return anySubmitted, nil
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

func (s *Scheduler) submitToClient(ctx context.Context, log zerolog.Logger, t *domain.Topic, payload *domain.Payload) error {
	_ = log
	if t.ClientID == nil {
		// No explicit client — fall back to the user's default client,
		// if any.
		def, err := s.clients.GetDefault(ctx, t.UserID)
		if err != nil {
			return errors.New("no client configured for this topic and no default client")
		}
		return s.sendViaClient(ctx, def, t, payload)
	}
	cfg, err := s.clients.GetByID(ctx, *t.ClientID, t.UserID)
	if err != nil {
		return fmt.Errorf("load client: %w", err)
	}
	return s.sendViaClient(ctx, cfg, t, payload)
}

func (s *Scheduler) sendViaClient(ctx context.Context, cfg *domain.Client, t *domain.Topic, payload *domain.Payload) error {
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
	}); err != nil {
		metrics.ClientSubmitTotal.WithLabelValues(cfg.ClientName, "error").Inc()
		return err
	}
	metrics.ClientSubmitTotal.WithLabelValues(cfg.ClientName, "ok").Inc()
	return nil
}

// backoff computes the next_check_at timestamp. On success we use the topic's
// configured interval. On failure we exponentially back off up to the cap.
func (s *Scheduler) backoff(t *domain.Topic, failure bool) time.Time {
	if !failure {
		return time.Now().UTC().Add(time.Duration(t.CheckIntervalSec) * time.Second)
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
