# Phase 1 — Event Spine + History Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Introduce a typed event taxonomy emitted from one place (`events.Bus`) that tees to the `topic_events` history table and the existing notifier dispatcher, expose per-event notifier subscriptions in the UI, and add a read-only per-topic event timeline.

**Architecture:** A new `internal/events` package owns the canonical `Type`, the `Event` struct, a per-type **policy table** (persist / notifiable / sse), and a `Bus.Emit` that fans each event to its sinks. The scheduler's existing direct `notifier.Send/SendVia("updated"|"error", …)` calls are replaced by `bus.Emit(typed Event)`. The dispatcher's `subscribed()` gains a legacy alias map so existing `['updated','error']` notifier rows keep working. The SSE sink is a no-op seam in Phase 1 (wired to the hub in Phase 3).

**Tech Stack:** Go 1.25 (chi, pgx v5, zerolog, uuid), React 19.2 + Vite + Tailwind 4 + shadcn + React Query, Vitest.

## Global Constraints

- **No new DB migration.** The `topic_events` table already exists (`db/migrations/0001_initial_schema.sql:106`): `{id BIGSERIAL, topic_id UUID, user_id UUID, event_type TEXT, severity TEXT CHECK(info|warn|error), message TEXT, data JSONB, created_at TIMESTAMPTZ}`.
- **Canonical event type strings (one-way door — do not rename):** `topic.added`, `check.started`, `check.completed`, `release.found`, `download.submitted`, `download.progress`, `download.completed`, `check.failed`, `session.expired`.
- **Notifier-subscribable subset (the only 4 the UI offers):** `release.found`, `download.submitted`, `download.completed`, and the error pair `check.failed`/`session.expired`. `check.started`, `download.progress`, `topic.added`, `check.completed` are never notifier-subscribable.
- **Phase 1 does NOT emit `download.progress` or `download.completed`** (those come from the Phase 2 watcher) and does NOT build the SSE hub (Phase 3). The policy table and SSE seam are defined now; the SSE sink is nil.
- **Backward compat (broad alias):** legacy `"updated"` ⇒ {`release.found`, `download.submitted`, `download.completed`}; legacy `"error"` ⇒ {`check.failed`, `session.expired`}.
- **Best-effort/fail-open everywhere:** an emit failure (persist or notify) is logged and never blocks a check, matching the existing `recordDelivery` ethos.
- **Go:** tabs, `gofmt`, wrap errors `fmt.Errorf("…: %w", err)`, table-driven tests `package <pkg>` for white-box, `t.Helper()` in fakes. Run `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go vet ./... && go test -race ./..."`.
- **Frontend:** `interface` for object shapes, `@/` alias, React Query keys from `QK` only, `useT()` for copy, `lucide-react` icons, max 250 lines/file. Run `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test && npm run build"`.
- **No AI-attribution in commits.** Imperative subject ≤72 chars. Reference `#93`.

---

## File Structure

**Backend (new):**
- `backend/internal/events/event.go` — `Type` consts, `Event` struct, `Policy`, `policyFor(Type)`.
- `backend/internal/events/event_test.go` — policy table assertions.
- `backend/internal/events/bus.go` — `Bus`, consumer-side seams (`notifier`, `recorder`, `publisher`), `New`, `Emit`.
- `backend/internal/events/bus_test.go` — fan-out routing with fakes.
- `backend/internal/db/repo/topic_events.go` — `TopicEvents` repo (`Record`, `ListForTopic`, `ListForUserSince`).
- `backend/internal/db/repo/topic_events_test.go` — repo SQL/scan via a fake pool.
- `backend/internal/api/handlers/topic_events.go` — `GET /topics/{id}/events` handler.
- `backend/internal/api/handlers/topic_events_test.go`.

**Backend (modified):**
- `backend/internal/notify/dispatcher.go` — alias map in `subscribed()`.
- `backend/internal/notify/dispatcher_test.go` — alias cases (create if absent).
- `backend/internal/scheduler/scheduler.go` — swap `eventNotifier` for an `emitter` seam; emit typed events.
- `backend/internal/scheduler/scheduler_test.go` — assert emitted events.
- `backend/internal/api/handlers/topics.go` — emit `topic.added` in `Create`.
- `backend/internal/api/handlers/notifiers.go` — canonical default events + validation.
- `backend/internal/api/router.go` — register `GET /topics/{id}/events`.
- `backend/cmd/server/main.go` — construct `events.Bus`, wire into scheduler + topics handler.

**Frontend (new):**
- `frontend/src/lib/events.ts` — canonical event constants + `EVENT_LABELS` map.
- `frontend/src/components/topics/TopicEventsTimeline.tsx` — read-only timeline.
- `frontend/src/components/topics/TopicEventsTimeline.test.tsx`.
- `frontend/src/components/notifiers/EventPicker.tsx` — shared checkbox group (Add + Edit).
- `frontend/src/components/notifiers/EventPicker.test.tsx`.

**Frontend (modified):**
- `frontend/src/lib/api.ts` — `topicEvents(id)` method + `TopicEvent` interface.
- `frontend/src/lib/queryKeys.ts` — `topicEvents(id)` key.
- `frontend/src/pages/Notifiers.tsx` — use `EventPicker` in `AddNotifierCard`; update `eventLabel`.
- `frontend/src/components/notifiers/EditNotifierCard.tsx` — use `EventPicker`.
- `frontend/src/i18n/en.ts` + `frontend/src/i18n/ru.ts` — event labels + timeline copy.

---

## Task 1: events package — Type constants + policy table

**Files:**
- Create: `backend/internal/events/event.go`
- Test: `backend/internal/events/event_test.go`

**Interfaces:**
- Produces: `events.Type` (string), the nine `Type` consts, `events.Policy{Persist, Notifiable, SSE bool}`, `events.PolicyFor(t Type) Policy`, `events.NotifiableTypes() []Type`.

- [ ] **Step 1: Write the failing test**

```go
package events

import "testing"

func TestPolicyFor(t *testing.T) {
	tests := []struct {
		typ                       Type
		persist, notify, sse      bool
	}{
		{TopicAdded, true, false, true},
		{CheckStarted, false, false, true},
		{CheckCompleted, false, false, true},
		{ReleaseFound, true, true, true},
		{DownloadSubmitted, true, true, true},
		{DownloadProgress, false, false, true},
		{DownloadCompleted, true, true, true},
		{CheckFailed, true, true, true},
		{SessionExpired, true, true, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.typ), func(t *testing.T) {
			p := PolicyFor(tt.typ)
			if p.Persist != tt.persist || p.Notifiable != tt.notify || p.SSE != tt.sse {
				t.Errorf("PolicyFor(%s) = %+v, want persist=%v notify=%v sse=%v",
					tt.typ, p, tt.persist, tt.notify, tt.sse)
			}
		})
	}
}

func TestPolicyFor_Unknown_DefaultsToInert(t *testing.T) {
	p := PolicyFor(Type("nope.nope"))
	if p.Persist || p.Notifiable || p.SSE {
		t.Errorf("unknown type should be inert, got %+v", p)
	}
}

func TestNotifiableTypes(t *testing.T) {
	got := NotifiableTypes()
	want := map[Type]bool{ReleaseFound: true, DownloadSubmitted: true, DownloadCompleted: true, CheckFailed: true, SessionExpired: true}
	if len(got) != len(want) {
		t.Fatalf("got %d notifiable types, want %d", len(got), len(want))
	}
	for _, ty := range got {
		if !want[ty] {
			t.Errorf("unexpected notifiable type %s", ty)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/events/..."`
