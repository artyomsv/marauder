# Issue #184 — per-topic notify-only mode: feasibility analysis

Grounded in code read on 2026-09-19 at repo `E:\Projects\Stukans\Marauder`. Every
claim cites `file:line`. No repo files were changed.

---

## 1. Premise check

**The user's claim ("release notifications are tied to successful submissions to a
download client") is wrong in mechanism but right in effect.**

### 1a. `release.found` fires BEFORE the client submit, and is notifiable on its own

- `backend/internal/scheduler/scheduler.go:412` computes
  `updated := check.Hash != "" && check.Hash != t.LastHash`.
- `scheduler.go:438-446` emits `events.ReleaseFound` immediately after that, with
  `Title: t.DisplayName, Body: "New release detected", SourceURL: t.URL`.
- `downloadAllPending` is only called afterwards at `scheduler.go:460`.
- `backend/internal/events/event.go:40`:
  `ReleaseFound: {Persist: true, Notifiable: true, SSE: true}`.
- `backend/internal/events/bus.go:79-84`: every notifiable event goes to
  `Notifier.SendVia(...)` with `Title/Body/Link/SourceURL/AuthorComment`.
- Telegram renders Title + Body + a `Source` link:
  `backend/internal/plugins/notifiers/telegram/telegram.go:105-111`.

So "a release is available" as an independent notification already exists.

### 1b. What happens TODAY with no client and no default client — exact path

1. `runCheck` → `downloadAllPending` (`scheduler.go:460`) → `tr.Download` returns a
   payload → `submitToClient` (`scheduler.go:815`).
2. `scheduler.go:816-823`: `t.ClientID == nil` → `s.clients.GetDefault` →
   `backend/internal/db/repo/clients.go:119-120` returns `repo.ErrNotFound` →
   `scheduler.go:821` returns
   `errors.New("no client configured for this topic and no default client")`.
3. `downloadAllPending` returns that error (`scheduler.go:747-753`, metric
   `submit_error`).
4. `runCheck` dlErr branch `scheduler.go:462-479`: persists the **OLD** hash
   (`scheduler.go:476`: `s.recordResult(ctx, log, t, t.LastHash, anySubmitted,
   s.backoff(t, true, dlErr), dlErr.Error(), dlErr)`), then `notifyError` (`:477`),
   `recordChecked(true, true)` and returns.
5. Classification: `classifyCause` (`scheduler.go:1173-1208`) matches no sentinel;
   `classifyError` (`scheduler.go:1216-1332`) — the message contains none of
   `plugin not installed`, `clearance`, `cloudflare`, `flaresolverr`, no HTTP status,
   none of the timeout/unreachable/auth/parse keywords → `errCodeUnknown`.
6. Backoff: `isTransientError` (`scheduler.go:1350-1387`) matches no marker →
   durable → `backoffDelay` (`scheduler.go:1043-1091`) exponential, capped at
   `CheckMaxBackoff` (default 6h).
7. `RecordCheckResult` (`backend/internal/db/repo/topics.go:222-246`) sets
   `status='error'`, `consecutive_errors + 1`, keeps `last_hash` (because the old
   hash is passed, `:231`).
8. Every later tick: `check.Hash != t.LastHash` is still true → re-enters the
   `updated` branch → fails the same way.

### 1c. The dedup gate — the crux

- `scheduler.go:438`: `if s.emit != nil && t.ConsecutiveErrors == 0 { emit ReleaseFound }`.
- `scheduler.go:598-601` (`notifyError`): `if s.emit == nil || t.ConsecutiveErrors > 0 { return }`.
- The comment at `scheduler.go:427-437` states the trade-off explicitly: "a genuinely
  new release arriving while the topic is still stuck stays silent until the topic
  recovers".

In the no-client case the topic never recovers. So:

- Tick 1: `release.found` (Telegram: "New release detected" + Source link) **and**
  `check.failed` ("Topic check failed: … no client configured …").
- Tick 2..N: `ConsecutiveErrors > 0` → both suppressed. A genuinely new release
  arrives → different hash → enters the branch → `release.found` **suppressed**.

