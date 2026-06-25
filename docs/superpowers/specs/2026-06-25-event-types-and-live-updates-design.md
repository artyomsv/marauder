# Event Types & Live UI Updates — Design Spec

**Status:** Approved design (2026-06-25) — ready for implementation planning
**Topic:** A typed event taxonomy that notifiers subscribe to per-event-type, a server-side
download-progress watcher, and live server→client push via SSE for download progress, tracker-check
status, and a per-topic event timeline.

---

## 1. Goal & decomposition

Three independently shippable capabilities, all in scope, delivered in three phases:

| # | Capability | Build state at start |
|---|---|---|
| **A** | Typed event taxonomy (topic added / check lifecycle / submitted / finished / errors) | Partially latent — `topic_events` table + `notifiers.events` filter exist, only `updated`/`error` emitted |
| **B** | Notifiers subscribe per-event-type | Data model + dispatch filter exist; **no UI editor**, only 2 events emitted |
| **C** | Live UI push (download progress + check status + event timeline) via **SSE** | Greenfield — no realtime transport |

## 2. Locked decisions

- **Scope:** all three capabilities, phased (§9).
- **Transport:** **SSE** (Server-Sent Events), not WebSocket — pure server→client push fan-out;
  native browser reconnect; one-line nginx change; no extra Go dependency.
- **Subscription granularity:** **notifier-level only.** A notifier subscribes to a set of events;
  a topic's existing `NotifierID` override picks *which* notifier (and thus inherits its event set).
  No per-topic event mask.
- **Notifiable events (the 4 a notifier may subscribe to):** `release.found`, `download.submitted`,
  `download.completed`, and the error pair `check.failed`/`session.expired`.
- **Live-feed-only (never notifier-subscribable):** `check.started`, `download.progress`,
  `topic.added`, `check.completed` — too frequent/low-value for push notifications.
- **Backward compat:** existing notifier rows (`events=['updated','error']`) keep working via an
  **alias map**. Legacy `"updated"` matches `release.found` + `download.submitted` +
  `download.completed` (**broad**); legacy `"error"` matches `check.failed` + `session.expired`.
  New UI writes canonical names.
- **Per-topic event timeline UI:** included (read-only), powered by wiring the dormant
  `topic_events` table.

## 3. Current state, grounded in code

**Notifier side (B half-done):**
- `notifiers.events TEXT[] DEFAULT ['updated','error']` — `db/migrations/0001_initial_schema.sql:128`
- `notify.Dispatcher.Send/SendVia` already filter via `subscribed(n.Events, event)` — `notify/dispatcher.go:118`
- Handler defaults events to `["updated","error"]` — `api/handlers/notifiers.go:101`; UI renders them
  read-only (`Notifiers.tsx:eventLabel`), no editor
- Only `"updated"` (new release, `scheduler.go:438`, per-topic `SendVia`) and `"error"`
  (session expired, `scheduler.go:490`, global `Send`, atomically deduped) are emitted

**Event-log backbone (A, dormant):**
- `topic_events` table — `0001_initial_schema.sql:106`:
  `{id BIGSERIAL, topic_id, user_id, event_type, severity CHECK(info|warn|error), message, data JSONB, created_at}`,
  indexed `(topic_id, created_at DESC)` and `(user_id, created_at DESC)`. **Nothing writes to it.**
- `domain.TopicEvent` type exists; no repo, no insert call.

**Status/realtime (C, greenfield):**
- `GET /topics/{id}/status` reads `topic_deliveries`, enriches via
  `registry.WithStatus.Status(ctx,cfg,hashes)` (10s timeout, fail-open) — `api/handlers/topics.go:358`
- qBittorrent + Transmission normalise to `StateDownloading/Seeding/Checking/Queued/Stopped/Error`,
  `percent_done 0..1`
- `DeliveryStatus.tsx` adaptive-polls 3s active / 20s idle
- No WS/SSE; chi router is REST behind `RequireAuth` JWT middleware

## 4. Architecture — a single event spine

```
                       ┌─────────────────────────────┐
  scheduler / handlers │   events.Bus.Emit(ctx, ev)   │
   / progress watcher  └──────────────┬──────────────┘
                                       │  per-type policy → tee (best-effort, non-blocking)
                    ┌─────────────────┼──────────────────┐
                    ▼                 ▼                  ▼
            topic_events DB     notify.Dispatcher       SSE Hub
            (history/timeline (telegram/email/…,    (in-mem per-user
             + SSE replay)     filtered by .Events)  pub/sub)
                                                          │
                                                  browsers (EventSource)
```