Expected: FAIL — package/identifiers undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// Package events defines the canonical event taxonomy emitted across the
// backend and the per-type policy that decides, for each event, whether it
// is persisted to history, eligible for notifier fan-out, and pushed over
// the live SSE feed. It is the single source of truth for "what happens to
// an event".
package events

// Type is a canonical event-type identifier. These strings are a one-way
// door: notifier subscriptions persist them, so they must not be renamed.
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

// Policy describes the routing for one event type.
type Policy struct {
	Persist    bool // write a topic_events history row
	Notifiable bool // eligible for notifier fan-out (subject to subscription)
	SSE        bool // push over the live feed
}

var policies = map[Type]Policy{
	TopicAdded:        {Persist: true, Notifiable: false, SSE: true},
	CheckStarted:      {Persist: false, Notifiable: false, SSE: true},
	CheckCompleted:    {Persist: false, Notifiable: false, SSE: true},
	ReleaseFound:      {Persist: true, Notifiable: true, SSE: true},
	DownloadSubmitted: {Persist: true, Notifiable: true, SSE: true},
	DownloadProgress:  {Persist: false, Notifiable: false, SSE: true},
	DownloadCompleted: {Persist: true, Notifiable: true, SSE: true},
	CheckFailed:       {Persist: true, Notifiable: true, SSE: true},
	SessionExpired:    {Persist: true, Notifiable: true, SSE: true},
}

// PolicyFor returns the routing policy for t. An unknown type is inert
// (no persist, no notify, no SSE) — a defensive default.
func PolicyFor(t Type) Policy { return policies[t] }

