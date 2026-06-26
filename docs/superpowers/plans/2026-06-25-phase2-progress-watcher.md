# Phase 2 — Download-Completion Watcher Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a bounded background watcher that polls download clients (via `registry.WithStatus`) for in-flight deliveries, detects when a torrent finishes, and emits a durable, deduped `download.completed` event — enabling the "download finished" notification.

**Architecture:** A new `internal/progress` package owns a `Watcher` goroutine that, every poll interval, lists not-yet-completed deliveries (`topic_deliveries.completed_at IS NULL`, joined to `topics` for owner/notifier/name), groups them by their resolving client, calls `WithStatus.Status`, and for each torrent now seeding/100% atomically marks `completed_at` (NULL→now) and emits `events.DownloadCompleted` via the Phase 1 `events.Bus`. Dedup survives restarts because completion is a persisted column, exactly like `session_expired_at`. The watcher reuses the same client-resolution + decrypt + `WithStatus` plumbing the `/status` endpoint already uses, and depends only on small consumer-side interfaces so it's unit-testable without a DB or the registry.

**Tech Stack:** Go 1.25 (pgx v5, zerolog, uuid, prometheus), goose migrations. Backend-only — no frontend changes.

## Global Constraints

- **Builds on Phase 1 (this branch is stacked on it).** The `events` package, `events.Bus.Emit`, `events.DownloadCompleted` (policy: persist=true, notifiable=true, sse=true), and the `registry.WithStatus` plumbing already exist. Do not re-create them.
- **Scope: `download.completed` only.** `download.progress` emission is **deferred to Phase 3** (it is SSE-only per its policy — not persisted, not notifiable — so with the Phase-1 nil SSE publisher it would go nowhere; the UI already shows live progress via `/status` polling). Do NOT emit `download.progress` in this phase.
- **Durable dedup:** completion fires exactly once per delivery, surviving backend restarts — via an atomic `completed_at` NULL→now() transition (`UPDATE … WHERE id=$1 AND completed_at IS NULL`, `RowsAffected()>0` wins). Mirror the existing `MarkSessionExpired` pattern (`tracker_credentials.session_expired_at`, migration `0003`).
- **Next migration is `0011`.** Latest existing is `0010_topic_display_name_placeholder.sql`. Use goose format (`-- +goose Up` / `-- +goose StatementBegin` … as in `0003_add_session_expired_at.sql`).
- **"Complete" = `State == registry.StateSeeding` OR `PercentDone >= 1.0`.** `registry.TorrentStatus{Hash string; PercentDone float64 /*0..1*/; State string}`. States: `StateDownloading/Seeding/Stopped/Checking/Queued/Error/Unknown`.
- **Fail-open everywhere** (matches `/status` `liveStatus` and `recordDelivery`): a client unreachable, config undecryptable, a client without `WithStatus`, or a DB error is logged and never affects anything else. The watcher never blocks shutdown (ctx-cancel).
- **Gated + zero idle cost:** the loop runs on `MARAUDER_PROGRESS_POLL_INTERVAL` (default `1m`); when no deliveries are in-flight the tick is a single empty query and returns. The whole watcher is gated by `MARAUDER_PROGRESS_WATCHER_ENABLED` (default `true`).
- **Working set is bounded:** `ListInFlight` only returns deliveries `delivered_at > now() - interval '30 days'` and `client_id IS NOT NULL`, so never-completable rows (client without `WithStatus`, removed torrents) age out instead of being polled forever.
- **Go conventions:** tabs, gofmt, `fmt.Errorf("…: %w", err)`, consumer-side interfaces at the consumer, table-driven tests, manual fakes (no mocking framework). Run via Docker mounting the **worktree** backend:
  `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-phase2-download-progress-watcher/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go vet ./... && go test -race ./..."`
- **No AI-attribution in commits.** Imperative subject ≤72 chars. Reference `#93`.

---

## File Structure

