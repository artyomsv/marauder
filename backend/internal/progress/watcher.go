// Package progress runs a background watcher that detects when in-flight
// deliveries finish downloading (via registry.WithStatus) and emits a durable,
// deduped download.completed event. It is the server-side completion detector
// the scheduler deliberately isn't — the scheduler stays a pure monitor.
package progress

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/events"
	"github.com/artyomsv/marauder/backend/internal/metrics"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// statusQueryTimeout bounds each per-client Status round-trip, matching the
// /status endpoint's fail-open budget.
const statusQueryTimeout = 10 * time.Second

// Deliveries is the consumer-side seam over *repo.Deliveries.
type Deliveries interface {
	ListInFlight(ctx context.Context) ([]*domain.InFlightDelivery, error)
	MarkCompleted(ctx context.Context, deliveryID uuid.UUID) (bool, error)
}

// ClientResolver resolves a delivery's client config by id. Satisfied by *repo.Clients.
type ClientResolver interface {
	GetByID(ctx context.Context, id, userID uuid.UUID) (*domain.Client, error)
}

// Decryptor decrypts a client config blob. Satisfied by *crypto.MasterKey.
type Decryptor interface {
	Decrypt(ct, nonce []byte) ([]byte, error)
}

// Emitter publishes a typed event. Satisfied by *events.Bus.
type Emitter interface {
	Emit(ctx context.Context, ev events.Event)
}

// Config holds the watcher's runtime knobs.
type Config struct {
	PollInterval  time.Duration
	PublicBaseURL string
}

// statusLookupFn resolves a client plugin name to its WithStatus capability.
type statusLookupFn func(clientName string) (registry.WithStatus, bool)

// progressKey captures the (percent, state) pair for change detection.
type progressKey struct {
	percent float64
	state   string
}

// Watcher polls clients for in-flight deliveries and fires download.completed
// and download.progress events.
type Watcher struct {
	deliveries   Deliveries
	clients      ClientResolver
	dec          Decryptor
	emit         Emitter
	cfg          Config
	log          zerolog.Logger
	statusLookup statusLookupFn
	lastSeen     map[string]progressKey
}

// New constructs a Watcher.
func New(deliveries Deliveries, clients ClientResolver, dec Decryptor, emit Emitter, cfg Config, log zerolog.Logger) *Watcher {
	return &Watcher{
		deliveries: deliveries,
		clients:    clients,
		dec:        dec,
		emit:       emit,
		cfg:        cfg,
		log:        log.With().Str("component", "progress").Logger(),
		statusLookup: func(name string) (registry.WithStatus, bool) {
			ws, ok := registry.GetClient(name).(registry.WithStatus)
			return ws, ok
		},
		lastSeen: map[string]progressKey{},
	}
}

// Start launches the poll loop in a goroutine and returns. The loop stops when
// ctx is cancelled.
func (w *Watcher) Start(ctx context.Context) error {
	go w.run(ctx)
	return nil
}

