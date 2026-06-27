# Multiple Sonarr Instances Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the single-instance Sonarr integration (singleton `settings`
columns) with N independently-configured, enable/disable-able Sonarr instances,
each with its own history cursor, plus a modern Integrations UI (catalog +
instance cards).

**Architecture:** New `sonarr_instances` table (migration `0013`, data-migrates
the configured singleton, then drops `settings.sonarr_*`). New
`repo.SonarrInstances`. Poller reworked to a single manager loop that polls every
*enabled* due instance each 60s tick. Singleton REST routes become a collection.
Frontend Integrations page becomes a catalog hosting a Sonarr instance list with
add/edit/delete/enable/test cards.

**Tech Stack:** Go 1.25 (pgx, chi, zerolog, goose migrations, AES-256-GCM),
React 19 + Vite + React Query + zustand + react-hook-form + zod + shadcn,
Vitest. Build/test via Docker (`golang:1.25`, `node:20-alpine`).

## Global Constraints

- API key **never** returned by any endpoint; responses expose `api_key_set`
  bool only. Empty `api_key` on write ⇒ keep stored key (rotation guard).
- All Sonarr endpoints stay **admin-only** (existing admin route group).
- Migration numbering: next is `0013`. goose Up/Down with StatementBegin/End.
- Go: 4-space? No — Go uses gofmt (tabs). TS/TSX: 2-space. Prefer explicit types.
- Commit messages: imperative, ≤72 char subject, no AI attribution.
- DTO names: `*View` for read responses (read-only projection), `*Req` for bodies.
- i18n: every user-facing string in both `en.ts` and `ru.ts`.
- Poll interval floor 60s; first-run (nil cursor) is go-forward (stamp to now).

---

### Task 1: domain.SonarrInstance type

**Files:**
- Modify: `backend/internal/domain/domain.go` (replace `SonarrConfig`, ~227-243)

**Interfaces:**
- Produces: `domain.SonarrInstance{ ID uuid.UUID; Name string; Enabled bool;
  URL string; APIKey string; PollIntervalSec int; AllowedTrackers []string;
  DefaultClientID *uuid.UUID; DefaultCategory string; DefaultDownloadDir string;
  UpdateExisting bool; OwnerUserID *uuid.UUID; LastSeenAt *time.Time;
  CreatedAt time.Time; UpdatedAt time.Time }`

- [ ] Replace `SonarrConfig` struct with `SonarrInstance` (add ID, Name,
  CreatedAt, UpdatedAt; keep the rest). Keep the `APIKey` doc comment
  ("decrypted; empty on read means no key stored").
- [ ] Commit: `refactor(sonarr): model Sonarr config as a multi-instance type`

---

### Task 2: Migration 0013 — sonarr_instances table

**Files:**
- Create: `backend/internal/db/migrations/0013_add_sonarr_instances.sql`

- [ ] Write migration per spec (CREATE TABLE; INSERT-SELECT carry-over of a
  configured singleton preserving cursor; DROP the 12 `settings.sonarr_*`
  columns). Down: re-add the 12 columns (0009 shape with defaults), copy back
  the most-recently-updated instance into the singleton (best-effort), DROP TABLE.
- [ ] Commit: `feat(db): add sonarr_instances table (migration 0013)`

Down body:
```sql
-- +goose Down
-- +goose StatementBegin
ALTER TABLE settings
    ADD COLUMN sonarr_enabled              BOOLEAN     NOT NULL DEFAULT false,
    ADD COLUMN sonarr_url                  TEXT,
    ADD COLUMN sonarr_api_key_enc          BYTEA,
    ADD COLUMN sonarr_api_key_nonce        BYTEA,
    ADD COLUMN sonarr_poll_interval_sec    INTEGER     NOT NULL DEFAULT 900,
    ADD COLUMN sonarr_allowed_trackers     TEXT[]      NOT NULL DEFAULT '{}',
    ADD COLUMN sonarr_default_client_id    UUID        REFERENCES clients(id) ON DELETE SET NULL,
    ADD COLUMN sonarr_default_category     TEXT        NOT NULL DEFAULT '',
    ADD COLUMN sonarr_default_download_dir TEXT        NOT NULL DEFAULT '',
    ADD COLUMN sonarr_update_existing      BOOLEAN     NOT NULL DEFAULT false,
    ADD COLUMN sonarr_owner_user_id        UUID        REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN sonarr_last_seen_at         TIMESTAMPTZ;
UPDATE settings s SET
    sonarr_enabled = i.enabled, sonarr_url = i.url,
    sonarr_api_key_enc = i.api_key_enc, sonarr_api_key_nonce = i.api_key_nonce,
    sonarr_poll_interval_sec = i.poll_interval_sec,
    sonarr_allowed_trackers = i.allowed_trackers,
    sonarr_default_client_id = i.default_client_id,
    sonarr_default_category = i.default_category,
    sonarr_default_download_dir = i.default_download_dir,
    sonarr_update_existing = i.update_existing,
    sonarr_owner_user_id = i.owner_user_id,
    sonarr_last_seen_at = i.last_seen_at
FROM (SELECT * FROM sonarr_instances ORDER BY updated_at DESC LIMIT 1) i
WHERE s.id = 1;
DROP TABLE sonarr_instances;
-- +goose StatementEnd
```

