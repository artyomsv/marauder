# Phase 3a — SSE Backend Infrastructure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up the server side of live updates: an in-memory per-user SSE hub that implements the Phase 1 `events.Publisher` seam, a ticket-authenticated `GET /api/v1/events` stream with `Last-Event-ID` replay, and `download.progress` emission from the watcher — so a browser can subscribe once and receive every event live.

**Architecture:** A new `internal/sse` package owns a `Hub` (per-user subscriber registry; `Publish` fans a serialized SSE frame to a user's connections, drop-on-full) and a `TicketStore` (one-time, 30s, JWT→ticket exchange so `EventSource` — which can't send `Authorization` — can authenticate). A `handlers.SSE` exposes `POST /events/ticket` (JWT-authed) and `GET /events?ticket=…` (ticket-gated, streams `text/event-stream`). `main.go` wires the hub as the bus's `Publisher` (replacing the Phase 1 nil), and the Phase 2 watcher gains `download.progress` emission on change. nginx gets a non-buffered location for the stream.

**Tech Stack:** Go 1.25 (chi, pgx v5, zerolog, uuid, crypto/rand, prometheus), nginx. No new third-party deps (SSE is plain `http.Flusher`). This is Phase 3**a** (backend); Phase 3b (frontend `EventSource` consumption + UI) is a separate plan on the same branch.

## Global Constraints

