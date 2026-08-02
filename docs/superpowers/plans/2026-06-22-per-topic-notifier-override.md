# Per-Topic Notifier Override Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a topic optionally route its "new release" notifications to one specific notifier instead of the user's global notifier set (GitHub issue #51).

**Architecture:** Mirror the existing per-topic `ClientID` override end-to-end. Add a nullable `topics.notifier_id` FK (`ON DELETE SET NULL`), thread it through `domain.Topic` → repo → API handler → React form. The scheduler's `notifyUpdated` path routes through a new `Dispatcher.SendVia(ctx, userID, notifierID, event, msg)` that scopes the existing list-and-filter fan-out to a single notifier when an ID is supplied, falling back to the global `Send` behaviour when it is `nil`.

**Tech Stack:** Go 1.25 (pgx, pgxmock, goose migrations, chi, zerolog), React 19 + TypeScript + Vite + Tailwind + React Query, shadcn/ui.

## Global Constraints

- **Singular notifier per topic** — `notifier_id` is a single nullable FK, NOT a join table (per issue owner's decision 2026-06-22).
- **`ON DELETE SET NULL`** — deleting a notifier reverts every topic that referenced it to default behaviour automatically; no dangling reference state is possible, so no "unknown notifier" handling is needed server-side.
- **Scope is the topic-scoped "updated" event only.** The credential session-expiry "error" alert (`scheduler.go` `loadCredentials`) stays on the global `Send` fan-out: it is credential-scoped and deduped across all topics sharing the credential, so routing it through one racing topic's override would be nondeterministic. Do not touch that call site.
- **No override → unchanged behaviour.** A `nil` `notifier_id` MUST produce the exact current global fan-out. Existing installations behave identically.
- **No new i18n keys.** `TopicForm.tsx` uses hardcoded English labels for the Client/Download/Category fields; mirror that for the notifier field. Do not add `en.ts`/`ru.ts` entries.
- **Column order:** add `notifier_id` immediately after `client_id` in `topicColumns` and the matching test header/row helpers. Keep `scanTopic` field order in lockstep with `topicColumns`.
- **Backend verify (per CLAUDE.md — never install Go locally):**
  `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "<cmd>"`
- **Frontend verify:**
  `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "<cmd>"`
- **Commits:** imperative mood, ≤72 char subject, reference `#51`. No AI/Claude attribution, no `Co-Authored-By`. Get explicit user confirmation before each commit (do not commit autonomously).

---

## File Structure

**Backend**
- Create: `backend/internal/db/migrations/0007_add_topic_notifier_id.sql` — schema column + FK.
- Modify: `backend/internal/domain/domain.go` — add `Topic.NotifierID *uuid.UUID`.
- Modify: `backend/internal/db/repo/topics.go` — `topicColumns`, `scanTopic`, `Create`, `Update` (signature + SQL).
- Modify: `backend/internal/db/repo/topics_test.go` — column header/row helpers + affected Create/Update tests + a new notifier round-trip assertion.
- Modify: `backend/internal/notify/dispatcher.go` — extract `sendOne`, add `SendVia`.
- Modify: `backend/internal/notify/dispatcher_test.go` — `SendVia` tests.
- Modify: `backend/internal/scheduler/scheduler.go` — `eventNotifier` interface gains `SendVia`; `notifyUpdated` calls it.
- Modify: `backend/internal/scheduler/scheduler_test.go` — `fakeNotifier` gains `SendVia`; new routing assertion.
- Modify: `backend/internal/api/handlers/topics.go` — `topicStore.Update` signature, `createTopicReq`/`updateTopicReq`, `Create`/`Update` wiring.
- Modify: `backend/internal/api/handlers/topics_handler_test.go` — `fakeTopicStore.Update` signature + a notifier-passthrough assertion.

**Frontend**
- Modify: `frontend/src/lib/api.ts` — `Topic.NotifierID`, `UpdateTopicBody.notifier_id`.
- Modify: `frontend/src/components/topics/TopicForm.tsx` — notifier query + select + `TopicFormValues.notifierId`.
- Modify: `frontend/src/components/topics/AddTopicCard.tsx` — `EMPTY` + POST body.
- Modify: `frontend/src/components/topics/EditTopicCard.tsx` — `initialFrom` + PUT body.
- Create: `frontend/src/components/topics/NotifierBadge.tsx` — card badge (shown only when an override is set).
- Modify: `frontend/src/pages/Topics.tsx` — notifiers query + render `NotifierBadge`.

---

## Task 1: Persistence layer (migration + domain + repo)

**Files:**
- Create: `backend/internal/db/migrations/0007_add_topic_notifier_id.sql`
- Modify: `backend/internal/domain/domain.go:74`
- Modify: `backend/internal/db/repo/topics.go:38-43` (`topicColumns`), `:45-78` (`scanTopic`), `:81-93` (`Create`), `:245-263` (`Update`)
- Test: `backend/internal/db/repo/topics_test.go`

**Interfaces:**
- Produces: `domain.Topic.NotifierID *uuid.UUID`; `(*repo.Topics).Update(ctx, id, userID uuid.UUID, displayName string, clientID, notifierID *uuid.UUID, downloadDir, category string, extra map[string]any) (*domain.Topic, error)` — note the new `notifierID` param inserted right after `clientID`.

- [ ] **Step 1: Write the migration file**

Create `backend/internal/db/migrations/0007_add_topic_notifier_id.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
ALTER TABLE topics
    ADD COLUMN notifier_id UUID REFERENCES notifiers(id) ON DELETE SET NULL;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
ALTER TABLE topics DROP COLUMN notifier_id;
-- +goose StatementEnd
```

- [ ] **Step 2: Add the domain field**

In `backend/internal/domain/domain.go`, add `NotifierID` directly after `ClientID` (line 74):

```go
	ClientID          *uuid.UUID
	NotifierID        *uuid.UUID
	DownloadDir       string
```

- [ ] **Step 3: Update the failing repo test header/row helpers first**

In `backend/internal/db/repo/topics_test.go`, insert `notifier_id` after `client_id` in both helpers so every existing test that uses them keeps matching `topicColumns`.

`topicColumnsAll` (currently line 219) becomes:

```go
var topicColumnsAll = []string{
	"id", "user_id", "tracker_name", "url", "display_name", "image_url", "client_id", "notifier_id",
	"download_dir", "category", "extra", "last_hash",
	"last_checked_at", "last_updated_at", "next_check_at",
	"check_interval_sec", "consecutive_errors", "status",
	"last_error", "created_at", "updated_at",
}
```

`topicRow` (currently line 203) gains a `notifier_id` nil right after `client_id`:

```go
func topicRow(id, userID uuid.UUID, now time.Time) []any {
	return []any{
		id, userID, "faketracker", "https://example.invalid/t/1",
		"My Topic", "", // display_name, image_url
		(*uuid.UUID)(nil), // client_id
		(*uuid.UUID)(nil), // notifier_id
		"",                            // download_dir
		"",                            // category
		[]byte(`{"quality":"1080p"}`), // extra
		"",                            // last_hash
		(*time.Time)(nil), (*time.Time)(nil), now,
		3600, 0, "active",
		"", now, now,
	}
}
```

In `TestTopics_ScanTopic_MalformedExtra` the row is built inline (currently lines 240-249) — insert the `notifier_id` nil after the `client_id` nil:

```go
	rows := pgxmock.NewRows(topicColumnsAll).AddRow(
		id, userID, "faketracker", "https://example.invalid/t/1",
		"My Topic", "", // display_name, image_url
		(*uuid.UUID)(nil), // client_id
		(*uuid.UUID)(nil), // notifier_id
		"", "",
		[]byte("{not valid json"), "",
		(*time.Time)(nil), (*time.Time)(nil), now,
		3600, 0, "active",
		"", now, now,
	)
```

- [ ] **Step 4: Fix the column-index references in the Create/Update tests**

Because `notifier_id` shifts later columns by one, update the index-based row mutations and the `WithArgs` lists.

`TestTopics_Create_RoundTripsCategory` (currently lines 326-376): category moves from index 8 to **9**, and the INSERT now passes a `notifier_id` arg after `client_id`:

```go
	// Build the RETURNING row with category="movies".
	row := topicRow(id, userID, now)
	// column index 9 is category (0-based: id,user_id,tracker_name,url,
	// display_name,image_url,client_id,notifier_id,download_dir,category,...)
	row[9] = "movies"

	rows := pgxmock.NewRows(topicColumnsAll).AddRow(row...)

	// Match INSERT containing the category column.
	mock.ExpectQuery(`INSERT INTO topics.*category.*RETURNING`).
		WithArgs(
			userID, "faketracker", "https://example.invalid/t/1",
			"My Topic", "", // display_name, image_url
			(*uuid.UUID)(nil), // client_id
			(*uuid.UUID)(nil), // notifier_id
			"",               // download_dir
			"movies",         // category
			pgxmock.AnyArg(), // extra (JSON)
			3600, pgxmock.AnyArg(), "active",
		).
		WillReturnRows(rows)
```

`TestTopics_Update_HappyPath` (currently lines 382-420): display_name index 4 is unchanged, category moves 8→**9**, extra moves 9→**10**, and the `WithArgs`/call gain `notifier_id`:

```go
	// Build the RETURNING row reflecting the updated values.
	row := topicRow(id, userID, now)
	row[4] = "Updated Name"                                 // display_name
	row[9] = "series"                                       // category
	row[10] = []byte(`{"quality":"720p","start_season":2}`) // extra

	rows := pgxmock.NewRows(topicColumnsAll).AddRow(row...)

	mock.ExpectQuery(`UPDATE topics SET`).
		WithArgs(
			id, userID,
			"Updated Name",    // $3 display_name
			(*uuid.UUID)(nil), // $4 client_id
			(*uuid.UUID)(nil), // $5 notifier_id
			"",                // $6 download_dir
			"series",          // $7 category
			pgxmock.AnyArg(),  // $8 extra (JSON)
		).
		WillReturnRows(rows)

	extra := map[string]any{"quality": "720p", "start_season": 2}
	got, err := r.Update(context.Background(), id, userID, "Updated Name", nil, nil, "", "series", extra)
```

`TestTopics_Update_NotFound` (currently lines 424-438): add one more `AnyArg` and the new nil param:

```go
	mock.ExpectQuery(`UPDATE topics SET`).
		WithArgs(id, userID, pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnError(pgx.ErrNoRows)

	_, err := r.Update(context.Background(), id, userID, "X", nil, nil, "", "", map[string]any{})
```

`TestTopics_Update_DBError` (currently lines 443+): apply the identical change — add one `pgxmock.AnyArg()` to `WithArgs` and a `nil` between the two `nil`s in the `r.Update(...)` call. Read the existing body and mirror the `Update_NotFound` edit exactly.

- [ ] **Step 5: Add a focused round-trip test for notifier_id**

Append to `backend/internal/db/repo/topics_test.go`:

```go
// TestTopics_Create_RoundTripsNotifierID verifies Create passes notifier_id
// through to the INSERT and scans it back out of the RETURNING row.
func TestTopics_Create_RoundTripsNotifierID(t *testing.T) {
	repo, mock := newMockTopics(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	id := uuid.New()
	userID := uuid.New()
	notifierID := uuid.New()
	now := time.Now().UTC()

	row := topicRow(id, userID, now)
	row[7] = &notifierID // notifier_id column

	rows := pgxmock.NewRows(topicColumnsAll).AddRow(row...)

	mock.ExpectQuery(`INSERT INTO topics.*notifier_id.*RETURNING`).
		WithArgs(
			userID, "faketracker", "https://example.invalid/t/1",
			"My Topic", "",
			(*uuid.UUID)(nil), // client_id
			&notifierID,       // notifier_id
			"", "",
			pgxmock.AnyArg(),
			3600, pgxmock.AnyArg(), "active",
		).
		WillReturnRows(rows)

	in := &domain.Topic{
		UserID:           userID,
		TrackerName:      "faketracker",
		URL:              "https://example.invalid/t/1",
		DisplayName:      "My Topic",
		NotifierID:       &notifierID,
		Extra:            map[string]any{"quality": "1080p"},
		CheckIntervalSec: 3600,
		NextCheckAt:      now,
		Status:           domain.TopicStatusActive,
	}

	got, err := repo.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}
	if got.NotifierID == nil || *got.NotifierID != notifierID {
		t.Errorf("Create: want NotifierID=%s, got %v", notifierID, got.NotifierID)
	}
}
```

- [ ] **Step 6: Run the repo tests to verify they fail (no impl yet)**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/db/repo/..."`
Expected: FAIL — `(*Topics).Update` has the old 8-arg signature and `scanTopic`/`Create` don't know `notifier_id`; the package won't compile.

- [ ] **Step 7: Implement the repo changes**

In `backend/internal/db/repo/topics.go`:

`topicColumns` (lines 38-43) — add `notifier_id` after `client_id`:

```go
const topicColumns = `id, user_id, tracker_name, url, display_name,
		COALESCE(image_url,''), client_id, notifier_id,
		COALESCE(download_dir,''), COALESCE(category,''), extra, COALESCE(last_hash,''),
		last_checked_at, last_updated_at, next_check_at,
		check_interval_sec, consecutive_errors, status,
		COALESCE(last_error,''), created_at, updated_at`
```

`scanTopic` (lines 45-78) — add a `notifierID` scan target after `clientID`:

```go
func scanTopic(row pgx.Row) (*domain.Topic, error) {
	var t domain.Topic
	var extraRaw []byte
	var lastChecked, lastUpdated *time.Time
	var status string
	var clientID, notifierID *uuid.UUID
	err := row.Scan(
		&t.ID, &t.UserID, &t.TrackerName, &t.URL, &t.DisplayName,
		&t.ImageURL, &clientID, &notifierID, &t.DownloadDir, &t.Category, &extraRaw, &t.LastHash,
		&lastChecked, &lastUpdated, &t.NextCheckAt,
		&t.CheckIntervalSec, &t.ConsecutiveErrors, &status,
		&t.LastError, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	t.ClientID = clientID
	t.NotifierID = notifierID
	t.LastCheckedAt = lastChecked
	// ... rest unchanged
```

`Create` (lines 81-93) — add `notifier_id` to the column list, a `$7` placeholder, and the arg (renumber the trailing placeholders):

```go
func (r *Topics) Create(ctx context.Context, t *domain.Topic) (*domain.Topic, error) {
	extra, _ := json.Marshal(t.Extra)
	q := `
INSERT INTO topics (user_id, tracker_name, url, display_name, image_url, client_id, notifier_id,
                    download_dir, category, extra, check_interval_sec, next_check_at, status)
VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,$7,NULLIF($8,''),NULLIF($9,''),$10,$11,$12,$13)
RETURNING ` + topicColumns
	row := r.pool.QueryRow(ctx, q,
		t.UserID, t.TrackerName, t.URL, t.DisplayName, t.ImageURL, t.ClientID, t.NotifierID,
		t.DownloadDir, t.Category, extra, t.CheckIntervalSec, t.NextCheckAt, string(t.Status),
	)
	return scanTopic(row)
}
```

