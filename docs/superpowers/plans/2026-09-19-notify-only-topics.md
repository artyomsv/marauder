# Notify-Only Topics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a per-topic "notify only" mode so a topic is monitored on its normal schedule and announces new releases, but never submits anything to a torrent client.

**Architecture:** One new pair of boolean columns on `topics` (`notify_only`, `notify_only_announce_current`), mirroring the `replace_on_update` / `replace_delete_data` precedent from migration `0014`. The scheduler's existing `if updated { ... }` block gains an `else`-style split: notify-only topics take a new `notifyOnlyRelease` helper that emits `release.found` and marks pending episodes seen, then falls through to the same tail (`recordResult`) as the success path — so the **new** hash is persisted and nothing is re-announced. No new event type: the issue's "release available vs download submitted" distinction is exactly the existing `release.found` vs `download.submitted`.

**Tech Stack:** Go 1.26 (chi, pgx, zerolog, goose migrations, pgxmock), React 19.2 + Vite + Tailwind 4 + shadcn, React Query, Vitest + Testing Library.

**Spec:** `docs/superpowers/specs/2026-09-19-notify-only-topics-design.md` (GitHub issue #184)

## Global Constraints

- Go indentation: tabs (gofmt). TypeScript/TSX indentation: 2 spaces.
- TypeScript object shapes use `interface`, not `type`, for new declarations. (`Topic` in `lib/api.ts` is an existing `type` — extend it in place, do not convert it.)
- Frontend component files: max 250 lines. `TopicForm.tsx` already breaches this (pre-existing tech debt, tracked in `techdebt/frontend/`); adding to it is acceptable but must be called out in the PR body.
- New DB columns must be `NOT NULL DEFAULT false` so every existing topic keeps today's behaviour with no data migration.
- No synthetic data: tests use obviously-fake values (`"old-hash"`, `"new-hash"`, `s01e01`). Never insert plausible-looking rows into user-visible tables.
- Commit messages: imperative mood, max 72 chars on the first line, body for non-trivial changes. **Never** mention AI/agentic authoring, model names, or vendor names in commits or the PR.
- The final PR title must start with `feat:` — `.github/workflows/auto-release.yml` derives the semver bump from it, and `feat` cuts a minor release.
- Backend verify command (run from repo root, Docker only — never install Go locally):
  ```bash
  docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.26 \
    sh -c "gofmt -l . | (! grep .) && go build ./... && go vet ./... && go test -race ./..."
  ```
- Frontend verify command (one-time volume setup is in `CLAUDE.md` under "Common dev commands"):
  ```bash
  docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/host:ro" -v marauder-fe-nm:/app -w //app node:22-alpine \
    sh -c "cp -r /host/src /host/vitest.config.ts /host/vite.config.ts /host/index.html /host/tsconfig*.json /app/ 2>/dev/null; npx tsc --noEmit && npx vitest run"
  ```

---

## File Structure

**Backend — create:**
- `backend/internal/db/migrations/0016_add_topic_notify_only.sql` — the two new columns.

**Backend — modify:**
- `backend/internal/db/repo/topics.go` — new `TopicFlags` struct; `topicColumns`, `scanTopic`, `Create`, `Update`.
- `backend/internal/domain/domain.go` — two new `Topic` fields.
- `backend/internal/topics/create.go` — `CreateInput` fields + build.
- `backend/internal/api/handlers/topics.go` — `topicsStore` interface, `createTopicReq`, `updateTopicReq`, `Create`, `Update`.
- `backend/internal/sonarr/poller.go` — `topicsStore` interface + the one `Update` call.
- `backend/internal/scheduler/scheduler.go` — the `if updated` split, `notifyOnlyRelease`, `notifyOnlyBody`.

**Backend — tests:**
- `backend/internal/scheduler/scheduler_test.go` — notify-only behaviour (the bulk of the risk lives here).
- `backend/internal/api/handlers/topics_handler_test.go` — round-trip of the new fields; fake store signature.
- `backend/internal/sonarr/poller_test.go` — fake store signature.
- `backend/internal/db/repo/topics_test.go` — pgxmock SQL pins.
- `backend/internal/db/repo/integration_test.go` — real-Postgres round-trip.

**Frontend — create:**
- `frontend/src/components/topics/NotifyOnlyBadge.tsx` + `NotifyOnlyBadge.test.tsx` — the watchlist badge. Its own file because badges are one-per-file here (`SonarrBadge.tsx`, `ClientBadge.tsx`, `NotifierBadge.tsx`).

**Frontend — modify:**
- `frontend/src/lib/api.ts` — `Topic` type + `UpdateTopicBody`.
- `frontend/src/components/topics/TopicForm.tsx` — form values, state, the toggle, conditional hiding.
- `frontend/src/components/topics/AddTopicCard.tsx` — defaults + POST body.
- `frontend/src/components/topics/EditTopicCard.tsx` — initial values + PUT body.
- `frontend/src/components/topics/TopicRow.tsx` — mount the badge.
- `frontend/src/components/topics/ClientBadge.tsx` — suppress for notify-only topics.

**Docs — create/modify:**
- `docs/notify-only.md` (new), `CLAUDE.md`, `CHANGELOG.md`.

---

## Task 1: Group topic flags into `repo.TopicFlags`

**Why this task exists:** `Topics.Update` already takes `replaceOnUpdate, replaceDeleteData bool` positionally. This feature adds two more booleans. Four adjacent `bool` parameters cannot be told apart by the compiler — a transposed pair silently swaps two policies, and the signature is declared in three places plus two test fakes. Refactor first, then the feature only adds named struct fields. **Pure refactor: no behaviour change, no new columns.**

**Files:**
- Modify: `backend/internal/db/repo/topics.go:492` (the `Update` signature)
- Modify: `backend/internal/api/handlers/topics.go:38` (interface), `:336` (call site)
- Modify: `backend/internal/sonarr/poller.go:51` (interface), `:329-331` (call site)
- Test: `backend/internal/api/handlers/topics_handler_test.go:134` (fake), `backend/internal/sonarr/poller_test.go:129` (fake), `backend/internal/api/handlers/topics_reset_test.go:523` (third fake, `concurrentResetStore.Update` — its bool parameters are **unnamed**, so a grep for the named signature misses it), `backend/internal/db/repo/topics_test.go:458,483,503` (three direct `r.Update(...)` calls, not declarations)

**Interfaces:**
- Consumes: nothing.
- Produces: `repo.TopicFlags` struct with fields `ReplaceOnUpdate bool`, `ReplaceDeleteData bool`. Signature becomes:
  ```go
  Update(ctx context.Context, id, userID uuid.UUID, displayName string, clientID, notifierID *uuid.UUID, downloadDir, category string, flags TopicFlags, extra map[string]any) (*domain.Topic, error)
  ```
  Later tasks add `NotifyOnly` and `NotifyOnlyAnnounceCurrent` fields to this struct.

- [ ] **Step 1: Add the struct and change the repo signature**

In `backend/internal/db/repo/topics.go`, immediately above `func (r *Topics) Update` (line ~486, after the doc comment block that ends `// topic doesn't belong to the user.`), insert:

```go
// TopicFlags groups a topic's boolean delivery policies. They travel as a
// struct rather than as positional parameters because Update would otherwise
// take several adjacent bools, which the compiler cannot tell apart: a
// transposed pair would silently swap two policies and no test that does not
// assert on both would notice.
type TopicFlags struct {
	// ReplaceOnUpdate opts the topic into the "replace previous version"
	// policy (issue #101); ReplaceDeleteData also deletes the old torrent's
	// files from disk.
	ReplaceOnUpdate   bool
	ReplaceDeleteData bool
}
```

Change the signature and the two uses in the SQL args:

```go
func (r *Topics) Update(ctx context.Context, id, userID uuid.UUID, displayName string, clientID, notifierID *uuid.UUID, downloadDir, category string, flags TopicFlags, extra map[string]any) (*domain.Topic, error) {
```

and in the `QueryRow` argument list replace the trailing `replaceOnUpdate, replaceDeleteData` with `flags.ReplaceOnUpdate, flags.ReplaceDeleteData`. The SQL text itself is unchanged.

- [ ] **Step 2: Update the two interface declarations**

`backend/internal/api/handlers/topics.go:38` and `backend/internal/sonarr/poller.go:51` both declare the same method. Replace `replaceOnUpdate, replaceDeleteData bool` with `flags repo.TopicFlags` in both. Both files already import `repo`.

- [ ] **Step 3: Update the two call sites**

`backend/internal/api/handlers/topics.go:336`:

```go
	updated, uerr := h.Topics.Update(r.Context(), id, uid, req.DisplayName, req.ClientID, req.NotifierID, req.DownloadDir, req.Category, repo.TopicFlags{
		ReplaceOnUpdate:   replaceOnUpdate,
		ReplaceDeleteData: replaceDeleteData,
	}, extra)
```

`backend/internal/sonarr/poller.go:329-331`:

```go
	if _, err := p.topics.Update(ctx, existing.ID, ownerID, existing.DisplayName,
		clientID, existing.NotifierID, downloadDir, category,
		repo.TopicFlags{
			ReplaceOnUpdate:   existing.ReplaceOnUpdate,
			ReplaceDeleteData: existing.ReplaceDeleteData,
		}, mergedExtra); err != nil {
```

- [ ] **Step 4: Update every remaining caller in tests (four files)**

There are **four** test files, not two. Two declare a fake `Update`, one declares a third fake whose bool parameters are unnamed, and one calls `r.Update` directly three times. Every one of these is behaviour-neutral: preserve the exact values each call already passes.

`backend/internal/db/repo/topics_test.go` — three direct calls at lines ~458, ~483, ~503. Each passes two positional bools; wrap them, keeping each call's own values:

```go
	// :458 passes true, false
	repo.TopicFlags{ReplaceOnUpdate: true, ReplaceDeleteData: false},
	// :483 and :503 pass false, true
	repo.TopicFlags{ReplaceOnUpdate: false, ReplaceDeleteData: true},
```
(inside the `repo` package itself the type is unqualified: `TopicFlags{...}`.)

`backend/internal/api/handlers/topics_reset_test.go:523` — the third fake, `concurrentResetStore.Update`. Its two bool parameters are unnamed (`bool, bool`), which is why they are easy to miss. Replace them with one `repo.TopicFlags` parameter; the body returns `nil, nil` and does not read them, so nothing else changes.



`backend/internal/api/handlers/topics_handler_test.go:134` — change the parameter list to match and rewrite the body's uses of `replaceOnUpdate` / `replaceDeleteData` to `flags.ReplaceOnUpdate` / `flags.ReplaceDeleteData`:

```go
func (s *fakeTopicStore) Update(_ context.Context, _, _ uuid.UUID, displayName string, clientID, notifierID *uuid.UUID, downloadDir, category string, flags repo.TopicFlags, extra map[string]any) (*domain.Topic, error) {
```

`backend/internal/sonarr/poller_test.go:129` — same change. Read each fake's body and replace every reference; do not change what it records or returns.

- [ ] **Step 5: Verify the refactor is behaviour-neutral**

Run:
```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.26 \
  sh -c "gofmt -l . | (! grep .) && go build ./... && go vet ./... && go test -race ./..."
```
Expected: PASS, with **zero** test changes beyond the signature and call-shape edits of Step 4. If any assertion had to change, the refactor was not behaviour-neutral — revert and investigate.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/db/repo/topics.go backend/internal/api/handlers/topics.go \
  backend/internal/sonarr/poller.go backend/internal/api/handlers/topics_handler_test.go \
  backend/internal/sonarr/poller_test.go backend/internal/api/handlers/topics_reset_test.go \
  backend/internal/db/repo/topics_test.go
git commit -m "refactor: group topic policy flags into repo.TopicFlags"
```

---

## Task 2: Migration, domain fields, repo persistence

**Files:**
- Create: `backend/internal/db/migrations/0016_add_topic_notify_only.sql`
- Modify: `backend/internal/domain/domain.go:88-92` (after `ReplaceDeleteData`)
- Modify: `backend/internal/db/repo/topics.go` — `topicColumns` (~:38), `scanTopic` (~:56), `Create` (~:84), `TopicFlags` (from Task 1), `Update` args
- Test: `backend/internal/db/repo/topics_test.go`, `backend/internal/db/repo/integration_test.go`

**Interfaces:**
- Consumes: `repo.TopicFlags` (Task 1).
- Produces: `domain.Topic.NotifyOnly bool`, `domain.Topic.NotifyOnlyAnnounceCurrent bool`; `repo.TopicFlags.NotifyOnly bool`, `repo.TopicFlags.NotifyOnlyAnnounceCurrent bool`. Both columns round-trip through `Create`, `GetByID`, `ListForUser` and `Update`.

- [ ] **Step 1: Write the failing integration test**

Add to `backend/internal/db/repo/integration_test.go` (it is `//go:build integration` and self-skips without `MARAUDER_TEST_DB_URL`). Read the file's existing helpers first — reuse whatever it already uses to make a throwaway user and a topic; the snippet below names them `newTestUser(t, pool)` and assumes a `*repo.Topics`, so adapt the two setup lines to match what is actually there.

```go
func TestTopicsNotifyOnlyRoundTrip(t *testing.T) {
	pool := testPool(t)
	topicsRepo := repo.NewTopics(pool)
	userID := newTestUser(t, pool)
	ctx := context.Background()

	created, err := topicsRepo.Create(ctx, &domain.Topic{
		UserID:                    userID,
		TrackerName:               "faketracker",
		URL:                       "https://example.com/notify-only-roundtrip",
		DisplayName:               "Notify Only Round Trip",
		NotifyOnly:                true,
		NotifyOnlyAnnounceCurrent: true,
		CheckIntervalSec:          900,
		NextCheckAt:               time.Now().UTC(),
		Status:                    domain.TopicStatusActive,
		Extra:                     map[string]any{},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !created.NotifyOnly || !created.NotifyOnlyAnnounceCurrent {
		t.Fatalf("Create did not round-trip the flags: %+v", created)
	}

	// A plain topic must default to the historical behaviour.
	plain, err := topicsRepo.Create(ctx, &domain.Topic{
		UserID:           userID,
		TrackerName:      "faketracker",
		URL:              "https://example.com/notify-only-default",
		DisplayName:      "Default",
		CheckIntervalSec: 900,
		NextCheckAt:      time.Now().UTC(),
		Status:           domain.TopicStatusActive,
		Extra:            map[string]any{},
	})
	if err != nil {
		t.Fatalf("Create plain: %v", err)
	}
	if plain.NotifyOnly || plain.NotifyOnlyAnnounceCurrent {
		t.Fatalf("expected both flags false by default, got %+v", plain)
	}

	// Update must persist both, and GetByID must read them back.
	if _, err := topicsRepo.Update(ctx, plain.ID, userID, plain.DisplayName, nil, nil, "", "",
		repo.TopicFlags{NotifyOnly: true, NotifyOnlyAnnounceCurrent: false}, map[string]any{}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := topicsRepo.GetByID(ctx, plain.ID, &userID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !got.NotifyOnly || got.NotifyOnlyAnnounceCurrent {
		t.Fatalf("Update did not persist the flags: %+v", got)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
docker network create marauder-itest-net
docker run --rm -d --name marauder-itest-pg --network marauder-itest-net \
  -e POSTGRES_PASSWORD=test -e POSTGRES_DB=marauder_test postgres:17-alpine
docker run --rm --network marauder-itest-net -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend \
  -e MARAUDER_TEST_DB_URL="postgres://postgres:test@marauder-itest-pg:5432/marauder_test?sslmode=disable" \
  golang:1.26 sh -c "go test -tags=integration -race ./internal/db/repo/... -run TestTopicsNotifyOnly -v"
```
Expected: FAIL to compile — `unknown field NotifyOnly in struct literal of type domain.Topic`.

Leave the Postgres container running for step 6; tear it down at the end of the task with
`docker rm -f marauder-itest-pg && docker network rm marauder-itest-net`.

- [ ] **Step 3: Write the migration**

Create `backend/internal/db/migrations/0016_add_topic_notify_only.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
-- Per-topic notify-only mode (issue #184). When notify_only is true the
-- scheduler keeps checking the topic on its normal schedule and announces new
-- releases, but never resolves a client or submits a torrent — so a topic can
-- be watched without a download client configured at all.
-- notify_only_announce_current additionally announces the release already on
-- the page at the topic's FIRST check (and the first after a reset), for users
-- who want confirmation that the watch works. Both default to false: existing
-- topics keep downloading, and adding a topic stays silent.
ALTER TABLE topics
    ADD COLUMN notify_only                   BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN notify_only_announce_current  BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE topics
    DROP COLUMN notify_only,
    DROP COLUMN notify_only_announce_current;
-- +goose StatementEnd
```

- [ ] **Step 4: Add the domain fields**

In `backend/internal/domain/domain.go`, directly after the `ReplaceOnUpdate` / `ReplaceDeleteData` pair (line ~91-92) and before `Extra`:

```go
	// NotifyOnly turns the topic into a watch-only entry (issue #184): the
	// scheduler still checks it on schedule and still emits release.found, but
	// never resolves a client and never submits a payload, so no download
	// client is required. Per-episode trackers still have their pending
	// episodes marked seen, so switching back to download mode fetches only
	// what appears afterwards rather than the accumulated backlog.
	NotifyOnly bool
	// NotifyOnlyAnnounceCurrent announces the release already on the page at
	// the topic's first check — and the first check after a reset, which also
	// clears LastHash. Off by default so adding a topic is silent.
	NotifyOnlyAnnounceCurrent bool
```

- [ ] **Step 5: Wire the columns through the repo**

In `backend/internal/db/repo/topics.go`:

`topicColumns` — append to the final line:
```go
		replace_on_update, replace_delete_data, notify_only, notify_only_announce_current`
```

`scanTopic` — append to the final `row.Scan` line:
```go
		&t.ReplaceOnUpdate, &t.ReplaceDeleteData, &t.NotifyOnly, &t.NotifyOnlyAnnounceCurrent,
```

`Create` — add the two columns, two placeholders and two values:
```go
INSERT INTO topics (user_id, tracker_name, url, display_name, image_url, client_id, notifier_id,
                    download_dir, category, extra, check_interval_sec, next_check_at, status,
                    display_name_is_placeholder, replace_on_update, replace_delete_data,
                    notify_only, notify_only_announce_current)
VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,$7,NULLIF($8,''),NULLIF($9,''),$10,$11,$12,$13,$14,$15,$16,$17,$18)
RETURNING ` + topicColumns
```
and in the args list, after `t.ReplaceOnUpdate, t.ReplaceDeleteData,`:
```go
		t.NotifyOnly, t.NotifyOnlyAnnounceCurrent,
```