// NotifiableTypes returns the event types a notifier may subscribe to.
func NotifiableTypes() []Type {
	var out []Type
	for t, p := range policies {
		if p.Notifiable {
			out = append(out, t)
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/events/..."`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/events/event.go backend/internal/events/event_test.go
git commit -m "feat: add event taxonomy and per-type policy table (#93)"
```

---

## Task 2: events.Bus — fan-out to recorder + notifier + sse seam

**Files:**
- Create: `backend/internal/events/bus.go`
- Test: `backend/internal/events/bus_test.go`

**Interfaces:**
- Consumes: `events.Type`, `events.PolicyFor` (Task 1); `domain.Message`, `domain.TopicEvent`.
- Produces:
  - `events.Event` struct: `{UserID uuid.UUID; TopicID *uuid.UUID; NotifierID *uuid.UUID; Type Type; Severity, Title, Body, Link string; Data map[string]any}`.
  - Seams: `Recorder interface { Record(ctx, *domain.TopicEvent) (int64, error) }`, `Notifier interface { SendVia(ctx, userID uuid.UUID, notifierID *uuid.UUID, event string, msg domain.Message) int }`, `Publisher interface { Publish(userID uuid.UUID, ev Event, id int64) }`.
  - `func New(rec Recorder, notif Notifier, pub Publisher, log zerolog.Logger) *Bus`.
  - `func (b *Bus) Emit(ctx context.Context, ev Event)`.

- [ ] **Step 1: Write the failing test**

```go
package events

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

type fakeRecorder struct {
	got    *domain.TopicEvent
	retID  int64
	called int
}

func (f *fakeRecorder) Record(_ context.Context, e *domain.TopicEvent) (int64, error) {
	f.called++
	f.got = e
	return f.retID, nil
}

type fakeNotifier struct {
	event      string
	notifierID *uuid.UUID
	called     int
}

func (f *fakeNotifier) SendVia(_ context.Context, _ uuid.UUID, nid *uuid.UUID, event string, _ domain.Message) int {
	f.called++
	f.event = event
	f.notifierID = nid
	return 1
}

type fakePublisher struct {
	called int
	id     int64
}

func (f *fakePublisher) Publish(_ uuid.UUID, _ Event, id int64) { f.called++; f.id = id }

func newBus(t *testing.T) (*Bus, *fakeRecorder, *fakeNotifier, *fakePublisher) {
	t.Helper()
	rec := &fakeRecorder{retID: 42}
	notif := &fakeNotifier{}
	pub := &fakePublisher{}
	return New(rec, notif, pub, zerolog.Nop()), rec, notif, pub
}

func TestEmit_ReleaseFound_PersistsNotifiesAndPublishes(t *testing.T) {
	bus, rec, notif, pub := newBus(t)
	tid := uuid.New()
	bus.Emit(context.Background(), Event{
		UserID: uuid.New(), TopicID: &tid, Type: ReleaseFound,
		Severity: "info", Title: "X", Body: "Y",
	})
	if rec.called != 1 {
		t.Errorf("recorder called %d, want 1", rec.called)
	}
	if rec.got.EventType != string(ReleaseFound) {
		t.Errorf("recorded type %s, want release.found", rec.got.EventType)
	}
	if notif.called != 1 || notif.event != string(ReleaseFound) {
		t.Errorf("notifier called %d event %q", notif.called, notif.event)
	}
	if pub.called != 1 || pub.id != 42 {
		t.Errorf("publisher called %d id %d, want 1/42", pub.called, pub.id)
	}
}

func TestEmit_CheckStarted_PublishesOnly(t *testing.T) {
	bus, rec, notif, pub := newBus(t)
	tid := uuid.New()
	bus.Emit(context.Background(), Event{UserID: uuid.New(), TopicID: &tid, Type: CheckStarted})
	if rec.called != 0 {
		t.Errorf("recorder called %d, want 0", rec.called)
	}
	if notif.called != 0 {
		t.Errorf("notifier called %d, want 0", notif.called)
	}
	if pub.called != 1 {
		t.Errorf("publisher called %d, want 1", pub.called)
	}
	if pub.id != 0 {
		t.Errorf("ephemeral publish id %d, want 0", pub.id)
	}
}

func TestEmit_NilSeams_NoPanic(t *testing.T) {
	bus := New(nil, nil, nil, zerolog.Nop())
	tid := uuid.New()
	bus.Emit(context.Background(), Event{UserID: uuid.New(), TopicID: &tid, Type: ReleaseFound})
	// no panic = pass
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/events/..."`
Expected: FAIL — `Bus`, `New`, `Event` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
package events

import (
	"context"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// Event is one thing that happened, tagged with its canonical Type.
type Event struct {
	UserID     uuid.UUID
	TopicID    *uuid.UUID // nil for non-topic events (e.g. credential session.expired)
	NotifierID *uuid.UUID // per-topic notifier override; nil => the user's default notifiers
	Type       Type
	Severity   string // info | warn | error (defaults to "info" when empty)
	Title      string
	Body       string
	Link       string
	Data       map[string]any
}

// Recorder persists an event to the topic_events history table.
type Recorder interface {
	Record(ctx context.Context, e *domain.TopicEvent) (int64, error)
}

// Notifier fans an event out to a user's notifier plugins. Implemented by
// *notify.Dispatcher. The scheduler already proved this seam shape.
type Notifier interface {
	SendVia(ctx context.Context, userID uuid.UUID, notifierID *uuid.UUID, event string, msg domain.Message) int
}

// Publisher pushes an event onto the live SSE feed. Phase 1 wires nil; the
// Phase 3 hub implements it. id is the persisted history id (0 if ephemeral).
type Publisher interface {
	Publish(userID uuid.UUID, ev Event, id int64)
}

// Bus is the single event->sinks fan-out point. Every sink is optional
// (nil-safe) so the bus is cheap to construct in tests and across phases.
type Bus struct {
	rec   Recorder
	notif Notifier
	pub   Publisher
	log   zerolog.Logger
}

// New constructs a Bus. Any of rec/notif/pub may be nil.
func New(rec Recorder, notif Notifier, pub Publisher, log zerolog.Logger) *Bus {
	return &Bus{rec: rec, notif: notif, pub: pub, log: log.With().Str("component", "events").Logger()}
}

// Emit routes ev to its policy-selected sinks. Best-effort: every sink
// failure is logged and never propagated — emitting an event must never
// break the caller's flow.
func (b *Bus) Emit(ctx context.Context, ev Event) {
	if ev.Severity == "" {
		ev.Severity = "info"
	}
	p := PolicyFor(ev.Type)

	var id int64
	if p.Persist && b.rec != nil && ev.TopicID != nil {
		rec := &domain.TopicEvent{
			TopicID:   *ev.TopicID,
			UserID:    ev.UserID,
			EventType: string(ev.Type),
			Severity:  ev.Severity,
			Message:   ev.Title,
			Data:      ev.Data,
		}
		got, err := b.rec.Record(ctx, rec)
		if err != nil {
			b.log.Warn().Err(err).Str("type", string(ev.Type)).Msg("events: record failed")
		} else {
			id = got
		}
	}

	if p.Notifiable && b.notif != nil {
		b.notif.SendVia(ctx, ev.UserID, ev.NotifierID, string(ev.Type), domain.Message{
			Title: ev.Title, Body: ev.Body, Link: ev.Link,
		})
	}

	if p.SSE && b.pub != nil {
		b.pub.Publish(ev.UserID, ev, id)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/events/..."`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/events/bus.go backend/internal/events/bus_test.go
git commit -m "feat: add events.Bus fan-out to history, notifier, sse (#93)"
```

---

## Task 3: TopicEvents repo

**Files:**
- Create: `backend/internal/db/repo/topic_events.go`
- Test: `backend/internal/db/repo/topic_events_test.go`

**Interfaces:**
- Consumes: `domain.TopicEvent`.
- Produces:
  - `type TopicEvents struct{…}`, `func NewTopicEvents(pool *pgxpool.Pool) *TopicEvents`.
  - `Record(ctx, *domain.TopicEvent) (int64, error)` — INSERT … RETURNING id.
  - `ListForTopic(ctx, topicID, userID uuid.UUID, limit int, beforeID int64) ([]*domain.TopicEvent, error)` — newest first; `beforeID==0` means "from newest".
  - `ListForUserSince(ctx, userID uuid.UUID, sinceID int64) ([]*domain.TopicEvent, error)` — ascending; for Phase 3 SSE replay.

- [ ] **Step 1: Write the failing test**

```go
package repo

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// fakeRow implements pgx.Row for QueryRow-based Record.
type fakeRow struct{ id int64 }

func (r fakeRow) Scan(dest ...any) error {
	*(dest[0].(*int64)) = r.id
	return nil
}

type fakeTEPool struct {
	lastSQL  string
	lastArgs []any
	row      fakeRow
}

func (f *fakeTEPool) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	f.lastSQL = sql
	f.lastArgs = args
	return f.row
}
func (f *fakeTEPool) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, nil
}
func (f *fakeTEPool) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func TestTopicEvents_Record_ReturnsID_AndMarshalsData(t *testing.T) {
	pool := &fakeTEPool{row: fakeRow{id: 7}}
	r := &TopicEvents{pool: pool}
	tid, uid := uuid.New(), uuid.New()
	id, err := r.Record(context.Background(), &domain.TopicEvent{
		TopicID: tid, UserID: uid, EventType: "release.found", Severity: "info",
		Message: "New release", Data: map[string]any{"labels": []string{"s01e01"}},
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if id != 7 {
		t.Errorf("id = %d, want 7", id)
	}
	// data arg must be JSON bytes, not a raw map (pgx can't encode map->jsonb directly here)
	raw, ok := pool.lastArgs[5].([]byte)
	if !ok {
		t.Fatalf("data arg type %T, want []byte", pool.lastArgs[5])
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("data not valid JSON: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/db/repo/ -run TopicEvents"`
Expected: FAIL — `TopicEvents` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// topicEventsPool is the minimal pgx surface used by TopicEvents.
type topicEventsPool interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// TopicEvents is the repository for topic_events — a topic's history feed.
type TopicEvents struct {
	pool topicEventsPool
}

// NewTopicEvents constructs the repository.
func NewTopicEvents(pool *pgxpool.Pool) *TopicEvents {
	return &TopicEvents{pool: pool}
}

// Record inserts a history row and returns its serial id. Data is marshalled
// to JSON for the jsonb column (nil Data → SQL NULL).
func (r *TopicEvents) Record(ctx context.Context, e *domain.TopicEvent) (int64, error) {
	var data []byte
	if e.Data != nil {
		b, err := json.Marshal(e.Data)
		if err != nil {
			return 0, fmt.Errorf("topic_events: marshal data: %w", err)
		}
		data = b
	}
	const q = `
INSERT INTO topic_events (topic_id, user_id, event_type, severity, message, data)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id`
	var id int64
	if err := r.pool.QueryRow(ctx, q, e.TopicID, e.UserID, e.EventType, e.Severity, e.Message, data).Scan(&id); err != nil {
		return 0, fmt.Errorf("topic_events: record: %w", err)
	}
	return id, nil
}

// ListForTopic returns a topic's events, newest first, capped at limit.
// beforeID==0 returns from the newest; a positive beforeID pages older.
func (r *TopicEvents) ListForTopic(ctx context.Context, topicID, userID uuid.UUID, limit int, beforeID int64) ([]*domain.TopicEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const q = `
SELECT id, topic_id, user_id, event_type, severity, message, data, created_at
FROM topic_events
WHERE topic_id = $1 AND user_id = $2 AND ($3 = 0 OR id < $3)
ORDER BY id DESC
LIMIT $4`
	rows, err := r.pool.Query(ctx, q, topicID, userID, beforeID, limit)
	if err != nil {
		return nil, fmt.Errorf("topic_events: list: %w", err)
	}
	defer rows.Close()
	return scanTopicEvents(rows)
}

// ListForUserSince returns a user's events with id > sinceID, oldest first.
// Used by the Phase 3 SSE reconnect replay.
func (r *TopicEvents) ListForUserSince(ctx context.Context, userID uuid.UUID, sinceID int64) ([]*domain.TopicEvent, error) {
	const q = `
SELECT id, topic_id, user_id, event_type, severity, message, data, created_at
FROM topic_events
WHERE user_id = $1 AND id > $2
ORDER BY id ASC
LIMIT 200`
	rows, err := r.pool.Query(ctx, q, userID, sinceID)
	if err != nil {
		return nil, fmt.Errorf("topic_events: list since: %w", err)
	}
	defer rows.Close()
	return scanTopicEvents(rows)
}

func scanTopicEvents(rows pgx.Rows) ([]*domain.TopicEvent, error) {
	var out []*domain.TopicEvent
	for rows.Next() {
		var e domain.TopicEvent
		var data []byte
		var createdAt time.Time
		if err := rows.Scan(&e.ID, &e.TopicID, &e.UserID, &e.EventType, &e.Severity, &e.Message, &data, &createdAt); err != nil {
			return nil, fmt.Errorf("topic_events: scan: %w", err)
		}
		if len(data) > 0 {
			_ = json.Unmarshal(data, &e.Data)
		}
		e.CreatedAt = createdAt
		out = append(out, &e)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/db/repo/ -run TopicEvents"`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/db/repo/topic_events.go backend/internal/db/repo/topic_events_test.go
git commit -m "feat: add TopicEvents repo for history feed (#93)"
```

---

## Task 4: Dispatcher legacy alias mapping

**Files:**
- Modify: `backend/internal/notify/dispatcher.go` (the `subscribed` func at the bottom)
- Test: `backend/internal/notify/dispatcher_test.go` (create if absent)

**Interfaces:**
- Produces: `subscribed(events []string, event string) bool` now matches canonical events through legacy aliases. No signature change.

- [ ] **Step 1: Write the failing test**

```go
package notify

import "testing"

func TestSubscribed_LegacyUpdated_MatchesNewReleaseEvents(t *testing.T) {
	legacy := []string{"updated", "error"}
	for _, ev := range []string{"release.found", "download.submitted", "download.completed"} {
		if !subscribed(legacy, ev) {
			t.Errorf("legacy 'updated' should match %s", ev)
		}
	}
}

func TestSubscribed_LegacyError_MatchesErrorEvents(t *testing.T) {
	legacy := []string{"error"}
	for _, ev := range []string{"check.failed", "session.expired"} {
		if !subscribed(legacy, ev) {
			t.Errorf("legacy 'error' should match %s", ev)
		}
	}
	if subscribed(legacy, "release.found") {
		t.Error("legacy 'error' must NOT match release.found")
	}
}

func TestSubscribed_Canonical_DirectMatch(t *testing.T) {
	if !subscribed([]string{"download.completed"}, "download.completed") {
		t.Error("canonical event should match itself")
	}
	if subscribed([]string{"download.completed"}, "release.found") {
		t.Error("canonical subscription must not match a different event")
	}
}

func TestSubscribed_EmptyMeansAll(t *testing.T) {
	if !subscribed(nil, "anything") {
		t.Error("empty subscription should match all")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/notify/ -run TestSubscribed"`
Expected: FAIL — legacy aliases not matched.

- [ ] **Step 3: Write minimal implementation** (replace the existing `subscribed` func)

```go
// legacyAliases maps a pre-taxonomy subscription keyword to the canonical
// event types it now covers, so notifiers created before per-event
// subscription (events = ['updated','error']) keep delivering. "updated" is
// intentionally broad — new releases, client submissions, and completions.
var legacyAliases = map[string][]string{
	"updated": {"release.found", "download.submitted", "download.completed"},
	"error":   {"check.failed", "session.expired"},
}

// subscribed reports whether a notifier with the given subscription list
// should receive an event. An empty list (or empty event) means "all events".
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/notify/..."`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/notify/dispatcher.go backend/internal/notify/dispatcher_test.go
git commit -m "feat: map legacy notifier events to canonical taxonomy (#93)"
```

---

## Task 5: Wire events.Bus into the scheduler

Replaces the scheduler's direct `notifier.Send/SendVia("updated"|"error")` calls with typed `bus.Emit`. The scheduler swaps its `eventNotifier` field for an `emitter` seam (the bus). `release.found` fires when a new hash is detected; `download.submitted` fires per delivery; `check.failed` replaces `notifyError`; `session.expired` replaces the in-loop `Send`. `check.started`/`check.completed` are added (ephemeral, Phase 3 surfaces them — but emitting now is harmless and testable).

**Files:**
- Modify: `backend/internal/scheduler/scheduler.go`
- Test: `backend/internal/scheduler/scheduler_test.go`

**Interfaces:**
- Consumes: `events.Event`, `events.Type` consts, `events.Bus.Emit` (Tasks 1–2).
- Produces: scheduler `emitter interface { Emit(ctx context.Context, ev events.Event) }`; `New(...)` last param becomes `emit emitter` (was `notifier eventNotifier`).

- [ ] **Step 1: Write the failing test** — add to `scheduler_test.go`. Use the existing test harness; add a fake emitter capturing events.

```go
type fakeEmitter struct {
	mu     sync.Mutex
	events []events.Event
}

func (f *fakeEmitter) Emit(_ context.Context, ev events.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
}

func (f *fakeEmitter) types() []events.Type {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []events.Type
	for _, e := range f.events {
		out = append(out, e.Type)
	}
	return out
}

// TestRunCheck_NewRelease_EmitsReleaseFoundAndSubmitted drives one check that
// detects a new hash and submits one payload, asserting the typed events fire.
func TestRunCheck_NewRelease_EmitsReleaseFoundAndSubmitted(t *testing.T) {
	// Build the scheduler with the existing test helpers, passing emit=&fakeEmitter{}.
	// (Mirror the existing "new release" scheduler test's setup — same fakes for
	// topics/clients/tracker — but inject the fake emitter as the last New() arg.)
	// After runCheck:
	//   types := emit.types()
	//   assert it contains events.ReleaseFound and events.DownloadSubmitted
	//   assert NO events.CheckFailed
}
```

> Implementer note: the existing `scheduler_test.go` already constructs a scheduler with fakes for a new-release scenario (the test that asserts the old `"updated"` `SendVia`). Copy that setup, swap the notifier fake for `&fakeEmitter{}`, and assert on `emit.types()`. Replace the old assertion on the notifier fake.

- [ ] **Step 2: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/scheduler/ -run TestRunCheck_NewRelease_EmitsReleaseFound"`
Expected: FAIL — `New` signature mismatch / emitter undefined.

- [ ] **Step 3: Implement the scheduler changes**

3a. Add the import and replace the `eventNotifier` interface (lines 86–94) with:

```go
// emitter is the subset of *events.Bus the scheduler uses to publish typed
// lifecycle events. Defined as an interface so the scheduler stays
// unit-testable without the bus, and nil-safe in tests that ignore events.
type emitter interface {
	Emit(ctx context.Context, ev events.Event)
}
```

Add to imports: `"github.com/artyomsv/marauder/backend/internal/events"`.

3b. Rename the struct field (line 135) and constructor param/assignment (lines 135, 156, 161):

```go
// struct field
emit emitter // nil-safe; publishes typed lifecycle events

// New signature — last param
func New(cfg *config.Config, log zerolog.Logger, topics *repo.Topics, clients *repo.Clients, creds *repo.TrackerCredentials, deliveries *repo.Deliveries, master *crypto.MasterKey, emit emitter) *Scheduler {
	// ... assign: emit: emit,
}
```

3c. Replace `notifyUpdated` (lines 426–446) body's send with an emit, and rename to keep call site:

```go
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
```

3d. Replace `notifyError` (lines 455–464):

```go
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
```

3e. Replace the in-loop session-expiry `Send` (line ~511) — it has a credential, not a topic. Emit with `TopicID: &t.ID` (so it shows on the topic timeline) and `NotifierID: t.NotifierID`:

```go
s.emit.Emit(ctx, events.Event{
	UserID: stored.UserID, TopicID: &t.ID, NotifierID: t.NotifierID,
	Type: events.SessionExpired, Severity: "error",
	Title: "Tracker session expired",
	Body:  t.TrackerName + " needs re-authentication",
	Link:  s.cfg.PublicBaseURL + "/credentials",
})
```

> Behavior note: session-expiry moves from `Send` (all notifiers) to the same `SendVia(NotifierID)`/defaults routing as other topic events — more consistent, and now visible on the topic timeline. Keep the atomic `MarkSessionExpired` dedup gate exactly as-is so it still fires once.

3f. Add `release.found` emission where a new hash is first detected (in `runCheck`, right after `check.Hash != t.LastHash` is confirmed and before `downloadAllPending`). Add `check.started` at the top of `runCheck` (after plugin lookup) and `check.completed` just before the final `recordResult` success call:

```go
// after confirming new hash, before draining episodes:
if s.emit != nil {
	s.emit.Emit(ctx, events.Event{
		UserID: t.UserID, TopicID: &t.ID, NotifierID: t.NotifierID,
		Type: events.ReleaseFound, Severity: "info",
		Title: t.DisplayName, Body: "New release detected",
		Link: s.cfg.PublicBaseURL + "/topics",
	})
}
```

```go
// check.started — top of runCheck, once the tracker plugin is resolved:
if s.emit != nil {
	s.emit.Emit(ctx, events.Event{UserID: t.UserID, TopicID: &t.ID, Type: events.CheckStarted})
}

// check.completed — before the success recordResult, carrying next_check_at:
if s.emit != nil {
	s.emit.Emit(ctx, events.Event{
		UserID: t.UserID, TopicID: &t.ID, Type: events.CheckCompleted,
		Data: map[string]any{"next_check_at": nextCheckAt.UTC().Format(time.RFC3339)},
	})
}
```

> Implementer note: `nextCheckAt` is the value passed to `recordResult` on the success path (`s.backoff(t, false, nil)`). Compute it once into a local and reuse for both the emit and `recordResult` to keep them consistent.

- [ ] **Step 4: Run tests to verify they pass** (and existing scheduler tests still pass)

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test -race ./internal/scheduler/..."`
Expected: PASS. Fix any existing test that referenced the old notifier fake by switching it to the emitter fake.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/scheduler/scheduler.go backend/internal/scheduler/scheduler_test.go
git commit -m "feat: emit typed lifecycle events from scheduler (#93)"
```

---

## Task 6: Construct the Bus and wire it (main.go) + emit topic.added

**Files:**
- Modify: `backend/cmd/server/main.go`
- Modify: `backend/internal/api/handlers/topics.go` (the `Create` handler)
- Test: `backend/internal/api/handlers/topics_test.go` (extend the create test)

**Interfaces:**
- Consumes: `events.New`, `repo.NewTopicEvents`, `events.Bus.Emit`.
- Produces: `handlers.Topics` gains an `Emit func(ctx, events.Event)` (or an `emitter` field) used by `Create`.

- [ ] **Step 1: Write the failing test** (handler) — assert `Create` calls the emitter with `topic.added`.

```go
func TestTopics_Create_EmitsTopicAdded(t *testing.T) {
	var got []events.Event
	h := &Topics{
		// ... existing fakes used by the create test ...
		Emit: func(_ context.Context, ev events.Event) { got = append(got, ev) },
	}
	// drive POST /topics with a valid body (reuse the existing create test setup)
	// then:
	if len(got) != 1 || got[0].Type != events.TopicAdded {
		t.Fatalf("want one topic.added event, got %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/api/handlers/ -run TestTopics_Create_EmitsTopicAdded"`
Expected: FAIL — `Emit` field undefined.

- [ ] **Step 3: Implement**

3a. In `topics.go`, add an optional emit hook to the `Topics` handler struct and call it after a successful `Create` (right after the topic is persisted, before writing the response):

```go
// field on Topics handler:
Emit func(ctx context.Context, ev events.Event) // nil-safe

// after successful create (created holds the new topic):
if h.Emit != nil {
	h.Emit(r.Context(), events.Event{
		UserID: created.UserID, TopicID: &created.ID, NotifierID: created.NotifierID,
		Type: events.TopicAdded, Severity: "info",
		Title: created.DisplayName, Body: "Topic added",
		Link: h.BaseURL + "/topics",
	})
}
```

Add import `"github.com/artyomsv/marauder/backend/internal/events"`. Guard nil so existing handler tests that don't set `Emit` keep passing.

3b. In `main.go`, construct the repo, bus, and wire it (near lines 105–144):

```go
topicEventsRepo := repo.NewTopicEvents(pool)
disp := notify.New(notifiersRepo, master, logger)
bus := events.New(topicEventsRepo, disp, nil, logger) // SSE publisher nil until Phase 3
sch := scheduler.New(cfg, logger, topicsRepo, clientsRepo, credsRepo, deliveriesRepo, master, bus)
```

And set `Emit: bus.Emit` on the `handlers.Topics` construction (where the handler struct is built, ~line 165+).

Add import `"github.com/artyomsv/marauder/backend/internal/events"`.

- [ ] **Step 4: Run build + tests**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go test ./internal/api/handlers/ -run TestTopics_Create"`
Expected: PASS, build clean.

- [ ] **Step 5: Commit**

```bash
git add backend/cmd/server/main.go backend/internal/api/handlers/topics.go backend/internal/api/handlers/topics_test.go
git commit -m "feat: wire events.Bus and emit topic.added on create (#93)"
```

---

## Task 7: GET /topics/{id}/events endpoint

**Files:**
- Create: `backend/internal/api/handlers/topic_events.go`
- Test: `backend/internal/api/handlers/topic_events_test.go`
- Modify: `backend/internal/api/router.go` (register route), `backend/cmd/server/main.go` (inject repo into handler)

**Interfaces:**
- Consumes: `repo.TopicEvents.ListForTopic`, `currentUserID`, `problem`, `writeJSON`.
- Produces: handler `TopicEvents{ Events topicEventsStore; Topics topicOwnerStore; BaseURL string }` with `List(w, r)`; response `{ "events": [ {id, event_type, severity, message, data, created_at} ] }`.

- [ ] **Step 1: Write the failing test**

```go
func TestTopicEvents_List_ReturnsTopicHistory(t *testing.T) {
	tid := uuid.New()
	store := &fakeTopicEventsStore{rows: []*domain.TopicEvent{
		{ID: 2, TopicID: tid, EventType: "release.found", Severity: "info", Message: "New release", CreatedAt: time.Now()},
		{ID: 1, TopicID: tid, EventType: "check.failed", Severity: "error", Message: "boom", CreatedAt: time.Now()},
	}}
	h := &TopicEvents{Events: store, Topics: &fakeTopicOwner{ok: true}, BaseURL: ""}
	// GET /topics/{id}/events with an authed context (reuse the handlers test helper that injects userID)
	// assert 200, body.events length 2, first event_type == "release.found"
}
```

> Implementer note: mirror the existing `topics_test.go` request-building helper (it injects the auth claims into the request context). `fakeTopicOwner.GetByID` returns a topic owned by the test user when `ok`.

- [ ] **Step 2: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/api/handlers/ -run TestTopicEvents_List"`
Expected: FAIL — `TopicEvents` handler undefined.

- [ ] **Step 3: Implement**

```go
package handlers

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/problem"
)

// topicEventsStore is the consumer seam over *repo.TopicEvents.
type topicEventsStore interface {
	ListForTopic(ctx context.Context, topicID, userID uuid.UUID, limit int, beforeID int64) ([]*domain.TopicEvent, error)
}

// topicOwnerStore verifies topic ownership before returning its history.
type topicOwnerStore interface {
	GetByID(ctx context.Context, id, userID uuid.UUID) (*domain.Topic, error)
}

// TopicEvents handles GET /topics/{id}/events.
type TopicEvents struct {
	Events  topicEventsStore
	Topics  topicOwnerStore
	BaseURL string
}

type topicEventView struct {
	ID        int64          `json:"id"`
	EventType string         `json:"event_type"`
	Severity  string         `json:"severity"`
	Message   string         `json:"message"`
	Data      map[string]any `json:"data,omitempty"`
	CreatedAt string         `json:"created_at"`
}

// List handles GET /topics/{id}/events?limit=&before=.
func (h *TopicEvents) List(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	id, ierr := uuid.Parse(chi.URLParam(r, "id"))
	if ierr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid id"))
		return
	}
	if _, err := h.Topics.GetByID(r.Context(), id, uid); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrNotFound("topic not found"))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	rows, err := h.Events.ListForTopic(r.Context(), id, uid, limit, before)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	out := make([]topicEventView, 0, len(rows))
	for _, e := range rows {
		out = append(out, topicEventView{
			ID: e.ID, EventType: e.EventType, Severity: e.Severity, Message: e.Message,
			Data: e.Data, CreatedAt: e.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}
```

3b. Register in `router.go` next to the status route (`r.Get("/topics/{id}/status", …)`):

```go
r.Get("/topics/{id}/events", topicEventsH.List)
```

3c. In `main.go`, construct `topicEventsH := &handlers.TopicEvents{Events: topicEventsRepo, Topics: topicsRepo, BaseURL: cfg.PublicBaseURL}` and pass it into the router deps (follow how `topicsH` is threaded into `router.New`/the deps struct).

- [ ] **Step 4: Run build + tests**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go vet ./... && go test -race ./..."`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/handlers/topic_events.go backend/internal/api/handlers/topic_events_test.go backend/internal/api/router.go backend/cmd/server/main.go
git commit -m "feat: add GET /topics/{id}/events history endpoint (#93)"
```

---

## Task 8: Notifier handler — canonical default + validation

**Files:**
- Modify: `backend/internal/api/handlers/notifiers.go` (Create + Update default; add validation)
- Test: `backend/internal/api/handlers/notifiers_test.go` (create if absent)

**Interfaces:**
- Consumes: `events.NotifiableTypes`.
- Produces: a package-level `validNotifierEvents(events []string) []string` that drops non-notifiable/unknown events; empty → full canonical notifiable set.

- [ ] **Step 1: Write the failing test**

```go
func TestValidNotifierEvents_FiltersAndDefaults(t *testing.T) {
	// empty -> full canonical notifiable set (5 entries)
	if got := validNotifierEvents(nil); len(got) != 5 {
		t.Errorf("default set size = %d, want 5", len(got))
	}
	// drops legacy 'updated' is allowed-through (kept for back-compat) but
	// unknown junk is dropped
	got := validNotifierEvents([]string{"release.found", "bogus.event", "download.completed"})
	for _, e := range got {
		if e == "bogus.event" {
			t.Errorf("bogus event should be dropped: %v", got)
		}
	}
	if len(got) != 2 {
		t.Errorf("got %v, want [release.found download.completed]", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/api/handlers/ -run TestValidNotifierEvents"`
Expected: FAIL — `validNotifierEvents` undefined.

- [ ] **Step 3: Implement** — add helper and use it in Create (lines 114–117) and Update (lines 214–217), replacing the `["updated","error"]` default:

```go
// validNotifierEvents keeps only events a notifier may subscribe to:
// the canonical notifiable set, plus the legacy "updated"/"error" keywords
// (still honored via dispatcher aliases). Empty input defaults to the full
// canonical notifiable set. Unknown/non-notifiable events are dropped.
func validNotifierEvents(in []string) []string {
	allowed := map[string]bool{"updated": true, "error": true}
	var canonical []string
	for _, ty := range events.NotifiableTypes() {
		allowed[string(ty)] = true
		canonical = append(canonical, string(ty))
	}
	if len(in) == 0 {
		return canonical
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, e := range in {
		if allowed[e] && !seen[e] {
			out = append(out, e)
			seen[e] = true
		}
	}
	if len(out) == 0 {
		return canonical
	}
	return out
}
```

Replace both `events := req.Events; if len(events) == 0 { events = []string{"updated","error"} }` blocks with `events := validNotifierEvents(req.Events)`. Add import `"github.com/artyomsv/marauder/backend/internal/events"`.

> Note: `NotifiableTypes()` is unordered (map iteration). That's fine for storage, but for a stable default ordering you may sort `canonical` with `sort.Strings(canonical)` before returning.

- [ ] **Step 4: Run build + tests**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/api/handlers/ -run TestValidNotifierEvents && go build ./..."`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/handlers/notifiers.go backend/internal/api/handlers/notifiers_test.go
git commit -m "feat: validate notifier events against canonical set (#93)"
```

---

## Task 9: Frontend — event constants, API method, query key

**Files:**
- Create: `frontend/src/lib/events.ts`
- Modify: `frontend/src/lib/api.ts`, `frontend/src/lib/queryKeys.ts`

**Interfaces:**
- Produces: `NOTIFIABLE_EVENTS` (canonical), `EVENT_LABELS` (i18n key map), `api.topicEvents(id)`, `TopicEvent` interface, `QK.topicEvents(id)`.

- [ ] **Step 1: Write the failing test** (api shape) — `frontend/src/lib/events.test.ts`

```ts
import { describe, it, expect } from "vitest";
import { NOTIFIABLE_EVENTS, EVENT_LABELS } from "@/lib/events";

describe("events catalog", () => {
  it("exposes the four notifiable groups", () => {
    expect(NOTIFIABLE_EVENTS).toContain("release.found");
    expect(NOTIFIABLE_EVENTS).toContain("download.submitted");
    expect(NOTIFIABLE_EVENTS).toContain("download.completed");
    expect(NOTIFIABLE_EVENTS).toContain("check.failed");
  });
  it("has an i18n label key for every notifiable event", () => {
    for (const e of NOTIFIABLE_EVENTS) {
      expect(EVENT_LABELS[e]).toBeTruthy();
    }
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm test -- events.test"`
Expected: FAIL — module missing.

- [ ] **Step 3: Implement**

`frontend/src/lib/events.ts`:

```ts
// Canonical notifier-subscribable checkbox keys (the four UI groups).
export const NOTIFIABLE_EVENTS = [
  "release.found",
  "download.submitted",
  "download.completed",
  "check.failed",
] as const;

export type NotifiableEvent = (typeof NOTIFIABLE_EVENTS)[number];

// Each checkbox key maps to the canonical backend event(s) it stores. The
// "check.failed" box covers BOTH error events so session.expired alerts are
// delivered when the user opts into "errors" — without this, the backend's
// distinct session.expired event would silently never match.
export const EVENT_GROUP_EVENTS: Record<NotifiableEvent, string[]> = {
  "release.found": ["release.found"],
  "download.submitted": ["download.submitted"],
  "download.completed": ["download.completed"],
  "check.failed": ["check.failed", "session.expired"],
};

// Flattened canonical default: every notifiable backend event. Use as the
// initial subscription for a new notifier so all groups (incl. session.expired)
// are on by default — matching the backend's empty-input default.
export const ALL_NOTIFIABLE_EVENTS: string[] = Object.values(EVENT_GROUP_EVENTS).flat();

// i18n keys for each event label, used by the notifier picker and the
// per-topic timeline. Timeline-only (non-notifiable) events are included so
// the history view can render them too.
export const EVENT_LABELS: Record<string, string> = {
  "topic.added": "events.topic_added",
  "check.started": "events.check_started",
  "check.completed": "events.check_completed",
  "release.found": "events.release_found",
  "download.submitted": "events.download_submitted",
  "download.progress": "events.download_progress",
  "download.completed": "events.download_completed",
  "check.failed": "events.check_failed",
  "session.expired": "events.session_expired",
  // legacy aliases still present on older notifier rows
  updated: "events.release_found",
  error: "events.check_failed",
};
```

`queryKeys.ts` — add after `topicStatus`:

```ts
  // /topics/{id}/events — read-only per-topic history timeline.
  topicEvents: (id: string) => ["topicEvents", id] as const,
```

`api.ts` — add the interface and method (near `topicStatus`):

```ts
export interface TopicEvent {
  id: number;
  event_type: string;
  severity: "info" | "warn" | "error";
  message: string;
  data?: Record<string, unknown>;
  created_at: string;
}

// inside the api object:
  topicEvents: (id: string) =>
    api.get<{ events: TopicEvent[] }>(`/topics/${id}/events`),
```

- [ ] **Step 4: Run test to verify it passes**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test -- events.test"`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/events.ts frontend/src/lib/events.test.ts frontend/src/lib/api.ts frontend/src/lib/queryKeys.ts
git commit -m "feat: frontend event catalog, topicEvents api + key (#93)"
```

---

## Task 10: Frontend — EventPicker + Notifiers wiring

**Files:**
- Create: `frontend/src/components/notifiers/EventPicker.tsx`, `EventPicker.test.tsx`
- Modify: `frontend/src/pages/Notifiers.tsx` (AddNotifierCard + `eventLabel`), `frontend/src/components/notifiers/EditNotifierCard.tsx`

**Interfaces:**
- Produces: `<EventPicker value={string[]} onChange={(next: string[]) => void} />` — checkbox group over `NOTIFIABLE_EVENTS`, maps legacy `updated`/`error` to checked groups on first render.

- [ ] **Step 1: Write the failing test** — `EventPicker.test.tsx`

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { EventPicker } from "@/components/notifiers/EventPicker";

describe("EventPicker", () => {
  it("renders a checkbox per notifiable event and toggles", async () => {
    const onChange = vi.fn();
    render(<EventPicker value={["release.found"]} onChange={onChange} />);
    const completed = screen.getByLabelText(/download finished/i);
    expect(completed).not.toBeChecked();
    await userEvent.click(completed);
    expect(onChange).toHaveBeenCalledWith(
      expect.arrayContaining(["release.found", "download.completed"]),
    );
  });

  it("shows legacy 'updated' as the three release-group boxes checked", () => {
    render(<EventPicker value={["updated", "error"]} onChange={() => {}} />);
    expect(screen.getByLabelText(/new release/i)).toBeChecked();
    expect(screen.getByLabelText(/sent to client/i)).toBeChecked();
    expect(screen.getByLabelText(/download finished/i)).toBeChecked();
    expect(screen.getByLabelText(/error/i)).toBeChecked();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm test -- EventPicker"`
Expected: FAIL — component missing.

- [ ] **Step 3: Implement** `EventPicker.tsx`

```tsx
import { useT } from "@/i18n";
import { NOTIFIABLE_EVENTS, EVENT_GROUP_EVENTS, EVENT_LABELS } from "@/lib/events";

interface EventPickerProps {
  value: string[];
  onChange: (next: string[]) => void;
}

// expandStored turns a stored subscription (which may contain the legacy
// "updated"/"error" keywords) into the set of canonical backend events.
function expandStored(value: string[]): Set<string> {
  const set = new Set<string>();
  for (const v of value) {
    if (v === "updated") {
      set.add("release.found");
      set.add("download.submitted");
      set.add("download.completed");
    } else if (v === "error") {
      set.add("check.failed");
      set.add("session.expired");
    } else {
      set.add(v);
    }
  }
  return set;
}

export function EventPicker({ value, onChange }: EventPickerProps) {
  const t = useT();
  const selected = expandStored(value);

  // A group box is checked if ANY of its canonical events is selected.
  const groupChecked = (key: (typeof NOTIFIABLE_EVENTS)[number]) =>
    EVENT_GROUP_EVENTS[key].some((e) => selected.has(e));

  const toggleGroup = (key: (typeof NOTIFIABLE_EVENTS)[number], on: boolean) => {
    const next = expandStored(value);
    for (const e of EVENT_GROUP_EVENTS[key]) {
      if (on) next.add(e);
      else next.delete(e);
    }
    onChange([...next]);
  };

  return (
    <div className="flex flex-wrap items-center gap-4 text-sm">
      <span className="text-muted-foreground">{t("notifiers.notify_on")}:</span>
      {NOTIFIABLE_EVENTS.map((key) => (
        <label key={key} className="inline-flex items-center gap-1.5">
          <input
            type="checkbox"
            checked={groupChecked(key)}
            onChange={(ev) => toggleGroup(key, ev.target.checked)}
          />
          {t(EVENT_LABELS[key])}
        </label>
      ))}
    </div>
  );
}
```

3b. In `Notifiers.tsx`, replace the inline `["updated","error"]` checkbox block (lines 318–334) with `<EventPicker value={events} onChange={setEvents} />`, change the initial state to `useState<string[]>(ALL_NOTIFIABLE_EVENTS)` (import `ALL_NOTIFIABLE_EVENTS` and `EventPicker`). Update `eventLabel` (lines 34–40) to read from `EVENT_LABELS` so cards render the new labels.

3c. In `EditNotifierCard.tsx`, swap its events checkbox block for `<EventPicker value={events} onChange={setEvents} />` (same import).

- [ ] **Step 4: Run tests**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test -- EventPicker Notifiers"`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/notifiers/EventPicker.tsx frontend/src/components/notifiers/EventPicker.test.tsx frontend/src/pages/Notifiers.tsx frontend/src/components/notifiers/EditNotifierCard.tsx
git commit -m "feat: per-event notifier subscription picker UI (#93)"
```

---

## Task 11: Frontend — per-topic event timeline

**Files:**
- Create: `frontend/src/components/topics/TopicEventsTimeline.tsx`, `TopicEventsTimeline.test.tsx`
- Modify: the topic row/detail in `frontend/src/pages/Topics.tsx` to mount the timeline (e.g. an expandable "History" section alongside the existing `DeliveryStatus`).

**Interfaces:**
- Consumes: `api.topicEvents`, `QK.topicEvents`, `EVENT_LABELS`, `TopicEvent`.
- Produces: `<TopicEventsTimeline topicId={string} />` — read-only, query-driven list, newest first.

- [ ] **Step 1: Write the failing test** — `TopicEventsTimeline.test.tsx`

```tsx
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { TopicEventsTimeline } from "@/components/topics/TopicEventsTimeline";
import { api } from "@/lib/api";

vi.mock("@/lib/api", () => ({
  api: { topicEvents: vi.fn() },
}));

function wrap(ui: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

describe("TopicEventsTimeline", () => {
  beforeEach(() => vi.clearAllMocks());
  it("renders events newest-first with labels", async () => {
    (api.topicEvents as ReturnType<typeof vi.fn>).mockResolvedValue({
      events: [
        { id: 2, event_type: "release.found", severity: "info", message: "New release", created_at: "2026-06-25T10:00:00Z" },
        { id: 1, event_type: "check.failed", severity: "error", message: "boom", created_at: "2026-06-25T09:00:00Z" },
      ],
    });
    wrap(<TopicEventsTimeline topicId="t1" />);
    expect(await screen.findByText("New release")).toBeInTheDocument();
    expect(screen.getByText("boom")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm test -- TopicEventsTimeline"`
Expected: FAIL — component missing.

- [ ] **Step 3: Implement** `TopicEventsTimeline.tsx`

```tsx
import { useQuery } from "@tanstack/react-query";
import { Loader2, Clock } from "lucide-react";

import { api } from "@/lib/api";
import { QK } from "@/lib/queryKeys";
import { useT } from "@/i18n";
import { EVENT_LABELS } from "@/lib/events";

interface TopicEventsTimelineProps {
  topicId: string;
}

const severityDot: Record<string, string> = {
  info: "bg-primary",
  warn: "bg-warning",
  error: "bg-destructive",
};

export function TopicEventsTimeline({ topicId }: TopicEventsTimelineProps) {
  const t = useT();
  const { data, isLoading } = useQuery({
    queryKey: QK.topicEvents(topicId),
    queryFn: () => api.topicEvents(topicId),
  });
  const events = data?.events ?? [];

  if (isLoading) {
    return (
      <div className="flex items-center gap-2 p-4 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" />
        {t("common.loading")}
      </div>
    );
  }
  if (events.length === 0) {
    return (
      <div className="flex items-center gap-2 p-4 text-sm text-muted-foreground">
        <Clock className="size-4" />
        {t("topics.history.empty")}
      </div>
    );
  }
  return (
    <ol className="space-y-2 p-2">
      {events.map((e) => (
        <li key={e.id} className="flex items-start gap-3 text-sm">
          <span className={`mt-1.5 size-2 shrink-0 rounded-full ${severityDot[e.severity] ?? "bg-muted"}`} />
          <div className="min-w-0">
            <div className="font-medium">
              {EVENT_LABELS[e.event_type] ? t(EVENT_LABELS[e.event_type]) : e.event_type}
            </div>
            {e.message && <div className="text-muted-foreground">{e.message}</div>}
            <time className="text-xs text-muted-foreground">
              {new Date(e.created_at).toLocaleString()}
            </time>
          </div>
        </li>
      ))}
    </ol>
  );
}
```

3b. Mount it in `Topics.tsx` where `DeliveryStatus` is rendered for an expanded topic — add a small "History" disclosure that renders `<TopicEventsTimeline topicId={topic.id} />`. Keep `Topics.tsx` within its size budget (it already breaches 250 lines — extract the disclosure into the timeline component, do not inline more than the mount).

- [ ] **Step 4: Run tests**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test -- TopicEventsTimeline"`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/topics/TopicEventsTimeline.tsx frontend/src/components/topics/TopicEventsTimeline.test.tsx frontend/src/pages/Topics.tsx
git commit -m "feat: per-topic event timeline view (#93)"
```

---

## Task 12: i18n keys

**Files:**
- Modify: `frontend/src/i18n/en.ts`, `frontend/src/i18n/ru.ts`

**Interfaces:**
- Produces: translation keys referenced by Tasks 10–11.

- [ ] **Step 1: Add keys to `en.ts`** (and Russian equivalents to `ru.ts`):

```ts
  "notifiers.notify_on": "Notify on",
  "events.topic_added": "Topic added",
  "events.check_started": "Checking",
  "events.check_completed": "Checked",
  "events.release_found": "New release",
  "events.download_submitted": "Sent to client",
  "events.download_progress": "Downloading",
  "events.download_completed": "Download finished",
  "events.check_failed": "Error",
  "events.session_expired": "Session expired",
  "topics.history.empty": "No history yet",
```

For the EventPicker test labels to match (`/new release/i`, `/sent to client/i`, `/download finished/i`, `/error/i`), keep those English strings.

- [ ] **Step 2: Verify build + full frontend suite**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test && npm run build"`
Expected: PASS (typecheck catches any missing key references if the i18n dict is typed).

- [ ] **Step 3: Commit**

```bash
git add frontend/src/i18n/en.ts frontend/src/i18n/ru.ts
git commit -m "feat: i18n labels for event taxonomy and timeline (#93)"
```

---

## Task 13: Docs — CLAUDE.md, CHANGELOG, getting-started

**Files:**
- Modify: `CLAUDE.md` (backend table: new `events` package + `TopicEvents` repo + scheduler emits typed events; frontend: timeline + event picker), `CHANGELOG.md` (`[Unreleased]`), `docs/getting-started.md` if it documents notifier events.

- [ ] **Step 1: Update `CLAUDE.md`** — add an `events` row to the backend package table:

```
| **`events`** | canonical event taxonomy (`Type` consts) + per-type `Policy` (persist/notifiable/sse) + `Bus.Emit` — the single event→sinks fan-out (history `topic_events`, notifier dispatcher, SSE seam). Phase-1 SSE publisher is nil |
```

Note `Topics` repo gains the `TopicEvents` sibling (history feed, `Record`/`ListForTopic`/`ListForUserSince`), the scheduler now emits typed `events.Event`s instead of calling the dispatcher directly, and the notifier dispatcher `subscribed()` carries legacy `updated`/`error` aliases. Frontend: `components/topics/TopicEventsTimeline`, `components/notifiers/EventPicker`, `lib/events.ts`.

- [ ] **Step 2: Add a `CHANGELOG.md` `[Unreleased]` bullet:**

```markdown
### Added
- Typed event taxonomy with per-event notifier subscriptions (new releases, sent-to-client, download finished, errors) and a read-only per-topic event timeline (#93).
```

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md CHANGELOG.md docs/getting-started.md
git commit -m "docs: event taxonomy + notifier subscriptions + timeline (#93)"
```

---

## Final verification

- [ ] **Backend full suite:** `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go vet ./... && go test -race ./..."` → all pass.
- [ ] **Frontend full suite:** `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test && npm run build"` → all pass.
- [ ] **Manual smoke (dev stack):** create a notifier with only "Download finished" checked; confirm an existing `['updated','error']` notifier still shows all boxes checked and still delivers; open a topic's History and confirm `topic.added` appears.

---

## Spec coverage check (Phase 1 scope of `2026-06-25-event-types-and-live-updates-design.md`)

- §5 policy table → Task 1. §6.1 Bus → Task 2. §6.2 repo → Task 3. §6.3 alias → Task 4. §6.4 emit points (topic.added, check.started/completed, release.found, download.submitted, check.failed, session.expired) → Tasks 5–6. §7 notifier event editor → Tasks 8–10. §7 per-topic timeline → Tasks 7, 11. Phase-2 (`download.progress`/`completed` watcher) and Phase-3 (SSE hub, ticket, frontend EventSource, nginx) are **out of scope** here, by design — `download.completed`/`download.progress` policy rows exist but nothing emits them yet, and the Bus SSE publisher is nil.
