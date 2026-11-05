# Default "replace previous version on update" for new topics — design

Issue: #239 (`artyomsv/marauder`). Status: approved for implementation (option A,
one PR). Written 2026-11-04 against `master` @ `f8dc7b2` (v1.27.2).

## 1. Problem

The per-topic replace-on-update policy (issue #101) exists, but every new topic
starts with "keep all versions":

- A topic added by hand needs the box ticked every time.
- A topic auto-created by the Sonarr poller is always stored with
  `replace_on_update=false, replace_delete_data=true`
  (`backend/internal/sonarr/poller.go:269-289` sets neither field;
  `backend/internal/topics/create.go:214-217` resolves the nil delete flag to
  `true`). Each update of a growing season pack stacks another torrent on the
  same folder.

Reporter's target setup: **replace = on, delete data = off** — the new torrent
reuses the files on disk, qBittorrent rechecks and fetches only new episodes,
and the old torrent entry is removed. Deleting data there would remove files
the new torrent uses, so `delete data = off` must be expressible as a default.

## 2. Decisions (fixed — do not revisit)

1. **Global defaults** live in the singleton `settings` row, next to
   `follow_tracker_title_default` (migration `0019`), and follow the same
   contract: **materialised onto the topic at create time; changing a default
   never touches an existing topic.**
2. **Per-Sonarr-instance defaults are nullable (tri-state).** `NULL` = inherit
   the global default. `true`/`false` = override for topics this instance
   creates. Each of the two fields inherits independently.
3. **One PR** for both parts.
4. **Existing topics are never changed** by this feature — not by a settings
   edit, not by an instance edit, and not by the poller's `update_existing`
   realign path.

## 3. Resolution rules (the core of the feature)

Built-in defaults (today's behaviour, unchanged when nobody configures
anything): `replace_on_update = false`, `replace_delete_data = true`.

### 3.1 `POST /api/v1/topics` (manual add, API clients)

| Field | Request value | Stored value |
|---|---|---|
| `replace_on_update` | present (`true`/`false`) | the request value |
| `replace_on_update` | omitted | global `replace_on_update_default` |
| `replace_delete_data` | present | the request value (the existing 422 guard still applies) |
| `replace_delete_data` | omitted | global `replace_delete_data_default` **AND NOT** `only_new_files` **AND NOT** folder-name mode `custom`/`keep` |

- A settings read error **fails open to the built-in defaults** (`false` /
  `true`) — same as `followTrackerTitle` at
  `backend/internal/api/handlers/topics.go:499-514`. Do not fail the create.
- The "AND NOT only-new-files / folder name" guard applies **only to a
  defaulted value**. An explicit `replace_delete_data=true` with
  `only_new_files=true` is still rejected with 422 by
  `topics.ValidUpdatePolicy` exactly as today. Reason: a user who never touched
  the delete box must never receive a 422 caused by an admin default.

### 3.2 Sonarr poller (topic auto-create)

Per field, first non-null wins:

1. the instance column (`default_replace_on_update` / `default_replace_delete_data`);
2. the global settings default;
3. the built-in default.

The poller never sets `only_new_files` or a folder name, so the delete guard
in 3.1 is a no-op there today — but route the value through the same
defaulted-value path (see 4.3) so the guard still holds if that changes.

A settings read error fails open to the built-in defaults for the fields that
the instance left `NULL` (log one Warn, as `defaultFollowTrackerTitle` does).

### 3.3 Things that do NOT change

- `PUT /topics/{id}` (topic edit): unchanged. Defaults apply only on create.
- Poller `handleExisting` / `needsRealign` (`poller.go:336-422`): **must not**
  read or apply the new defaults. It keeps writing back
  `existing.ReplaceOnUpdate` / `existing.ReplaceDeleteData`. Add a test that
  pins this (section 7).
- Scheduler: unchanged. It already ignores replace-on-update for per-episode
  trackers (`scheduler.go:584`, `isEpisodic`), so a default of `true` stored on
  a LostFilm topic is harmless.
- `topics.ValidUpdatePolicy` (`backend/internal/topics/policy.go`): unchanged.

## 4. Backend changes

### 4.1 Migration `backend/internal/db/migrations/0020_add_replace_on_update_defaults.sql`

```sql
-- +goose Up
-- +goose StatementBegin
-- Instance-wide defaults for the per-topic replace-on-update policy (issue
-- #101) applied to topics created without an explicit choice (issue #239).
-- Materialised onto the topic at create time, so changing them never touches
-- an existing topic. The defaults match the historical behaviour: keep every
-- version; delete data once replace is enabled.
ALTER TABLE settings
    ADD COLUMN replace_on_update_default   BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN replace_delete_data_default BOOLEAN NOT NULL DEFAULT true;
-- Per-Sonarr-instance overrides for topics the poller creates. NULL inherits
-- the instance-wide default above; each column inherits independently.
ALTER TABLE sonarr_instances
    ADD COLUMN default_replace_on_update   BOOLEAN NULL,
    ADD COLUMN default_replace_delete_data BOOLEAN NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sonarr_instances
    DROP COLUMN default_replace_delete_data,
    DROP COLUMN default_replace_on_update;
ALTER TABLE settings
    DROP COLUMN replace_delete_data_default,
    DROP COLUMN replace_on_update_default;
-- +goose StatementEnd
```

Check `0019_add_follow_tracker_title.sql` for the exact goose header style and
match it.

### 4.2 Settings repo — `backend/internal/db/repo/settings.go`

- `InstanceSettings` gains `DefaultReplaceOnUpdate bool` and
  `DefaultReplaceDeleteData bool`.
- `Get` selects all three columns.
- Replace the single-field setter with one patch method, so a partial update is
  one statement:

  ```go
  // SettingsPatch carries the fields to change; nil leaves a column alone.
  type SettingsPatch struct {
      DefaultFollowTrackerTitle *bool
      DefaultReplaceOnUpdate    *bool
      DefaultReplaceDeleteData  *bool
  }

  // Update applies the non-nil fields of p and returns the row as stored.
  func (r *Settings) Update(ctx context.Context, p SettingsPatch) (*InstanceSettings, error)
  ```

  SQL: `UPDATE settings SET follow_tracker_title_default = COALESCE($1, follow_tracker_title_default), replace_on_update_default = COALESCE($2, …), replace_delete_data_default = COALESCE($3, …), updated_at = now() WHERE id = 1 RETURNING <the three columns>`.
  Pass `*bool` straight to pgx (nil → SQL NULL).
- Remove `SetDefaultFollowTrackerTitle` and update every caller and fake
  (search the tree: `grep -rn SetDefaultFollowTrackerTitle backend`). Known
  fakes: `handlers/topics_followtitle_test.go`, the poller tests, the settings
  handler tests.
- Update `repo/settings_test.go` (it pins the exact SQL strings).

### 4.3 Shared create — `backend/internal/topics/create.go`

- Keep `CreateInput.ReplaceOnUpdate bool` (concrete: callers resolve it, the
  same way `FollowTrackerTitle` works — see the comment at `create.go:104-110`).
- Keep `CreateInput.ReplaceDeleteData *bool` meaning **explicit choice only**.
- Add:

  ```go
  // DefaultReplaceDeleteData is the delete-data value to use when
  // ReplaceDeleteData is nil (no explicit choice): the creator's resolved
  // default (instance-wide or Sonarr instance). Nil means the built-in
  // default, true. Like the built-in default it is overridden to false with
  // only-new-files or a folder name, so an inherited default can never cause
  // the ErrOnlyNewFilesDeletesData / ErrFolderNameDeletesData rejection.
  DefaultReplaceDeleteData *bool
  ```

- Change `create.go:214`:

  ```go
  base := true
  if in.DefaultReplaceDeleteData != nil {
      base = *in.DefaultReplaceDeleteData
  }
  replaceDeleteData := base && !in.OnlyNewFiles && !usesFolderName(folderMode)
  if in.ReplaceDeleteData != nil {
      replaceDeleteData = *in.ReplaceDeleteData
  }
  ```

  Update the comment block above it to describe the default.

- Add one small exported helper in the `topics` package so the handler and the
  poller do not duplicate the precedence logic:

  ```go
  // ReplaceDefaults is the replace-on-update policy a new topic inherits
  // when its creator sends no explicit choice.
  type ReplaceDefaults struct {
      ReplaceOnUpdate   bool
      ReplaceDeleteData bool
  }

  // BuiltinReplaceDefaults is the behaviour before any default is configured.
  var BuiltinReplaceDefaults = ReplaceDefaults{ReplaceOnUpdate: false, ReplaceDeleteData: true}
  ```

  Optional: a `Resolve(override *bool, fallback bool) bool` helper. Keep it
  minimal; do not build a framework.

### 4.4 Topic create handler — `backend/internal/api/handlers/topics.go`

- `createTopicReq.ReplaceOnUpdate` becomes `*bool` (`json:"replace_on_update,omitempty"`).
  Update its comment: omitted = instance default applies; non-nil `false` is an
  explicit choice.
- Read settings **once** per create. Refactor `followTrackerTitle` so the
  handler calls `h.Settings.Get` a single time and derives all three values
  from that one result (fail-open to built-ins on error or nil `h.Settings`).
  Keep the follow-title rules exactly as they are (explicit wins; a supplied
  display name suppresses the default).
- Pass to `BuildAndCreate`:
  - `ReplaceOnUpdate`: explicit value, else `settings.DefaultReplaceOnUpdate`.
  - `ReplaceDeleteData`: `req.ReplaceDeleteData` unchanged (explicit only).
  - `DefaultReplaceDeleteData`: `&settings.DefaultReplaceDeleteData`.
- `settingsStore` interface in `handlers/settings.go` changes to
  `Get` + `Update(ctx, repo.SettingsPatch)`. If the topics handler uses a
  narrower read-only interface, keep it read-only.

### 4.5 Settings handler — `backend/internal/api/handlers/settings.go`

- `settingsView` gains `default_replace_on_update` and
  `default_replace_delete_data` (plain `bool`).
- `updateSettingsReq` gains both as `*bool` with `omitempty`.
- `Update`: 400 `"no settings to update"` only when **all three** are nil.
  Call `Store.Update(ctx, patch)` once and return the **stored row** it
  returns (not an echo of the request — the request is partial now).
- Audit: one `settings_update` entry; the resource id stays `"settings"`-scoped;
  the metadata map holds only the keys that were sent. Keep the existing
  resource-id string for the follow-title case if tests pin it; otherwise use
  `"topic_defaults"` for all.
- Routes do not change (`router.go:181` GET any user, `:242` PUT admin).

### 4.6 Sonarr instance — domain, repo, handler

- `backend/internal/domain/domain.go` (`SonarrInstance`, ~line 349): add
  `DefaultReplaceOnUpdate *bool` and `DefaultReplaceDeleteData *bool` with a
  comment: nil inherits the instance-wide default.
- `backend/internal/db/repo/sonarr_instances.go`:
  - add both columns to `sonarrCols` and to the `scanInstance` scan list
    (scan into `*bool`);
  - `Create`: add both to `cols`/`vals`/`args`; **renumber** the API-key
    placeholders (`$11,$12` → `$13,$14`);
  - `Update`: add both to the `SET` list; **renumber** the API-key and id
    placeholders (`$10,$11,$12` → `$12,$13,$14`; the no-key branch `$10` →
    `$12`). Instance update is a full replacement: a nil value writes NULL
    (that is how a user returns to "inherit").
  - Update the repo's pgxmock tests (they pin SQL and argument order).
- `backend/internal/api/handlers/sonarr.go`:
  - `instanceView`: add `DefaultReplaceOnUpdate *bool \`json:"default_replace_on_update"\``
    and `DefaultReplaceDeleteData *bool \`json:"default_replace_delete_data"\``
    (no `omitempty` — the UI must see `null` explicitly).
  - `instanceReq`: same two `*bool` fields (`null` / omitted = inherit).
  - `decodeInstance` and `toInstanceView`: copy them through.
  - No validation rule is needed: `only_new_files` / folder name are never set
    by the poller, so no combination can 422.

### 4.7 Sonarr poller — `backend/internal/sonarr/poller.go`

- Replace `defaultFollowTrackerTitle(ctx) bool` with one method that reads
  settings once and returns everything a new topic inherits, e.g.:

  ```go
  // topicDefaults is what a Sonarr-created topic inherits: the instance's own
  // overrides first, then the instance-wide defaults, then the built-in
  // behaviour. Fails open to the built-ins on a settings read error.
  func (p *Poller) topicDefaults(ctx context.Context, inst domain.SonarrInstance) (follow bool, replace topics.ReplaceDefaults)
  ```

- In `processURL`, pass `FollowTrackerTitle: follow`,
  `ReplaceOnUpdate: replace.ReplaceOnUpdate`,
  `DefaultReplaceDeleteData: &replace.ReplaceDeleteData`. Leave
  `ReplaceDeleteData` (explicit) nil.
- Do **not** read settings when the URL is a duplicate or goes to
  `handleExisting` — call `topicDefaults` only right before `BuildAndCreate`.
- `handleExisting` and `needsRealign`: **no change**.
- Keep the Warn log on settings failure; extend its message to name the
  replace defaults too.

## 5. Frontend changes

### 5.1 API types — `frontend/src/lib/api.ts`

- `InstanceSettings` (~line 423): add `default_replace_on_update: boolean` and
  `default_replace_delete_data: boolean`.
- `UpdateSettingsBody` (~line 429): all three fields optional.
- `SonarrInstance` (~line 255) and `SonarrInstanceBody` (~line 274): add
  `default_replace_on_update: boolean | null` and
  `default_replace_delete_data: boolean | null`.
- Use `interface`, not `type`, for object shapes (project rule).

### 5.2 Settings page — `frontend/src/components/settings/TopicDefaultsCard.tsx`

- Add two rows under the existing follow-title row:
  1. "Replace previous version on update" (reuse the existing per-topic label
     key from `UpdatePolicyFields`/i18n if one exists).
  2. "Also delete the old version's files" — indented, **disabled while row 1
     is off**, with help text explaining the Sonarr season-pack case (keep the
     files when the new torrent reuses the folder).
- `mutationFn` takes a partial `UpdateSettingsBody` (one field per click).
  `onSuccess` keeps `setQueryData(QK.settings, next)` — the server now returns
  the full stored row.
- Keep the "never invent a value" behaviour: all rows disabled until
  `settings.isSuccess`; the error + retry block covers all rows.
- The three rows share markup → extract a small `DefaultToggleRow` component
  (props: `checked`, `disabled`, `label`, `help`, `onChange`, `indent?`). Keep
  the file under 250 lines.

### 5.3 Add-topic form — `AddTopicCard.tsx` and `TopicForm.tsx`

- `AddTopicCard.tsx:156-160`: seed `replaceOnUpdate` and `replaceDeleteData`
  from `settings.data` (fall back to `EMPTY`'s `false` / `true` while loading
  or on error), next to `followTrackerTitle`.
- `TopicForm.tsx:288-298`: the late-arrival `useEffect` + "owned" ref exists
  for `followTrackerTitle`. Do the same for the two replace fields: while the
  user has not touched the replace-on-update block, keep applying
  `initial.replaceOnUpdate` / `initial.replaceDeleteData` when they change.
  One shared ref for the block is fine (set it in the block's change handler).
  Apply only in add mode — edit mode `initial` comes from the stored topic.
- Submit (`TopicForm.tsx:360`): send `replaceOnUpdate: !episodic && delivery.replaceOnUpdate`.
  The block is hidden for per-episode trackers, and the form must not save
  state the user cannot see (same rule as `addPausedOnUpdate` at `:365`).
  Leave the existing `replaceDeleteData` masking at `:361` as it is.
- `AddTopicCard` continues to send both fields explicitly; that is correct —
  the user saw the seeded values.
- Check `UpdatePolicyFields.tsx` still renders correctly when the form opens
  with replace already on (delete box visible immediately, locked off when
  only-new-files or a folder name is set).

### 5.4 Sonarr instance form

- `frontend/src/components/integrations/sonarrForm.ts`: add both fields to
  `SonarrForm`, `EMPTY_FORM` (`null`), `fromInstance`, `toBody`, **and
  `instanceToBody`**. Missing them in `instanceToBody` would reset an
  instance's overrides to inherit every time the card's enable toggle is
  clicked.
- `SonarrInstanceForm.tsx` (near the client/category/dir fields, ~146-175): two
  tri-state selects — "Use global default (currently: On/Off)", "On", "Off".
  Show the current global value in the first option when the settings query
  has it (`QK.settings`; non-admins never see this page, so the read is
  allowed). The delete-data select is disabled when the **effective** replace
  value (instance override, else global) is off.
- `SonarrInstanceCard.tsx` (~118-125): optional summary line, e.g.
  "Replace on update: inherit / on / off". Add only if it fits the existing
  card layout.

### 5.5 i18n — `frontend/src/i18n/en.ts` and `ru.ts`

Add keys for: the two settings rows + help text, the Sonarr select label(s),
"Use global default (currently {value})", "On", "Off". English and Russian
both. Reuse existing keys where the wording is identical.

## 6. Docs (same PR)

- `docs/replace-on-update.md`: a "Defaults for new topics" section — the
  global defaults, the Sonarr per-instance override, the precedence order, and
  the season-pack recipe (replace on, delete off).
- `docs/sonarr-integration.md`: the two new instance fields.
- `CLAUDE.md`: the `Settings` description (only lists
  `follow_tracker_title_default` today) and the `sonarr` package row (says the
  poller uses "that instance's default client/category/dir").
- `CHANGELOG.md` `[Unreleased]`: one `Added` bullet, ending `Ref #239` style
  per existing entries.

## 7. Tests

Write tests first where practical. Backend (stdlib assertions, manual fakes,
table-driven):

1. `topics` — `BuildAndCreate` delete-data matrix: `DefaultReplaceDeleteData`
   nil/true/false × `OnlyNewFiles` × folder mode × explicit
   `ReplaceDeleteData` nil/true/false. Assert the stored value and that a
   defaulted `true` with only-new-files **does not** 422 while an explicit
   `true` still does.
2. Topic create handler: omitted vs explicit `replace_on_update`; omitted
   delete-data takes the global default; settings `Get` error → built-ins;
   settings read exactly once per create.
3. Settings handler: partial PUT of each field; empty body → 400; response is
   the stored row; audit metadata holds only the sent keys.
4. `repo/settings_test.go`: new SQL for `Get` and `Update`.
5. `repo/sonarr_instances` tests: column list, argument order, renumbered
   placeholders for both API-key branches, NULL round-trip.
6. Integration (`//go:build integration`, `internal/db/repo/integration_test.go`
   harness): migration applies; settings defaults are `false`/`true`; instance
   columns default NULL; `Update` with nil writes NULL.
7. Poller: precedence table — instance override vs global vs built-in, per
   field independently; settings error → built-ins for NULL fields; settings
   not read for duplicates.
8. Poller: `handleExisting` with `UpdateExisting=true` and instance overrides
   set **does not change** `ReplaceOnUpdate`/`ReplaceDeleteData` of the
   existing topic (extend `TestPoller_UpdateExistingRealigns` at
   `poller_test.go:491` or add a sibling).

Frontend (Vitest + Testing Library, co-located):

9. `TopicDefaultsCard.test.tsx`: three rows; delete row disabled while replace
   off; each click sends a one-field body; disabled until loaded.
10. `AddTopicCard`/`TopicForm`: seeds from settings; a late settings answer
    updates untouched fields; a user change is not overwritten; episodic
    tracker submits `replace_on_update: false`.
11. `sonarrForm.test.ts`: `fromInstance`/`toBody`/`instanceToBody` round-trip
    `null`/`true`/`false`.
12. `SonarrInstanceForm.test.tsx`: tri-state selects; delete select disabled
    when effective replace is off. Update fixtures in
    `SonarrInstanceCard.test.tsx`, `SonarrInstanceList.test.tsx`,
    `pages/Integrations.test.tsx` for the new fields.

## 8. Acceptance criteria

1. With no configuration, behaviour is identical to v1.27.2 (manual add,
   API add, Sonarr add).
2. Admin sets Settings → replace on, delete off. A topic added by hand opens
   with replace on / delete off; an API `POST /topics` without the two fields
   stores `true`/`false`.
3. A Sonarr instance with both fields NULL creates topics with the global
   values; with an override, the override wins for that field only.
4. Toggling a Sonarr instance enabled/disabled on its card keeps its overrides.
5. Global delete-data `true` + a manual add with "only new files" stores
   delete-data `false` and does not 422.
6. No existing topic changes after any settings or instance edit, or after the
   poller realigns an existing topic.
7. CI is green: `ci.yml` (backend build/vet/test/lint, `backend-integration`,
   frontend typecheck + vitest) on the PR.

## 9. Working rules for the implementing agent

- **Do not run builds, tests, or linters on this host** (no `go test`,
  `go build`, `golangci-lint`, `npm test`, `npx vitest`, `npx tsc`, Docker
  build containers). Push the branch; CI runs them. Read failures with
  `gh run view <run-id> --log-failed`. Formatting a file you edited
  (`gofmt -w <file>`, `prettier --write <file>`) is allowed.
- Go: tabs, `gofmt`; wrap errors with `%w`; small consumer-side interfaces;
  manual fakes. TS: 2 spaces, `interface` for object shapes, `@/` imports,
  React Query keys from `lib/queryKeys.ts`, i18n via `useT()`.
- Commits: imperative mood, first line ≤ 72 chars, body for non-trivial
  changes, `Ref #239`. **No closing keyword** (`close/fix/resolve…`) before
  the issue number anywhere, no sidebar issue link. **No AI/agent/model/vendor
  attribution** in commits or the PR (no `Co-Authored-By`, no "generated
  with").
- PR title must be Conventional Commits and bump a minor version:
  `feat(topics): default replace-on-update policy for new topics`.
- Branch name must start with the issue number (the release job reads it):
  `239-replace-on-update-defaults`.
- After opening the PR, set the review label:
  `node "$HOME/.claude/skills/pr-review/scripts/pr-review.mjs" request-review --pr <n>`.
- Keep every change in scope. No refactors outside the files listed above
  unless a listed change requires it.