`Update` (lines 245-263) — add `notifierID *uuid.UUID` to the signature (after `clientID`), set `notifier_id = $5`, and renumber the rest:

```go
func (r *Topics) Update(ctx context.Context, id, userID uuid.UUID, displayName string, clientID, notifierID *uuid.UUID, downloadDir, category string, extra map[string]any) (*domain.Topic, error) {
	raw, err := json.Marshal(extra)
	if err != nil {
		return nil, fmt.Errorf("topics: marshal extra: %w", err)
	}
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	row := r.pool.QueryRow(ctx, `UPDATE topics SET
		display_name = $3, client_id = $4, notifier_id = $5, download_dir = $6, category = $7,
		extra = $8, updated_at = now()
	WHERE id = $1 AND user_id = $2
	RETURNING `+topicColumns, id, userID, displayName, clientID, notifierID, downloadDir, category, raw)
	t, err := scanTopic(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}
```

Also update the doc comment above `Update` (lines 241-244) to mention `notifier`.

- [ ] **Step 8: Run the repo tests to verify they pass**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go test ./internal/db/repo/..."`
Expected: PASS. (`go build ./...` will still fail later packages — the handler/scheduler — until their tasks land; if you want a clean build now, scope to `go vet ./internal/db/...`. The repo tests themselves must pass.)

