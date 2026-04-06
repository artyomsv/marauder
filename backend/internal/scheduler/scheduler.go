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
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/config"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// Scheduler is the running scheduler service.
type Scheduler struct {
	cfg     *config.Config
	log     zerolog.Logger
	topics  *repo.Topics
	clients *repo.Clients
	// Future: credentials repo, events repo, notifications dispatcher.

	jobs  chan *domain.Topic
	wg    sync.WaitGroup
	stop  chan struct{}
	ready chan struct{}
}

// New constructs a scheduler.
func New(cfg *config.Config, log zerolog.Logger, topics *repo.Topics, clients *repo.Clients) *Scheduler {
	return &Scheduler{
		cfg:     cfg,
		log:     log.With().Str("component", "scheduler").Logger(),
		topics:  topics,
		clients: clients,
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
		return
	}
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

	tr := registry.GetTracker(t.TrackerName)
	if tr == nil {
		log.Error().Msg("no registered tracker")
		_ = s.topics.RecordCheckResult(ctx, t.ID, "", false, s.backoff(t, true), "tracker plugin not installed")
		return
	}

	checkCtx, cancel := context.WithTimeout(ctx, s.cfg.TrackerHTTPTimeout+5*time.Second)
	defer cancel()

	check, err := tr.Check(checkCtx, t, nil)
	if err != nil {
		log.Warn().Err(err).Msg("check failed")
		_ = s.topics.RecordCheckResult(ctx, t.ID, "", false, s.backoff(t, true), err.Error())
		return
	}

	updated := check.Hash != "" && check.Hash != t.LastHash
	if updated {
		log.Info().Str("old_hash", t.LastHash).Str("new_hash", check.Hash).Msg("topic updated")
		payload, derr := tr.Download(checkCtx, t, check, nil)
		if derr != nil {
			log.Warn().Err(derr).Msg("download failed")
			_ = s.topics.RecordCheckResult(ctx, t.ID, check.Hash, false, s.backoff(t, true), derr.Error())
			return
		}

		if err := s.submitToClient(ctx, log, t, payload); err != nil {
			log.Warn().Err(err).Msg("submit to client failed")
			_ = s.topics.RecordCheckResult(ctx, t.ID, check.Hash, false, s.backoff(t, true), err.Error())
			return
		}
	}

	_ = s.topics.RecordCheckResult(ctx, t.ID, check.Hash, updated, s.backoff(t, false), "")
}

func (s *Scheduler) submitToClient(ctx context.Context, log zerolog.Logger, t *domain.Topic, payload *domain.Payload) error {
	if t.ClientID == nil {
		return errors.New("no client configured for this topic")
	}
	cfg, err := s.clients.GetByID(ctx, *t.ClientID, t.UserID)
	if err != nil {
		return fmt.Errorf("load client: %w", err)
	}
	clientPlugin := registry.GetClient(cfg.ClientName)
	if clientPlugin == nil {
		return fmt.Errorf("client plugin %q not installed", cfg.ClientName)
	}
	// For v0.1 we store config as JSON in ConfigEnc (encrypted); however
	// in this scaffold we pass the raw bytes through unchanged because
	// the encryption/decryption is wired elsewhere. The plugin unmarshals
	// JSON from its schema.
	rawConfig := cfg.ConfigEnc
	return clientPlugin.Add(ctx, rawConfig, payload, domain.AddOptions{
		DownloadDir: t.DownloadDir,
	})
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

// marshalExtra is kept for future "event details" use.
func marshalExtra(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