**Create:**
- `backend/internal/db/migrations/0011_add_delivery_completed_at.sql` — `completed_at` column + partial index.
- `backend/internal/progress/watcher.go` — the `Watcher` (loop, completion detection, emission).
- `backend/internal/progress/watcher_test.go` — unit tests with fakes.

**Modify:**
- `backend/internal/domain/domain.go` — add the `InFlightDelivery` read-model struct.
- `backend/internal/db/repo/deliveries.go` — add `ListInFlight` + `MarkCompleted` (and `QueryRow` to the pool interface for the atomic update's RowsAffected — actually `Exec` already suffices).
- `backend/internal/db/repo/deliveries_test.go` — repo tests for the two new methods (create if absent).
- `backend/internal/config/config.go` — two env vars.
- `backend/internal/metrics/metrics.go` — a completions counter.
- `backend/cmd/server/main.go` — construct + start the watcher.
- `CLAUDE.md`, `CHANGELOG.md`, `deploy/.env.example` — docs.

---

## Task 1: Migration + Deliveries repo (ListInFlight, MarkCompleted)

**Files:**
- Create: `backend/internal/db/migrations/0011_add_delivery_completed_at.sql`
- Modify: `backend/internal/domain/domain.go`, `backend/internal/db/repo/deliveries.go`
- Test: `backend/internal/db/repo/deliveries_test.go`

**Interfaces:**
- Produces:
  - `domain.InFlightDelivery{ DeliveryID uuid.UUID; TopicID uuid.UUID; UserID uuid.UUID; NotifierID *uuid.UUID; ClientID *uuid.UUID; Infohash string; Label string; DisplayName string }`.
  - `(*repo.Deliveries) ListInFlight(ctx context.Context) ([]*domain.InFlightDelivery, error)`.
  - `(*repo.Deliveries) MarkCompleted(ctx context.Context, deliveryID uuid.UUID) (bool, error)` — true when this call won the NULL→now() transition.

- [ ] **Step 1: Write the migration**

`0011_add_delivery_completed_at.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
ALTER TABLE topic_deliveries ADD COLUMN completed_at TIMESTAMPTZ;
-- +goose StatementEnd
-- +goose StatementBegin
-- Go-forward only: stamp every pre-existing delivery as already accounted for,
-- so enabling the watcher does NOT back-notify "download finished" for torrents
-- that completed before this feature shipped. Only deliveries recorded after
-- this migration are watched. (Mirrors the Sonarr integration's go-forward enable.)
UPDATE topic_deliveries SET completed_at = now() WHERE completed_at IS NULL;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_topic_deliveries_incomplete ON topic_deliveries (delivered_at) WHERE completed_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_topic_deliveries_incomplete;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE topic_deliveries DROP COLUMN completed_at;
-- +goose StatementEnd
```

> **Why the backfill is load-bearing:** without the `UPDATE`, the new column is NULL for every historical row, so the watcher's first poll would mark and emit `download.completed` for every already-seeding torrent within the 30-day window — a retroactive notification burst on upgrade. The backfill makes the feature go-forward-only.

- [ ] **Step 2: Add the domain read-model** to `domain.go` (next to `TopicDelivery`):

```go
// InFlightDelivery is a not-yet-completed delivery joined with its topic's
// owner, notifier override, and display name — the read model the progress
// watcher needs to poll the client and route a download.completed event.
type InFlightDelivery struct {
	DeliveryID  uuid.UUID
	TopicID     uuid.UUID
	UserID      uuid.UUID
	NotifierID  *uuid.UUID
	ClientID    *uuid.UUID
	Infohash    string
	Label       string
	DisplayName string
}
```

- [ ] **Step 3: Write the failing repo tests**

Add to `deliveries_test.go` (mirror the fake-pool pattern in `topic_events_test.go`; `deliveriesPool` is `{Exec, Query}`):

```go
package repo

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestDeliveries_MarkCompleted_ReturnsTrueOnTransition(t *testing.T) {
	pool := &fakeDelivPool{tag: pgconn.NewCommandTag("UPDATE 1")}
	r := &Deliveries{pool: pool}
	won, err := r.MarkCompleted(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}
	if !won {
		t.Error("expected won=true when one row updated")
	}
}

func TestDeliveries_MarkCompleted_ReturnsFalseWhenAlreadyComplete(t *testing.T) {
	pool := &fakeDelivPool{tag: pgconn.NewCommandTag("UPDATE 0")}
	r := &Deliveries{pool: pool}
	won, err := r.MarkCompleted(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}
	if won {
		t.Error("expected won=false when no row updated (already completed)")
	}
}
```

Add the fake pool (only if `deliveries_test.go` doesn't already define one):

```go
type fakeDelivPool struct {
	tag     pgconn.CommandTag
	lastSQL string
}

func (f *fakeDelivPool) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	f.lastSQL = sql
	return f.tag, nil
}
func (f *fakeDelivPool) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, nil
}
```

(Add `"github.com/jackc/pgx/v5"` to the test imports for the `Query` signature.)

- [ ] **Step 4: Run tests to verify they fail**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-phase2-download-progress-watcher/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/db/repo/ -run TestDeliveries_MarkCompleted"`
Expected: FAIL — `MarkCompleted` undefined.

- [ ] **Step 5: Implement `MarkCompleted` and `ListInFlight`** in `deliveries.go`:

```go
// MarkCompleted atomically stamps completed_at for a delivery, but only if it
// was still NULL. Returns true when this call won the NULL→now() transition —
// the caller that gets true is the one (and only one) that should fire the
// download.completed event. Survives restarts: a completed row never fires again.
func (r *Deliveries) MarkCompleted(ctx context.Context, deliveryID uuid.UUID) (bool, error) {
	const q = `UPDATE topic_deliveries SET completed_at = now() WHERE id = $1 AND completed_at IS NULL`
	ct, err := r.pool.Exec(ctx, q, deliveryID)
	if err != nil {
		return false, fmt.Errorf("deliveries: mark completed: %w", err)
	}
	return ct.RowsAffected() > 0, nil
}

// ListInFlight returns deliveries not yet marked complete, joined with their
// topic's owner, notifier override, and display name. Bounded to the last 30
// days and to deliveries with a known client, so rows that can never complete
// (client without WithStatus, removed torrents) age out of the working set
// instead of being polled forever.
func (r *Deliveries) ListInFlight(ctx context.Context) ([]*domain.InFlightDelivery, error) {
	const q = `
SELECT d.id, d.topic_id, t.user_id, t.notifier_id, d.client_id, d.infohash, d.label, t.display_name
FROM topic_deliveries d
JOIN topics t ON t.id = d.topic_id
WHERE d.completed_at IS NULL
  AND d.client_id IS NOT NULL
  AND d.delivered_at > now() - interval '30 days'`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("deliveries: list in-flight: %w", err)
	}
	defer rows.Close()
	var out []*domain.InFlightDelivery
	for rows.Next() {
		var d domain.InFlightDelivery
		if err := rows.Scan(&d.DeliveryID, &d.TopicID, &d.UserID, &d.NotifierID, &d.ClientID, &d.Infohash, &d.Label, &d.DisplayName); err != nil {
			return nil, fmt.Errorf("deliveries: scan in-flight: %w", err)
		}
		out = append(out, &d)
	}
	return out, rows.Err()
}
```

- [ ] **Step 6: Run tests to verify they pass + build**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-phase2-download-progress-watcher/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go test ./internal/db/repo/ -run TestDeliveries"`
Expected: PASS, build clean.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/db/migrations/0011_add_delivery_completed_at.sql backend/internal/domain/domain.go backend/internal/db/repo/deliveries.go backend/internal/db/repo/deliveries_test.go
git commit -m "feat: track delivery completion (completed_at) + in-flight query (#93)"
```

---

## Task 2: progress.Watcher — completion detection + emission

**Files:**
- Create: `backend/internal/progress/watcher.go`, `backend/internal/progress/watcher_test.go`
- Modify: `backend/internal/metrics/metrics.go`

**Interfaces:**
- Consumes: `domain.InFlightDelivery`, `domain.Client`, `events.Event`/`events.DownloadCompleted`, `registry.WithStatus`/`TorrentStatus`/`StateSeeding`, `domain.Message` (indirectly via the bus).
- Produces:
  - `progress.Watcher` with `New(lister InFlightLister, clients ClientResolver, dec Decryptor, emit Emitter, cfg Config, log zerolog.Logger) *Watcher`.
  - Consumer-side interfaces (defined in `progress`): `InFlightLister{ ListInFlight(ctx) ([]*domain.InFlightDelivery, error) }`; `Completer{ MarkCompleted(ctx, uuid.UUID) (bool, error) }` — both satisfied by `*repo.Deliveries`; `ClientResolver{ GetByID(ctx, id, userID uuid.UUID) (*domain.Client, error) }`; `Decryptor{ Decrypt(ct, nonce []byte) ([]byte, error) }`; `Emitter{ Emit(ctx, events.Event) }`.
  - `progress.Config{ PollInterval time.Duration; PublicBaseURL string }`.
  - `(*Watcher) Start(ctx context.Context) error` — launches the loop goroutine, returns nil immediately (mirrors `scheduler.Start`/`sonarrPoller.Start`).
  - A `statusLookup func(clientName string) (registry.WithStatus, bool)` seam, defaulting to a `registry.GetClient` wrapper, injectable for tests.

> Note: `InFlightLister` and `Completer` are both implemented by `*repo.Deliveries`; `New` takes one `deliveries` value satisfying both (compose them into one param `deliveries Deliveries` where `type Deliveries interface { InFlightLister; Completer }`). Keep it as a single interface with both methods to avoid passing the repo twice.

- [ ] **Step 1: Add the metric** to `metrics.go` (follow the existing collector style):

```go
// ProgressCompletionsTotal counts download.completed events the watcher fired.
var ProgressCompletionsTotal = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "marauder_progress_completions_total",
	Help: "Total downloads the progress watcher detected as finished.",
})
```

Register it wherever the package registers its collectors (add `ProgressCompletionsTotal` to the `MustRegister(...)` / registry list next to the other counters).

- [ ] **Step 2: Write the failing test** (`watcher_test.go`)

```go
package progress

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/events"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