One `events` package owns the canonical `Type`, the `Event` struct, the **policy table**, and the
`Bus` that tees to three sinks. The scheduler gains one new nil-safe seam (`eventEmitter`); its
current direct `notifier.Send/SendVia` calls are **replaced** by `emit()` so there is a single
emission point feeding all three consumers.

## 5. Event model & per-type policy

```go
type Type string
const (
  TopicAdded        Type = "topic.added"
  CheckStarted      Type = "check.started"
  CheckCompleted    Type = "check.completed"
  ReleaseFound      Type = "release.found"
  DownloadSubmitted Type = "download.submitted"
  DownloadProgress  Type = "download.progress"
  DownloadCompleted Type = "download.completed"
  CheckFailed       Type = "check.failed"
  SessionExpired    Type = "session.expired"
)

type Event struct {
  ID       int64          // topic_events BIGSERIAL → SSE Last-Event-ID (0 when not persisted)
  UserID   uuid.UUID
  TopicID  uuid.UUID
  Type     Type
  Severity string         // info | warn | error
  Title    string
  Body     string
  Data     map[string]any
  At       time.Time
}
```

**Policy table — the single source of truth (unit-tested):**

| Type | persist (history) | notifier | SSE |
|---|:--:|:--:|:--:|
| `topic.added` | ✓ | — | ✓ |
| `check.started` | — | — | ✓ |
| `check.completed` | — | — | ✓ |
| `release.found` | ✓ | ✓ | ✓ |
| `download.submitted` | ✓ | ✓ | ✓ |
| `download.progress` | **—** | — | ✓ |
| `download.completed` | ✓ | ✓ | ✓ |
| `check.failed` | ✓ | ✓ | ✓ |
| `session.expired` | ✓ | ✓ | ✓ |

`download.progress` is never persisted (write-storm avoidance) — SSE-only. `persist` events get a
real `ID` for SSE replay; ephemeral events carry `ID=0`.

## 6. Backend components

### 6.1 `events` package
`Event`, `Type`, `policy(Type) → {persist, notifiable, sse}`, and `Bus`:
- `Emit(ctx, Event)`: reads policy, then (a) if `persist`: insert into `topic_events`, set `ev.ID`;
  (b) if `notifiable`: `notify.Dispatcher.SendVia(userID, notifierID, string(Type), msg)`;
  (c) if `sse`: publish to hub. All best-effort — failures logged, never block the caller.
- Notifier-id source: events carry the topic's `NotifierID` so per-topic override is honored;
  session-expiry stays global (`notifierID == nil`).

### 6.2 `topic_events` repo (`db/repo/topic_events.go`)
`Record(ctx, Event) (int64, error)` (returns serial id) + `ListForTopic(ctx, topicID, userID, limit, beforeID)`
(paginated timeline) + `ListForUserSince(ctx, userID, sinceID)` (SSE reconnect replay of persisted events).

### 6.3 Dispatcher alias mapping (`notify/dispatcher.go`)
Extend `subscribed(events, event)` with an alias map so legacy rows keep working:
`"updated"` ⇒ {`release.found`, `download.submitted`, `download.completed`} (broad);
`"error"` ⇒ {`check.failed`, `session.expired`}. New canonical names match directly.

### 6.4 Emit points (replace/augment existing calls)
| Event | Site |
|---|---|
| `topic.added` | `handlers/topics.go:217` (after Create) |
| `check.started` | `scheduler.go` runCheck, before `tr.Check` |
| `check.completed` | `scheduler.go` after every non-errored check (both change and no-change paths), carries `next_check_at` in `Data` |
| `release.found` | `scheduler.go` when `check.Hash != t.LastHash`, before download loop |
| `download.submitted` | `scheduler.go:668` (after client `Add`, alongside `recordDelivery`) |
| `download.completed` | progress watcher (§6.5) |
| `check.failed` | `scheduler.go:360` error paths |
| `session.expired` | `scheduler.go:490` (keep atomic `MarkSessionExpired` dedup gate) |

Today's `"updated"` (`scheduler.go:438`) and `"error"` (`:490`) `SendVia/Send` calls are removed in
favor of `emit()`.

### 6.5 Progress watcher (`progress` package, Phase 2)
Bounded goroutine injected into the scheduler. Every ~5s:
1. Gather **in-flight** infohashes (deliveries not known-terminal), grouped topic→client.
2. For clients implementing `WithStatus`, call `Status(hashes)` (reuses `/status` plumbing).
3. Diff vs in-memory last-seen map: emit `download.progress` on percent change,
   `download.completed` on transition to seeding-or-100%.