Net effect today: the user gets exactly one release notification and one error
notification, then the topic sits red in `error` status, backs off to 6h, and is
silent forever. The user's premise is wrong about *why* (the event is not tied to the
submit) but right about the *outcome*.

### 1d. What exists, what is missing

Exists:
- Event type `release.found` with `SourceURL` (`event.go:40`, `bus.go:80-83`).
- Notifier fan-out incl. per-topic override and default notifiers
  (`backend/internal/notify/dispatcher.go:57-84`).
- Legacy subscription alias covering `release.found` (`dispatcher.go:117-120`).
- Persisted history + SSE for the event (`event.go:40`).
- Client is already optional in the form and API (`frontend/src/components/topics/TopicForm.tsx:371,378`;
  `backend/internal/api/handlers/topics.go:109` is `*uuid.UUID`, never required).

Missing:
- A per-topic flag.
- A scheduler branch that skips `downloadAllPending`/`submitToClient` entirely and
  persists the **new** hash.
- An emit that does not sit behind the `ConsecutiveErrors == 0` gate (see 5).
- For per-episode trackers: marking seen episodes (see 3).
- UI: toggle, badge, hidden download fields.

---

## 2. Where the state lives

### 2a. Single-release topics

- The only "seen" state is `topics.last_hash` (`domain.Topic.LastHash`,
  `backend/internal/domain/domain.go:94`).
- Written by `RecordCheckResult` (`repo/topics.go:222-246`):
  `last_hash = CASE WHEN $2 = '' THEN last_hash ELSE $2 END` (`:231`), guarded by
  `WHERE id = $1 AND last_checked_at IS NOT DISTINCT FROM $7 AND next_check_at = $8`
  (`:238`) → `ErrStaleCheckResult` on zero rows (`:243-245`).
- Success path persists the **new** hash: `scheduler.go:519`
  `s.recordResult(ctx, log, t, check.Hash, updated || anySubmitted, nextCheckAt, "", nil)`.
- Failed-download path persists the **old** hash on purpose: `scheduler.go:462-476`,
  so the change is re-detected and the download retried.

**Notify-only must differ:** there is no download to retry, so it must take the
success-path shape — persist `check.Hash` with `updated=true` (which also stamps
`last_updated_at`, `repo/topics.go:232`). One tick later `check.Hash == t.LastHash`,
`updated` is false, nothing is re-announced. That is "remember what was already
detected/notified" for free, using the existing column.

### 2b. Per-episode topics (LostFilm only)

- `grep -rn "SupportsEpisodeFilter() bool" backend/internal/plugins/trackers` hits
  only `backend/internal/plugins/trackers/lostfilm/lostfilm.go:173`.
- The watermark is `topic.Extra["downloaded_episodes"]`, documented at
  `lostfilm.go:40-47`.
- `Check` (`lostfilm.go:201-284`): reads `downloaded_episodes` into a set
  (`:241-247`), applies `start_season`/`start_episode` (`:249-258`), skips downloaded
  ids (`:259-262`), fills `check.Extra["pending_episodes"]` and
  `["pending_human"]` (`:270-271`), and builds the hash from **counts**:
  `check.Hash = fmt.Sprintf("eps:%d/done:%d/pending:%d", ...)` (`:266`).
- `MarkEpisodeDownloaded` (`repo/topics.go:341-364`): atomic JSONB append to
  `extra.downloaded_episodes`, same token guard (`:355`).
- The scheduler marks after every successful submit (`scheduler.go:785-797`) and
  trims `pending_episodes`/`pending_human` locally (`:802-810`).
- `ResetCheckState` (`repo/topics.go:394-421`) deletes the key (`:411`).

### 2c. Toggle-back (the user's backlog question)