type fakeDeliveries struct {
	inflight  []*domain.InFlightDelivery
	completed []uuid.UUID
	markWon   bool
}

func (f *fakeDeliveries) ListInFlight(_ context.Context) ([]*domain.InFlightDelivery, error) {
	return f.inflight, nil
}
func (f *fakeDeliveries) MarkCompleted(_ context.Context, id uuid.UUID) (bool, error) {
	f.completed = append(f.completed, id)
	return f.markWon, nil
}

type fakeClients struct{ client *domain.Client }

func (f *fakeClients) GetByID(_ context.Context, _ uuid.UUID, _ uuid.UUID) (*domain.Client, error) {
	return f.client, nil
}

type fakeDecryptor struct{}

func (fakeDecryptor) Decrypt(ct, _ []byte) ([]byte, error) { return ct, nil }

type fakeEmitter struct{ events []events.Event }

func (f *fakeEmitter) Emit(_ context.Context, ev events.Event) { f.events = append(f.events, ev) }

type fakeStatus struct{ statuses []registry.TorrentStatus }

func (f fakeStatus) Name() string                  { return "fake" }
func (f fakeStatus) DisplayName() string            { return "Fake" }
func (f fakeStatus) ConfigSchema() map[string]any   { return nil }
func (f fakeStatus) Test(context.Context, []byte) error { return nil }
func (f fakeStatus) Add(context.Context, []byte, *domain.Payload, domain.AddOptions) error {
	return nil
}
func (f fakeStatus) Status(_ context.Context, _ []byte, _ []string) ([]registry.TorrentStatus, error) {
	return f.statuses, nil
}

