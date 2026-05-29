# Topic Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Per-topic client + download folder + category, topic editing, and auto-retry of errored topics.

**Spec:** `docs/superpowers/specs/2026-05-29-topic-management-design.md`. Branch `feature/topic-management`.

**Conventions:** consumer-side interfaces; gofmt-clean changed files (pre-existing `lostfilm_parse.go`/`credentials_test.go` violations are out of scope); backend gate `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.23 sh -c "go build ./... && go vet ./... && go test -race ./..."`; frontend gate `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test && npm run build"`.

---

## Task T1: Category column + scheduler wiring + error-retry (D)

**Files:** migration `0004_add_topic_category.sql`; `domain/domain.go`; `db/repo/topics.go`; `db/repo/topics_test.go`; `scheduler/scheduler.go`; `api/handlers/topics.go`.

- [ ] **Step 1: migration** — `backend/internal/db/migrations/0004_add_topic_category.sql`:
```sql
-- +goose Up
-- +goose StatementBegin
ALTER TABLE topics ADD COLUMN category TEXT;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
ALTER TABLE topics DROP COLUMN category;
-- +goose StatementEnd
```
- [ ] **Step 2: domain** — add `Category string` to `domain.Topic` (after `DownloadDir`).
- [ ] **Step 3: repo columns** — in `topics.go`, add `COALESCE(category,'')` to `topicColumns` immediately after `COALESCE(download_dir,'')`, and add `&t.Category` to `scanTopic` immediately after `&t.DownloadDir`. Add `category` to the `Create` INSERT column list + values + args. (Keep SELECT/scan order identical.)
- [ ] **Step 4: D — DueForCheck** — change `WHERE status = 'active'` to `WHERE status IN ('active','error')` in `DueForCheck`.
- [ ] **Step 5: createTopicReq + Create** — add `Category string \`json:"category"\`` to `createTopicReq` (topics.go) and set `Category: req.Category` on the `domain.Topic` built in `Create`.
- [ ] **Step 6: scheduler** — in `sendViaClient`, add `Category: t.Category,` to the `domain.AddOptions{...}` (next to `DownloadDir: t.DownloadDir`).
- [ ] **Step 7: tests** — in `topics_test.go` (pgxmock): a `DueForCheck` test asserting the SQL now matches `status IN ('active','error')` (or that an error-status row is returned); a `Create`/round-trip test that `category` is persisted + scanned. Mirror existing topic repo tests.
- [ ] **Step 8: gate** (full backend) — PASS. Apply migration: `docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml up -d backend` → confirm goose version 4 in logs.
- [ ] **Step 9: commit** — `git add backend/internal/{domain,db,scheduler,api} && git commit -m "feat(topics): category column + per-topic category submit + retry errored topics"`

---

## Task T2: PUT /topics/{id} edit endpoint

**Files:** `db/repo/topics.go` (+ `Update`); `api/handlers/topics.go` (+ `Update` handler); `api/router.go`; `topics_test.go`; handler test.

- [ ] **Step 1: repo Update** — add:
```go
// Update edits the user-editable fields of a topic (not url/tracker/
// scheduling/hash). Returns ErrNotFound when the row doesn't belong to
// the user.
func (r *Topics) Update(ctx context.Context, id, userID uuid.UUID, displayName string, clientID *uuid.UUID, downloadDir, category string, extra map[string]any) (*domain.Topic, error) {
	raw, err := json.Marshal(extra)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	row := r.pool.QueryRow(ctx, `UPDATE topics SET
		display_name = $3, client_id = $4, download_dir = $5, category = $6,
		extra = $7, updated_at = now()
	WHERE id = $1 AND user_id = $2
	RETURNING `+topicColumns, id, userID, displayName, clientID, downloadDir, category, raw)
	t, err := scanTopic(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}
```
(Confirm `errors`/`pgx`/`json` imports exist in topics.go.)

- [ ] **Step 2: handler** — add `updateTopicReq` (display_name, client_id, download_dir, category, quality, start_season, start_episode) and `func (h *Topics) Update(w, r)`:
  - `currentUserID`; parse `{id}` via `chi.URLParam`; decode body.
  - Load the existing topic (`GetByID(id, &uid)`); 404 if missing.
  - Validate `quality` against the plugin's quality list if non-empty (mirror Create's validation; resolve the plugin via `registry.GetTracker(existing.TrackerName)`).
  - Merge capability fields into a copy of `existing.Extra`: set/replace `quality`, `start_season`, `start_episode` (omit/clear when not provided per the same rules Create uses).
  - Call `h.Topics.Update(...)`; on `repo.ErrNotFound` → 404; success → `writeJSON(200, toTopicView(updated))` (reuse the existing topic view used by List/Get).