- [ ] **Step 9: Commit**

```bash
git add backend/internal/db/migrations/0007_add_topic_notifier_id.sql backend/internal/domain/domain.go backend/internal/db/repo/topics.go backend/internal/db/repo/topics_test.go
git commit -m "feat: persist per-topic notifier_id (#51)"
```

---

## Task 2: Notify dispatcher — scoped SendVia

**Files:**
- Modify: `backend/internal/notify/dispatcher.go:37-92`
- Test: `backend/internal/notify/dispatcher_test.go`

**Interfaces:**
- Consumes: `notifiersRepo.ListForUser` (existing seam — no change).
- Produces: `(*notify.Dispatcher).SendVia(ctx context.Context, userID uuid.UUID, notifierID *uuid.UUID, event string, msg domain.Message) int` — when `notifierID == nil` it behaves exactly like `Send`; when non-nil it delivers only through the user's notifier whose `ID` matches, still honouring that notifier's event subscription. Returns success count (0 or 1 for the scoped case).

- [ ] **Step 1: Write the failing tests**

Append to `backend/internal/notify/dispatcher_test.go`:

```go
func TestSendVia_NilID_BehavesLikeSend(t *testing.T) {
	uid := uuid.New()
	repo := &fakeRepo{}
	d, enc, nonce := newTestDispatcher(t, repo)

	repo.items = []*domain.Notifier{
		{ID: uuid.New(), UserID: uid, NotifierName: okPlugin.Name(), ConfigEnc: enc, ConfigNonce: nonce},
		{ID: uuid.New(), UserID: uid, NotifierName: okPlugin.Name(), ConfigEnc: enc, ConfigNonce: nonce},
	}

	got := d.SendVia(context.Background(), uid, nil, "updated", domain.Message{Title: "t"})
	if got != 2 {
		t.Errorf("want 2 (global fan-out), got %d", got)
	}
}

func TestSendVia_TargetsSingleNotifier(t *testing.T) {
	uid := uuid.New()
	repo := &fakeRepo{}
	d, enc, nonce := newTestDispatcher(t, repo)

	target := uuid.New()
	repo.items = []*domain.Notifier{
		{ID: uuid.New(), UserID: uid, NotifierName: okPlugin.Name(), ConfigEnc: enc, ConfigNonce: nonce},
		{ID: target, UserID: uid, NotifierName: okPlugin.Name(), ConfigEnc: enc, ConfigNonce: nonce},
	}

	got := d.SendVia(context.Background(), uid, &target, "updated", domain.Message{Title: "t"})
	if got != 1 {
		t.Errorf("want 1 (only the targeted notifier), got %d", got)
	}
}

func TestSendVia_TargetUnsubscribedToEvent_ReturnsZero(t *testing.T) {
	uid := uuid.New()
	repo := &fakeRepo{}
	d, enc, nonce := newTestDispatcher(t, repo)

	target := uuid.New()
	repo.items = []*domain.Notifier{
		// Targeted notifier is subscribed only to "error" — an "updated"
		// event must NOT reach it even though it was explicitly selected.
		{ID: target, UserID: uid, NotifierName: okPlugin.Name(), Events: []string{"error"}, ConfigEnc: enc, ConfigNonce: nonce},
	}

	got := d.SendVia(context.Background(), uid, &target, "updated", domain.Message{Title: "t"})
	if got != 0 {
		t.Errorf("want 0 (target not subscribed to updated), got %d", got)
	}
}

func TestSendVia_UnknownTarget_ReturnsZero(t *testing.T) {
	uid := uuid.New()
	repo := &fakeRepo{}
	d, enc, nonce := newTestDispatcher(t, repo)

	repo.items = []*domain.Notifier{
		{ID: uuid.New(), UserID: uid, NotifierName: okPlugin.Name(), ConfigEnc: enc, ConfigNonce: nonce},
	}

	missing := uuid.New()
	got := d.SendVia(context.Background(), uid, &missing, "updated", domain.Message{Title: "t"})
	if got != 0 {
		t.Errorf("want 0 (target id not in user's list), got %d", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/notify/..."`