func newTestWatcher(t *testing.T, del *fakeDeliveries, emit *fakeEmitter, st fakeStatus) *Watcher {
	t.Helper()
	cid := uuid.New()
	w := New(del, &fakeClients{client: &domain.Client{ID: cid, ClientName: "fake"}}, fakeDecryptor{}, emit,
		Config{PollInterval: 0, PublicBaseURL: "http://x"}, zerolog.Nop())
	w.statusLookup = func(string) (registry.WithStatus, bool) { return st, true }
	return w
}

func inflight(clientID uuid.UUID) *domain.InFlightDelivery {
	tid, uid, did := uuid.New(), uuid.New(), uuid.New()
	return &domain.InFlightDelivery{
		DeliveryID: did, TopicID: tid, UserID: uid, ClientID: &clientID,
		Infohash: "abc123", Label: "s01e01", DisplayName: "Show",
	}
}

func TestPoll_Seeding_MarksCompletedAndEmits(t *testing.T) {
	cid := uuid.New()
	d := inflight(cid)
	del := &fakeDeliveries{inflight: []*domain.InFlightDelivery{d}, markWon: true}
	emit := &fakeEmitter{}
	st := fakeStatus{statuses: []registry.TorrentStatus{{Hash: "ABC123", PercentDone: 1.0, State: registry.StateSeeding}}}
	w := newTestWatcher(t, del, emit, st)
	w.poll(context.Background())
	if len(del.completed) != 1 || del.completed[0] != d.DeliveryID {
		t.Fatalf("expected MarkCompleted for the delivery, got %v", del.completed)
	}
	if len(emit.events) != 1 || emit.events[0].Type != events.DownloadCompleted {
		t.Fatalf("expected one DownloadCompleted event, got %+v", emit.events)
	}
	if emit.events[0].TopicID == nil || *emit.events[0].TopicID != d.TopicID {
		t.Error("event should carry the delivery's topic id")
	}
}