`TopicFlags` — add the two fields (keep the doc comments from the domain type short here):
```go
	// NotifyOnly / NotifyOnlyAnnounceCurrent are the notify-only watch mode
	// (issue #184). See domain.Topic for the full semantics.
	NotifyOnly                bool
	NotifyOnlyAnnounceCurrent bool
```

**Also fix a godoc regression Task 1 introduced here** (carried forward as a Minor finding from Task 1's review). Task 1 inserted the `TopicFlags` doc comment directly under `Update`'s doc comment with no blank line, so Go now attaches the whole combined block to `TopicFlags` and `Update` is left with no doc comment at all. Move `Update`'s doc block back down so it sits immediately above `func (r *Topics) Update`, leaving a blank line between it and the `TopicFlags` type. The two blocks are:

```go
// TopicFlags groups a topic's boolean delivery policies. ...
type TopicFlags struct { ... }

// Update edits a topic's user-editable fields (display name, client, notifier,
// download dir, category, and the capability Extra map). It does NOT
// touch url/tracker/status/hash/scheduling. Returns ErrNotFound when the
// topic doesn't belong to the user.
func (r *Topics) Update(...
```

Do not reword either comment — this is purely moving the `Update` block below the type.

`Update` — add to the SET clause and the args:
```go
		extra = $8, replace_on_update = $9, replace_delete_data = $10,
		notify_only = $11, notify_only_announce_current = $12,
```
and append `flags.NotifyOnly, flags.NotifyOnlyAnnounceCurrent` to the `QueryRow` arguments, after `flags.ReplaceDeleteData`.

- [ ] **Step 6: Run the integration test to verify it passes**

```bash
docker run --rm --network marauder-itest-net -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend \
  -e MARAUDER_TEST_DB_URL="postgres://postgres:test@marauder-itest-pg:5432/marauder_test?sslmode=disable" \
  golang:1.26 sh -c "go test -tags=integration -race ./internal/db/repo/... -run TestTopicsNotifyOnly -v"
```
Expected: PASS.

- [ ] **Step 7: Fix the pgxmock SQL pins**

`backend/internal/db/repo/topics_test.go` pins SQL text with regexes. Run:
```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.26 \
  sh -c "go test -race ./internal/db/repo/..."
```
Any failure is a pin that no longer matches the widened column list or the new `$11`/`$12` placeholders. Update each failing regex and each `pgxmock.NewRows([]string{...})` column list to include `notify_only` and `notify_only_announce_current` in the same position `topicColumns` puts them (last). Do **not** loosen a regex to `.*` to make it pass — the pins exist to catch exactly this kind of drift.

- [ ] **Step 8: Full backend verify**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.26 \
  sh -c "gofmt -l . | (! grep .) && go build ./... && go vet ./... && go test -race ./..."
```
Expected: PASS. (`topics/create.go`, `handlers` and `sonarr` still compile — they just never set the new fields yet.)

Tear down the test DB: `docker rm -f marauder-itest-pg && docker network rm marauder-itest-net`

- [ ] **Step 9: Commit**

```bash
git add backend/internal/db/migrations/0016_add_topic_notify_only.sql \
  backend/internal/domain/domain.go backend/internal/db/repo/topics.go \
  backend/internal/db/repo/topics_test.go backend/internal/db/repo/integration_test.go
git commit -m "feat: add notify_only topic columns"
```

---

## Task 3: Expose notify-only through the create and update API

**Files:**
- Modify: `backend/internal/topics/create.go:58-73` (`CreateInput`), `:165-181` (the `domain.Topic` literal)
- Modify: `backend/internal/api/handlers/topics.go:106-125` (`createTopicReq`), `:171-196` (`CreateInput` literal), `:242-255` (`updateTopicReq`), `:326-340` (pointer-preserve + `Update` call)
- Modify: `backend/internal/sonarr/poller.go:329-335` (forward `existing.NotifyOnly`)
- Test: `backend/internal/api/handlers/topics_handler_test.go`

**Interfaces:**
- Consumes: `domain.Topic.NotifyOnly`, `domain.Topic.NotifyOnlyAnnounceCurrent`, `repo.TopicFlags` (Task 2).
- Produces: JSON fields `notify_only` (bool on POST, `*bool` on PUT) and `notify_only_announce_current` (same shape); `topics.CreateInput.NotifyOnly bool` and `.NotifyOnlyAnnounceCurrent bool`.

- [ ] **Step 1: Write the failing handler tests**

Add to `backend/internal/api/handlers/topics_handler_test.go`. Read the file's existing update tests (around `:183`, `:230`) and copy their request/response plumbing exactly — the snippet below assumes a `newTopicsHandler(t, store)`-style helper and a `fakeTopicStore` that records the last `Update` call; adapt names to what is actually there.

```go
// PUT /topics/{id} must persist notify_only when supplied, and must PRESERVE
// the topic's current value when the field is omitted — the same pointer
// semantics replace_on_update uses (issue #184).
func TestUpdateTopic_NotifyOnlyPointerSemantics(t *testing.T) {
	store := &fakeTopicStore{topic: &domain.Topic{
		ID: topicID, UserID: userID, NotifyOnly: true, NotifyOnlyAnnounceCurrent: true,
		Extra: map[string]any{},
	}}
	h := newTopicsHandler(t, store)

	// Omitted → preserved.
	w := httptest.NewRecorder()
	req := newAuthedRequest(t, http.MethodPut, "/topics/"+topicID.String(),
		`{"display_name":"x"}`, userID, topicID)
	h.Update(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !store.lastFlags.NotifyOnly || !store.lastFlags.NotifyOnlyAnnounceCurrent {
		t.Errorf("omitted flags must be preserved, got %+v", store.lastFlags)
	}

	// Supplied false → applied.
	w = httptest.NewRecorder()
	req = newAuthedRequest(t, http.MethodPut, "/topics/"+topicID.String(),
		`{"display_name":"x","notify_only":false}`, userID, topicID)
	h.Update(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if store.lastFlags.NotifyOnly {
		t.Errorf("notify_only:false must be applied, got %+v", store.lastFlags)
	}
	if !store.lastFlags.NotifyOnlyAnnounceCurrent {
		t.Errorf("announce_current was omitted and must still be preserved, got %+v", store.lastFlags)
	}
}
```

Add a `lastFlags repo.TopicFlags` field to `fakeTopicStore` and record it in its `Update` method.

- [ ] **Step 2: Run to verify it fails**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.26 \
  sh -c "go test -race ./internal/api/handlers/... -run TestUpdateTopic_NotifyOnly -v"
```
Expected: FAIL — `store.lastFlags.NotifyOnly` is false because the handler never reads the field.

- [ ] **Step 3: Add the fields to `topics.CreateInput` and the build**

`backend/internal/topics/create.go` — after `ReplaceDeleteData *bool` in `CreateInput`:

```go
	// NotifyOnly makes the topic watch-only (issue #184): monitored and
	// announced, never submitted to a client. NotifyOnlyAnnounceCurrent
	// additionally announces the release already present at the first check.
	NotifyOnly                bool
	NotifyOnlyAnnounceCurrent bool
```

and in the `domain.Topic` literal, after `ReplaceDeleteData: replaceDeleteData,`:

```go
		NotifyOnly:                in.NotifyOnly,
		NotifyOnlyAnnounceCurrent: in.NotifyOnlyAnnounceCurrent,
```

- [ ] **Step 4: Add the request fields and wire the handler**

`backend/internal/api/handlers/topics.go` — in `createTopicReq`, after `ReplaceDeleteData *bool`:

```go
	// NotifyOnly makes the topic watch-only (issue #184). Plain bool, not a
	// pointer: false is the correct default for a new topic, so there is no
	// "omitted means something else" case to express.
	NotifyOnly                bool `json:"notify_only,omitempty"`
	NotifyOnlyAnnounceCurrent bool `json:"notify_only_announce_current,omitempty"`
```

In the `topics.CreateInput{...}` literal, after `ReplaceDeleteData: req.ReplaceDeleteData,`:

```go
		NotifyOnly:                req.NotifyOnly,
		NotifyOnlyAnnounceCurrent: req.NotifyOnlyAnnounceCurrent,
```

In `updateTopicReq`, after `ReplaceDeleteData *bool`:

```go
	// Pointers so an omitted field preserves the topic's current value
	// (issue #184), matching the replace-* flags above.
	NotifyOnly                *bool `json:"notify_only,omitempty"`
	NotifyOnlyAnnounceCurrent *bool `json:"notify_only_announce_current,omitempty"`
```

In `Update`, after the existing `replaceDeleteData` preserve block:

```go
	notifyOnly := existing.NotifyOnly
	if req.NotifyOnly != nil {
		notifyOnly = *req.NotifyOnly
	}
	notifyOnlyAnnounceCurrent := existing.NotifyOnlyAnnounceCurrent
	if req.NotifyOnlyAnnounceCurrent != nil {
		notifyOnlyAnnounceCurrent = *req.NotifyOnlyAnnounceCurrent
	}
```

and extend the `repo.TopicFlags` literal in the `Update` call:

```go
	}, extra)
```
becomes
```go
	updated, uerr := h.Topics.Update(r.Context(), id, uid, req.DisplayName, req.ClientID, req.NotifierID, req.DownloadDir, req.Category, repo.TopicFlags{
		ReplaceOnUpdate:           replaceOnUpdate,
		ReplaceDeleteData:         replaceDeleteData,
		NotifyOnly:                notifyOnly,
		NotifyOnlyAnnounceCurrent: notifyOnlyAnnounceCurrent,
	}, extra)
```

**No new validation.** A client is already optional on both POST (`ClientID *uuid.UUID`) and the form, so notify-only needs nothing relaxed. Do not reject `notify_only` combined with `replace_on_update` — a stored `replace_on_update:true` is inert while notify-only is on and must survive a toggle back.

- [ ] **Step 5: Forward the flags in the Sonarr poller**

`backend/internal/sonarr/poller.go` — extend the `repo.TopicFlags` literal from Task 1:

```go
		repo.TopicFlags{
			ReplaceOnUpdate:           existing.ReplaceOnUpdate,
			ReplaceDeleteData:         existing.ReplaceDeleteData,
			NotifyOnly:                existing.NotifyOnly,
			NotifyOnlyAnnounceCurrent: existing.NotifyOnlyAnnounceCurrent,
		}, mergedExtra); err != nil {
```

Sonarr's create path goes through `topics.BuildAndCreate` without setting the new fields, so imported topics default to download mode — correct.

- [ ] **Step 6: Run the tests to verify they pass**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.26 \
  sh -c "gofmt -l . | (! grep .) && go build ./... && go vet ./... && go test -race ./..."
```
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/topics/create.go backend/internal/api/handlers/topics.go \
  backend/internal/sonarr/poller.go backend/internal/api/handlers/topics_handler_test.go
git commit -m "feat: accept notify_only on topic create and update"
```

---

## Task 4: Scheduler notify-only branch (single-release topics)

**Files:**
- Modify: `backend/internal/scheduler/scheduler.go:413-490` (split the `if updated` body), plus a new helper after `notifyUpdated` (~:557)
- Test: `backend/internal/scheduler/scheduler_test.go`

**Interfaces:**
- Consumes: `domain.Topic.NotifyOnly`, `domain.Topic.NotifyOnlyAnnounceCurrent` (Task 2); existing `isEpisodic(tr registry.Tracker) bool` (`scheduler.go:924`); existing `s.emit.Emit`, `s.fetchAuthorComment`.
- Produces: `func (s *Scheduler) notifyOnlyRelease(ctx context.Context, log zerolog.Logger, t *domain.Topic, tr registry.Tracker, check *domain.Check, authorComment string)` and `func notifyOnlyBody(labels []string) string`. Task 5 extends `notifyOnlyRelease` with episode marking.

**Design note — why the branch falls through instead of returning early.** `runCheck`'s tail (display-name self-heal, `check.completed`, `recordResult`, `recordChecked`) must still run. Returning early would duplicate four blocks. Instead the download machinery becomes the `else` arm, `anySubmitted` stays `false` (so `notifyUpdated` is correctly skipped), and the existing tail call `s.recordResult(ctx, log, t, check.Hash, updated || anySubmitted, nextCheckAt, "", nil)` persists the **new** hash with `updated=true` — exactly the success-path shape notify-only needs.

- [ ] **Step 1: Write the failing tests**

Add to `backend/internal/scheduler/scheduler_test.go`:

```go
// A notify-only topic must announce the release, never touch a client, and
// advance the persisted hash so the same release is not re-announced (#184).
func TestRunCheck_NotifyOnly_AnnouncesAndSkipsClient(t *testing.T) {
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{check: &domain.Check{Hash: "new-hash", Extra: map[string]any{}}, err: nil},
		},
	}
	f := newFixture(t, tr)
	f.topic.NotifyOnly = true

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if f.clientPlugin.addCalls != 0 {
		t.Errorf("notify-only must not submit to a client, got %d Add calls", f.clientPlugin.addCalls)
	}
	if n := len(f.clients.getDefaultCalls); n != 0 {
		t.Errorf("notify-only must not resolve a client at all, got %d GetDefault calls", n)
	}
	if got := f.emitter.ofType(events.ReleaseFound); len(got) != 1 {
		t.Fatalf("expected exactly 1 release.found, got %d", len(got))
	}
	if got := f.emitter.ofType(events.DownloadSubmitted); len(got) != 0 {
		t.Errorf("notify-only must not emit download.submitted, got %d", len(got))
	}
	ev := f.emitter.ofType(events.ReleaseFound)[0]
	if ev.SourceURL != f.topic.URL {
		t.Errorf("expected SourceURL %q, got %q", f.topic.URL, ev.SourceURL)
	}
	if strings.Contains(ev.Body, "Sent to client") {
		t.Errorf("body must not imply a download happened: %q", ev.Body)
	}
	rec := f.lastRecord(t)
	if rec.hash != "new-hash" {
		t.Errorf("notify-only must advance the hash, got %q", rec.hash)
	}
	if rec.errMsg != "" {
		t.Errorf("notify-only is not a failure, got errMsg %q", rec.errMsg)
	}
}

// The release.found emit must NOT be gated on ConsecutiveErrors. The download
// path can afford that gate because it re-persists the OLD hash and replays
// next tick; notify-only persists the NEW hash on this very tick, so a
// suppressed event is lost for good.
func TestRunCheck_NotifyOnly_EmitsDespitePriorErrors(t *testing.T) {
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{check: &domain.Check{Hash: "new-hash", Extra: map[string]any{}}, err: nil},
		},
	}
	f := newFixture(t, tr)
	f.topic.NotifyOnly = true
	f.topic.ConsecutiveErrors = 5

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if got := f.emitter.ofType(events.ReleaseFound); len(got) != 1 {
		t.Fatalf("expected release.found despite prior errors, got %d", len(got))
	}
}

// The first check of a notify-only topic (LastHash == "") is silent unless the
// user opted in — adding a topic must not announce what is already there.
func TestRunCheck_NotifyOnly_FirstCheckSilentByDefault(t *testing.T) {
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{check: &domain.Check{Hash: "first-hash", Extra: map[string]any{}}, err: nil},
		},
	}
	f := newFixture(t, tr)
	f.topic.NotifyOnly = true
	f.topic.LastHash = ""

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if got := f.emitter.ofType(events.ReleaseFound); len(got) != 0 {
		t.Errorf("first check must be silent by default, got %d release.found", len(got))
	}
	// It must still baseline, or the NEXT check would announce this same release.
	if rec := f.lastRecord(t); rec.hash != "first-hash" {
		t.Errorf("first check must still persist the baseline hash, got %q", rec.hash)
	}
}

func TestRunCheck_NotifyOnly_FirstCheckAnnouncesWhenOptedIn(t *testing.T) {
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{check: &domain.Check{Hash: "first-hash", Extra: map[string]any{}}, err: nil},
		},
	}
	f := newFixture(t, tr)
	f.topic.NotifyOnly = true
	f.topic.NotifyOnlyAnnounceCurrent = true
	f.topic.LastHash = ""

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if got := f.emitter.ofType(events.ReleaseFound); len(got) != 1 {
		t.Errorf("opted-in first check must announce, got %d release.found", len(got))
	}
}
```

`fakeClients` may not record `GetDefault` calls yet. Read it (`scheduler_test.go:~150`) and add a `getDefaultCalls []uuid.UUID` slice appended to in its `GetDefault` method if it is missing.

- [ ] **Step 2: Run to verify they fail**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.26 \
  sh -c "go test -race ./internal/scheduler/... -run TestRunCheck_NotifyOnly -v"
```
Expected: FAIL — the client is still called and `release.found` still carries the download-mode body.

- [ ] **Step 3: Split the `if updated` block**

In `backend/internal/scheduler/scheduler.go`, inside `if updated {`, after `authorComment = s.fetchAuthorComment(ctx, log, t, tr, creds)` (~:424), wrap everything from the existing `release.found` emit down to the `replacePrevious` call in an `if t.NotifyOnly { ... } else { ... }`:

```go
		if t.NotifyOnly {
			// Watch-only topic (issue #184): announce, never deliver. No client
			// is resolved, so this mode works with no download client at all.
			// Control falls through to the shared tail below, which persists
			// the NEW hash — there is no download to retry, so nothing is
			// gained by replaying the change next tick.
			s.notifyOnlyRelease(ctx, log, t, tr, check, authorComment)
		} else {
			// ... the existing body, unchanged: the ConsecutiveErrors-gated
			// release.found emit, priorDeliveries, downloadAllPending, the
			// dlErr branch (which still returns early), and replacePrevious.
		}
```

Keep the existing `else` body **byte-for-byte** apart from the added indentation, including the `return` inside the `dlErr` branch. The variables `anySubmitted`, `delivered` and `deliveredHashes` are declared outside `if updated`, so the `else` arm still assigns them and the notify-only arm leaves `anySubmitted == false`.

- [ ] **Step 4: Add the helper**

Add after `notifyUpdated` (~:557):

```go
// notifyOnlyRelease handles a detected update on a notify-only topic: it
// announces the release and never resolves or contacts a torrent client.
// Task 5 extends it to mark a per-episode tracker's pending episodes seen.
func (s *Scheduler) notifyOnlyRelease(ctx context.Context, log zerolog.Logger, t *domain.Topic, tr registry.Tracker, check *domain.Check, authorComment string) {
	pendingHuman := extra.StringSlice(check.Extra, "pending_human")

	// The first check of a topic — and the first after a reset, which also
	// clears LastHash — has no baseline to compare against, so "changed" here
	// only means "seen for the first time". Announce it only when the user
	// opted in, so adding a topic stays silent.
	announce := t.LastHash != "" || t.NotifyOnlyAnnounceCurrent

	// Deliberately NOT gated on t.ConsecutiveErrors, unlike the download
	// path's emit. That gate is safe there because a failed download
	// re-persists the OLD hash and replays the same release next tick. This
	// branch persists the NEW hash on this very tick, so a suppressed event is
	// lost for good rather than delayed.
	if announce && s.emit != nil {
		s.emit.Emit(ctx, events.Event{
			UserID: t.UserID, TopicID: &t.ID, NotifierID: t.NotifierID,
			Type: events.ReleaseFound, Severity: "info",
			Title: t.DisplayName, Body: notifyOnlyBody(pendingHuman),
			Link: s.cfg.PublicBaseURL + "/topics", SourceURL: t.URL,
			AuthorComment: authorComment,
			Data:          map[string]any{"notify_only": true},
		})
	}
}

// notifyOnlyBody builds the release.found body for a watch-only topic. The
// wording must never imply a download happened. Episode labels are capped the
// same way notifyUpdated caps them, so one catch-up tick cannot produce a wall
// of text in a Telegram message.
func notifyOnlyBody(labels []string) string {
	if len(labels) == 0 {
		return "New release available — not downloaded (notify-only topic)"
	}
	const maxList = 10
	shown := labels
	overflow := 0
	if len(shown) > maxList {
		overflow = len(shown) - maxList
		shown = shown[:maxList]
	}
	body := "New episodes available — not downloaded: " + strings.Join(shown, ", ")
	if overflow > 0 {
		body += fmt.Sprintf(" (+%d more)", overflow)
	}
	return body
}
```

The `tr` parameter is unused until Task 5. Take it anyway and do **not** add a `_ = tr` line: Go only rejects unused *locals*, never unused function parameters, so this compiles and vets cleanly as written, and Task 5 fills it in.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.26 \
  sh -c "gofmt -l . | (! grep .) && go build ./... && go vet ./... && go test -race ./internal/scheduler/... -v"
```
Expected: PASS, including every pre-existing scheduler test — the `else` arm is unchanged code and download-mode topics must behave identically.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/scheduler/scheduler.go backend/internal/scheduler/scheduler_test.go
git commit -m "feat: skip client delivery for notify-only topics"
```

---

## Task 5: Notify-only for per-episode trackers

**Why this is separable:** LostFilm is the only per-episode tracker (`SupportsEpisodeFilter` is implemented once, at `backend/internal/plugins/trackers/lostfilm/lostfilm.go:173`). Its `Check` already produces the full pending-episode list, so nothing tracker-side changes — but two things must happen that single-release topics do not need, and each can fail independently of Task 4.

1. **Mark every pending episode seen.** Without it, switching the topic back to download mode fetches everything that appeared while notify-only was on — the accumulated backlog the issue explicitly asks us to avoid.
2. **Gate the announcement on a non-empty pending list.** LostFilm's hash is derived from counts (`eps:N/done:D/pending:P`, `lostfilm.go:266`), so the tick *after* marking sees a changed hash with nothing pending. Announcing on the hash alone would send a second, empty "new release" message.

**Files:**
- Modify: `backend/internal/scheduler/scheduler.go` — `notifyOnlyRelease` (from Task 4)
- Test: `backend/internal/scheduler/scheduler_test.go`

**Interfaces:**
- Consumes: `notifyOnlyRelease` (Task 4); `isEpisodic(tr) bool`; `s.topics.MarkEpisodeDownloaded(ctx, t, packed) error`; `repo.ErrStaleCheckResult`.
- Produces: no new exported names.

- [ ] **Step 1: Write the failing tests**

```go
// A notify-only episodic topic must mark every pending episode seen, so a
// later switch back to download mode fetches only what appears afterwards
// rather than the whole backlog (#184).
func TestRunCheck_NotifyOnly_EpisodicMarksPendingSeen(t *testing.T) {
	tr := &fakeTracker{
		name:     "faketracker",
		episodic: true,
		checks: []checkResult{
			{check: &domain.Check{Hash: "new-hash", Extra: map[string]any{
				"pending_episodes": []string{"1-1", "1-2", "1-3"},
				"pending_human":    []string{"s01e01", "s01e02", "s01e03"},
			}}, err: nil},
		},
	}
	f := newFixture(t, tr)
	f.topic.NotifyOnly = true

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if f.clientPlugin.addCalls != 0 {
		t.Errorf("notify-only must not submit, got %d Add calls", f.clientPlugin.addCalls)
	}
	if tr.callsDownload != 0 {
		t.Errorf("notify-only must not call tr.Download, got %d", tr.callsDownload)
	}
	if len(f.topics.markCalls) != 3 {
		t.Fatalf("expected 3 episodes marked seen, got %d", len(f.topics.markCalls))
	}
	for i, want := range []string{"1-1", "1-2", "1-3"} {
		if f.topics.markCalls[i].packed != want {
			t.Errorf("mark %d: expected %q, got %q", i, want, f.topics.markCalls[i].packed)
		}
	}
	got := f.emitter.ofType(events.ReleaseFound)
	if len(got) != 1 {
		t.Fatalf("expected 1 release.found, got %d", len(got))
	}
	for _, label := range []string{"s01e01", "s01e02", "s01e03"} {
		if !strings.Contains(got[0].Body, label) {
			t.Errorf("body %q must name episode %s", got[0].Body, label)
		}
	}
}

// LostFilm's hash is derived from counts, so the tick AFTER marking sees a
// changed hash with nothing pending. That must not produce a second, empty
// "new release" message.
func TestRunCheck_NotifyOnly_EpisodicSilentWhenNothingPending(t *testing.T) {
	tr := &fakeTracker{
		name:     "faketracker",
		episodic: true,
		checks: []checkResult{
			{check: &domain.Check{Hash: "recounted-hash", Extra: map[string]any{
				"pending_episodes": []string{},
				"pending_human":    []string{},
			}}, err: nil},
		},
	}
	f := newFixture(t, tr)
	f.topic.NotifyOnly = true

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if got := f.emitter.ofType(events.ReleaseFound); len(got) != 0 {
		t.Errorf("no pending episodes means nothing to announce, got %d", len(got))
	}
	// The hash must still advance, or this recount repeats every tick forever.
	if rec := f.lastRecord(t); rec.hash != "recounted-hash" {
		t.Errorf("expected hash to advance to recounted-hash, got %q", rec.hash)
	}
}

// A reset landing mid-check invalidates the state token. Marking must stop
// rather than write episodes into state that no longer exists.
func TestRunCheck_NotifyOnly_EpisodicStopsOnStaleToken(t *testing.T) {
	tr := &fakeTracker{
		name:     "faketracker",
		episodic: true,
		checks: []checkResult{
			{check: &domain.Check{Hash: "new-hash", Extra: map[string]any{
				"pending_episodes": []string{"1-1", "1-2", "1-3"},
				"pending_human":    []string{"s01e01", "s01e02", "s01e03"},
			}}, err: nil},
		},
	}
	f := newFixture(t, tr)
	f.topic.NotifyOnly = true
	f.topics.markErr = repo.ErrStaleCheckResult

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if len(f.topics.markCalls) != 1 {
		t.Errorf("expected marking to stop after the first stale result, got %d calls",
			len(f.topics.markCalls))
	}
}
```

- [ ] **Step 1b: Close the three test gaps Task 4's review found**

All three are test-strength gaps in Task 4's tests, and this is the task that makes them fixable — it is the first one with episode labels.

**(a) `TestRunCheck_NotifyOnly_FirstCheckAnnouncesWhenOptedIn` cannot fail if the notify-only arm is never taken.** It only counts one `release.found`, and the download path emits one too — so it passed even before Task 4's fix. Add a client assertion so it stands on its own:

```go
	if f.clientPlugin.addCalls != 0 {
		t.Errorf("notify-only must not submit even when announcing the current release, got %d Add calls", f.clientPlugin.addCalls)
	}
```

**(b) The body assertion in `TestRunCheck_NotifyOnly_AnnouncesAndSkipsClient` pins nothing.** It only checks that `"Sent to client"` is absent — but the old download-path wording `"New release detected"` would also pass that. Replace the negative check with a positive one, keeping the negative as well:

```go
	if !strings.Contains(ev.Body, "not downloaded") {
		t.Errorf("body must say the release was not downloaded, got %q", ev.Body)
	}
	if strings.Contains(ev.Body, "Sent to client") {
		t.Errorf("body must not imply a download happened: %q", ev.Body)
	}
```

**(c) `notifyOnlyBody`'s label path has zero coverage.** Every Task 4 test passes an empty `Extra`, so only the no-labels branch ever ran — the 10-label cap, the slice, and the `(+N more)` suffix are untested. Add a table test:

```go
// notifyOnlyBody caps its label list the same way notifyUpdated does, so one
// catch-up tick cannot produce a wall of text in a Telegram message.
func TestNotifyOnlyBody(t *testing.T) {
	labels := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("s01e%02d", i+1)
		}
		return out
	}
	cases := []struct {
		name          string
		in            []string
		wantContains  []string
		wantOmits     []string
	}{
		{"none", nil, []string{"not downloaded"}, []string{"s01e", "more)"}},
		{"one", labels(1), []string{"s01e01", "not downloaded"}, []string{"more)"}},
		{"exactly ten", labels(10), []string{"s01e01", "s01e10"}, []string{"more)"}},
		{"eleven caps at ten", labels(11), []string{"s01e01", "s01e10", "(+1 more)"}, []string{"s01e11"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := notifyOnlyBody(tc.in)
			for _, want := range tc.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("body %q must contain %q", got, want)
				}
			}
			for _, omit := range tc.wantOmits {
				if strings.Contains(got, omit) {
					t.Errorf("body %q must not contain %q", got, omit)
				}
			}
		})
	}
}
```

`fmt` and `strings` are already imported by `scheduler_test.go`.

- [ ] **Step 2: Run to verify they fail**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.26 \
  sh -c "go test -race ./internal/scheduler/... -run 'TestRunCheck_NotifyOnly_Episodic|TestNotifyOnlyBody' -v"
```
Expected: FAIL — zero mark calls, and the empty-pending case emits a release.found. `TestNotifyOnlyBody` should PASS immediately (it tests Task 4 code that is already correct); it exists to lock that behaviour, not to drive new code. If it fails, the cap logic is genuinely wrong — report that.