`Topics.Update` (`repo/topics.go:492-514`) writes only
`display_name, client_id, notifier_id, download_dir, category, extra,
replace_on_update, replace_delete_data, display_name_is_placeholder, updated_at`
(`:501-505`). It never touches `last_hash`. Note it DOES overwrite `extra` wholesale
with the handler's merged map (`handlers/topics.go:297-316` copies `existing.Extra`
first, so `downloaded_episodes` survives — but a Sonarr-driven `Update` at
`backend/internal/sonarr/poller.go:329-331` also passes a merged map; verify
`mergedExtra` carries `downloaded_episodes` — not determined from code in this pass).

- Single-release: after toggle-back, `last_hash` is the last-seen hash, so only the
  **next** change downloads. The code does this naturally; no explicit step.
- Per-episode: depends on whether notify-only marked the seen episodes (section 3).
  If marked → only future episodes download (natural). If not marked → the whole
  backlog since the start floor downloads on the first tick after toggle-back.

---

## 3. Per-episode trackers are NOT the hard part

**Verdict: (a) nearly free. No tracker-side change required.**

`Download` is not what enumerates episodes. `Check` already does:
`lostfilm.go:249-271` produces the full `pending_episodes` (packed ids) and
`pending_human` (e.g. `s01e06`) lists. `Download` (`lostfilm.go:290-321`) only
reads `check.Extra["pending_episodes"]` (`:305-308`) and returns
`ErrNoPendingEpisodes` when it is empty (`:311-315`), else fetches `pending[0]`
(`:319-321`). The scheduler itself reads `pending_human` for labels
(`scheduler.go:700-704`).

### Recommendation for the notify-only branch on an episodic topic

1. Build the notification body from `extra.StringSlice(check.Extra, "pending_human")`.
2. For each packed id in `pending_episodes`, call `s.topics.MarkEpisodeDownloaded(ctx,
   t, packed)` (`repo/topics.go:341-364`). Stop on `repo.ErrStaleCheckResult` exactly
   as `scheduler.go:786-795` does. This is what makes toggle-back safe: LostFilm
   `Check` skips marked ids (`lostfilm.go:259-262`), so switching back downloads only
   episodes that appear afterwards. **If you do not mark, toggle-back downloads the
   entire backlog** — the user's stated fear.
3. Gate the notification on `len(pending_episodes) > 0`, not on hash change alone
   (see the trap below).

### Trap: the count-based hash flips again one tick after marking

- The persisted hash is the **pre-mark** value: `scheduler.go:519` stores
  `check.Hash`, computed at `:409` before any mark.
- Next tick `Check` computes `eps:N/done:D+P/pending:0` ≠ persisted
  `eps:N/done:D/pending:P` → `updated` is true with zero pending.
- In download mode today this already produces a **spurious second `release.found`**
  one tick after every LostFilm download: `scheduler.go:438-446` emits before
  `downloadAllPending` returns `nil` on `ErrNoPendingEpisodes` (`:714-724`). The
  test `TestRunCheck_CaughtUpNoPending` (`scheduler_test.go:1028-1058`) pins the hash
  advance but does not assert emitted events. This is derived from code, not verified
  live.
- Step 3 avoids inheriting that bug in notify-only mode. Fixing it in download mode
  is a separate, optional change (open question 5).

### On the user's "detection should not mark an item as downloaded"

`downloaded_episodes` is a plugin-internal watermark, not user-visible state. The
user-visible "downloaded" record is `topic_deliveries` (written only by
`recordDelivery`, `scheduler.go:897-918`, only after a successful client `Add`,
`:893`), which stays empty in notify-only mode. Reusing the key keeps the plugin
untouched. A separate `seen_episodes` key would require LostFilm `Check` to exclude
both sets — a tracker change with no functional gain (open question 3).

---

## 4. Schema + API + type surface

Precedent: `replace_on_update` / `replace_delete_data`, migration `0014`
(`backend/internal/db/migrations/0014_add_topic_replace_on_update.sql`). Last
migration present is `0015_add_tracker_settings.sql`, so the next is **`0016`**.