Expected: FAIL — `d.SendVia undefined`.

- [ ] **Step 3: Refactor `Send` to extract `sendOne`, then add `SendVia`**

In `backend/internal/notify/dispatcher.go`, replace the body of `Send` (lines 41-76) so the per-notifier work lives in a shared helper, and add `SendVia` plus the helper:

```go
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
// notifierID) when notifierID is non-nil, still honouring that notifier's
// event subscription. When notifierID is nil it falls back to the global
// fan-out (Send). A notifierID that doesn't match any of the user's
// notifiers (e.g. a stale reference) delivers to nobody and returns 0 —
// the topics.notifier_id FK uses ON DELETE SET NULL so this is defensive.
// Returns the count of successes.
func (d *Dispatcher) SendVia(ctx context.Context, userID uuid.UUID, notifierID *uuid.UUID, event string, msg domain.Message) int {
	if notifierID == nil {
		return d.Send(ctx, userID, event, msg)
	}
	list, err := d.notifiers.ListForUser(ctx, userID)
	if err != nil {
		d.log.Warn().Err(err).Str("user_id", userID.String()).Msg("notify: list notifiers failed")
		return 0
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
```

Leave `subscribed` (lines 82-92) unchanged.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/notify/..."`
Expected: PASS (the new `SendVia*` tests plus all existing `Send*` tests, which now exercise `sendOne` via `Send`).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/notify/dispatcher.go backend/internal/notify/dispatcher_test.go
git commit -m "feat: add notifier-scoped SendVia to dispatcher (#51)"
```

---

## Task 3: Scheduler routing

**Files:**
- Modify: `backend/internal/scheduler/scheduler.go:86-93` (`eventNotifier`), `:422-442` (`notifyUpdated`)
- Test: `backend/internal/scheduler/scheduler_test.go:178-192` (`fakeNotifier`) + new test

**Interfaces:**
- Consumes: `(*notify.Dispatcher).SendVia` (Task 2). Real `*notify.Dispatcher` is injected as `notifier`, so it already satisfies the extended interface once the method exists.
- Produces: no new exported surface; `notifyUpdated` now routes through `t.NotifierID`.

- [ ] **Step 1: Extend the `fakeNotifier` test double first**

In `backend/internal/scheduler/scheduler_test.go`, replace the `fakeNotifier` block (lines 178-192) so it records both methods and the per-topic id:

```go
// fakeNotifier records Send / SendVia calls.
type fakeNotifier struct {
	calls          int
	lastID         uuid.UUID
	lastEvent      string
	lastMsg        domain.Message
	lastNotifierID *uuid.UUID
}