- [ ] **Step 3: Extend `notifyOnlyRelease`**

Replace the body written in Task 4 with:

```go
func (s *Scheduler) notifyOnlyRelease(ctx context.Context, log zerolog.Logger, t *domain.Topic, tr registry.Tracker, check *domain.Check, authorComment string) {
	pendingPacked := extra.StringSlice(check.Extra, "pending_episodes")
	pendingHuman := extra.StringSlice(check.Extra, "pending_human")

	// A per-episode tracker derives its hash from counts (LostFilm:
	// "eps:N/done:D/pending:P"), so the tick AFTER we mark episodes seen
	// reports a changed hash with nothing pending. Announcing on the hash
	// alone would send a second, empty "new release" message.
	announce := !isEpisodic(tr) || len(pendingPacked) > 0

	// The first check of a topic — and the first after a reset, which also
	// clears LastHash — has no baseline to compare against, so "changed" here
	// only means "seen for the first time". Announce it only when the user
	// opted in, so adding a topic stays silent.
	if t.LastHash == "" && !t.NotifyOnlyAnnounceCurrent {
		announce = false
	}

	// Deliberately NOT gated on t.ConsecutiveErrors, unlike the download
	// path's emit. That gate is safe there because a failed download
	// re-persists the OLD hash and replays the same release next tick. This
	// branch persists the NEW hash on this very tick, so a suppressed event is
	// lost for good rather than delayed.
	if announce && s.emit != nil {
		s.emit.Emit(ctx, events.Event{
			UserID: t.UserID, TopicID: &t.ID, NotifierID: t.NotifierID,
			Type: events.ReleaseFound, Severity: "info",
			Title: t.DisplayName, Body: notifyOnlyBody(pendingHuman),
			Link: s.cfg.PublicBaseURL + "/topics", SourceURL: t.URL,
			AuthorComment: authorComment,
			Data:          map[string]any{"notify_only": true},
		})
	}

	// Mark every pending episode seen. Without this, switching the topic back
	// to download mode fetches everything that appeared while notify-only was
	// on — the accumulated backlog issue #184 explicitly asks us to avoid.
	// Fail-open: a plain DB error stops the loop and lets the tick finish, so
	// the worst case is that a later toggle-back re-downloads a few episodes,
	// never that the check fails.
	for _, packed := range pendingPacked {
		if err := s.topics.MarkEpisodeDownloaded(ctx, t, packed); err != nil {
			if errors.Is(err, repo.ErrStaleCheckResult) {
				// A reset (or a delete) landed mid-check. Stop rather than
				// write into state that no longer exists; recordResult below
				// is guarded by the same token and will be dropped too.
				log.Info().Str("packed", packed).
					Msg("notify-only episode mark discarded: another write won the state guard")
				return
			}
			log.Warn().Err(err).Str("packed", packed).Msg("notify-only episode mark failed")
			return
		}
	}
}
```