func TestPoll_StillDownloading_NoMarkNoEmit(t *testing.T) {
	cid := uuid.New()
	del := &fakeDeliveries{inflight: []*domain.InFlightDelivery{inflight(cid)}, markWon: true}
	emit := &fakeEmitter{}
	st := fakeStatus{statuses: []registry.TorrentStatus{{Hash: "abc123", PercentDone: 0.5, State: registry.StateDownloading}}}
	w := newTestWatcher(t, del, emit, st)
	w.poll(context.Background())
	if len(del.completed) != 0 || len(emit.events) != 0 {
		t.Fatalf("downloading torrent must not complete/emit: completed=%v events=%v", del.completed, emit.events)
	}
}

func TestPoll_LostTransition_NoEmit(t *testing.T) {
	cid := uuid.New()
	del := &fakeDeliveries{inflight: []*domain.InFlightDelivery{inflight(cid)}, markWon: false} // already completed by someone
	emit := &fakeEmitter{}
	st := fakeStatus{statuses: []registry.TorrentStatus{{Hash: "abc123", PercentDone: 1.0, State: registry.StateSeeding}}}
	w := newTestWatcher(t, del, emit, st)
	w.poll(context.Background())
	if len(emit.events) != 0 {
		t.Fatalf("a lost NULL→now transition must not emit: %+v", emit.events)
	}
}

func TestPoll_ClientWithoutStatus_Skipped(t *testing.T) {
	cid := uuid.New()
	del := &fakeDeliveries{inflight: []*domain.InFlightDelivery{inflight(cid)}, markWon: true}
	emit := &fakeEmitter{}
	w := newTestWatcher(t, del, emit, fakeStatus{})
	w.statusLookup = func(string) (registry.WithStatus, bool) { return nil, false }
	w.poll(context.Background())
	if len(del.completed) != 0 || len(emit.events) != 0 {
		t.Fatalf("client without WithStatus must be skipped")
	}
}
```

> Implementer note: `fakeStatus` must satisfy `registry.WithStatus`, which embeds `registry.Client`. Open `backend/internal/plugins/registry/registry.go` and confirm the exact `Client` method set + signatures (the fake above implements `Name`/`DisplayName`/`ConfigSchema`/`Test`/`Add`/`Status` — add or correct any method so the fake compiles against the real interface). The test failing to compile because of a missing method is itself the RED signal; complete the fake, don't stub the interface.

- [ ] **Step 3: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-phase2-download-progress-watcher/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/progress/..."`
Expected: FAIL — `Watcher`, `New`, `poll` undefined.

