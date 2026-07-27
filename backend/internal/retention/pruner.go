// Package retention runs a background pruner that trims the append-only
// topic_events history to a bounded age. Nothing else in the codebase deletes
// from that table, so without this the history — and the per-topic timeline
// built on it — grows for the life of the install.
package retention

import (
	"context"
	"time"

	"github.com/rs/zerolog"
)

// Store is the consumer-side seam over *repo.TopicEvents.
type Store interface {
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// Config holds the pruner's runtime knobs. A zero MaxAge disables pruning
// entirely (history is kept forever).
type Config struct {
	MaxAge   time.Duration
	Interval time.Duration
}

// Pruner periodically deletes history rows older than Config.MaxAge.
type Pruner struct {
	store Store
	cfg   Config
	log   zerolog.Logger

	// now is a clock seam so tests can pin the cutoff.
	now func() time.Time
	// done closes when the loop exits, letting tests await shutdown.
	done chan struct{}
}

// New constructs a Pruner.
func New(store Store, cfg Config, log zerolog.Logger) *Pruner {
	return &Pruner{
		store: store,
		cfg:   cfg,
		log:   log.With().Str("component", "retention").Logger(),
		now:   time.Now,
		done:  make(chan struct{}),
	}
}

// Start launches the prune loop in a goroutine and returns. The loop stops
// when ctx is cancelled.
func (p *Pruner) Start(ctx context.Context) error {
	go p.run(ctx)
	return nil
}

func (p *Pruner) run(ctx context.Context) {
	defer close(p.done)
	ticker := time.NewTicker(p.cfg.Interval)
	defer ticker.Stop()
	// Prune once at boot so a long-running install that was restarted doesn't
	// wait a full interval before reclaiming.
	p.pruneOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pruneOnce(ctx)
		}
	}
}

// pruneOnce deletes one batch. Housekeeping is best-effort: a DB error is
// logged and swallowed so the loop survives to retry on the next tick.
func (p *Pruner) pruneOnce(ctx context.Context) {
	if p.cfg.MaxAge <= 0 {
		return
	}
	cutoff := p.now().Add(-p.cfg.MaxAge)
	deleted, err := p.store.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		p.log.Warn().Err(err).Time("cutoff", cutoff).Msg("topic_events prune failed")
		return
	}
	if deleted > 0 {
		p.log.Info().Int64("deleted", deleted).Time("cutoff", cutoff).Msg("pruned topic_events history")
	}
}