| Layer | File:line (precedent) | Change |
|---|---|---|
| Migration | new `backend/internal/db/migrations/0016_add_topic_notify_only.sql` | `ALTER TABLE topics ADD COLUMN notify_only BOOLEAN NOT NULL DEFAULT false;` + `-- +goose Down` drop. Default `false` preserves every existing topic's behaviour. |
| Domain | `backend/internal/domain/domain.go:91-92` (`ReplaceOnUpdate`, `ReplaceDeleteData`) | add `NotifyOnly bool` with a doc comment |
| Repo select | `backend/internal/db/repo/topics.go:38-44` (`topicColumns`), `:56-59` (`scanTopic`) | append `notify_only` and `&t.NotifyOnly` |
| Repo create | `topics.go:84-99` (`Create`, columns `:89`, values `:95`) | add column/value |
| Repo update | `topics.go:492-514` (`Update` signature `:492`, SET `:501-502`) | add `notifyOnly bool` param + `notify_only = $11` |
| Repo untouched | `RecordCheckResult :222-246`, `MarkEpisodeDownloaded :341-364`, `ResetCheckState :394-421`, `DueForCheck :534` | no change; `DueForCheck` selects `active`/`error` regardless of mode |
| Shared create | `backend/internal/topics/create.go:68-73` (`CreateInput.ReplaceOnUpdate`), `:176-177` (build) | add `NotifyOnly bool` and pass to `domain.Topic` |
| Handler create | `backend/internal/api/handlers/topics.go:106-125` (`createTopicReq`, `:118`), `:188-189` (pass into `CreateInput`) | add `NotifyOnly bool \`json:"notify_only,omitempty"\`` |
| Handler update | `handlers/topics.go:242-255` (`updateTopicReq`, `:250` `*bool`), `:326-334` (pointer-preserve), `:336` (`Update` call) | add `NotifyOnly *bool`, same preserve pattern, new arg |
| Handler validation | `handlers/topics.go:834` (`validateOwnership`) | no change: client is already optional; ownership check is nil-safe |
| Sonarr poller | `backend/internal/sonarr/poller.go:329-331` (`p.topics.Update(... existing.ReplaceOnUpdate, existing.ReplaceDeleteData, mergedExtra)`) | pass `existing.NotifyOnly` (compile error otherwise) |
| Scheduler | `scheduler.go:412-520` | new branch (section 6) |
| Frontend type | `frontend/src/lib/api.ts:465-472` (`ClientID`, `NotifierID`, `ReplaceOnUpdate :469`, `Extra :471`, `LastHash :472`) | add `NotifyOnly: boolean`. Response is `domain.Topic` serialised without json tags → PascalCase, automatic once the Go field exists. |
| Form values | `frontend/src/components/topics/TopicForm.tsx:42-56` (`TopicFormValues`, `replaceOnUpdate :54`) | add `notifyOnly: boolean` |
| Form state | `TopicForm.tsx:213-220` (`delivery` state), `:243-252` (`handleSubmit`) | add `notifyOnly` |
| Form UI | `TopicForm.tsx:370-384` (client select, `"Client (optional)"`, `"Use default client"`), `:386-389` (`NotifierSelect`), `:391-411` (download dir + `CategoryField`), `:413-441` (replace-on-update block) | add a checkbox/2-way selector above the client select; when `notifyOnly` hide client, download dir, category and the replace block; keep `NotifierSelect` visible |
| Add card | `frontend/src/components/topics/AddTopicCard.tsx:29-30` (defaults), `:81-82` (`replace_on_update` mapping) | `notifyOnly: false`, `notify_only: v.notifyOnly` |
| Edit card | `frontend/src/components/topics/EditTopicCard.tsx:32-33`, `:54-55` | `notifyOnly: topic.NotifyOnly ?? false`, `notify_only: v.notifyOnly` |
| Badge | new `frontend/src/components/topics/NotifyOnlyBadge.tsx` (+ test), pattern `SonarrBadge.tsx:11-19`; mount in `TopicRow.tsx:99-110` next to `SonarrBadge`/`ClientBadge` | `if (!topic.NotifyOnly) return null;` render `<Badge variant="secondary"><BellRing/> Notify only</Badge>` |
| ClientBadge | `frontend/src/components/topics/ClientBadge.tsx:25-27` resolves default client when `ClientID` is null | early-return `null` when `topic.NotifyOnly`, otherwise it shows "default client" for a topic that will never deliver |
| i18n | `frontend/src/i18n/en.ts:295-296` (`events.release_found`, `events.download_submitted`) | the replace-on-update labels are hardcoded English at `TopicForm.tsx:422-429`, so parity needs no new i18n key; add a key only if the badge text should be localised (en + ru) |
| Docs | `docs/replace-on-update.md` (user guide precedent), `CLAUDE.md` domain/repo/scheduler rows, `CHANGELOG.md [Unreleased]` | `docs/notify-only.md`, CLAUDE.md row, changelog bullet |