func (w *Watcher) run(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()
	w.log.Info().Dur("interval", w.cfg.PollInterval).Msg("progress watcher started")
	for {
		select {
		case <-ctx.Done():
			w.log.Info().Msg("progress watcher stopped")
			return
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

// resolvedClient pairs a client's plugin name + decrypted config with the
// in-flight deliveries pushed to it, so one Status call covers them all.
type resolvedClient struct {
	plugin     registry.WithStatus
	rawConfig  []byte
	deliveries []*domain.InFlightDelivery
}

// poll runs one detection pass. Fail-open: any error is logged and skipped.
func (w *Watcher) poll(ctx context.Context) {
	inflight, err := w.deliveries.ListInFlight(ctx)
	if err != nil {
		w.log.Warn().Err(err).Msg("list in-flight failed")
		return
	}
	if len(inflight) == 0 {
		return // zero idle cost
	}

	// Group by client so each client is queried once.
	byClient := map[uuid.UUID]*resolvedClient{}
	for _, d := range inflight {
		if d.ClientID == nil {
			continue
		}
		rc, ok := byClient[*d.ClientID]
		if !ok {
			rc = w.resolveClient(ctx, d)
			byClient[*d.ClientID] = rc // cache even if nil, so we resolve once
		}
		if rc == nil {
			continue
		}
		rc.deliveries = append(rc.deliveries, d)
	}

	for _, rc := range byClient {
		if rc == nil || len(rc.deliveries) == 0 {
			continue
		}
		w.checkClient(ctx, rc)
	}
}

// resolveClient loads + decrypts the delivery's client and checks WithStatus.
// Returns nil (cached) when the client is missing, undecryptable, or lacks
// status support — all fail-open.
func (w *Watcher) resolveClient(ctx context.Context, d *domain.InFlightDelivery) *resolvedClient {
	client, err := w.clients.GetByID(ctx, *d.ClientID, d.UserID)
	if err != nil || client == nil {
		// Client deleted after the delivery was recorded — fail-open. A debug
		// breadcrumb helps answer "why didn't my completion notify?".
		w.log.Debug().Str("client_id", d.ClientID.String()).Msg("in-flight delivery's client unresolved; skipping")
		return nil
	}
	plugin, ok := w.statusLookup(client.ClientName)
	if !ok {
		return nil // client can't report status; nothing to detect
	}
	raw, derr := w.dec.Decrypt(client.ConfigEnc, client.ConfigNonce)
	if derr != nil {
		w.log.Warn().Err(derr).Str("client", client.ClientName).Msg("decrypt client config failed")
		return nil
	}
	return &resolvedClient{plugin: plugin, rawConfig: raw}
}

// checkClient queries one client, completes any seeded/100% deliveries, and
// emits download.progress for still-downloading torrents whose (percent,state)
// changed since the last poll. Completion takes precedence: a finished torrent
// emits only download.completed, never a progress event.
func (w *Watcher) checkClient(ctx context.Context, rc *resolvedClient) {
	hashes := make([]string, 0, len(rc.deliveries))
	for _, d := range rc.deliveries {
		hashes = append(hashes, d.Infohash)
	}
	qctx, cancel := context.WithTimeout(ctx, statusQueryTimeout)
	statuses, err := rc.plugin.Status(qctx, rc.rawConfig, hashes)
	cancel()
	if err != nil {
		w.log.Warn().Err(err).Msg("client status query failed")
		return
	}

	byHash := make(map[string]*domain.InFlightDelivery, len(rc.deliveries))
	for _, d := range rc.deliveries {
		byHash[strings.ToLower(d.Infohash)] = d
	}
	for _, st := range statuses {
		hash := strings.ToLower(st.Hash)
		d := byHash[hash]
		if d == nil {
			continue
		}
		// Completion wins: seeding or 100% → mark + (on won transition)
		// download.completed. No progress event for a finished torrent.
		if st.State == registry.StateSeeding || st.PercentDone >= 1.0 {
			w.complete(ctx, d)
			delete(w.lastSeen, hash) // stop tracking a finished torrent
			continue
		}
		// Live progress: emit only when (percent,state) changed since last poll.
		key := progressKey{percent: st.PercentDone, state: st.State}
		if w.lastSeen[hash] != key {
			w.lastSeen[hash] = key
			w.emit.Emit(ctx, events.Event{
				UserID: d.UserID, TopicID: &d.TopicID, NotifierID: d.NotifierID,
				Type: events.DownloadProgress, Severity: "info",
				Data: map[string]any{"infohash": d.Infohash, "percent_done": st.PercentDone, "state": st.State},
			})
		}
	}
}

// complete marks the delivery done and, only on winning the NULL→now()
// transition, emits download.completed (so a restart never re-notifies).
func (w *Watcher) complete(ctx context.Context, d *domain.InFlightDelivery) {
	won, err := w.deliveries.MarkCompleted(ctx, d.DeliveryID)
	if err != nil {
		w.log.Warn().Err(err).Str("delivery_id", d.DeliveryID.String()).Msg("mark completed failed")
		return
	}
	if !won {
		return // already completed elsewhere — no duplicate notification
	}
	metrics.ProgressCompletionsTotal.Inc()
	w.emit.Emit(ctx, events.Event{
		UserID: d.UserID, TopicID: &d.TopicID, NotifierID: d.NotifierID,
		Type: events.DownloadCompleted, Severity: "info",
		Title: d.DisplayName, Body: "Finished downloading: " + d.Label,
		Link: w.cfg.PublicBaseURL + "/topics",
	})
}