func (f *fakeNotifier) Send(_ context.Context, userID uuid.UUID, event string, msg domain.Message) int {
	f.calls++
	f.lastID = userID
	f.lastEvent = event
	f.lastMsg = msg
	f.lastNotifierID = nil
	return 1
}

func (f *fakeNotifier) SendVia(_ context.Context, userID uuid.UUID, notifierID *uuid.UUID, event string, msg domain.Message) int {
	f.calls++
	f.lastID = userID
	f.lastEvent = event
	f.lastMsg = msg
	f.lastNotifierID = notifierID
	return 1
}
```

- [ ] **Step 2: Add a routing test**

Append to `backend/internal/scheduler/scheduler_test.go`:

```go
// TestNotifyUpdated_RoutesToTopicNotifier verifies notifyUpdated forwards
// the topic's NotifierID to the dispatcher so a per-topic override is honoured.
func TestNotifyUpdated_RoutesToTopicNotifier(t *testing.T) {
	notifier := &fakeNotifier{}
	s := &Scheduler{cfg: &config.Config{PublicBaseURL: "http://x"}, notifier: notifier}

	notifierID := uuid.New()
	topic := &domain.Topic{UserID: uuid.New(), DisplayName: "My Show", NotifierID: &notifierID}

	s.notifyUpdated(context.Background(), topic, []string{"s01e01"})

	if notifier.calls != 1 {
		t.Fatalf("want 1 notification, got %d", notifier.calls)
	}
	if notifier.lastEvent != "updated" {
		t.Errorf("event = %q, want updated", notifier.lastEvent)
	}
	if notifier.lastNotifierID == nil || *notifier.lastNotifierID != notifierID {
		t.Errorf("notifierID = %v, want %s", notifier.lastNotifierID, notifierID)
	}
}

// TestNotifyUpdated_NilNotifierID_GlobalFanOut verifies a topic without an
// override passes nil through (global fan-out, unchanged behaviour).
func TestNotifyUpdated_NilNotifierID_GlobalFanOut(t *testing.T) {
	notifier := &fakeNotifier{}
	s := &Scheduler{cfg: &config.Config{PublicBaseURL: "http://x"}, notifier: notifier}

	topic := &domain.Topic{UserID: uuid.New(), DisplayName: "My Show", NotifierID: nil}

	s.notifyUpdated(context.Background(), topic, []string{"s01e01"})

	if notifier.calls != 1 {
		t.Fatalf("want 1 notification, got %d", notifier.calls)
	}
	if notifier.lastNotifierID != nil {
		t.Errorf("notifierID = %v, want nil", notifier.lastNotifierID)
	}
}
```

(If `config` isn't already imported in the test file, add `"github.com/artyomsv/marauder/backend/internal/config"`. The existing session-expiry tests construct a `Scheduler` directly, so the pattern is already present — reuse their imports.)

- [ ] **Step 3: Run the tests to verify they fail**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/scheduler/..."`
Expected: FAIL — `eventNotifier` has no `SendVia`, and `notifyUpdated` still calls `Send` (so `lastNotifierID` stays nil in the routing test).

- [ ] **Step 4: Extend the `eventNotifier` interface**

In `backend/internal/scheduler/scheduler.go` (lines 86-93), add `SendVia`:

```go
type eventNotifier interface {
	Send(ctx context.Context, userID uuid.UUID, event string, msg domain.Message) int
	SendVia(ctx context.Context, userID uuid.UUID, notifierID *uuid.UUID, event string, msg domain.Message) int
}
```

- [ ] **Step 5: Route `notifyUpdated` through the override**

In `backend/internal/scheduler/scheduler.go`, change the `Send` call in `notifyUpdated` (lines 437-441) to `SendVia` with the topic's notifier:

```go
	s.notifier.SendVia(ctx, t.UserID, t.NotifierID, "updated", domain.Message{
		Title: t.DisplayName,
		Body:  body,
		Link:  s.cfg.PublicBaseURL + "/topics",
	})
```

Leave the session-expiry `Send` call (lines 488-494) untouched — it stays on the global fan-out per the Global Constraints.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/scheduler/..."`
Expected: PASS — including the pre-existing `notifyUpdated` tests (they assert `calls`/`lastEvent`/`lastMsg`, which `SendVia` still records).

- [ ] **Step 7: Commit**

```bash
git add backend/internal/scheduler/scheduler.go backend/internal/scheduler/scheduler_test.go
git commit -m "feat: route topic update alerts to per-topic notifier (#51)"
```

---

## Task 4: API handler

**Files:**
- Modify: `backend/internal/api/handlers/topics.go:25-32` (`topicStore`), `:63-76` (`createTopicReq`), `:188-201` (Create wiring), `:210-218` (`updateTopicReq`), `:284` (Update call)
- Test: `backend/internal/api/handlers/topics_handler_test.go:45-95` (`fakeTopicStore`) + new test

**Interfaces:**
- Consumes: `(*repo.Topics).Update(...notifierID...)` (Task 1).
- Produces: `POST /topics` and `PUT /topics/{id}` accept `"notifier_id"` (a UUID string or null) and persist it onto `domain.Topic.NotifierID`.

- [ ] **Step 1: Update the `fakeTopicStore.Update` signature and add a capture field**

In `backend/internal/api/handlers/topics_handler_test.go`, the `fakeTopicStore` struct (lines 45-57) gains a capture field next to `updateClientID`:

```go
	updateClientID    *uuid.UUID
	updateNotifierID  *uuid.UUID
```

And its `Update` method (line 72) takes the new param and records it:

```go
func (s *fakeTopicStore) Update(_ context.Context, _, _ uuid.UUID, displayName string, clientID, notifierID *uuid.UUID, downloadDir, category string, extra map[string]any) (*domain.Topic, error) {
	s.updateDisplayName = displayName // keep whatever existing assignments are here
	s.updateClientID = clientID
	s.updateNotifierID = notifierID
	// ... preserve the existing body (it builds and returns a *domain.Topic).
	// Ensure the returned topic carries NotifierID so handler responses reflect it:
	// e.g. add `NotifierID: notifierID,` to the returned &domain.Topic{...} literal.
	return /* existing return, with NotifierID: notifierID added to the struct literal */, nil
}
```

> Read the current method body (lines 72-90) and splice in `s.updateNotifierID = notifierID` plus `NotifierID: notifierID` in the returned `domain.Topic` literal. Don't drop the existing `ClientID: clientID` line.

- [ ] **Step 2: Add a handler test asserting notifier_id passes through Update**

Append to `backend/internal/api/handlers/topics_handler_test.go` (mirror the existing Update test that exercises `updateClientID` — find it for the exact request/router setup helpers, then):

```go
func TestTopics_Update_PassesNotifierID(t *testing.T) {
	notifierID := uuid.New()
	store := &fakeTopicStore{
		getByID: &domain.Topic{ID: uuid.New(), Extra: map[string]any{}},
	}
	h := &Topics{Topics: store, BaseURL: "http://x"}

	body := `{"display_name":"X","notifier_id":"` + notifierID.String() + `"}`
	// Build the authenticated PUT /topics/{id} request the same way the
	// neighbouring Update tests do (claims context + chi URLParam "id").
	rr := doUpdate(t, h, store, body) // reuse the existing test harness helper

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if store.updateNotifierID == nil || *store.updateNotifierID != notifierID {
		t.Errorf("updateNotifierID = %v, want %s", store.updateNotifierID, notifierID)
	}
}
```

> The exact request-construction helper name (`doUpdate` above is illustrative) must match what the existing `Update` tests use. Read the existing `TestTopics_Update_*` cases first and copy their setup verbatim, changing only the JSON body and the assertion.

