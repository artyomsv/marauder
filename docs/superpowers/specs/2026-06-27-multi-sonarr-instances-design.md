# Multi-Sonarr Instances — Design (#92)

## Problem

The Sonarr integration supports a **single** instance, stored as 13 columns on
the singleton `settings` row (migration `0009`). Users commonly run multiple
Sonarr instances (e.g. one for TV, one for anime), each with its own download
client, category, save path, allowed-tracker set, and **independent history
cursor**. The singleton schema makes this impossible.

## Goal

Support **N Sonarr instances**, each independently configured and
enabled/disabled, each with its own poll cursor. Ship a modern Integrations UI:
an integration catalog plus a list of Sonarr instance cards with add / edit /
delete / enable-disable / test.

## Decisions on open questions (resolved autonomously)

1. **"Draft" state** — model as a plain `enabled BOOLEAN`. A newly-created
   instance defaults to **disabled** (this *is* the "draft": saved + testable but
   not polling). The card shows a "Draft" badge for a disabled instance that has
   never successfully polled (`last_seen_at IS NULL && !enabled`), "Paused" for a
   disabled instance that has polled before, and "Active" when enabled. No third
   DB state — the badge is derived. Keeps schema simple, satisfies the UX ask.
2. **Integration catalog** — only Sonarr exists today. The page renders a
   catalog header + a Sonarr "integration type" section containing the instance
   cards. We do **not** build a generic plugin framework (YAGNI); the layout just
   reads as a catalog so a future Radarr is a copy-paste.
3. **Migration of existing config** — if the singleton `settings` row has a
   non-empty `sonarr_url`, the up-migration copies it into one `sonarr_instances`
   row, **preserving `last_seen_at`** (cursor), `enabled`, encrypted key + nonce,
   owner, and all defaults. Name defaults to `'Sonarr'`. Then the `sonarr_*`
   columns are dropped from `settings`. No re-import on upgrade.
4. **Owner** — unchanged resolution (per-instance `owner_user_id`, fallback to
   first admin). Carried from the singleton on migration.
