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
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/config"
	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/metrics"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

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
	topics  *repo.Topics
	clients *repo.Clients
	creds   *repo.TrackerCredentials
	master  *crypto.MasterKey

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
		cfg:     cfg,
		log:     log.With().Str("component", "scheduler").Logger(),
		topics:  topics,
		clients: clients,
		creds:   creds,
		master:  master,
		jobs:    make(chan *domain.Topic, cfg.SchedulerWorkers*4),
		stop:    make(chan struct{}),
		ready:   make(chan struct{}),
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

	tr := registry.GetTracker(t.TrackerName)
	if tr == nil {
		log.Error().Msg("no registered tracker")
		metrics.SchedulerTopicChecksTotal.WithLabelValues(t.TrackerName, "no_plugin").Inc()
		_ = s.topics.RecordCheckResult(ctx, t.ID, "", false, s.backoff(t, true), "tracker plugin not installed")
		s.recordChecked(false, true)
		return
	}

	checkCtx, cancel := context.WithTimeout(ctx, s.cfg.TrackerHTTPTimeout+5*time.Second)
	defer cancel()

	// Look up the per-user credential for this tracker (if any) and
	// perform Login first. The credential's SecretEnc field carries
	// the plaintext password in-memory after decryption — that's the
	// contract WithCredentials plugins expect.
	var creds *domain.TrackerCredential
	if wc, ok := tr.(registry.WithCredentials); ok && s.creds != nil {
		stored, lerr := s.creds.GetForTracker(ctx, t.UserID, t.TrackerName)
		if lerr == nil && stored != nil {
			plain, derr := s.master.Decrypt(stored.SecretEnc, stored.SecretNonce)
			if derr != nil {
				log.Warn().Err(derr).Msg("decrypt credential failed")
			} else {
				stored.SecretEnc = plain
				if loginErr := wc.Login(checkCtx, stored); loginErr != nil {
					log.Warn().Err(loginErr).Msg("tracker login failed")
					metrics.SchedulerTopicChecksTotal.WithLabelValues(t.TrackerName, "auth_error").Inc()
					_ = s.topics.RecordCheckResult(ctx, t.ID, "", false, s.backoff(t, true), "auth failed: "+loginErr.Error())
					s.recordChecked(false, true)
					return
				}
				creds = stored
			}
		}
	}

	check, err := tr.Check(checkCtx, t, creds)
	if err != nil {
		log.Warn().Err(err).Msg("check failed")
		metrics.SchedulerTopicChecksTotal.WithLabelValues(t.TrackerName, "error").Inc()
		_ = s.topics.RecordCheckResult(ctx, t.ID, "", false, s.backoff(t, true), err.Error())
		s.recordChecked(false, true)
		return
	}

	updated := check.Hash != "" && check.Hash != t.LastHash
	anySubmitted := false
	if updated {
		log.Info().Str("old_hash", t.LastHash).Str("new_hash", check.Hash).Msg("topic updated")
		metrics.TrackerUpdatesTotal.WithLabelValues(t.TrackerName).Inc()

		// Per-episode loop: keep calling Download until pending is
		// empty OR a download fails. Plugins that don't track per-
		// episode state (most of them) will simply return one
		// payload and Download() will then return "no pending
		// episodes" on the second call, breaking the loop cleanly.
		const maxPerTick = 25 // safety guard against runaway loops
		for i := 0; i < maxPerTick; i++ {
			payload, derr := tr.Download(checkCtx, t, check, creds)
			if derr != nil {
				if i > 0 && isNoPendingError(derr) {
					// Loop terminated naturally — at least one
					// episode was downloaded this tick.
					break
				}
				if i == 0 {
					log.Warn().Err(derr).Msg("download failed")
					metrics.SchedulerTopicChecksTotal.WithLabelValues(t.TrackerName, "download_error").Inc()
					_ = s.topics.RecordCheckResult(ctx, t.ID, check.Hash, false, s.backoff(t, true), derr.Error())
					s.recordChecked(true, true)
					return
				}
				// Mid-loop failure — surface it but keep the
				// progress made so far.
				log.Warn().Err(derr).Int("submitted_so_far", i).Msg("download failed mid-loop")
				_ = s.topics.RecordCheckResult(ctx, t.ID, check.Hash, true, s.backoff(t, true), derr.Error())
				s.recordChecked(true, true)
				return
			}

			if err := s.submitToClient(ctx, log, t, payload); err != nil {
				log.Warn().Err(err).Msg("submit to client failed")
				metrics.SchedulerTopicChecksTotal.WithLabelValues(t.TrackerName, "submit_error").Inc()
				_ = s.topics.RecordCheckResult(ctx, t.ID, check.Hash, false, s.backoff(t, true), err.Error())
				s.recordChecked(true, true)
				return
			}
			anySubmitted = true

			// Mark this episode as downloaded in topic.extra and
			// persist. Then re-run Check on the (now-updated) topic
			// state so the next loop iteration sees the shrunken
			// pending list.
			if !s.markEpisodeDownloaded(ctx, t, check, log) {
				break
			}
			fresh, ferr := tr.Check(checkCtx, t, creds)
			if ferr != nil || fresh == nil {
				break
			}
			check = fresh
			if len(extraStringSliceLocal(check.Extra, "pending_episodes")) == 0 {
				break
			}
		}
	}

	metrics.SchedulerTopicChecksTotal.WithLabelValues(t.TrackerName, "ok").Inc()
	_ = s.topics.RecordCheckResult(ctx, t.ID, check.Hash, updated || anySubmitted, s.backoff(t, false), "")
	s.recordChecked(updated, false)
}

// markEpisodeDownloaded pops the first pending episode off check.Extra,
// appends it to topic.Extra["downloaded_episodes"], and persists. Returns
// false if there's nothing to mark or persistence fails (caller should
// stop looping).
func (s *Scheduler) markEpisodeDownloaded(ctx context.Context, t *domain.Topic, check *domain.Check, log zerolog.Logger) bool {
	if check == nil || check.Extra == nil {
		return false
	}
	pending := extraStringSliceLocal(check.Extra, "pending_episodes")
	if len(pending) == 0 {
		return false
	}
	just := pending[0]
	if t.Extra == nil {
		t.Extra = map[string]any{}
	}
	already := extraStringSliceLocal(t.Extra, "downloaded_episodes")
	already = append(already, just)
	t.Extra["downloaded_episodes"] = already
	if err := s.topics.UpdateExtra(ctx, t.ID, t.Extra); err != nil {
		log.Warn().Err(err).Str("packed", just).Msg("failed to persist downloaded_episodes")
		return false
	}
	log.Info().Str("packed", just).Int("done", len(already)).Msg("marked episode downloaded")
	return true
}

// extraStringSliceLocal handles JSON-roundtripped string slices that
// arrive as []any.
func extraStringSliceLocal(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// isNoPendingError matches the error returned by LostFilm Download
// when the pending list is empty.
func isNoPendingError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "no pending episodes")
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
	clientPlugin := registry.GetClient(cfg.ClientName)
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