---

## 5. Interactions and traps

### `replace_on_update`
Inert, no conflict needed. It is read only inside the download branch:
`scheduler.go:455-457` (snapshot) and `:485-487` (`replacePrevious`). The notify-only
branch returns before both. Hide the block in the form when notify-only is on; do
not reject the combination server-side (a stored `true` is harmless and survives a
toggle-back).

### Topic reset (`POST /topics/{id}/reset`) and `clientremove`
Safe and mostly meaningful:
- `removeDeliveredTorrents` (`handlers/topics.go:758-806`): `ListForTopic` returns
  zero rows → `GroupByClient` empty → `orphaned == 0` → `len(byClient) == 0` →
  returns `0, []` (no warnings).
- `Deliveries.DeleteForTopic` (`:703`) deletes nothing.
- `ResetCheckState` (`:721`; `repo/topics.go:394-421`) nulls `last_hash`, drops
  `downloaded_episodes`, sets `next_check_at = now()`.
- Result: the next tick sees `LastHash == ""` and behaves per the first-check rule
  (open question 1). Under "silent baseline" a reset just re-baselines; under
  "announce" it re-announces the current release.
- Cosmetic: the `topic.reset` event body `"Topic reset — will re-download from
  scratch"` (`handlers/topics.go:733`) is wrong wording for this mode. Defer.

### `topic_deliveries` / `progress` watcher / `GET /topics/{id}/status` / `DeliveryStatus`
Degrades gracefully, nothing broken:
- `Status` handler (`handlers/topics.go:389-440`) → `liveStatus` (`:451-457`) returns
  `(false, empty)` when `len(deliveries) == 0`, so the response is
  `{"client_supports_status": false, "deliveries": []}`.
- `frontend/src/components/topics/DeliveryStatus.tsx:113-114`:
  `if (deliveries.length === 0) return null;`.
- The progress watcher polls only `completed_at IS NULL` rows (`ListInFlight`, per
  CLAUDE.md); none exist → zero cost.

### Client field / toggle-back
- Covered in 2c and 3. Recommendation: keep `client_id`, `download_dir`, `category`
  stored but hidden while notify-only is on, so toggle-back needs no re-entry.
- Behaviour on toggle-back is naturally "only the NEXT change downloads" for
  single-release topics (`Update` never touches `last_hash`, `repo/topics.go:501-505`)
  and "only future episodes" for LostFilm **provided step 3.2 marks seen episodes**.
- Make it explicit in the UI: when the edit form flips notify-only from on to off,
  show one line: "Releases seen while in notify-only mode will not be downloaded.
  Use Reset to fetch the current one." Reset is the existing, documented escape
  hatch (`ResetTopicCard`).

### Notification wording / `download.submitted`
- `notifyUpdated` (`scheduler.go:528-560`) emits `download.submitted` with body
  `"Sent to client"` (`:546`) or `"Sent to client: <labels>"` (`:548`). It is only
  called when `anySubmitted` (`:500-502`), which stays false in notify-only mode, so
  it never fires. Correct.