- [ ] **Step 4: Implement** `watcher.go`:

```go
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

// Watcher polls clients for in-flight deliveries and fires download.completed.
type Watcher struct {
	deliveries   Deliveries
	clients      ClientResolver
	dec          Decryptor
	emit         Emitter
	cfg          Config
	log          zerolog.Logger
	statusLookup statusLookupFn
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

// checkClient queries one client and completes any seeded/100% deliveries.
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
	done := map[string]bool{}
	for _, st := range statuses {
		if st.State == registry.StateSeeding || st.PercentDone >= 1.0 {
			done[strings.ToLower(st.Hash)] = true
		}
	}
	for _, d := range rc.deliveries {
		if !done[strings.ToLower(d.Infohash)] {
			continue
		}
		w.complete(ctx, d)
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
```

- [ ] **Step 5: Run tests to verify they pass + build/vet**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-phase2-download-progress-watcher/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go vet ./internal/progress/... && go test -race ./internal/progress/..."`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/progress/watcher.go backend/internal/progress/watcher_test.go backend/internal/metrics/metrics.go
git commit -m "feat: add download-completion watcher emitting download.completed (#93)"
```

---

## Task 3: Config + wire the watcher into main.go

**Files:**
- Modify: `backend/internal/config/config.go`, `backend/cmd/server/main.go`

**Interfaces:**
- Consumes: `progress.New`, `progress.Config`, `progress.Watcher.Start` (Task 2); `repo.Deliveries` (now satisfies `progress.Deliveries`).
- Produces: `cfg.ProgressWatcherEnabled bool`, `cfg.ProgressPollInterval time.Duration`.

- [ ] **Step 1: Add config fields** in `config.go`, in the Scheduler block (after `TrackerHTTPTimeout`):

```go
	// Progress watcher (download-completion detection)
	ProgressWatcherEnabled bool          `env:"MARAUDER_PROGRESS_WATCHER_ENABLED" envDefault:"true"`
	ProgressPollInterval   time.Duration `env:"MARAUDER_PROGRESS_POLL_INTERVAL" envDefault:"1m"`
```

- [ ] **Step 2: Write the failing test** — config default coverage in `config_test.go` (mirror the existing config test that asserts defaults; if none exists, add a minimal one):

```go
func TestConfig_ProgressDefaults(t *testing.T) {
	t.Setenv("MARAUDER_DB_URL", "postgres://x")
	t.Setenv("MARAUDER_MASTER_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.ProgressWatcherEnabled {
		t.Error("progress watcher should default enabled")
	}
	if c.ProgressPollInterval != time.Minute {
		t.Errorf("poll interval = %v, want 1m", c.ProgressPollInterval)
	}
}
```

> Implementer note: match the real constructor (`Load`/`Parse`) and the required-env setup used by the existing config tests — read `config_test.go` first; the master-key value must be a valid base64 32-byte key like the other tests use.

- [ ] **Step 3: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-phase2-download-progress-watcher/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/config/ -run TestConfig_ProgressDefaults"`
Expected: FAIL — fields undefined.

- [ ] **Step 4: Wire the watcher in `main.go`** — after the scheduler is constructed/started (around `sch := scheduler.New(...)` / `sch.Start(rootCtx)`), add:

