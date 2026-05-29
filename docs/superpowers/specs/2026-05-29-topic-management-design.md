# Topic management: per-topic client, save-path/category, editing, error-retry

Date: 2026-05-29
Status: Approved design — ready for planning
Branch: `feature/topic-management`

## Problem (four gaps found in acceptance testing)

- **A** — AddTopic has no client picker; with multiple clients a topic can only use the default.
- **B** — No way to edit a topic after creation (quality, season/episode, client, etc.) — only delete + re-add.
- **C** — No per-topic download destination (subfolder/category), e.g. movies → /movies, TV → /tv.
- **D** — An errored topic parks permanently: `DueForCheck` selects only `status='active'`, so a transient error stops checking until manual reactivation.

## What already exists (narrows the work)

- `createTopicReq` already accepts `client_id` and `download_dir`; `Topic.ClientID` + `Topic.DownloadDir` are columns; `sendViaClient` already passes `AddOptions.DownloadDir: t.DownloadDir`. → **A and C-folder are frontend-only.**
- The scheduler computes exponential backoff (capped 6h) + `consecutive_errors`; `RecordCheckResult` already sets `next_check_at` to the backoff time on error. → **D is a one-line query change.**
- `quality`/`start_season`/`start_episode` live in `Topic.Extra` (jsonb); `client_id`/`download_dir` are columns.

## Decisions (confirmed)
- C exposes **both** a download folder (`download_dir` → `AddOptions.DownloadDir`) and a **category** (`AddOptions.Category`, qBittorrent; ignored by Transmission).
- D: errored topics **retry on the existing backoff forever** (never auto-park); `paused` still excluded.

## Architecture

### Backend

**Category (new):**
- Migration `0004_add_topic_category.sql`: `ALTER TABLE topics ADD COLUMN category TEXT;`
- `domain.Topic` gains `Category string`; add `COALESCE(category,'')` to `topicColumns` (after `download_dir`) and `&t.Category` to `scanTopic` in matching order.
- `createTopicReq` gains `Category string json:"category"`; `Create` inserts it.
- `sendViaClient`: pass `Category: t.Category` in `AddOptions` (alongside the existing `DownloadDir`).

**Edit (new):**
- `repo.Topics.Update(ctx, id, userID, fields)` — updates `display_name`, `client_id`, `download_dir`, `category`, and `extra` (carrying quality/start_season/start_episode). Returns the updated topic; `ErrNotFound` on 0 rows. Does NOT touch url/tracker_name/status/hash/scheduling.
- `PUT /topics/{id}` handler: decode an `updateTopicReq` (display_name, client_id, download_dir, category, quality, start_season, start_episode), validate quality against the plugin (like Create), merge the capability fields into `Extra`, call `Update`, return the topic view. Route in the authenticated group.

**Error retry (D):**
- `DueForCheck`: change `WHERE status = 'active'` → `WHERE status IN ('active','error')`. (paused stays excluded.) Errored topics now retry on their already-persisted backoff `next_check_at`; a successful check flips status back to `active` (RecordCheckResult already does this).

### Frontend

- `api.ts`: `Topic`/topic view gains `client_id`, `download_dir`, `category`; add `updateTopic(id, body)` (PUT). A clients list query (reuse `/clients`).
- **AddTopic form** (gap A + C): add a **Client** `<select>` ("Use default client" + each client), a **Download folder (optional)** text input, and a **Category (optional)** text input. Send `client_id`/`download_dir`/`category`. Native `<select>` styling like Quality.
- **Edit (gap B):** an **Edit** action on each topic row opening a form prefilled with the topic's current values (display name, client, quality, season/episode via the catalog dropdowns, download folder, category), calling `updateTopic`. Reuse the AddTopic field components (extract a shared `TopicFields`/form where reasonable; URL is read-only in edit).
- Errored topics (D) already show a red dot; no FE change required, but the edit/retry affordance + the auto-retry make them recoverable.

## Error handling

| Condition | Behavior |
|---|---|
| Edit unknown/foreign topic | 404 |
| Edit with invalid quality | 422 (validated like Create) |
| Category set, client is Transmission | ignored (Transmission has no categories) — documented, not an error |
| Errored topic | retries on backoff (≤6h); success → active; never auto-parks |

## Testing

- repo: pgxmock for `Topics.Update` (column set + ErrNotFound on 0 rows) and `DueForCheck` now selecting `active`+`error`.
- handler: `PUT /topics/{id}` table test (success, 404 foreign, 422 bad quality, Extra-merge of quality/start).
- scheduler: `sendViaClient` passes `Category`; a `DueForCheck` test confirming an `error`-status topic past its next_check is returned.
- frontend: AddTopic sends client_id/download_dir/category; Edit form prefills + PUTs; Vitest.

Success check: backend `go build/vet/test -race ./...` green; frontend typecheck/test/build green; manual: add a topic with a client + folder + category, edit its start episode, and confirm an errored topic retries on its own.

## Documentation
CLAUDE.md (PUT /topics endpoint, `category` column, errored-topic retry behavior) + a note that `DownloadDir`/`Category` are per-topic in `docs/plugin-development.md` if relevant.

## Out of scope
- Per-client save-path browsing/autocomplete (free-text path for now).
- Bulk edit; per-topic check-interval editing (could be added to the edit form later).
