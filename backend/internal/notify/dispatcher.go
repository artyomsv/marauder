// Package notify dispatches domain.Message notifications to a user's
// configured notifier plugins. It is the single event->notifier fan-out
// point (first consumer: scheduler session-expiry alerts).
package notify

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/metrics"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// notifiersRepo is the consumer-side seam over *repo.Notifiers.
type notifiersRepo interface {
	ListForUser(ctx context.Context, userID uuid.UUID) ([]*domain.Notifier, error)
}

// Dispatcher fans a domain.Message out to a user's configured notifiers.
type Dispatcher struct {
	notifiers notifiersRepo
	master    *crypto.MasterKey
	log       zerolog.Logger
	timeout   time.Duration
}

// New creates a Dispatcher. The default per-send timeout is 15 seconds.
func New(notifiers notifiersRepo, master *crypto.MasterKey, log zerolog.Logger) *Dispatcher {
	return &Dispatcher{notifiers: notifiers, master: master, log: log, timeout: 15 * time.Second}
}

// Send delivers msg to every notifier configured by userID that is
// subscribed to the given event. Best-effort; returns the count of successes.
func (d *Dispatcher) Send(ctx context.Context, userID uuid.UUID, event string, msg domain.Message) int {
	list, err := d.notifiers.ListForUser(ctx, userID)
	if err != nil {
		d.log.Warn().Err(err).Str("user_id", userID.String()).Msg("notify: list notifiers failed")
		return 0
	}
	sent := 0
	for _, n := range list {
		if d.sendOne(ctx, n, event, msg) {
			sent++
		}
	}
	return sent
}

// SendVia delivers msg through a single notifier (the one whose ID matches
// notifierID) when notifierID is non-nil. When notifierID is nil it fans out
// to the user's DEFAULT notifiers only (subscription-filtered) — a topic with
// no per-topic override routes to the defaults; if the user has marked no
// defaults, nothing is sent (strict). Returns the count of successes.
func (d *Dispatcher) SendVia(ctx context.Context, userID uuid.UUID, notifierID *uuid.UUID, event string, msg domain.Message) int {
	list, err := d.notifiers.ListForUser(ctx, userID)
	if err != nil {
		d.log.Warn().Err(err).Str("user_id", userID.String()).Msg("notify: list notifiers failed")
		return 0
	}
	if notifierID == nil {
		sent := 0
		for _, n := range list {
			if n.IsDefault && d.sendOne(ctx, n, event, msg) {
				sent++
			}
		}
		return sent
	}
	for _, n := range list {
		if n.ID != *notifierID {
			continue
		}
		if d.sendOne(ctx, n, event, msg) {
			return 1
		}
		return 0
	}
	d.log.Warn().Str("notifier_id", notifierID.String()).Msg("notify: per-topic notifier not found")
	return 0
}

// sendOne attempts delivery through a single notifier, applying event
// subscription filtering, config decryption, and per-send timeout. Returns
// true on a successful send. Every failure path is logged and metered.
func (d *Dispatcher) sendOne(ctx context.Context, n *domain.Notifier, event string, msg domain.Message) bool {
	if !subscribed(n.Events, event) {
		return false
	}
	plugin := registry.GetNotifier(n.NotifierName)
	if plugin == nil {
		d.log.Warn().Str("notifier", n.NotifierName).Msg("notify: unknown notifier plugin")
		metrics.NotificationsSentTotal.WithLabelValues(n.NotifierName, "error").Inc()
		return false
	}
	raw, derr := d.master.Decrypt(n.ConfigEnc, n.ConfigNonce)
	if derr != nil {
		d.log.Warn().Err(derr).Str("notifier", n.NotifierName).Msg("notify: decrypt config failed")
		metrics.NotificationsSentTotal.WithLabelValues(n.NotifierName, "error").Inc()
		return false
	}
	sctx, cancel := context.WithTimeout(ctx, d.timeout)
	serr := plugin.Send(sctx, raw, msg)
	cancel()
	if serr != nil {
		d.log.Warn().Err(serr).Str("notifier", n.NotifierName).Msg("notify: send failed")
		metrics.NotificationsSentTotal.WithLabelValues(n.NotifierName, "error").Inc()
		return false
	}
	metrics.NotificationsSentTotal.WithLabelValues(n.NotifierName, "ok").Inc()
	return true
}

// legacyAliases maps a pre-taxonomy subscription keyword to the canonical
// event types it now covers, so notifiers created before per-event
// subscription (events = ['updated','error']) keep delivering. "updated" is
// intentionally broad — new releases, client submissions, and completions.
var legacyAliases = map[string][]string{
	"updated": {"release.found", "download.submitted", "download.completed"},
	"error":   {"check.failed", "session.expired"},
}

// subscribed reports whether a notifier with the given event subscription
// list should receive an event. An empty list (or empty event) means "all
// events" — a defensive default so a notifier created before event
// filtering, or a caller that doesn't categorise, still delivers.
// A subscription entry matches directly, or via its legacy alias expansion.
func subscribed(events []string, event string) bool {
	if len(events) == 0 || event == "" {
		return true
	}
	for _, e := range events {
		if e == event {
			return true
		}
		for _, alias := range legacyAliases[e] {
			if alias == event {
				return true
			}
		}
	}
	return false
}