```go
	if cfg.ProgressWatcherEnabled {
		watcher := progress.New(
			deliveriesRepo, clientsRepo, master, bus,
			progress.Config{PollInterval: cfg.ProgressPollInterval, PublicBaseURL: cfg.PublicBaseURL},
			logger,
		)
		if err := watcher.Start(rootCtx); err != nil {
			logger.Error().Err(err).Msg("progress watcher failed to start")
		}
	}
```

Add the import `"github.com/artyomsv/marauder/backend/internal/progress"`. (`deliveriesRepo`, `clientsRepo`, `master`, `bus`, `rootCtx`, `logger`, `cfg` are all already in scope from the scheduler wiring.)

- [ ] **Step 5: Run build + config test**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-phase2-download-progress-watcher/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go vet ./... && go test ./internal/config/..."`
Expected: PASS, whole module builds.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/config/config.go backend/internal/config/config_test.go backend/cmd/server/main.go
git commit -m "feat: wire progress watcher into server startup (#93)"
```

---

## Task 4: Docs — CLAUDE.md, CHANGELOG, .env.example

**Files:**
- Modify: `CLAUDE.md`, `CHANGELOG.md`, `deploy/.env.example`

- [ ] **Step 1: `CLAUDE.md`** — add a `progress` row to the backend package table:

```
| **`progress`** | background download-completion watcher: polls `WithStatus` clients for in-flight `topic_deliveries` (`completed_at IS NULL`), and on seeding/100% atomically marks `completed_at` (NULL→now, dedup survives restart) and emits `events.DownloadCompleted` via the bus. Go-forward only — migration `0011` backfills existing deliveries as complete so upgrading doesn't back-notify. Gated by `MARAUDER_PROGRESS_WATCHER_ENABLED`, polls every `MARAUDER_PROGRESS_POLL_INTERVAL` (1m); zero idle cost. The server-side completion detector the scheduler deliberately isn't |
```

Update the `db/db/repo` row's `Deliveries` note to mention `completed_at` (migration `0011`) + `MarkCompleted`/`ListInFlight`. Update the Delivery-tracking paragraph in the Scheduler section to note that completion is now detected by the `progress` watcher (not "no background poller" anymore — correct that line).

- [ ] **Step 2: `CHANGELOG.md`** — add under `[Unreleased]` → `### Added`:

```markdown
- "Download finished" notifications: a background watcher detects when a delivered torrent finishes downloading and emits a `download.completed` event to subscribed notifiers (#93).
```

- [ ] **Step 3: `deploy/.env.example`** — add the two env vars near the scheduler ones, with safe defaults + a one-line comment each:

```bash
# Progress watcher: detect finished downloads and fire "download finished" notifications
MARAUDER_PROGRESS_WATCHER_ENABLED=true
MARAUDER_PROGRESS_POLL_INTERVAL=1m
```

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md CHANGELOG.md deploy/.env.example
git commit -m "docs: document the download-completion watcher (#93)"
```

---

## Final verification

- [ ] **Full backend suite:** `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-phase2-download-progress-watcher/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go vet ./... && go test -race ./..."` → all pass.
- [ ] **Migration sanity:** confirm `0011_add_delivery_completed_at.sql` is the highest-numbered migration and has both Up and Down blocks.

---

## Spec coverage check (Phase 2 of `2026-06-25-event-types-and-live-updates-design.md` §6.5, §9)

- §6.5 "progress watcher … poll `WithStatus` … emit `download.completed` on transition to seeding-or-100% … bounded, zero idle cost, dedup via persisted marker" → Tasks 1–3. The spec's `download.progress` emission is **intentionally deferred to Phase 3** (Global Constraints, scope note) because it is SSE-only and has no consumer until the hub exists; this is recorded here and in the CHANGELOG so Phase 3 picks it up.
- §9 Phase-2 line ("WithStatus poller → download.progress/completed; enables download-finished notifications") → delivered for `download.completed`; `download.progress` deferred as above.
- No frontend changes: the existing `/status` polling already renders live progress; the watcher only adds server-side completion detection + notification.