4. Terminal infohashes leave the watch set → **zero cost when idle**.
On startup, seed the watch set from `topic_deliveries` and classify once (bounded). Fail-open like
`/status`.

### 6.6 SSE hub + endpoint (Phase 3)
- `GET /api/v1/events?ticket=…` → `text/event-stream`. Hub: `map[userID][]chan Event`, filtered by
  `event.UserID`. Per-client buffered channel; **drop-on-full** so a slow client never blocks the bus.
- `:` heartbeat comment every ~25s to survive proxy idle timeouts. `id:` = `Event.ID` for persisted
  events; on reconnect the browser sends `Last-Event-ID` → replay persisted events via
  `ListForUserSince` (progress events are ephemeral, refreshed by the next poll, not replayed).
- **Auth:** `POST /api/v1/events/ticket` (normal JWT) → one-time token, in-memory, 30s TTL, bound to
  userID. SSE endpoint consumes the ticket once and binds the connection to that user. Keeps the JWT
  out of URLs/logs and is independent of the future HttpOnly-cookie migration.
- **nginx:** add a `/api/v1/events` location with `proxy_buffering off;` and a long read timeout in
  **both** `deploy/nginx/gateway.conf` and the inline copy inside `deploy/docker-compose.ghcr.yml`
  (keep-in-sync rule).

## 7. Frontend components (Phase 3)

- **`useEventStream()` provider** — one app-wide connection. Fetch ticket → open `EventSource`. On
  `error`/close: refetch ticket + recreate (browser auto-reconnect can't refresh our ticket, so we
  wrap it). Route by type:
  - `download.progress` / `download.completed` → `queryClient.setQueryData(QK.topicStatus(topicId), …)`
  - `release.found` / `download.submitted` → invalidate topics list + toast
  - `check.started` / `check.completed` / `check.failed` → drive a live "checking…" pulse +
    `next_check_at` countdown per topic row
- **Polling fallback** — `DeliveryStatus` poll stays, **disabled while SSE is connected**, re-enabled
  at the slow 20s idle rate if the stream drops. No regression if SSE fails.
- **Notifiers page** — event-picker checkboxes for the 4 notifiable events, writing `notifiers.events`.
  Render legacy `updated`/`error` rows correctly (map to the new labels).
- **Per-topic event timeline** — a read-only history view (drawer or tab on the topic) backed by
  `GET /api/v1/topics/{id}/events` (paginated `ListForTopic`). New `QK.topicEvents(id)` query key.

## 8. Error handling & testing

**Fail-open everywhere** (matches codebase ethos): emit failures logged, never block a check; SSE hub
never blocks the emitter (drop-on-full per slow client); watcher errors fail-open like `/status`.

**Tests:**
- `events`: policy-table assertions; `Bus.Emit` fan-out with fake persist/notify/sse sinks (verifies
  each policy routes correctly); `ID` propagation.
- Dispatcher alias mapping (legacy `updated`/`error` → canonical sets).
- Progress watcher: fake `WithStatus`; assert percent-change → `progress`, seeding/100% → `completed`,
  terminal-gating drops infohash from poll set.
- SSE hub: subscribe/publish/unsubscribe, ticket consume-once + TTL expiry, `Last-Event-ID` replay.
- `topic_events` repo: record + paginated `ListForTopic` + `ListForUserSince`.
- Frontend: `EventSource` mock → `setQueryData` routing; reconnect → ticket refetch; notifier
  event-picker; timeline render.

## 9. Implementation phasing

1. **Phase 1 — event spine + history.** `events` pkg + policy + `topic_events` repo + emit points +
   dispatcher aliases + notifier event-picker UI + per-topic timeline view. Ships richer notifications
   and history with **zero transport risk**.
2. **Phase 2 — progress watcher.** `WithStatus` poller → `download.progress`/`download.completed`.
   Enables "download finished" notifications.
3. **Phase 3 — SSE transport.** Hub + ticket endpoint + nginx + frontend `EventSource` +
   retire-to-fallback polling + live check-status UI.

## 10. Risks

- **Event taxonomy is a one-way door** for notifier configs — names locked in §2/§5; renames later are
  migrations.
- **"Download finished" requires the watcher** — confirmed in scope (Phase 2).
- **Single-process in-memory hub** — fine for the single-backend deployment; document a Redis/NATS
  fan-out escape hatch before any multi-replica deploy. SSE reconnect replay via `topic_events`
  already survives a backend restart for persisted events.
- **Notification spam** — `check.started` / `download.progress` are policy-enforced live-feed-only;
  the notifier UI never offers them.
- **ghcr inline nginx drift** — the `/api/v1/events` location must be added to both gateway configs.