- [ ] **Step 3: Run the handler tests to verify they fail**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/api/..."`
Expected: FAIL — `topicStore.Update` signature mismatch (won't compile) and `notifier_id` not yet read from the request.

- [ ] **Step 4: Update the `topicStore` interface**

In `backend/internal/api/handlers/topics.go` (line 31):

```go
	Update(ctx context.Context, id, userID uuid.UUID, displayName string, clientID, notifierID *uuid.UUID, downloadDir, category string, extra map[string]any) (*domain.Topic, error)
```

- [ ] **Step 5: Add `notifier_id` to the request DTOs**

`createTopicReq` (lines 63-76) — add after `ClientID`:

```go
	ClientID         *uuid.UUID `json:"client_id"`
	NotifierID       *uuid.UUID `json:"notifier_id"`
```

`updateTopicReq` (lines 210-218) — add after `ClientID`:

```go
	ClientID     *uuid.UUID `json:"client_id"`
	NotifierID   *uuid.UUID `json:"notifier_id"`
```

- [ ] **Step 6: Wire it through Create and Update**

In `Create` (the `domain.Topic` literal at lines 188-201), add after `ClientID`:

```go
		ClientID:         req.ClientID,
		NotifierID:       req.NotifierID,
```

In `Update` (line 284), pass `req.NotifierID` after `req.ClientID`:

```go
	updated, uerr := h.Topics.Update(r.Context(), id, uid, req.DisplayName, req.ClientID, req.NotifierID, req.DownloadDir, req.Category, extra)
```

- [ ] **Step 7: Run the handler tests to verify they pass**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go test ./internal/api/..."`
Expected: PASS, and `go build ./...` now succeeds across the whole backend (all consumers of the changed signatures are updated).

- [ ] **Step 8: Run the full backend suite (race) to confirm nothing else broke**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go vet ./... && go test -race ./..."`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add backend/internal/api/handlers/topics.go backend/internal/api/handlers/topics_handler_test.go
git commit -m "feat: accept notifier_id on topic create/update (#51)"
```

---

## Task 5: Frontend form + API types

**Files:**
- Modify: `frontend/src/lib/api.ts:201-209` (`UpdateTopicBody`), `:274-295` (`Topic`)
- Modify: `frontend/src/components/topics/TopicForm.tsx`
- Modify: `frontend/src/components/topics/AddTopicCard.tsx:14-23` (`EMPTY`), `:30-44` (POST body)
- Modify: `frontend/src/components/topics/EditTopicCard.tsx:18-32` (`initialFrom`), `:40-56` (PUT body)

**Interfaces:**
- Consumes: `GET /notifiers` → `{ notifiers: { id, display_name, ... }[] | null }` (existing endpoint).
- Produces: `TopicFormValues.notifierId: string` (`""` = use default); POST/PUT send `notifier_id` as a UUID string, `null` (PUT) / `undefined` (POST) when empty.

- [ ] **Step 1: Extend the API types**

In `frontend/src/lib/api.ts`, add `NotifierID` to the `Topic` type (after `ClientID`, line 281):

```ts
  ClientID: string | null;
  NotifierID: string | null;
```

And `notifier_id` to `UpdateTopicBody` (after `client_id`, line 203):

```ts
  client_id?: string | null;
  notifier_id?: string | null;
```

- [ ] **Step 2: Add the notifier field to `TopicFormValues` and the form**

In `frontend/src/components/topics/TopicForm.tsx`:

Add to the `TopicFormValues` interface (after `clientId`, line 42):

```ts
  clientId: string;
  notifierId: string;
  downloadDir: string;
```

Add a `NotifierOption` interface beside `ClientOption` (after line 32):

```ts
// Minimal shape of a configured notifier for the picker — id + label only.
interface NotifierOption {
  id: string;
  display_name: string;
}
```

Add a notifiers query beside the clients query (after line 94):

```ts
  const notifiersQuery = useQuery({
    queryKey: QK.notifiers,
    queryFn: () => api.get<{ notifiers: NotifierOption[] | null }>("/notifiers"),
    staleTime: 60_000,
  });
  const notifiers = notifiersQuery.data?.notifiers ?? [];
```

Seed the notifier into the `delivery` state object (lines 186-190):

```ts
  const [delivery, setDelivery] = useState({
    clientId: initial.clientId,
    notifierId: initial.notifierId,
    downloadDir: initial.downloadDir,
    category: initial.category,
  });
```

Include it in the `onSubmit` payload (lines 194-203):

```ts
    onSubmit({
      url: effectiveUrl,
      displayName,
      quality,
      startSeason,
      startEpisode,
      clientId: delivery.clientId,
      notifierId: delivery.notifierId,
      downloadDir: delivery.downloadDir,
      category: delivery.category,
    });
```

Add the select markup right after the Client select block (after line 328), mirroring it:

```tsx
      <div className="space-y-1.5">
        <Label htmlFor="notifier">Notifier (optional)</Label>
        <select
          id="notifier"
          value={delivery.notifierId}
          onChange={(e) => setDelivery((d) => ({ ...d, notifierId: e.target.value }))}
          className={SELECT_CLASS}
        >
          <option value="">Use default notifiers</option>
          {notifiers.map((n) => (
            <option key={n.id} value={n.id}>
              {n.display_name}
            </option>
          ))}
        </select>
        <p className="text-xs text-muted-foreground">
          Route this topic's release alerts to one notifier instead of all of them.
        </p>
      </div>
```

- [ ] **Step 3: Update AddTopicCard**

In `frontend/src/components/topics/AddTopicCard.tsx`, add `notifierId: ""` to `EMPTY` (after `clientId`, line 20):

```ts
  clientId: "",
  notifierId: "",
  downloadDir: "",
```

And `notifier_id` to the POST body (after `client_id`, line 38):

```ts
        client_id: v.clientId || undefined,
        notifier_id: v.notifierId || undefined,
        download_dir: v.downloadDir || undefined,
```

- [ ] **Step 4: Update EditTopicCard**

In `frontend/src/components/topics/EditTopicCard.tsx`, add to `initialFrom` (after `clientId`, line 29):

```ts
    clientId: topic.ClientID ?? "",
    notifierId: topic.NotifierID ?? "",
    downloadDir: topic.DownloadDir ?? "",
```

And to the PUT body (after `client_id`, line 44), using `|| null` so an empty selection clears the column (matching the `client_id` semantics):

```ts
        client_id: v.clientId || null,
        notifier_id: v.notifierId || null,
        download_dir: v.downloadDir,
```

- [ ] **Step 5: Update the existing Topics test fixtures if they construct `Topic` literals**

`frontend/src/pages/Topics.test.tsx` may build `Topic` objects. TypeScript will now require `NotifierID`. Run the typecheck (next step) — if it flags missing `NotifierID`, add `NotifierID: null` to each `Topic` literal in the test (and any other test that constructs a `Topic`).

- [ ] **Step 6: Run typecheck + tests to verify**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test"`
Expected: PASS. Fix any `NotifierID`-missing type errors in test fixtures (Step 5) until green.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/lib/api.ts frontend/src/components/topics/TopicForm.tsx frontend/src/components/topics/AddTopicCard.tsx frontend/src/components/topics/EditTopicCard.tsx frontend/src/pages/Topics.test.tsx
git commit -m "feat: add notifier selector to topic form (#51)"
```

---

## Task 6: Notifier badge on the topic card

**Files:**
- Create: `frontend/src/components/topics/NotifierBadge.tsx`
- Test: `frontend/src/components/topics/NotifierBadge.test.tsx`
- Modify: `frontend/src/pages/Topics.tsx:46-52` (queries), `:206-210` (render)

**Interfaces:**
- Consumes: `GET /notifiers` (already fetched on this page after Step 3); `Topic.NotifierID` (Task 5).
- Produces: `NotifierBadge` component + `NotifierRef` type.

**Design note:** unlike `ClientBadge` (every topic delivers to exactly one client, so it always renders), a topic with no `NotifierID` uses the global fan-out — the normal default for every install. Showing a "default" badge on every card would be noise, so `NotifierBadge` renders **nothing** when there is no override. With `ON DELETE SET NULL`, a set `NotifierID` always resolves to a live notifier, so there is no "unknown notifier" branch.

- [ ] **Step 1: Write the failing component test**

Create `frontend/src/components/topics/NotifierBadge.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";

import { NotifierBadge, type NotifierRef } from "./NotifierBadge";
import type { Topic } from "@/lib/api";

function topic(overrides: Partial<Topic>): Topic {
  return {
    ID: "t1", UserID: "u1", TrackerName: "rutracker", URL: "x",
    DisplayName: "Show", ImageURL: "", ClientID: null, NotifierID: null,
    DownloadDir: "", Category: "", Extra: null, LastHash: "",
    LastCheckedAt: null, LastUpdatedAt: null, NextCheckAt: "", CheckIntervalSec: 900,
    ConsecutiveErrors: 0, Status: "active", LastError: "", CreatedAt: "", UpdatedAt: "",
    ...overrides,
  };
}

const byId = new Map<string, NotifierRef>([["n1", { id: "n1", display_name: "Main Telegram" }]]);

describe("NotifierBadge", () => {
  it("renders the notifier name when an override is set", () => {
    render(<NotifierBadge topic={topic({ NotifierID: "n1" })} notifierById={byId} />);
    expect(screen.getByText("Main Telegram")).toBeInTheDocument();
  });

  it("renders nothing when no override is set", () => {
    const { container } = render(<NotifierBadge topic={topic({ NotifierID: null })} notifierById={byId} />);
    expect(container).toBeEmptyDOMElement();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm test -- NotifierBadge"`
Expected: FAIL — module `./NotifierBadge` not found.

- [ ] **Step 3: Implement the component**

Create `frontend/src/components/topics/NotifierBadge.tsx`:

```tsx
import { Bell } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import type { Topic } from "@/lib/api";

// Minimal notifier shape needed to render the badge — id → display name.
export interface NotifierRef {
  id: string;
  display_name: string;
}

interface NotifierBadgeProps {
  topic: Topic;
  notifierById: Map<string, NotifierRef>;
}

// NotifierBadge shows which single notifier a topic's release alerts are
// routed to, when the topic overrides the default. Topics without an
// override use the global notifier fan-out (the default for every install),
// so nothing is rendered for them to keep the card uncluttered.
export function NotifierBadge({ topic, notifierById }: NotifierBadgeProps) {
  if (!topic.NotifierID) return null;
  const notifier = notifierById.get(topic.NotifierID);
  if (!notifier) return null;

  return (
    <Badge variant="outline" className="shrink-0 gap-1 font-normal text-muted-foreground">
      <Bell className="size-3" />
      {notifier.display_name}
    </Badge>
  );
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm test -- NotifierBadge"`
Expected: PASS.

- [ ] **Step 5: Render the badge on the topic card**

In `frontend/src/pages/Topics.tsx`, add a notifiers query + lookup map beside the clients ones (after line 52):

```tsx
  const { data: notifiersData } = useQuery({
    queryKey: QK.notifiers,
    queryFn: () => api.get<{ notifiers: NotifierRef[] | null }>("/notifiers"),
  });
  const notifierById = new Map((notifiersData?.notifiers ?? []).map((n) => [n.id, n]));
```

Add the import near the `ClientBadge` import (line 29):

```tsx
import { ClientBadge, type ClientRef } from "@/components/topics/ClientBadge";
import { NotifierBadge, type NotifierRef } from "@/components/topics/NotifierBadge";
```

Render it right after `<ClientBadge .../>` (after line 210):

```tsx
                      <ClientBadge
                        topic={t}
                        clientById={clientById}
                        defaultClient={defaultClient}
                      />
                      <NotifierBadge topic={t} notifierById={notifierById} />
```

- [ ] **Step 6: Run the full frontend suite**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test && npm run build"`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/components/topics/NotifierBadge.tsx frontend/src/components/topics/NotifierBadge.test.tsx frontend/src/pages/Topics.tsx
git commit -m "feat: show per-topic notifier badge on topic card (#51)"
```

---

## Final verification

- [ ] **Backend, full race suite:**
  `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go vet ./... && go test -race ./..."` → PASS
- [ ] **Frontend, full suite:**
  `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test && npm run build"` → PASS
- [ ] **Manual smoke (optional, dev stack):** create a topic with a notifier selected, confirm `topics.notifier_id` is set; delete that notifier and confirm the column reverts to `NULL` (FK `ON DELETE SET NULL`); confirm a topic with no override still notifies the full set.
- [ ] **CHANGELOG:** add a bullet under `[Unreleased]` (per CLAUDE.md release automation): `- Per-topic notifier override: route a topic's release alerts to one notifier (#51).`
- [ ] **Docs:** update `CLAUDE.md` if the `domain` / `notify` / scheduler descriptions need the new field noted (the `Topic` line and the `notify` dispatcher line). Keep it to one commit alongside the structural change.

---

## Self-Review

**Spec coverage (issue #51):**
- "Optional `Notifier` selector on Add/Edit form, default `Use default notifiers`" → Task 5.
- "Selectable at create AND edit" → Tasks 4 (API) + 5 (both cards).
- "Selected notifier used instead of default set" → Tasks 2 (`SendVia` scopes to one) + 3 (scheduler routes the topic's id).
- "No selection → unchanged" → `SendVia(nil)` ≡ `Send` (Task 2 test `TestSendVia_NilID_BehavesLikeSend`); session-expiry path untouched.
- "Respect the notifier's own enabled event types" → `SendVia` reuses `subscribed()` (Task 2 test `TestSendVia_TargetUnsubscribedToEvent_ReturnsZero`).
- "Topic card may show a notifier badge" → Task 6 (explicitly the optional item; rendered only when overridden).
- "Existing installs keep working" → Global Constraints + `nil` fallback; migration is additive + nullable.

**Singular-notifier decision:** single FK column, single-id DTO field, single-string form value — no join table anywhere. ✓

**Type/signature consistency:** `Update(... clientID, notifierID *uuid.UUID, downloadDir, category, extra)` is identical across repo (Task 1), `topicStore` interface + handler call (Task 4), and both test fakes (Tasks 1, 4). `SendVia(ctx, userID, *uuid.UUID, event, msg) int` is identical across dispatcher (Task 2), `eventNotifier` interface (Task 3), and `fakeNotifier` (Task 3). `NotifierID` column sits after `client_id` consistently in `topicColumns`, `scanTopic`, `topicColumnsAll`, and `topicRow`. ✓

**Known pre-existing debt touched:** `TopicForm.tsx` is already >250 lines (documented debt); this adds ~25 lines mirroring the client field rather than restructuring — consistent with "follow established patterns; don't unilaterally restructure."