- Reuse `release.found`. Its body today is fixed `"New release detected"`
  (`scheduler.go:442`). For notify-only set `Body` to e.g. `"Available on tracker —
  not downloaded"` and, for episodic topics, append the `pending_human` list (cap at
  10 like `notifyUpdated :533-539`). `SourceURL: t.URL` is already stamped (`:443`),
  which satisfies "include the original tracker URL". Optionally add
  `Data: {"notify_only": true}` for the timeline. **No new `events.Type`** is
  needed; the user's suggested distinction "release available" vs "download
  submitted" is exactly `release.found` vs `download.submitted`.
- Timeline label already exists: `frontend/src/lib/events.ts:89` maps
  `release.found` → `events.release_found` → `"new release"` (`en.ts:295`).

### Notifier subscription upgrade trap — checked, NOT a trap
- Legacy notifier rows default to `events TEXT[] NOT NULL DEFAULT ARRAY['updated','error']`
  (`backend/internal/db/migrations/0001_initial_schema.sql:128`).
- `legacyAliases` (`backend/internal/notify/dispatcher.go:117-120`):
  `"updated": {"release.found", "download.submitted", "download.completed"}`.
- `subscribed` (`dispatcher.go:128-142`) expands aliases; empty list = all events.
- New notifiers default to `ALL_NOTIFIABLE_EVENTS` (`frontend/src/pages/Notifiers.tsx:229`);
  `EventPicker.expandStored` (`frontend/src/components/notifiers/EventPicker.tsx:10-25`)
  mirrors the alias map.
- So an old-style Telegram notifier receives `release.found`.
- **The real precondition:** `SendVia` with `notifierID == nil` sends to **default**
  notifiers only and sends nothing when none is default
  (`dispatcher.go:57-70`, comment `:52-56` "strict"). A notify-only topic with no
  per-topic notifier and no default notifier is completely silent — and unlike
  download mode, nothing else happens either. The form should warn when
  `notifyOnly && !notifierId && no default notifier exists`.

### AddTopic form validation
None required. Client is optional today (`TopicForm.tsx:371` label
`"Client (optional)"`, `:378` `"Use default client"` = empty string →
`client_id: null`). Backend `createTopicReq.ClientID` is `*uuid.UUID`
(`handlers/topics.go:109`) and only URL is required (`:157-160`).

### The error-gate trap (must-do)
The notify-only emit must **not** use the `t.ConsecutiveErrors == 0` gate
(`scheduler.go:438`). The gate exists because the failed-download path keeps the
old hash and re-enters the branch every tick (`:427-437`). In notify-only mode the
new hash is persisted on the very tick the tracker recovers, so a gated
`release.found` is not "delayed until recovery" — it is **lost**. Emit
unconditionally on `updated` (single-release) or `len(pending) > 0` (episodic).

### First check
`updated` is true when `t.LastHash == ""` (`scheduler.go:412`). In download mode the
first check downloads the current release and emits `release.found`. For notify-only
the maintainer must pick: silent baseline (persist hash, no event) or announce the
current release. See open question 1. The same rule governs the post-reset tick.

### Sonarr-created topics
`poller.go:329-331` updates existing Sonarr-owned topics; it must forward
`existing.NotifyOnly` or the compile fails. Sonarr's own create path
(`topics.BuildAndCreate`) will default the new field to `false` — correct, Sonarr
topics are meant to download.

---

## 6. Effort and shape

### Scheduler branch (the core, ~60-80 LOC)
Insert inside `if updated {` at `scheduler.go:413`, after `fetchAuthorComment`
(`:424`) and before the `release.found` emit (`:438`):

```
if t.NotifyOnly {
    pendingPacked := extra.StringSlice(check.Extra, "pending_episodes")
    pendingHuman  := extra.StringSlice(check.Extra, "pending_human")
    episodic := isEpisodic(tr)                          // scheduler.go:924
    announce := t.LastHash != ""                        // Q1: silent baseline
    if episodic { announce = announce && len(pendingPacked) > 0 }
    if announce && s.emit != nil {
        emit ReleaseFound{Body: "Available on tracker — not downloaded" + labels, SourceURL: t.URL, AuthorComment}
    }
    for _, packed := range pendingPacked {              // episodic only
        if err := s.topics.MarkEpisodeDownloaded(ctx, t, packed); err != nil {
            if errors.Is(err, repo.ErrStaleCheckResult) { break }   // scheduler.go:786-795 pattern
            log.Warn()...; break
        }
    }
    metrics ok; emit CheckCompleted (:510-515 pattern)
    s.recordResult(ctx, log, t, check.Hash, true, s.backoff(t, false, nil), "", nil)   // :519 pattern
    s.recordChecked(true, false)
    return
}
```
No client lookup, no `Download`, no `topic_deliveries`, no `replacePrevious`.
Display-name self-heal (`:491-499`) should still run — hoist it or duplicate the
three lines.