5. **Poller fan-out** — single manager loop (preserves today's "re-read each
   tick" model). Ticks at the 60s floor; each tick lists **enabled** instances
   and polls any instance that is due (`now - lastPolled >= instance interval`),
   tracking `lastPolled` per instance id in memory. Add/edit/enable/disable/delete
   take effect on the next tick with no goroutine lifecycle to manage. Instances
   polled sequentially (instance count is small).
6. **Metrics** — keep existing collector names/labels (`SonarrPollsTotal{result}`,
   `SonarrTopicsCreatedTotal`, `SonarrRecordsProcessedTotal{outcome}`); they now
   aggregate across instances. Avoids breaking existing dashboards / cardinality
   growth. (Noted as a deliberate non-change.)
7. **Test endpoints** — keep both: an ad-hoc test (`POST /system/sonarr/test`
   with url+key, used by the add form before the instance exists) and a
   per-instance test (`POST /system/sonarr/instances/{id}/test`, falls back to
   the stored url+key, used by the edit form / card).

## Schema — migration `0013_add_sonarr_instances.sql`

```sql
-- +goose Up
CREATE TABLE sonarr_instances (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                   TEXT        NOT NULL,
    enabled                BOOLEAN     NOT NULL DEFAULT false,
    url                    TEXT        NOT NULL DEFAULT '',
    api_key_enc            BYTEA,
    api_key_nonce          BYTEA,
    poll_interval_sec      INTEGER     NOT NULL DEFAULT 900,
    allowed_trackers       TEXT[]      NOT NULL DEFAULT '{}',
    default_client_id      UUID        REFERENCES clients(id) ON DELETE SET NULL,
    default_category       TEXT        NOT NULL DEFAULT '',
    default_download_dir   TEXT        NOT NULL DEFAULT '',
    update_existing        BOOLEAN     NOT NULL DEFAULT false,
    owner_user_id          UUID        REFERENCES users(id)   ON DELETE SET NULL,
    last_seen_at           TIMESTAMPTZ,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Carry a configured singleton into one instance (preserve cursor).
INSERT INTO sonarr_instances
    (name, enabled, url, api_key_enc, api_key_nonce, poll_interval_sec,
     allowed_trackers, default_client_id, default_category, default_download_dir,
     update_existing, owner_user_id, last_seen_at)
SELECT 'Sonarr', sonarr_enabled, sonarr_url, sonarr_api_key_enc,
       sonarr_api_key_nonce, sonarr_poll_interval_sec, sonarr_allowed_trackers,
       sonarr_default_client_id, sonarr_default_category,
       sonarr_default_download_dir, sonarr_update_existing,
       sonarr_owner_user_id, sonarr_last_seen_at
FROM settings
WHERE id = 1 AND sonarr_url IS NOT NULL AND sonarr_url <> '';

ALTER TABLE settings
    DROP COLUMN sonarr_enabled, DROP COLUMN sonarr_url,
    DROP COLUMN sonarr_api_key_enc, DROP COLUMN sonarr_api_key_nonce,
    DROP COLUMN sonarr_poll_interval_sec, DROP COLUMN sonarr_allowed_trackers,
    DROP COLUMN sonarr_default_client_id, DROP COLUMN sonarr_default_category,
    DROP COLUMN sonarr_default_download_dir, DROP COLUMN sonarr_update_existing,
    DROP COLUMN sonarr_owner_user_id, DROP COLUMN sonarr_last_seen_at;

-- +goose Down
-- Re-add the singleton columns (migration 0009 shape) and copy back the
-- most-recently-updated instance (best-effort; lossy for N>1), then drop table.
```

`index`: a partial index on `enabled` is unnecessary (small table; full scan each
tick is trivial). Skip it.

## Backend changes

**`domain` (`domain.go`)** — replace `SonarrConfig` with `SonarrInstance`:
adds `ID uuid.UUID`, `Name string`, `CreatedAt/UpdatedAt time.Time`; keeps all
existing fields (`Enabled, URL, APIKey, PollIntervalSec, AllowedTrackers,
DefaultClientID, DefaultCategory, DefaultDownloadDir, UpdateExisting,
OwnerUserID, LastSeenAt`).

**`db/repo/sonarr_instances.go`** (new) — `SonarrInstances` repo:
- `List(ctx, master) ([]domain.SonarrInstance, error)` — all, ordered by created_at; decrypts keys.
- `ListEnabled(ctx, master) ([]domain.SonarrInstance, error)` — `WHERE enabled`.
- `Get(ctx, master, id) (*domain.SonarrInstance, error)`.
- `Create(ctx, master, inst) (*domain.SonarrInstance, error)` — encrypts key.
- `Update(ctx, master, id, inst) (*domain.SonarrInstance, error)` — empty `APIKey` ⇒ keep stored key (rotation guard, same as today).
- `Delete(ctx, id) error`.
- `UpdateCursor(ctx, id, lastSeen) error`.
Reuse the existing `master.EncryptString/DecryptString` pattern. Delete the old
`GetSonarr/UpsertSonarr/UpdateSonarrCursor` from `settings.go` (and their tests).

**`sonarr/poller.go`** — swap `settingsStore` for an `instancesStore`
(`ListEnabled`, `UpdateCursor`). Rework `Start`:
- ticker at `minPollInterval` (60s);
- each tick: `ListEnabled`; for each instance, if due (in-memory `lastPolled[id]`),
  `pollOnce(ctx, instance)` then stamp `lastPolled[id]`; prune `lastPolled` of ids
  no longer present.
- `pollOnce` takes a `domain.SonarrInstance` (was a fetched config); cursor read
  from `instance.LastSeenAt`, advanced via `instances.UpdateCursor(id, max)`.
- `processURL` / `handleExisting` unchanged except they receive the instance's
  defaults + owner. First-run (nil cursor) still stamps to now (go-forward).
- owner resolution unchanged (per instance).

**`api/handlers/sonarr.go`** — collection + per-id handlers:
- `List` `GET  /system/sonarr/instances` → `[]instanceView` (key never leaked; `api_key_set`).
- `Create` `POST /system/sonarr/instances` → validates (name required; url required if enabled; valid http(s); interval ≥ 0; client uuid). Owner set to current admin. Returns created view.
- `Get`    `GET  /system/sonarr/instances/{id}`.
- `Update` `PUT  /system/sonarr/instances/{id}` → same validation; empty key preserved.
- `Delete` `DELETE /system/sonarr/instances/{id}`.
- `TestExisting` `POST /system/sonarr/instances/{id}/test` → stored url+key fallback.
- `TestAdHoc` `POST /system/sonarr/test` → url+key from body (pre-create).
- Audit events: `sonarr.instance.create/update/delete/test`.
- DTOs: `instanceView` (adds `id`, `name`, derived nothing else), `instanceReq`
  (adds `name`), `testSonarrReq` unchanged.

**`api/router.go`** — replace the 3 singleton routes with the collection routes
above (still inside the admin-only group). Handler constructed with the new
`SonarrInstances` repo (field `Instances`).

**`cmd/server/main.go`** — build `sonarrInstancesRepo := repo.NewSonarrInstances(pool)`;
pass to `sonarr.New(...)` and to the handler wiring.

**Tests** — `sonarr_instances_test.go` (repo: encrypt round-trip, empty-key
preserve, cursor update, list/get/delete); `poller_test.go` (multi-instance:
two instances with separate cursors, disabled instance skipped, per-instance
interval due-logic); `sonarr_test.go` (handlers: list/create/update/delete/test,
key-never-leaked, validation 422s).

## Frontend changes

**`pages/Integrations.tsx`** — catalog layout: page header + a Sonarr section
hosting the instance list. Admin-gate unchanged.

**`components/integrations/`** (new/rework):
- `SonarrInstanceList.tsx` — fetches `QK.sonarrInstances`, renders cards or an
  empty state with an "Add Sonarr instance" CTA; hosts the add dialog.
- `SonarrInstanceCard.tsx` — name, URL, status badge (Active/Paused/Draft),
  default client/category, last-seen; actions: Enable/Disable toggle, Edit,
  Delete (uses `DeleteConfirm`), Test.
- `SonarrInstanceForm.tsx` — add/edit dialog (react-hook-form + zod): name,
  enabled, url, api key (password, "key stored" hint on edit), poll interval,
  allowed trackers (multi), default client/category/dir, update-existing, inline
  Test button.
- Reuse `sonarrForm.ts` helpers, extended with `name`/`id`.
- Old `SonarrCard.tsx` removed (its logic moves into the form/card).

**`lib/api.ts`** — `SonarrInstance`, `SonarrInstanceCreate/Update` types +
`listSonarrInstances`, `getSonarrInstance`, `createSonarrInstance`,
`updateSonarrInstance`, `deleteSonarrInstance`, `testSonarrInstance`,
`testSonarr` (ad-hoc). Remove the old singleton calls/types.

**`lib/queryKeys.ts`** — `sonarrInstances: ["sonarr-instances"]`,
`sonarrInstance: (id) => ["sonarr-instances", id]`. Remove `sonarrConfig`.

**i18n en/ru** — new keys under `integrations.*` / `settings.sonarr.*` for list,
empty state, add/edit/delete, status badges, per-card actions; keep existing
field labels.

**Tests** — `SonarrInstanceList.test.tsx`, `SonarrInstanceCard.test.tsx`,
`SonarrInstanceForm.test.tsx` (load list, add preserves empty key, test shows
version, delete confirm, enable/disable). Replace `SonarrCard.test.tsx`.

## Docs & site

- `docs/sonarr-integration.md` — rewrite Configuration section for N instances
  (each instance card; per-instance enable/cursor; "add an instance" flow).
- `site/src/pages/integrations.astro` — Sonarr handoff copy mentions multiple
  instances (TV + anime example).
- `CLAUDE.md` — update the `sonarr` package + Settings repo + Integrations rows.

## Success criteria

- Backend: `go build ./... && go vet ./... && go test -race ./...` green.
- Frontend: `npm run typecheck && npm test && npm run build` green.
- Migration up carries a configured singleton into one instance (cursor intact)
  and drops the columns; down restores columns.
- code-review findings all resolved; PR CI all green.
- Manual (local docker): add two instances (TV + anime) pointing at the dev
  Sonarr, enable one, Test connection succeeds, disabled/draft instance does not
  poll, enabled instance imports a grab into the correct client/category.