- [ ] **Step 4: Run to verify they pass**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.26 \
  sh -c "gofmt -l . | (! grep .) && go build ./... && go vet ./... && go test -race ./..."
```
Expected: PASS — the whole backend suite, not just the scheduler.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/scheduler/scheduler.go backend/internal/scheduler/scheduler_test.go
git commit -m "feat: mark episodes seen on notify-only topics"
```

- [ ] **Step 6 (addendum — added after Task 5's review): pin the two error paths apart**

Task 5's review found that `TestRunCheck_NotifyOnly_EpisodicStopsOnStaleToken` proves "stop on the first error", not "stop on *that* error": the fake returns the sentinel unwrapped, so a `==` comparison would pass it too, and a plain DB error stops the loop identically. Separately, the plain-DB-error path is fail-open — it lets the tick finish — which is the **opposite** of the download path, where the same class of failure fails the check with `errCodeInternal`. That divergence is deliberate and nothing pinned it. Two changes close both.

**(a) Make the stale test prove `errors.Is`, not `==`.** Wrap the sentinel the way the repo layer does:

```go
	f.topics.markErr = fmt.Errorf("topics: mark episode: %w", repo.ErrStaleCheckResult)
```

A `==` comparison fails against a wrapped error, so this is what makes the test name true.

**(b) Pin the fail-open divergence.** Add:

```go
// A plain DB error while marking must NOT fail the check. This is deliberately
// the opposite of the download path, where the same class of failure fails the
// tick with errCodeInternal: there, nothing was delivered and a retry is the
// point; here the user has already been told about the release, and refusing
// the whole tick over a bookkeeping write would strand the topic. The cost is
// bounded — an unmarked episode is re-downloaded once on a later toggle-back.
func TestRunCheck_NotifyOnly_EpisodicPlainDBErrorDoesNotFailCheck(t *testing.T) {
	tr := &fakeTracker{
		name:     "faketracker",
		episodic: true,
		checks: []checkResult{
			{check: &domain.Check{Hash: "new-hash", Extra: map[string]any{
				"pending_episodes": []string{"1-1", "1-2", "1-3"},
				"pending_human":    []string{"s01e01", "s01e02", "s01e03"},
			}}, err: nil},
		},
	}
	f := newFixture(t, tr)
	f.topic.NotifyOnly = true
	f.topics.markErr = errors.New("connection reset by peer")

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if len(f.topics.markCalls) != 1 {
		t.Errorf("expected marking to stop after the first error, got %d calls", len(f.topics.markCalls))
	}
	rec := f.lastRecord(t)
	if rec.errMsg != "" {
		t.Errorf("a failed mark must not fail the check, got errMsg %q", rec.errMsg)
	}
	if rec.errCode != "" {
		t.Errorf("a failed mark must not set an error code, got %q", rec.errCode)
	}
	if rec.hash != "new-hash" {
		t.Errorf("the hash must still advance, got %q", rec.hash)
	}
	// The release was still announced — the user was told, which is why
	// refusing the tick over the mark would be the wrong trade.
	if got := f.emitter.ofType(events.ReleaseFound); len(got) != 1 {
		t.Errorf("expected the release to still be announced, got %d", len(got))
	}
}
```

`errors` and `fmt` are already imported by `scheduler_test.go`. No production code changes — if either test fails, the production behaviour is genuinely wrong and must be reported, not adjusted away.

---

## Task 6: Frontend — type, form toggle, conditional fields

**Files:**
- Modify: `frontend/src/lib/api.ts:458-480` (`Topic`), `:350-363` (`UpdateTopicBody`)
- Modify: `frontend/src/components/topics/TopicForm.tsx:42-56` (`TopicFormValues`), `:213-220` (state), `:243-252` (submit), `:370-441` (the delivery fields)
- Modify: `frontend/src/components/topics/AddTopicCard.tsx:20-32` (defaults), `:68-83` (POST body)
- Modify: `frontend/src/components/topics/EditTopicCard.tsx:23-34` (initial), `:44-58` (PUT body)
- Test: `frontend/src/components/topics/AddTopicCard.test.tsx`

**Interfaces:**
- Consumes: the backend JSON fields from Task 3. Note `domain.Topic` has **no** json tags, so the GET response is PascalCase: `NotifyOnly`, `NotifyOnlyAnnounceCurrent`. Request bodies are snake_case: `notify_only`, `notify_only_announce_current`.
- Produces: `TopicFormValues.notifyOnly: boolean` and `.notifyOnlyAnnounceCurrent: boolean`; `Topic.NotifyOnly` / `.NotifyOnlyAnnounceCurrent`. Task 7 reads `Topic.NotifyOnly`.

- [ ] **Step 1: Write the failing test**

Add to `frontend/src/components/topics/AddTopicCard.test.tsx` (read the file first and reuse its render helper and API mock; the snippet assumes `renderAddTopicCard()` and a mocked `api.post`):

```tsx
it("sends notify_only and hides the client selector when notify-only is on", async () => {
  const user = userEvent.setup();
  renderAddTopicCard();

  await user.type(screen.getByLabelText(/URL or magnet link/i), "https://example.com/t/1");
  await user.click(screen.getByLabelText(/Notify only/i));

  // Download settings are irrelevant in this mode and must be out of the way.
  expect(screen.queryByLabelText(/Client \(optional\)/i)).not.toBeInTheDocument();
  // The notifier selector must stay — it is the whole point of the mode.
  expect(screen.getByLabelText(/Notifier/i)).toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: /add topic/i }));

  expect(apiPost).toHaveBeenCalledWith(
    "/topics",
    expect.objectContaining({ notify_only: true, notify_only_announce_current: false }),
  );
});
```

- [ ] **Step 2: Run to verify it fails**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/host:ro" -v marauder-fe-nm:/app -w //app node:22-alpine \
  sh -c "cp -r /host/src /host/vitest.config.ts /host/vite.config.ts /host/index.html /host/tsconfig*.json /app/ 2>/dev/null; npx vitest run src/components/topics/AddTopicCard.test.tsx"
```
Expected: FAIL — no element matches `/Notify only/i`.

- [ ] **Step 3: Extend the API types**

`frontend/src/lib/api.ts` — in the `Topic` type, after `ReplaceDeleteData: boolean;`:

```ts
  NotifyOnly: boolean;
  NotifyOnlyAnnounceCurrent: boolean;
```

In `UpdateTopicBody`, after `replace_delete_data?: boolean;`:

```ts
  // Notify-only watch mode (issue #184). notify_only_announce_current only
  // matters when notify_only is true.
  notify_only?: boolean;
  notify_only_announce_current?: boolean;
```

- [ ] **Step 4: Extend the form values, state and submit**

`frontend/src/components/topics/TopicForm.tsx` — in `TopicFormValues`, after `replaceDeleteData: boolean;`:

```ts
  // Notify-only watch mode (issue #184). notifyOnlyAnnounceCurrent only
  // applies when notifyOnly is on.
  notifyOnly: boolean;
  notifyOnlyAnnounceCurrent: boolean;
```

In the `delivery` state object (~:213), after `replaceDeleteData: initial.replaceDeleteData,`:

```ts
    notifyOnly: initial.notifyOnly,
    notifyOnlyAnnounceCurrent: initial.notifyOnlyAnnounceCurrent,
```

In `handleSubmit`'s `onSubmit({...})` (~:243), after `replaceDeleteData: delivery.replaceDeleteData,`:

```ts
      notifyOnly: delivery.notifyOnly,
      notifyOnlyAnnounceCurrent: delivery.notifyOnlyAnnounceCurrent,
```

- [ ] **Step 5: Add the toggle and hide the download fields**

In `TopicForm.tsx`, insert this block **immediately before** the client `<div>` (~:370, the one whose label reads "Client (optional)"). It copies the visual shape of the replace-on-update block at `:414-441` — bordered container, main checkbox, helper text, nested sub-checkbox:

```tsx
      <div className="space-y-2 rounded-md border border-border/60 bg-muted/20 p-3">
        <label className="flex items-center gap-2 text-sm font-medium text-foreground">
          <input
            type="checkbox"
            checked={delivery.notifyOnly}
            onChange={(e) =>
              setDelivery((d) => ({ ...d, notifyOnly: e.target.checked }))
            }
          />
          <span>Notify only — do not download</span>
        </label>
        <p className="text-xs text-muted-foreground">
          Keep checking this topic and send a notification when a new release
          appears, without sending anything to a torrent client. No download
          client is required.
        </p>
        {delivery.notifyOnly && (
          <label className="flex items-center gap-2 pt-1 text-sm">
            <input
              type="checkbox"
              checked={delivery.notifyOnlyAnnounceCurrent}
              onChange={(e) =>
                setDelivery((d) => ({
                  ...d,
                  notifyOnlyAnnounceCurrent: e.target.checked,
                }))
              }
            />
            <span>Also tell me about the release that is there now</span>
          </label>
        )}
      </div>
```

Then wrap the download-only fields so they disappear while notify-only is on. The client `<div>`, the download-dir/category `<div>` and the whole replace-on-update block become:

```tsx
      {!delivery.notifyOnly && (
        <>
          {/* the existing client select div, unchanged */}
          {/* the existing download dir + CategoryField div, unchanged */}
          {/* the existing replace-on-update bordered block, unchanged */}
        </>
      )}
```

**The `NotifierSelect` (~:386) must stay outside this wrapper** — it is the only way a notify-only topic reaches the user.

The stored `clientId`, `downloadDir`, `category` and `replaceOnUpdate` values are deliberately left untouched in state, so toggling back restores them without re-entry.

- [ ] **Step 6: Wire the two cards**

`AddTopicCard.tsx` — in the defaults object, after `replaceDeleteData: true,`:
```ts
  // Default to today's behaviour: download automatically. The announce-current
  // sub-option stays off so adding a topic is silent.
  notifyOnly: false,
  notifyOnlyAnnounceCurrent: false,
```
and in the POST body, after `replace_delete_data: v.replaceDeleteData,`:
```ts
        notify_only: v.notifyOnly,
        notify_only_announce_current: v.notifyOnlyAnnounceCurrent,
```

`EditTopicCard.tsx` — in the initial-values mapper, after `replaceDeleteData: topic.ReplaceDeleteData ?? true,`:
```ts
    notifyOnly: topic.NotifyOnly ?? false,
    notifyOnlyAnnounceCurrent: topic.NotifyOnlyAnnounceCurrent ?? false,
```
and in the PUT body, after `replace_delete_data: v.replaceDeleteData,`:
```ts
        notify_only: v.notifyOnly,
        notify_only_announce_current: v.notifyOnlyAnnounceCurrent,
```

- [ ] **Step 7: Run to verify it passes**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/host:ro" -v marauder-fe-nm:/app -w //app node:22-alpine \
  sh -c "cp -r /host/src /host/vitest.config.ts /host/vite.config.ts /host/index.html /host/tsconfig*.json /app/ 2>/dev/null; npx tsc --noEmit && npx vitest run"
```
Expected: PASS, including every existing topic test. Other tests construct `TopicFormValues`; TypeScript will flag each one missing the two new required fields — add `notifyOnly: false, notifyOnlyAnnounceCurrent: false` to those fixtures.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/lib/api.ts frontend/src/components/topics/TopicForm.tsx \
  frontend/src/components/topics/AddTopicCard.tsx frontend/src/components/topics/EditTopicCard.tsx \
  frontend/src/components/topics/AddTopicCard.test.tsx
git commit -m "feat: add notify-only toggle to the topic form"
```

---

## Task 7: Frontend — badge, client-badge suppression, warnings

**Files:**
- Create: `frontend/src/components/topics/NotifyOnlyBadge.tsx`, `NotifyOnlyBadge.test.tsx`
- Modify: `frontend/src/components/topics/TopicRow.tsx:99-110` (badge row)
- Modify: `frontend/src/components/topics/ClientBadge.tsx:25` (early return)
- Modify: `frontend/src/components/topics/TopicForm.tsx` (two inline notices)

**Interfaces:**
- Consumes: `Topic.NotifyOnly` (Task 6); `TopicFormValues.notifyOnly`; the `notifiers` list the form already receives for `NotifierSelect` — read its prop name from the component's props type before using it.
- Produces: `export function NotifyOnlyBadge({ topic }: { topic: Topic })`.

- [ ] **Step 1: Write the failing badge test**

Create `frontend/src/components/topics/NotifyOnlyBadge.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { NotifyOnlyBadge } from "./NotifyOnlyBadge";
import type { Topic } from "@/lib/api";

function topicWith(notifyOnly: boolean): Topic {
  return { NotifyOnly: notifyOnly } as unknown as Topic;
}

describe("NotifyOnlyBadge", () => {
  it("renders nothing for a normal topic", () => {
    const { container } = render(<NotifyOnlyBadge topic={topicWith(false)} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders the badge for a notify-only topic", () => {
    render(<NotifyOnlyBadge topic={topicWith(true)} />);
    expect(screen.getByText("Notify only")).toBeInTheDocument();
  });
});
```

- [ ] **Step 1b: Close the sub-checkbox gap Task 6's review found**

Task 6's test only ever exercised `notify_only_announce_current: false` — nothing clicks the sub-checkbox, so the `true` case is never proven to reach the request body. A sub-option that silently never sends is exactly the kind of bug that survives to production. Add to `frontend/src/components/topics/AddTopicCard.test.tsx`, alongside the notify-only test Task 6 added, reusing that file's existing render helper and API mock:

```tsx
it("forwards notify_only_announce_current when the sub-option is ticked", async () => {
  const user = userEvent.setup();
  renderAddTopicCard();

  await user.type(screen.getByLabelText(/URL or magnet link/i), "https://example.com/t/1");
  await user.click(screen.getByLabelText(/Notify only/i));
  // The sub-option only exists once notify-only is on.
  await user.click(screen.getByLabelText(/release that is there now/i));
  await user.click(screen.getByRole("button", { name: /add topic/i }));

  expect(apiPost).toHaveBeenCalledWith(
    "/topics",
    expect.objectContaining({ notify_only: true, notify_only_announce_current: true }),
  );
});
```

Adapt only the helper/mock names to what the file actually provides; keep both assertions.

- [ ] **Step 2: Run to verify it fails**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/host:ro" -v marauder-fe-nm:/app -w //app node:22-alpine \
  sh -c "cp -r /host/src /host/vitest.config.ts /host/vite.config.ts /host/index.html /host/tsconfig*.json /app/ 2>/dev/null; npx vitest run --maxWorkers=2 src/components/topics/NotifyOnlyBadge.test.tsx src/components/topics/AddTopicCard.test.tsx"
```
Expected: the badge test FAILS (cannot resolve `./NotifyOnlyBadge`). The Step 1b sub-option test should PASS immediately — it locks Task 6 behaviour that is already wired. If it fails, the sub-option genuinely does not reach the request body; report that rather than adjusting the test.

- [ ] **Step 3: Write the badge**

Create `frontend/src/components/topics/NotifyOnlyBadge.tsx`:

```tsx
import { BellRing } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import type { Topic } from "@/lib/api";

/**
 * Shows a "Notify only" badge for watch-only topics (issue #184) — monitored
 * and announced, never delivered to a torrent client. Renders nothing for
 * ordinary topics. Mirrors SonarrBadge / ClientBadge.
 */
export function NotifyOnlyBadge({ topic }: { topic: Topic }) {
  if (!topic.NotifyOnly) return null;
  return (
    <Badge variant="secondary" className="shrink-0 gap-1 font-normal">
      <BellRing className="size-3" />
      Notify only
    </Badge>
  );
}
```

- [ ] **Step 4: Mount it and suppress the client badge**

`TopicRow.tsx` — in the badge row (~:103), add it directly after `<SonarrBadge topic={topic} />`:

```tsx
          <NotifyOnlyBadge topic={topic} />
```
with the matching import.

`ClientBadge.tsx` — add as the **first** statement of the component body, before the `explicit` lookup:

```tsx
  // A notify-only topic never delivers, so naming a client — and especially
  // rendering the red "no default client" error state — would be a lie.
  if (topic.NotifyOnly) return null;
```

- [ ] **Step 5: Add the two form notices**

Both go inside `TopicForm.tsx`, directly under the notify-only bordered block from Task 6.

The silent-mode warning — a notify-only topic with no notifier at all sends nothing, and unlike download mode nothing else happens either, so there is no visible symptom:

```tsx
        {delivery.notifyOnly &&
          notifiersLoaded &&
          !delivery.notifierId &&
          !notifiers.some((n) => n.is_default) && (
            <p className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-400">
              This topic has no notifier and there is no default notifier, so it
              will be checked silently and you will never hear about it. Pick a
              notifier below, or mark one as default on the Notifiers page.
            </p>
          )}
```

The toggle-back notice — only in edit mode, only when turning notify-only **off** on a topic that had it on:

```tsx
        {isEdit && initial.notifyOnly && !delivery.notifyOnly && (
          <p className="rounded-md border border-border/60 bg-muted/20 px-3 py-2 text-xs text-muted-foreground">
            Releases seen while this topic was notify-only will not be
            downloaded — only the next change will. Use Reset on the topic if
            you want the current release fetched now.
          </p>
        )}
```

`isEdit` is an existing name in this component. `notifiers` is NOT a prop — the list lives inside `NotifierSelect` as a local `QK.notifiers` query, so the form observes that same cached query itself (React Query dedupes on the shared key, so it costs no extra request).

**`notifiersLoaded` is load-state gating, and it is not optional.** Derive it from the query — `const notifiersLoaded = notifiersQuery.isSuccess;` — and keep it in the condition. Without it, `notifiersQuery.data?.notifiers ?? []` yields `[]` while the request is in flight, so `!notifiers.some((n) => n.is_default)` is `true` and **loading is indistinguishable from "no default notifier exists"**. In add mode that is harmless, because `delivery.notifyOnly` starts false and the user cannot tick it before the query resolves. In **edit mode it is a visible false alarm**: a topic that is already notify-only with no per-topic notifier seeds `delivery.notifyOnly = true` on the very first render, so the warning flashes on every page load even when a default notifier exists. Gating on `isSuccess` also means a *failed* notifier fetch shows nothing rather than a scary warning derived from an empty list — correct, since a fetch failure is not evidence that no default exists.

- [ ] **Step 6: Run to verify it passes**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/host:ro" -v marauder-fe-nm:/app -w //app node:22-alpine \
  sh -c "cp -r /host/src /host/vitest.config.ts /host/vite.config.ts /host/index.html /host/tsconfig*.json /app/ 2>/dev/null; npx tsc --noEmit && npx vitest run"
```
Expected: PASS. Existing `ClientBadge.test.tsx` fixtures may now need `NotifyOnly: false`; add it where TypeScript asks.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/components/topics/NotifyOnlyBadge.tsx \
  frontend/src/components/topics/NotifyOnlyBadge.test.tsx \
  frontend/src/components/topics/TopicRow.tsx frontend/src/components/topics/ClientBadge.tsx \
  frontend/src/components/topics/TopicForm.tsx
git commit -m "feat: badge and warn notify-only topics in the UI"
```

---

## Task 8: Documentation

**Files:**
- Create: `docs/notify-only.md`
- Modify: `CLAUDE.md`, `CHANGELOG.md`

**Interfaces:**
- Consumes: everything above.
- Produces: nothing code-facing.

- [ ] **Step 1: Write the user guide**

Create `docs/notify-only.md`, mirroring the structure of `docs/replace-on-update.md` (read it first and follow its heading style). It must cover:

- What the mode does: the topic is checked on its normal schedule and announces new releases; nothing is ever sent to a torrent client, and no download client is required.
- How to turn it on: the "Notify only — do not download" checkbox in Add topic and Edit topic.
- The sub-option: "Also tell me about the release that is there now" announces the release already on the page at the first check. Off by default, so adding a topic is silent. It applies again after a Reset, because Reset clears the topic's recorded state.
- **You need a notifier.** A notify-only topic with no per-topic notifier and no default notifier is completely silent. The form warns about this.
- What the notification says: the topic name, the episode labels for per-episode trackers, the release author's latest comment when the tracker supplies one, and a link to the tracker page so you can download it yourself.
- Which event it is: `release.found`, not `download.submitted`. A notifier subscribed the legacy way (`updated`) still receives it.
- Switching back to downloading: only the **next** change is downloaded. Anything seen while notify-only was on is not fetched. Use Reset if you want the current release now.
- What is hidden while the mode is on: client, download directory, category, and the replace-on-update policy. Their values are kept, so turning the mode off restores them.

- [ ] **Step 2: Update CLAUDE.md**

Three edits, all required by the repo's documentation-maintenance rule:

1. In the `domain` table row, add `NotifyOnly`/`NotifyOnlyAnnounceCurrent` to the `Topic` field list alongside `ReplaceOnUpdate`/`ReplaceDeleteData`, noting migration `0016`.
2. In the `db` / `db/repo` row, add a sentence: `Topics` also carries `notify_only`/`notify_only_announce_current` (migration `0016`): the per-topic watch-only mode — the scheduler checks and announces but never resolves a client, so no download client is needed; the second flag announces the release already present at the first check (and the first after a reset, which clears `last_hash`). Mention that the boolean policies now travel as `repo.TopicFlags` rather than positional parameters.
3. In the **Scheduler design** section, add a `**Notify-only topics (issue #184):**` paragraph after the replace-on-update one, stating: the `updated` branch splits, notify-only takes `notifyOnlyRelease` and falls through to the shared tail so the **new** hash is persisted (there is no download to retry); the `release.found` emit is deliberately **not** gated on `ConsecutiveErrors`, unlike the download path's, because a suppressed event here is lost rather than replayed; per-episode trackers have their pending episodes marked seen so a toggle back to download mode does not fetch the backlog; and the announcement is gated on a non-empty pending list because LostFilm's count-derived hash changes again on the tick after marking.

- [ ] **Step 3: Update the changelog**

`## [Unreleased]` in `CHANGELOG.md` is currently empty (line 8), so create the `### Added` subsection under it:

```markdown
### Added


- Per-topic **notify-only** mode: watch a tracker page and get a notification
  when a new release appears, without sending anything to a download client.
  No download client is required. An optional sub-setting also announces the
  release already present when the topic is first checked. (#184)
```

- [ ] **Step 4: Verify nothing is stale**

Re-read the three CLAUDE.md edits against the code as merged. Every file path, function name and migration number in them must exist. Then run both full verify commands one last time:

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.26 \
  sh -c "gofmt -l . | (! grep .) && go build ./... && go vet ./... && go test -race ./..."
```
```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/host:ro" -v marauder-fe-nm:/app -w //app node:22-alpine \
  sh -c "cp -r /host/src /host/vitest.config.ts /host/vite.config.ts /host/index.html /host/tsconfig*.json /app/ 2>/dev/null; npx tsc --noEmit && npx vitest run"
```
Expected: both PASS.

- [ ] **Step 5: Commit**

```bash
git add docs/notify-only.md docs/superpowers/plans/2026-09-19-notify-only-topics.md \
  docs/superpowers/specs/2026-09-19-notify-only-topics-design.md CLAUDE.md CHANGELOG.md
git commit -m "docs: document notify-only topic mode"
```

---

## Manual verification (before opening the PR)

Automated tests do not exercise a real tracker, a real Telegram bot, or the migration against a populated database. Run this once on the dev stack:

```bash
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml up -d --build
```
Remember `--no-deps` when rebuilding only the backend, and restart the gateway after any container is recreated:
```bash
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml up -d --no-deps --build backend
docker restart deploy-gateway-1
```

Then, at http://localhost:34080 (`admin` / `pleasechangeme`):

1. Existing topics still show their client badge and still download. **This is the regression that matters most.**
2. Add a topic with "Notify only" ticked and **no** client selected. It saves without complaint and shows the "Notify only" badge and no client badge.
3. With no default notifier set and none picked, the form shows the amber silent-topic warning.
4. Edit that topic, untick "Notify only": the download fields reappear with their previous values, and the toggle-back notice is shown.
5. Check the topic's history timeline after a check — a notify-only topic that detects a change records `release.found` and **no** `download.submitted`.
6. The notify-only topic's row shows no delivery-status section at all. It has no `topic_deliveries` rows, so `GET /topics/{id}/status` returns an empty list and `DeliveryStatus` renders nothing (`DeliveryStatus.tsx:113-114`) — confirm it is absent, not a broken or spinning widget.

---

## Deferred (do not build in this PR)

- A per-release "Download now" action for a release seen in notify-only mode. The issue's author explicitly plans to download by hand; this needs a new endpoint and a new UI surface.
- Reset's event body text (`backend/internal/api/handlers/topics.go:733`, "Topic reset — will re-download from scratch") reads oddly for a notify-only topic. Cosmetic.
- The pre-existing spurious second `release.found` on a LostFilm catch-up tick in **download** mode (spec section 3). It changes notification behaviour for existing users and belongs in its own issue.
- Localising the new badge and notice strings. The sibling replace-on-update block is hardcoded English today (`TopicForm.tsx:422-429`); matching it keeps the form consistent. Localise both together later.