### Size estimate
Backend (7 files, ~250 LOC + ~200 LOC tests):
- `0016` migration (new), `domain.go`, `repo/topics.go` (columns/scan/Create/Update),
  `topics/create.go`, `handlers/topics.go`, `sonarr/poller.go`, `scheduler.go`.
- Tests: `scheduler_test.go` — single-release notify + hash advance; episodic marks +
  body labels; no repeat on equal hash; emit despite `ConsecutiveErrors > 0`; zero
  `clientPlugin.addCalls` and zero `GetDefault` calls; first-check rule; stale-token
  abort on mark. `repo/topics_test.go` pgxmock SQL pins for the new column;
  `integration_test.go` round-trip; `handlers/topics_test.go` create/update
  round-trip of `notify_only`.

Frontend (7 files, ~200 LOC):
- `api.ts`, `TopicForm.tsx` (toggle + conditional hiding + toggle-back notice +
  no-notifier warning), `AddTopicCard.tsx`, `EditTopicCard.tsx`, `TopicRow.tsx`,
  `ClientBadge.tsx`, new `NotifyOnlyBadge.tsx` + `.test.tsx`; extend
  `TopicRow.test.tsx`/`AddTopicCard.test.tsx`.
- `TopicForm.tsx` is already over the 250-line limit (tracked tech debt per
  CLAUDE.md); adding ~30 lines there is acceptable but worth noting in the PR.

Docs: `docs/notify-only.md` (mirror `docs/replace-on-update.md`), CLAUDE.md
(domain, repo, scheduler rows; migration list), `CHANGELOG.md [Unreleased]` — a
`feat:` PR title will cut a minor release via auto-release.

### Smallest coherent v1
One bool column `notify_only`; scheduler branch as above (with episode marking);
form toggle that hides download fields; "Notify only" badge; `ClientBadge` suppressed;
toggle-back notice; docs. This satisfies every bullet in the issue's "Expected
behavior" list.

### Defer
- A per-release "Download now" action for a seen release (the user said they will
  do it by hand; needs a new endpoint and a UI surface).
- Reset event body wording (`handlers/topics.go:733`).
- The pre-existing spurious second `release.found` on LostFilm catch-up ticks in
  download mode (section 3 trap).
- Localising the badge/notice text (form labels for the sibling feature are
  hardcoded English today).

---

## 7. Open questions for the maintainer (max 5)

1. **First check / post-reset tick in notify-only mode:** silent baseline (persist the
   hash, no message) or announce the current release? Recommendation: silent — the
   user just looked at the page, and it keeps "adding must not trigger" literal.
2. **Column shape:** bool `notify_only` (mirrors `0014`, minimal surface) or an enum
   `mode` (`download` / `notify`) for future modes? Recommendation: bool.
3. **Episode watermark:** reuse `extra.downloaded_episodes` (zero plugin change,
   toggle-back safe) or add a separate `seen_episodes` key (LostFilm `Check` must
   learn to exclude it)? Recommendation: reuse.
4. **On enabling notify-only:** keep `client_id` / `download_dir` / `category` stored
   but hidden, or clear them to null/empty? Recommendation: keep — toggle-back needs
   no re-entry and `ClientBadge` is suppressed anyway.
5. **Scope of the LostFilm double-`release.found` fix:** same PR (gate the download
   path's emit on `len(pending) > 0` too) or separate issue? Recommendation:
   separate — it changes download-mode notification behaviour for existing users.