- **Builds on Phases 1+2 (same branch).** The `events.Publisher` interface already exists: `Publish(userID uuid.UUID, ev events.Event, id int64)` (`events/bus.go:38`), wired `nil` in `main.go:147`. The policy table already marks `download.progress` and `download.completed` as `SSE:true`. `repo.TopicEvents.ListForUserSince(ctx, userID, sinceID)` already exists for replay. Do not recreate these.
- **Transport = SSE.** One-directional server→client; no WebSocket. The stream endpoint is `GET /api/v1/events`, `Content-Type: text/event-stream`.
- **Auth = one-time ticket.** Browsers cannot set `Authorization` on `EventSource`. `POST /api/v1/events/ticket` (under the existing `RequireAuth` group) returns a single-use opaque token (32 random bytes, hex), bound to the caller's userID, **30s TTL**. `GET /api/v1/events?ticket=…` is registered **outside** `RequireAuth` and validates+consumes the ticket itself. A ticket is consumed on first use (deleted) and rejected if expired/unknown.
- **Per-user isolation:** the hub only ever delivers a user their own events (`Publish` is keyed by `event.UserID`; the stream subscribes the ticket's userID). Never leak another user's events.
- **Non-blocking fan-out:** each subscriber has a buffered channel; `Publish` does a non-blocking send and **drops on full** (a slow client must never block the bus or other clients). Count drops in a metric.
- **Replay on reconnect:** the browser sends `Last-Event-ID` on auto-reconnect; the handler replays persisted events with `id > Last-Event-ID` via `ListForUserSince` before going live. Only persisted events carry an `id:` line (ephemeral events like `download.progress` have `id=0` and no `id:` line, so they're never replayed — the next poll refreshes them).
- **Heartbeat:** the stream writes a `: ping\n\n` comment every **25s** to survive proxy idle timeouts.
- **SSE fully replaces polling (per the design decision):** the watcher now emits `download.progress` (Data carries `infohash`, `percent_done`, `state`) on a fast cadence so the live bar is driven by SSE. **Lower the watcher default poll interval** from `1m` to `5s` (`MARAUDER_PROGRESS_POLL_INTERVAL` envDefault). Emit progress **only when an infohash's (percent,state) changed since the last poll** (in-memory last-seen map) to bound SSE traffic. (The frontend's `/status` poll removal happens in Phase 3b.)
- **nginx (both files, keep in sync):** add a `location /api/v1/events` with `proxy_buffering off;`, `proxy_cache off;`, `proxy_http_version 1.1;`, and a long `proxy_read_timeout` (e.g. `3600s`) — in **`deploy/nginx/gateway.conf`** AND the inline copy inside **`deploy/docker-compose.ghcr.yml`** (the inline block escapes nginx `$` as `$$`).
- **Fail-open / lifecycle:** the hub never blocks; the stream handler unsubscribes and returns cleanly on request-context cancel; a marshal error on one event is logged and skipped, never killing the stream.
- **Go conventions:** tabs, gofmt, `fmt.Errorf("…: %w", err)`, consumer-side interfaces at the consumer, table-driven tests, manual fakes. Run via Docker mounting the worktree backend:
  `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go vet ./... && go test -race ./..."`
- **No AI-attribution in commits.** Imperative subject ≤72 chars. Reference `#93`.

---

## File Structure

**Create:**
- `backend/internal/sse/hub.go` — `Hub` (Publisher impl), `wireEvent` DTO + frame serialization, subscriber registry.
- `backend/internal/sse/hub_test.go`
- `backend/internal/sse/ticket.go` — `TicketStore` (Issue/Consume, TTL).
- `backend/internal/sse/ticket_test.go`
- `backend/internal/api/handlers/sse.go` — `SSE` handler: `Ticket` + `Stream`.
- `backend/internal/api/handlers/sse_test.go`
- `backend/internal/metrics` addition — SSE drop counter (in existing `metrics.go`).

**Modify:**
- `backend/cmd/server/main.go` — construct `hub`+`tickets`, pass `hub` as the bus Publisher, add to `api.Deps`.
- `backend/internal/api/router.go` — `Deps.Hub`/`Deps.Tickets`, build `sseH`, register `POST /events/ticket` (authed) + `GET /events` (public).
- `backend/internal/progress/watcher.go` — emit `download.progress` on change.
- `backend/internal/config/config.go` — lower `ProgressPollInterval` default to `5s`.
- `deploy/nginx/gateway.conf`, `deploy/docker-compose.ghcr.yml` — non-buffered SSE location.
- `CLAUDE.md`, `CHANGELOG.md`, `deploy/.env.example` — docs.

---

## Task 1: SSE Hub (Publisher impl) + frame serialization

**Files:**
- Create: `backend/internal/sse/hub.go`, `backend/internal/sse/hub_test.go`
- Modify: `backend/internal/metrics/metrics.go`

**Interfaces:**
- Consumes: `events.Event` (Phase 1).
- Produces:
  - `sse.Hub` with `NewHub(log zerolog.Logger) *Hub`.
  - `(*Hub) Publish(userID uuid.UUID, ev events.Event, id int64)` — satisfies `events.Publisher`.
  - `(*Hub) Subscribe(userID uuid.UUID) (<-chan []byte, func())` — returns a frames channel + an unsubscribe func.
  - Internal: `serializeFrame(ev events.Event, id int64) ([]byte, error)` building the SSE `id:`/`data:` frame from a `wireEvent` JSON.

- [ ] **Step 1: Add the drop metric** to `metrics.go` (use `promauto`, the established pattern):

```go
// SSEDroppedFramesTotal counts SSE frames dropped because a subscriber's
// buffer was full (slow client) — drop-on-full keeps the hub non-blocking.
var SSEDroppedFramesTotal = promauto.NewCounter(prometheus.CounterOpts{
	Name: "marauder_sse_dropped_frames_total",
	Help: "SSE frames dropped due to a full subscriber buffer.",
})
```

- [ ] **Step 2: Write the failing test** (`hub_test.go`)

```go
package sse

import (
	"bufio"
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/events"
)

func TestPublish_DeliversToSubscriberOfSameUser(t *testing.T) {
	h := NewHub(zerolog.Nop())
	uid := uuid.New()
	ch, unsub := h.Subscribe(uid)
	defer unsub()

	tid := uuid.New()
	h.Publish(uid, events.Event{UserID: uid, TopicID: &tid, Type: events.DownloadProgress, Title: "X",
		Data: map[string]any{"percent_done": 0.5}}, 0)

	select {
	case frame := <-ch:
		if !bytes.Contains(frame, []byte("download.progress")) {
			t.Errorf("frame missing type: %s", frame)
		}
		if !bytes.Contains(frame, []byte("data: ")) {
			t.Errorf("frame missing data line: %s", frame)
		}
		if bytes.Contains(frame, []byte("id:")) {
			t.Errorf("ephemeral event (id=0) must not carry an id line: %s", frame)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for frame")
	}
}

func TestPublish_PersistedEventCarriesIDLine(t *testing.T) {
	h := NewHub(zerolog.Nop())
	uid := uuid.New()
	ch, unsub := h.Subscribe(uid)
	defer unsub()
	tid := uuid.New()
	h.Publish(uid, events.Event{UserID: uid, TopicID: &tid, Type: events.DownloadCompleted, Title: "Done"}, 42)
	frame := <-ch
	sc := bufio.NewScanner(bytes.NewReader(frame))
	var hasID bool
	for sc.Scan() {
		if sc.Text() == "id: 42" {
			hasID = true
		}
	}
	if !hasID {
		t.Errorf("persisted event must carry 'id: 42': %s", frame)
	}
}

func TestPublish_OnlyToOwningUser(t *testing.T) {
	h := NewHub(zerolog.Nop())
	me, other := uuid.New(), uuid.New()
	mine, unsub1 := h.Subscribe(me)
	defer unsub1()
	theirs, unsub2 := h.Subscribe(other)
	defer unsub2()
	h.Publish(other, events.Event{UserID: other, Type: events.CheckStarted}, 0)
	select {
	case <-mine:
		t.Fatal("received another user's event")
	case <-theirs:
		// correct
	case <-time.After(500 * time.Millisecond):
		t.Fatal("owner did not receive event")
	}
}

func TestPublish_DropsOnFullBuffer_NoBlock(t *testing.T) {
	h := NewHub(zerolog.Nop())
	uid := uuid.New()
	_, unsub := h.Subscribe(uid) // never drained
	defer unsub()
	done := make(chan struct{})
	go func() {
		for i := 0; i < subscriberBuffer+50; i++ {
			h.Publish(uid, events.Event{UserID: uid, Type: events.CheckStarted}, 0)
		}
		close(done)
	}()
	select {
	case <-done: // Publish never blocked despite a full, undrained buffer
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a full subscriber buffer")
	}
}

func TestUnsubscribe_StopsDelivery(t *testing.T) {
	h := NewHub(zerolog.Nop())
	uid := uuid.New()
	ch, unsub := h.Subscribe(uid)
	unsub()
	h.Publish(uid, events.Event{UserID: uid, Type: events.CheckStarted}, 0)
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("received a frame after unsubscribe")
		}
	case <-time.After(200 * time.Millisecond):
		// also acceptable: nothing delivered
	}
	_ = context.Background()
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/sse/..."`
Expected: FAIL — `NewHub`, `subscriberBuffer` undefined.

- [ ] **Step 4: Implement** `hub.go`

```go
// Package sse provides the live Server-Sent-Events fan-out: an in-memory
// per-user Hub (the events.Publisher implementation) and a one-time ticket
// store for authenticating EventSource connections that cannot send an
// Authorization header.
package sse

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/events"
	"github.com/artyomsv/marauder/backend/internal/metrics"
)

// subscriberBuffer is the per-connection frame buffer. A connection that
// can't keep up past this many queued frames drops frames rather than
// blocking the hub.
const subscriberBuffer = 64

// subscriber is one live connection's frame queue.
type subscriber struct {
	ch chan []byte
}

// Hub fans serialized SSE frames out to a user's live connections. It is the
// events.Publisher implementation wired into the bus in Phase 3.
type Hub struct {
	mu   sync.Mutex
	subs map[uuid.UUID]map[*subscriber]struct{}
	log  zerolog.Logger
}

// NewHub constructs an empty Hub.
func NewHub(log zerolog.Logger) *Hub {
	return &Hub{subs: map[uuid.UUID]map[*subscriber]struct{}{}, log: log.With().Str("component", "sse").Logger()}
}

// Subscribe registers a connection for userID and returns its frames channel
// plus an unsubscribe func (idempotent). The channel is closed by unsubscribe.
func (h *Hub) Subscribe(userID uuid.UUID) (<-chan []byte, func()) {
	s := &subscriber{ch: make(chan []byte, subscriberBuffer)}
	h.mu.Lock()
	set, ok := h.subs[userID]
	if !ok {
		set = map[*subscriber]struct{}{}
		h.subs[userID] = set
	}
	set[s] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			h.mu.Lock()
			if set, ok := h.subs[userID]; ok {
				delete(set, s)
				if len(set) == 0 {
					delete(h.subs, userID)
				}
			}
			h.mu.Unlock()
			close(s.ch)
		})
	}
	return s.ch, unsub
}

// Publish serializes ev (with its history id) once and non-blockingly sends
// the frame to every live connection of ev's owner. A full buffer drops the
// frame (metered) rather than blocking. id<=0 means ephemeral (no id: line).
func (h *Hub) Publish(userID uuid.UUID, ev events.Event, id int64) {
	frame, err := serializeFrame(ev, id)
	if err != nil {
		h.log.Warn().Err(err).Str("type", string(ev.Type)).Msg("serialize frame failed")
		return
	}
	h.mu.Lock()
	set := h.subs[userID]
	targets := make([]*subscriber, 0, len(set))
	for s := range set {
		targets = append(targets, s)
	}
	h.mu.Unlock()
	for _, s := range targets {
		select {
		case s.ch <- frame:
		default:
			metrics.SSEDroppedFramesTotal.Inc()
		}
	}
}

// wireEvent is the JSON shape sent in each SSE data: line. The frontend
// (Phase 3b) consumes this. topic_id is omitted when nil; id is the history
// id (0 for ephemeral events).
type wireEvent struct {
	ID       int64          `json:"id"`
	Type     string         `json:"type"`
	TopicID  string         `json:"topic_id,omitempty"`
	Severity string         `json:"severity,omitempty"`
	Title    string         `json:"title,omitempty"`
	Body     string         `json:"body,omitempty"`
	Link     string         `json:"link,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

// serializeFrame renders an SSE frame. Persisted events (id>0) get an `id:`
// line so the browser's Last-Event-ID drives replay; ephemeral events don't.
func serializeFrame(ev events.Event, id int64) ([]byte, error) {
	w := wireEvent{
		ID: id, Type: string(ev.Type), Severity: ev.Severity,
		Title: ev.Title, Body: ev.Body, Link: ev.Link, Data: ev.Data,
	}
	if ev.TopicID != nil {
		w.TopicID = ev.TopicID.String()
	}
	payload, err := json.Marshal(w)
	if err != nil {
		return nil, fmt.Errorf("sse: marshal event: %w", err)
	}
	var b []byte
	if id > 0 {
		b = append(b, fmt.Sprintf("id: %d\n", id)...)
	}
	b = append(b, "data: "...)
	b = append(b, payload...)
	b = append(b, "\n\n"...)
	return b, nil
}
```

- [ ] **Step 5: Run tests + build**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go test -race ./internal/sse/..."`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/sse/hub.go backend/internal/sse/hub_test.go backend/internal/metrics/metrics.go
git commit -m "feat: add SSE hub implementing events.Publisher (#93)"
```

---

## Task 2: Ticket store

**Files:**
- Create: `backend/internal/sse/ticket.go`, `backend/internal/sse/ticket_test.go`

**Interfaces:**
- Produces:
  - `sse.TicketStore` with `NewTicketStore() *TicketStore`.
  - `(*TicketStore) Issue(userID uuid.UUID) (string, error)` — random 32-byte hex token, 30s TTL.
  - `(*TicketStore) Consume(token string) (uuid.UUID, bool)` — returns the bound userID and true on a valid, unexpired, not-yet-consumed token; deletes it; false otherwise.
  - `ticketTTL = 30 * time.Second` (package const).
  - A `now func() time.Time` field defaulting to `time.Now`, injectable for TTL tests.

- [ ] **Step 1: Write the failing test** (`ticket_test.go`)

```go
package sse

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTicket_IssueThenConsume_Once(t *testing.T) {
	ts := NewTicketStore()
	uid := uuid.New()
	tok, err := ts.Issue(uid)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	got, ok := ts.Consume(tok)
	if !ok || got != uid {
		t.Fatalf("Consume = (%v,%v), want (%v,true)", got, ok, uid)
	}
	if _, ok := ts.Consume(tok); ok {
		t.Error("ticket must be single-use")
	}
}

func TestTicket_Unknown_Rejected(t *testing.T) {
	ts := NewTicketStore()
	if _, ok := ts.Consume("nope"); ok {
		t.Error("unknown token must be rejected")
	}
}

func TestTicket_Expired_Rejected(t *testing.T) {
	ts := NewTicketStore()
	base := time.Unix(1000, 0)
	ts.now = func() time.Time { return base }
	uid := uuid.New()
	tok, _ := ts.Issue(uid)
	ts.now = func() time.Time { return base.Add(ticketTTL + time.Second) }
	if _, ok := ts.Consume(tok); ok {
		t.Error("expired ticket must be rejected")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/sse/ -run TestTicket"`
Expected: FAIL — `NewTicketStore` undefined.

- [ ] **Step 3: Implement** `ticket.go`

```go
package sse

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ticketTTL bounds how long an issued SSE ticket is valid. Short by design:
// the browser exchanges its JWT for a ticket immediately before opening the
// EventSource.
const ticketTTL = 30 * time.Second

type ticketEntry struct {
	userID  uuid.UUID
	expires time.Time
}

// TicketStore issues and consumes one-time SSE auth tickets. In-memory and
// single-process (matches the single-backend deployment).
type TicketStore struct {
	mu  sync.Mutex
	m   map[string]ticketEntry
	now func() time.Time
}

// NewTicketStore constructs an empty store.
func NewTicketStore() *TicketStore {
	return &TicketStore{m: map[string]ticketEntry{}, now: time.Now}
}

// Issue mints a single-use ticket bound to userID, valid for ticketTTL.
func (t *TicketStore) Issue(userID uuid.UUID) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("sse: ticket rand: %w", err)
	}
	tok := hex.EncodeToString(buf)
	t.mu.Lock()
	t.pruneLocked()
	t.m[tok] = ticketEntry{userID: userID, expires: t.now().Add(ticketTTL)}
	t.mu.Unlock()
	return tok, nil
}

// Consume validates and deletes a ticket, returning its userID. A second
// consume, an unknown token, or an expired token returns ok=false.
func (t *TicketStore) Consume(token string) (uuid.UUID, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.m[token]
	if !ok {
		return uuid.Nil, false
	}
	delete(t.m, token)
	if t.now().After(e.expires) {
		return uuid.Nil, false
	}
	return e.userID, true
}

// pruneLocked drops expired tickets; caller holds the lock.
func (t *TicketStore) pruneLocked() {
	now := t.now()
	for k, e := range t.m {
		if now.After(e.expires) {
			delete(t.m, k)
		}
	}
}
```

- [ ] **Step 4: Run tests + commit**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/backend:/backend" -w //backend golang:1.25 sh -c "go test -race ./internal/sse/..."`
Expected: PASS.

```bash
git add backend/internal/sse/ticket.go backend/internal/sse/ticket_test.go
git commit -m "feat: add one-time SSE ticket store (#93)"
```

---

## Task 3: SSE handler (ticket + stream) + routes

**Files:**
- Create: `backend/internal/api/handlers/sse.go`, `backend/internal/api/handlers/sse_test.go`
- Modify: `backend/internal/api/router.go`

**Interfaces:**
- Consumes: `sse.Hub.Subscribe`, `sse.TicketStore.Issue/Consume`, `repo.TopicEvents.ListForUserSince`, `currentUserID`, `writeJSON`, `problem`, `events`/`sse.serializeFrame` (via the hub — see note).
- Produces:
  - `handlers.SSE{ Hub sseHub; Tickets sseTickets; Events sseEventLister; HeartbeatInterval time.Duration; BaseURL string }`.
  - Consumer-side interfaces (in handlers): `sseHub{ Subscribe(uuid.UUID) (<-chan []byte, func()) }`; `sseTickets{ Issue(uuid.UUID) (string, error); Consume(string) (uuid.UUID, bool) }`; `sseEventLister{ ListForUserSince(ctx, uuid.UUID, int64) ([]*domain.TopicEvent, error) }`.
  - `(*SSE) Ticket(w, r)` — `POST /events/ticket`, JWT-authed, returns `{"ticket": "<hex>"}`.
  - `(*SSE) Stream(w, r)` — `GET /events?ticket=…`, ticket-gated, streams `text/event-stream`.

> Note on replay framing: `Stream` needs to render replayed `domain.TopicEvent`s as SSE frames in the same shape the hub uses. Add a small exported helper to the `sse` package: `func FrameFromTopicEvent(e *domain.TopicEvent) []byte` that builds the same `id:`/`data:` frame from a persisted row (id = `e.ID`, type = `e.EventType`, topic_id = `e.TopicID`, severity/title/data from the row). Implement it in `sse/hub.go` next to `serializeFrame` (reuse the `wireEvent` marshal). The handler imports `sse` for this.

- [ ] **Step 1: Add `FrameFromTopicEvent` to `sse/hub.go`** (and a tiny test in `hub_test.go`):

```go
// FrameFromTopicEvent renders a persisted history row as an SSE frame for
// Last-Event-ID replay — same wire shape as a live frame.
func FrameFromTopicEvent(e *domain.TopicEvent) []byte {
	w := wireEvent{
		ID: e.ID, Type: e.EventType, TopicID: e.TopicID.String(),
		Severity: e.Severity, Title: e.Message, Data: e.Data,
	}
	payload, err := json.Marshal(w)
	if err != nil {
		return nil
	}
	b := []byte(fmt.Sprintf("id: %d\ndata: ", e.ID))
	b = append(b, payload...)
	return append(b, "\n\n"...)
}
```

(test: build a `domain.TopicEvent{ID:7,...}` and assert the frame contains `id: 7` and the type.)

- [ ] **Step 2: Write the failing handler test** (`sse_test.go`)

```go
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

type fakeHub struct{ ch chan []byte }

func (f *fakeHub) Subscribe(_ uuid.UUID) (<-chan []byte, func()) { return f.ch, func() {} }

type fakeTickets struct {
	issued string
	uid    uuid.UUID
	valid  bool
}

func (f *fakeTickets) Issue(uid uuid.UUID) (string, error) { f.uid = uid; return f.issued, nil }
func (f *fakeTickets) Consume(tok string) (uuid.UUID, bool) {
	if f.valid && tok == f.issued {
		return f.uid, true
	}
	return uuid.Nil, false
}

type fakeEventLister struct{}

func (fakeEventLister) ListForUserSince(_ context.Context, _ uuid.UUID, _ int64) ([]*domain.TopicEvent, error) {
	return nil, nil
}

func TestSSE_Stream_RejectsMissingTicket(t *testing.T) {
	h := &SSE{Hub: &fakeHub{ch: make(chan []byte, 1)}, Tickets: &fakeTickets{}, Events: fakeEventLister{}, HeartbeatInterval: time.Hour}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	rec := httptest.NewRecorder()
	h.Stream(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing ticket: status = %d, want 401", rec.Code)
	}
}

func TestSSE_Stream_ValidTicket_StreamsFrames(t *testing.T) {
	hub := &fakeHub{ch: make(chan []byte, 1)}
	tickets := &fakeTickets{issued: "tok123", uid: uuid.New(), valid: true}
	h := &SSE{Hub: hub, Tickets: tickets, Events: fakeEventLister{}, HeartbeatInterval: time.Hour}

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events?ticket=tok123", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() { h.Stream(rec, req); close(done) }()

	hub.ch <- []byte("data: {\"type\":\"check.started\"}\n\n")
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, "check.started") {
		t.Fatalf("stream body missing pushed frame: %q", body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
}
```

> Implementer note: `httptest.ResponseRecorder` implements `http.Flusher` (its `Flush()` sets `Flushed`), so `Stream` can type-assert the flusher and the test works without a live server. Confirm by reading the recorder docs; if `Flush` isn't satisfied in this Go version, switch the test to `httptest.NewServer` + an `http.Client` reading the body stream.

- [ ] **Step 3: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/api/handlers/ -run TestSSE"`
Expected: FAIL — `SSE` undefined.

- [ ] **Step 4: Implement** `sse.go`

```go
package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/problem"
	"github.com/artyomsv/marauder/backend/internal/sse"
)

type sseHub interface {
	Subscribe(userID uuid.UUID) (<-chan []byte, func())
}
type sseTickets interface {
	Issue(userID uuid.UUID) (string, error)
	Consume(token string) (uuid.UUID, bool)
}
type sseEventLister interface {
	ListForUserSince(ctx context.Context, userID uuid.UUID, sinceID int64) ([]*domain.TopicEvent, error)
}

// SSE serves the live event stream and its ticket exchange.
type SSE struct {
	Hub               sseHub
	Tickets           sseTickets
	Events            sseEventLister
	HeartbeatInterval time.Duration
	BaseURL           string
}

// Ticket handles POST /events/ticket. JWT-authed (registered under
// RequireAuth); returns a single-use token the browser puts on the
// EventSource URL.
func (h *SSE) Ticket(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	tok, err := h.Tickets.Issue(uid)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("ticket: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": tok})
}

// Stream handles GET /events?ticket=…. Ticket-gated (registered OUTSIDE
// RequireAuth). Streams text/event-stream until the client disconnects.
func (h *SSE) Stream(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.Tickets.Consume(r.URL.Query().Get("ticket"))
	if !ok {
		http.Error(w, "invalid or expired ticket", http.StatusUnauthorized)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // belt-and-suspenders vs proxy buffering
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Replay missed persisted events on reconnect.
	if lastID, err := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64); err == nil && lastID > 0 {
		if rows, lerr := h.Events.ListForUserSince(r.Context(), uid, lastID); lerr == nil {
			for _, e := range rows {
				if frame := sse.FrameFromTopicEvent(e); frame != nil {
					_, _ = w.Write(frame)
				}
			}
			flusher.Flush()
		}
	}

	frames, unsub := h.Hub.Subscribe(uid)
	defer unsub()

	hb := time.NewTicker(h.HeartbeatInterval)
	defer hb.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case frame, ok := <-frames:
			if !ok {
				return
			}
			if _, err := w.Write(frame); err != nil {
				return
			}
			flusher.Flush()
		case <-hb.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
```

- [ ] **Step 5: Register routes** in `router.go`. Add to `Deps`:

```go
	Hub     *sse.Hub
	Tickets *sse.TicketStore
```

(import `"github.com/artyomsv/marauder/backend/internal/sse"`.) Build the handler near the others:

```go
	sseH := &handlers.SSE{
		Hub:               d.Hub,
		Tickets:           d.Tickets,
		Events:            d.TopicEvents,
		HeartbeatInterval: 25 * time.Second,
		BaseURL:           d.Cfg.PublicBaseURL,
	}
```

Register `Stream` in the **public** `/api/v1` section (next to `system/info`, OUTSIDE the RequireAuth group), and `Ticket` **inside** the authed group (next to `auth/me`):

```go
	// public (ticket-gated in the handler):
	r.Get("/events", sseH.Stream)
	// inside r.Group(RequireAuth):
	r.Post("/events/ticket", sseH.Ticket)
```

- [ ] **Step 6: Run handler tests + build**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go test -race ./internal/api/handlers/ -run TestSSE && go test ./internal/sse/..."`
Expected: PASS. (Whole-module `go build` will fail only in `main.go` until Task 4 supplies `Hub`/`Tickets` — note it, like the Phase 1 Task 5 break.)

- [ ] **Step 7: Commit**

```bash
git add backend/internal/api/handlers/sse.go backend/internal/api/handlers/sse_test.go backend/internal/sse/hub.go backend/internal/sse/hub_test.go backend/internal/api/router.go
git commit -m "feat: add SSE stream + ticket endpoints (#93)"
```

---

## Task 4: Wire the hub into the bus + server startup

**Files:**
- Modify: `backend/cmd/server/main.go`

**Interfaces:**
- Consumes: `sse.NewHub`, `sse.NewTicketStore`; `events.New` (hub as Publisher); `api.Deps.Hub/Tickets`.

- [ ] **Step 1: Construct the hub + tickets and wire them** — in `main.go`, replace the bus construction (`bus := events.New(topicEventsRepo, disp, nil, logger)`) with:

```go
	hub := sse.NewHub(logger)
	tickets := sse.NewTicketStore()
	bus := events.New(topicEventsRepo, disp, hub, logger) // hub is the live SSE publisher
```

Add `Hub: hub, Tickets: tickets,` to the `api.Deps{…}` literal. Add the import `"github.com/artyomsv/marauder/backend/internal/sse"`.

- [ ] **Step 2: Build + full suite**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go vet ./... && go test -race ./..."`
Expected: PASS (the Task 3 main.go build break is now resolved).

- [ ] **Step 3: Commit**

```bash
git add backend/cmd/server/main.go
git commit -m "feat: wire SSE hub as the events publisher (#93)"
```

---

## Task 5: Emit download.progress from the watcher (on change)

**Files:**
- Modify: `backend/internal/progress/watcher.go`, `backend/internal/progress/watcher_test.go`, `backend/internal/config/config.go`

**Interfaces:**
- Consumes: `events.DownloadProgress`, the existing `Watcher` (Phase 2).
- Produces: the watcher emits `events.DownloadProgress` with `Data{"infohash": string, "percent_done": float64, "state": string}` for each in-flight delivery whose (percent, state) changed since the last poll. Adds an in-memory `lastSeen map[string]progressKey` field guarded by the watcher's existing single-goroutine loop (no new mutex needed — `poll` runs serially).

- [ ] **Step 1: Write the failing test** — add to `watcher_test.go`:

```go
func TestPoll_DownloadingEmitsProgressOnChange(t *testing.T) {
	cid := uuid.New()
	d := inflight(cid)
	del := &fakeDeliveries{inflight: []*domain.InFlightDelivery{d}, markWon: true}
	emit := &fakeEmitter{}
	st := fakeStatus{statuses: []registry.TorrentStatus{{Hash: "abc123", PercentDone: 0.5, State: registry.StateDownloading}}}
	w := newTestWatcher(t, del, emit, st)

	w.poll(context.Background()) // first sight → emit progress
	if got := countType(emit, events.DownloadProgress); got != 1 {
		t.Fatalf("first poll progress emits = %d, want 1", got)
	}
	d0 := lastProgress(emit)
	if d0["percent_done"] != 0.5 || d0["state"] != registry.StateDownloading || d0["infohash"] != "abc123" {
		t.Fatalf("progress data wrong: %+v", d0)
	}

	emit.events = nil
	w.poll(context.Background()) // unchanged → no re-emit
	if got := countType(emit, events.DownloadProgress); got != 0 {
		t.Fatalf("unchanged poll progress emits = %d, want 0", got)
	}
}

// countType / lastProgress are small test helpers — add them to the test file:
func countType(e *fakeEmitter, ty events.Type) int {
	n := 0
	for _, ev := range e.events {
		if ev.Type == ty {
			n++
		}
	}
	return n
}
func lastProgress(e *fakeEmitter) map[string]any {
	for i := len(e.events) - 1; i >= 0; i-- {
		if e.events[i].Type == events.DownloadProgress {
			return e.events[i].Data
		}
	}
	return nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/progress/ -run TestPoll_Downloading"`
Expected: FAIL — no progress emitted.

- [ ] **Step 3: Implement** — add the last-seen map + emission. In `watcher.go`:

Add a field to `Watcher`: `lastSeen map[string]progressKey` (init in `New`: `lastSeen: map[string]progressKey{}`), and the type:

```go
type progressKey struct {
	percent float64
	state   string
}
```

In `checkClient`, replace the status loop. **Completion takes precedence over progress** — a finished torrent emits only `download.completed`, never a progress event (this preserves the Phase 2 completion tests, which assert exactly one event). A still-downloading torrent emits `download.progress` only when its (percent,state) changed since the last poll:

```go
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
```

> Implementer note: this replaces the previous two-loop structure (a `done` set + a `for _, d := range rc.deliveries` completion loop). Preserve the existing fail-open behavior and the `complete()` dedup (emit only on the won transition). Because completion `continue`s before the progress branch, the Phase 2 tests `TestPoll_Seeding_MarksCompletedAndEmits` and `TestPoll_LostTransition_NoEmit` (both use seeding) still see exactly their completion event and no progress.

- [ ] **Step 3b: Update the one Phase 2 test that now changes behavior** — `TestPoll_StillDownloading_NoMarkNoEmit` asserted *zero* events for a downloading torrent; a downloading torrent now emits one `download.progress`. Update it to assert no completion but one progress event (rename for accuracy):

```go
func TestPoll_StillDownloading_EmitsProgressNotCompletion(t *testing.T) {
	cid := uuid.New()
	del := &fakeDeliveries{inflight: []*domain.InFlightDelivery{inflight(cid)}, markWon: true}
	emit := &fakeEmitter{}
	st := fakeStatus{statuses: []registry.TorrentStatus{{Hash: "abc123", PercentDone: 0.5, State: registry.StateDownloading}}}
	w := newTestWatcher(t, del, emit, st)
	w.poll(context.Background())
	if len(del.completed) != 0 {
		t.Fatalf("downloading torrent must not complete: %v", del.completed)
	}
	if countType(emit, events.DownloadProgress) != 1 || countType(emit, events.DownloadCompleted) != 0 {
		t.Fatalf("expected one progress, no completion: %+v", emit.events)
	}
}
```

- [ ] **Step 4: Lower the poll-interval default** in `config.go`: change `ProgressPollInterval` envDefault from `"1m"` to `"5s"` (now that the watcher drives live progress, not just completion). Update its test (`TestConfig_ProgressDefaults`) to expect `5 * time.Second`.

- [ ] **Step 5: Run tests + build**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go test -race ./internal/progress/... ./internal/config/..."`
Expected: PASS (Phase 2 completion tests + the new progress test).

- [ ] **Step 6: Commit**

```bash
git add backend/internal/progress/watcher.go backend/internal/progress/watcher_test.go backend/internal/config/config.go
git commit -m "feat: emit download.progress on change for the live feed (#93)"
```

---

## Task 6: nginx — non-buffered SSE location (both configs)

**Files:**
- Modify: `deploy/nginx/gateway.conf`, `deploy/docker-compose.ghcr.yml`

- [ ] **Step 1: Add the SSE location** to `deploy/nginx/gateway.conf`, **before** the existing `location /api/` block (nginx matches the most specific prefix; an explicit `location /api/v1/events` wins over `/api/`):

```nginx
    # --- SSE live event stream (no buffering, long-lived) ---
    location /api/v1/events {
        proxy_pass         http://marauder_backend;
        proxy_http_version 1.1;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Real-IP         $remote_addr;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
        proxy_set_header   X-Request-ID      $request_id;
        proxy_buffering    off;
        proxy_cache        off;
        proxy_read_timeout 3600s;
    }
```

- [ ] **Step 2: Mirror it into the inline config** inside `deploy/docker-compose.ghcr.yml` — find the `configs:` block that inlines `gateway.conf` and add the same location, with nginx `$` escaped as `$$` (e.g. `$$host`, `$$remote_addr`, `$$proxy_add_x_forwarded_for`, `$$scheme`, `$$request_id`). Keep it byte-for-byte consistent with Step 1 modulo the `$$` escaping (the keep-in-sync rule).

- [ ] **Step 3: Validate** the compose file parses and (best-effort) the nginx block is well-formed:

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/deploy:/deploy" -w //deploy mikefarah/yq:4 e '.configs' docker-compose.ghcr.yml`
Expected: prints the configs block including the new location (sanity that YAML still parses).

- [ ] **Step 4: Commit**

```bash
git add deploy/nginx/gateway.conf deploy/docker-compose.ghcr.yml
git commit -m "feat: nginx non-buffered location for the SSE stream (#93)"
```

---

## Task 7: Docs

**Files:**
- Modify: `CLAUDE.md`, `CHANGELOG.md`, `deploy/.env.example`

- [ ] **Step 1: `CLAUDE.md`** — add an `sse` row to the backend package table:

```
| **`sse`** | live Server-Sent-Events fan-out: in-memory per-user `Hub` (the `events.Publisher` impl, wired into the bus) + one-time 30s `TicketStore`. `POST /api/v1/events/ticket` (JWT-authed) exchanges the access token for a single-use ticket; `GET /api/v1/events?ticket=…` (ticket-gated, public route) streams `text/event-stream` with `Last-Event-ID` replay (persisted events) + 25s heartbeats. Drop-on-full per slow client. Single-process (Redis/NATS fan-out is the multi-replica escape hatch) |
```

Update the `events` row: SSE publisher is now the `sse.Hub` (no longer nil); `download.progress` is emitted by the watcher on change and pushed over SSE. Update the `progress` row: also emits `download.progress` (Data: infohash/percent_done/state) on change; default poll interval lowered to `5s` for live progress. Note the new env default in the env-vars section.

- [ ] **Step 2: `CHANGELOG.md`** — add under `[Unreleased]` → `### Added`:

```markdown
- Live updates over Server-Sent Events: the backend now streams events (checks, releases, download progress/finished) to the UI via `GET /api/v1/events` with one-time ticket auth and reconnect replay (#93).
```

- [ ] **Step 3: `deploy/.env.example`** — the `MARAUDER_PROGRESS_POLL_INTERVAL` default changes to `5s`; update the example value + comment to note it now drives live progress.

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md CHANGELOG.md deploy/.env.example
git commit -m "docs: document the SSE live-event backend (#93)"
```

---

## Final verification

- [ ] **Full backend suite:** `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go vet ./... && go test -race ./..."` → all pass.
- [ ] **Manual smoke (optional, dev stack):** `curl -N -H "Last-Event-ID: 0" "http://localhost:34080/api/v1/events?ticket=$(curl -s -X POST -H "Authorization: Bearer $JWT" http://localhost:34080/api/v1/events/ticket | jq -r .ticket)"` → holds open, receives `: ping` heartbeats and live frames when a check runs.

---

## Spec coverage (Phase 3 backend portion of `2026-06-25-event-types-and-live-updates-design.md` §6.6, §7, §8)

- §6.6 SSE hub + endpoint, per-user filter, heartbeat, `Last-Event-ID` replay → Tasks 1, 3. Ticket auth (`POST /events/ticket`, 30s, one-time) → Tasks 2, 3. nginx `proxy_buffering off` in both configs → Task 6. Wire hub as bus Publisher → Task 4.
- §8 Phase-3 "tee events to a hub" + "download.progress emission" → Tasks 4, 5.
- **Out of scope (Phase 3b, separate plan, same branch):** frontend `useEventStream` provider, routing events into the React Query cache, retiring the `/status` poll to fallback, and the live check-status UI (`next_check_at` countdown / "checking…" pulse). Phase 3a delivers a curl-testable stream that 3b consumes.