---

### Task 3: repo.SonarrInstances + drop settings sonarr methods

**Files:**
- Create: `backend/internal/db/repo/sonarr_instances.go`
- Create: `backend/internal/db/repo/sonarr_instances_test.go`
- Modify: `backend/internal/db/repo/settings.go` (remove GetSonarr/UpsertSonarr/
  UpdateSonarrCursor + the SonarrConfig SQL)
- Modify: `backend/internal/db/repo/settings_test.go` (remove the 2 sonarr tests)

**Interfaces:**
- Produces:
  - `func NewSonarrInstances(pool *pgxpool.Pool) *SonarrInstances`
  - `List(ctx, master *crypto.MasterKey) ([]domain.SonarrInstance, error)`
  - `ListEnabled(ctx, master) ([]domain.SonarrInstance, error)`
  - `Get(ctx, master, id uuid.UUID) (*domain.SonarrInstance, error)`
  - `Create(ctx, master, inst domain.SonarrInstance) (*domain.SonarrInstance, error)`
  - `Update(ctx, master, id uuid.UUID, inst domain.SonarrInstance) (*domain.SonarrInstance, error)`
  - `Delete(ctx, id uuid.UUID) error`
  - `UpdateCursor(ctx, id uuid.UUID, lastSeen time.Time) error`

- [ ] Write `sonarr_instances_test.go` (DB-gated like `settings_test.go` —
  reuse its skip-without-DB harness): encrypt round-trip on Create/Get;
  empty-APIKey Update preserves stored key; UpdateCursor advances; Delete removes;
  ListEnabled filters. Mirror the existing settings_test fixtures.
- [ ] Run: expect FAIL (repo not implemented).
- [ ] Implement `sonarr_instances.go` (scan helper shared across List/Get;
  encrypt on Create; conditional enc/nonce on Update; `gen_random_uuid()` default
  or generate in Go — match existing repo style). Decrypt key in scan; normalize
  nil AllowedTrackers to `[]string{}`.
- [ ] Remove the 3 sonarr methods from `settings.go` and their 2 tests.
- [ ] Run full repo build/test (DB tests skip locally): green.
- [ ] Commit: `feat(db): add SonarrInstances repo; drop singleton sonarr config`

---

### Task 4: Poller rework (per-instance fan-out)

**Files:**
- Modify: `backend/internal/sonarr/poller.go`
- Modify: `backend/internal/sonarr/poller_test.go`

**Interfaces:**
- Consumes: `repo.SonarrInstances` via a local `instancesStore` interface:
  `ListEnabled(ctx, *crypto.MasterKey) ([]domain.SonarrInstance, error)` +
  `UpdateCursor(ctx, uuid.UUID, time.Time) error`.
- Produces: `New(log, master, instances instancesStore, admin adminResolver,
  topics topicsStore, httpTimeout time.Duration) *Poller` (signature changes:
  `settings`→`instances`).

- [ ] Update `poller_test.go` fakes: `fakeInstances` implementing ListEnabled/
  UpdateCursor holding a slice of `domain.SonarrInstance` with per-id cursors.
  New tests:
  - `TestPoller_PollsEachEnabledInstanceWithOwnCursor` (two instances, separate
    cursors advance independently).
  - `TestPoller_SkipsDisabledInstance` (disabled not in ListEnabled ⇒ no calls).
  - `TestPoller_PerInstanceIntervalDue` (instance not yet due is not polled).
  - Keep existing single-instance behavioral assertions adapted to one instance.