- [ ] **Step 3: route** — `r.Put("/topics/{id}", topicsH.Update)` next to the other topic routes.
- [ ] **Step 4: tests** — repo pgxmock test for `Update` (SQL columns + ErrNotFound on 0 rows); handler test (success 200 + Extra merge; 404 foreign/unknown; 422 bad quality). Mirror existing patterns.
- [ ] **Step 5: gate + commit** — `git add backend/internal/{db,api} && git commit -m "feat(api): PUT /topics/{id} to edit a topic"`

---

## Task T3: AddTopic form — client + folder + category

**Files:** `frontend/src/lib/api.ts`; `frontend/src/pages/Topics.tsx`; `Topics.test.tsx`.

- [ ] **Step 1: api/types** — add `client_id?: string | null`, `download_dir?: string`, `category?: string` to the topic view type; ensure a clients list fetch is available (reuse `api.get<{clients: ClientView[]}>("/clients")` — see Credentials/Clients pages for the shape).
- [ ] **Step 2: form fields** — in `AddTopicCard`, add state + inputs:
  - **Client** native `<select>` (styling like the existing Quality select / `SELECT_CLASS`): option `""` → "Use default client", then one option per client (`value=client.id`, label `display_name`). Fetch clients via React Query (`QK.clients` if it exists, else a local key).
  - **Download folder (optional)** text `<Input>`.
  - **Category (optional)** text `<Input>`.
  - Include `client_id` (or omit when ""), `download_dir`, `category` in the create payload.
- [ ] **Step 3: test** — extend `Topics.test.tsx`: mock `/clients`; assert the client select lists the clients and the create payload includes the chosen `client_id` + `download_dir` + `category`.
- [ ] **Step 4: frontend gate + commit** — `git add frontend/src && git commit -m "feat(frontend): client picker + download folder/category in AddTopic"`

---

## Task T4: Topic edit form

**Files:** `frontend/src/lib/api.ts` (+ `updateTopic`); `frontend/src/pages/Topics.tsx`; `Topics.test.tsx`.

- [ ] **Step 1: api** — `updateTopic: (id, body) => request("PUT", \`/topics/${id}\`, body)`.
- [ ] **Step 2: edit UI** — add an **Edit** button on each topic row that opens a form prefilled from the topic (display name, client_id, quality, start season/episode via the catalog dropdowns, download_dir, category). URL shown read-only. Submit calls `updateTopic` then invalidates `QK.topics`. Reuse the AddTopic field rendering — extract a shared `TopicFields` component or an `EditTopicCard` mirroring `AddTopicCard` (prefer extraction to avoid duplicating the season-catalog dropdown logic; keep within the React rules / file-size guidance — if Topics.tsx grows too large, split the edit card into its own file).
- [ ] **Step 3: test** — `Topics.test.tsx`: clicking Edit on a topic shows the prefilled form; submitting calls `updateTopic` with the changed fields; the season/episode dropdowns work in edit too.
- [ ] **Step 4: frontend gate + commit** — `git add frontend/src && git commit -m "feat(frontend): edit topics (client, quality, season/episode, folder, category)"`

---

## Task T5: Docs

**Files:** `CLAUDE.md`; `docs/plugin-development.md` (if relevant).

- [ ] **Step 1:** CLAUDE.md — note `PUT /topics/{id}`, the `category` column, per-topic `download_dir`/`category` → client `AddOptions`, and that errored topics now auto-retry on backoff (DueForCheck includes `error`).
- [ ] **Step 2: commit** — `git add CLAUDE.md docs && git commit -m "docs: topic editing, category, errored-topic retry"`

---

## Self-Review notes
- Coverage: A (T3 client picker), B (T2 endpoint + T4 form), C (T1 category backend + T3/T4 fields; download_dir FE-only since column+submit already exist), D (T1 DueForCheck one-liner).
- No regression: Create unchanged except +category; Update is additive; DueForCheck change only widens to include `error` (paused still excluded); sendViaClient adds Category alongside existing DownloadDir.
- Type consistency: `Topic.Category`/`category`, `updateTopicReq`, `Topics.Update`, `AddOptions.Category`, `status IN ('active','error')`.