- [ ] Run: expect FAIL.
- [ ] Rework `poller.go`:
  - `Start`: ticker at `minPollInterval`; maintain `lastPolled map[uuid.UUID]time.Time`.
    Each tick: `instances.ListEnabled`; for each, if `now.Sub(lastPolled[id]) >=
    instanceInterval(inst)` (or absent) ⇒ `p.pollOnce(ctx, inst)` then
    `lastPolled[id]=now`; prune ids not in current list.
  - `pollOnce(ctx, inst domain.SonarrInstance) error`: cursor from `inst.LastSeenAt`;
    first-run stamps `instances.UpdateCursor(inst.ID, now)`; build client from
    inst.URL/APIKey; fetch + processRecords; advance via
    `instances.UpdateCursor(inst.ID, max)`. resolveOwner uses `inst.OwnerUserID`.
  - `processURL`/`handleExisting`/`needsRealign`/`resolveOwner` take the instance
    (defaults + owner) instead of cfg.
  - `instanceInterval(inst)`: floor at minPollInterval, default if 0.
- [ ] Run poller tests: green.
- [ ] Commit: `feat(sonarr): poll every enabled instance with its own cursor`

---

### Task 5: API handlers + router

**Files:**
- Modify: `backend/internal/api/handlers/sonarr.go`
- Modify: `backend/internal/api/handlers/sonarr_test.go`
- Modify: `backend/internal/api/router.go` (lines ~122-129 init, ~206-208 routes)

**Interfaces:**
- Consumes: `repo.SonarrInstances` via handler field `Instances` + a local
  `instancesStore` interface (List/Get/Create/Update/Delete + master).
- Produces routes (admin group):
  - `GET    /system/sonarr/instances`            → `[]instanceView`
  - `POST   /system/sonarr/instances`            → `instanceView` (201)
  - `GET    /system/sonarr/instances/{id}`       → `instanceView`
  - `PUT    /system/sonarr/instances/{id}`       → `instanceView`
  - `DELETE /system/sonarr/instances/{id}`       → 204
  - `POST   /system/sonarr/instances/{id}/test`  → `{ok,version,app_name}`
  - `POST   /system/sonarr/test`                 → `{ok,version,app_name}` (ad-hoc)

- [ ] DTOs:
  - `instanceView{ id, name, enabled, sonarr_url, api_key_set, poll_interval_sec,
    allowed_trackers, default_client_id *string, default_category,
    default_download_dir, update_existing, last_seen_at *string, created_at,
    updated_at }`
  - `instanceReq{ name, enabled, sonarr_url, api_key, poll_interval_sec,
    allowed_trackers, default_client_id, default_category, default_download_dir,
    update_existing }`
  - `testSonarrReq{ sonarr_url, api_key }` (unchanged).
- [ ] Update `sonarr_test.go`: list/create/update/delete/test handlers; assert
  key never leaked (`api_key_set` present, no `api_key`); empty-key preserve on
  update; 422 when enabled w/o URL; create sets owner to current admin.
- [ ] Run: expect FAIL.
- [ ] Implement handlers (validation: name required; URL required if enabled +
  valid http(s); interval ≥ 0; client uuid optional). Audit
  `sonarr.instance.{create,update,delete,test}`. TestExisting falls back to stored
  url+key via `Instances.Get` (decrypt). Reuse `validHTTPURL`, `parseOptionalUUID`,
  client build from existing handler.
- [ ] Rewire `router.go` (handler field `Instances: d.SonarrInstances`; replace
  the 3 routes with the 7 above).
- [ ] Run handler tests: green.
- [ ] Commit: `feat(api): Sonarr instances collection endpoints`

---

### Task 6: main.go + deps wiring

**Files:**
- Modify: `backend/cmd/server/main.go` (~174-179)
- Modify: router deps struct (wherever `d.Settings` Sonarr usage was) to add
  `SonarrInstances`.

- [ ] Build `sonarrInstancesRepo := repo.NewSonarrInstances(pool)`. Pass to
  `sonarr.New(logger, master, sonarrInstancesRepo, users, topicsRepo, cfg.TrackerHTTPTimeout)`
  and to the router deps (`SonarrInstances: sonarrInstancesRepo`).
- [ ] Full backend build/vet/test:
  `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go vet ./... && go test -race ./..."`
  Expected: PASS.
- [ ] Commit: `feat(sonarr): wire multi-instance poller and repo`

---

### Task 7: Frontend api + queryKeys

**Files:**
- Modify: `frontend/src/lib/api.ts` (~197-242)
- Modify: `frontend/src/lib/queryKeys.ts` (~39)

**Interfaces:**
- Produces TS types `SonarrInstance` (incl `id`, `name`, `api_key_set`,
  `last_seen_at`, `created_at`, `updated_at`), `SonarrInstanceCreate`,
  `SonarrInstanceUpdate` (both with `name`, `api_key`), `SonarrTestResult`.
- API: `listSonarrInstances()`, `getSonarrInstance(id)`,
  `createSonarrInstance(body)`, `updateSonarrInstance(id, body)`,
  `deleteSonarrInstance(id)`, `testSonarrInstance(id)`, `testSonarr(body)`.
- queryKeys: `sonarrInstances: ["sonarr-instances"]`,
  `sonarrInstance: (id) => ["sonarr-instances", id]`. Remove `sonarrConfig`.

- [ ] Implement types + calls; remove old singleton ones.
- [ ] Commit: `feat(web): Sonarr instances API client`

---

### Task 8: Frontend components + page + i18n

**Files:**
- Create: `frontend/src/components/integrations/SonarrInstanceList.tsx`
- Create: `frontend/src/components/integrations/SonarrInstanceCard.tsx`
- Create: `frontend/src/components/integrations/SonarrInstanceForm.tsx`
- Modify: `frontend/src/components/integrations/sonarrForm.ts` (add `id`,`name`;
  `fromInstance`/`toCreate`/`toUpdate` helpers)
- Delete: `frontend/src/components/integrations/SonarrCard.tsx` + `SonarrCard.test.tsx`
- Modify: `frontend/src/pages/Integrations.tsx` (catalog layout hosting the list)
- Create: `*.test.tsx` for List, Card, Form
- Modify: `frontend/src/i18n/en.ts`, `frontend/src/i18n/ru.ts`

**Interfaces:**
- Consumes Task 7 api + queryKeys.
- `SonarrInstanceList` self-contained (query + add dialog); `Integrations.tsx`
  renders catalog header + `<SonarrInstanceList/>`.

- [ ] i18n: add keys — `integrations.catalog.*` (section header/desc),
  `settings.sonarr.instances.*` (list title, empty state, add, count),
  `settings.sonarr.status.{active,paused,draft}`, `settings.sonarr.actions.{edit,
  delete,enable,disable,test}`, `settings.sonarr.name` (label), delete-confirm
  copy. en + ru.
- [ ] `sonarrForm.ts`: extend form interface with `name`; `EMPTY_FORM` name '';
  `fromInstance(inst)`; `toCreate(form)`/`toUpdate(form)` (blank api_key handling
  unchanged).
- [ ] Write tests (vitest): List loads cards + empty state; Card shows status
  badge + fires enable/disable + delete-confirm; Form add preserves empty key +
  test shows version. Mock `@/lib/api` + `useSystemInfo` (as in old test).
- [ ] Run: expect FAIL.
- [ ] Implement `SonarrInstanceCard` (status badge derived: enabled→active,
  !enabled && last_seen_at→paused, else draft; actions row), `SonarrInstanceForm`
  (react-hook-form + zod; fields from old SonarrCard; inline Test), and
  `SonarrInstanceList` (cards grid + empty CTA + add dialog). Update
  `Integrations.tsx`.
- [ ] Delete old `SonarrCard.tsx` + test.
- [ ] Run frontend:
  `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test && npm run build"`
  Expected: PASS.
- [ ] Commit: `feat(web): Sonarr instances catalog UI (cards, add/edit/delete)`

---

### Task 9: Docs, site, CLAUDE.md

**Files:**
- Modify: `docs/sonarr-integration.md` (multi-instance configuration section)
- Modify: `site/src/pages/integrations.astro` (Sonarr handoff: TV+anime example)
- Modify: `CLAUDE.md` (sonarr package, Settings repo → SonarrInstances repo,
  Integrations page rows, migration list note 0013)

- [ ] Update docs: Configuration becomes "each instance" (add/edit/delete,
  per-instance enable + cursor; note first-enable per instance is go-forward).
- [ ] Update site copy (multiple instances, one per Sonarr).
- [ ] Update CLAUDE.md structural notes.
- [ ] Build site in Linux container to verify (per memory: site/node_modules is
  glibc): `docker run --rm -v "E:/Projects/Stukans/Marauder/site:/site" -w //site node:20 sh -c "npm run build"`.
- [ ] Commit: `docs: document multiple Sonarr instances`

---

## Self-Review

- **Spec coverage:** schema (T2), repo (T3), poller fan-out (T4), collection API
  (T5), wiring (T6), api client (T7), catalog UI + draft/enable/disable (T8),
  docs/site (T9), migration carry-over (T2), tests each task. ✅
- **Placeholders:** none — each task names exact files, signatures, test names.
- **Type consistency:** `domain.SonarrInstance` (T1) used by repo (T3), poller
  (T4), handlers (T5). `instanceView`/`instanceReq` (T5) mirror TS
  `SonarrInstance`/`SonarrInstanceCreate/Update` (T7). queryKeys `sonarrInstances`
  consistent T7↔T8.
- **Verification:** backend docker build/vet/test (T6); frontend typecheck/test/
  build (T8); site build (T9); manual local docker after merge-ready.
