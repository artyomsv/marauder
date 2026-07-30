# Topic reset & re-download from scratch

Date: 2026-07-30
Status: approved, ready for planning

## Problem

A misconfigured torrent client (wrong volume mapping, corrupted download
directory) leaves every already-delivered torrent broken on disk. After fixing
the client, there is no way to make Marauder re-deliver what it already sent:
the topic's `last_hash` still matches the tracker, so `runCheck` sees no update
and never calls `Download` again. For per-episode trackers the problem is
worse — `extra.downloaded_episodes` permanently excludes every episode already
grabbed.

Today the only recovery is to delete the topic and add it again, which loses
its configuration (client, category, download dir, quality, start
season/episode, notifier override, check interval) and its event history.

## Goal

A **Reset** action, available per topic and for a multi-select of topics, that
discards the topic's check/download state so the next check re-detects the
current release as new and re-delivers it — while preserving the topic's
configuration and identity.

## Behaviour

Reset performs, in order:

1. **Remove the previously delivered torrents from the client(s) they were sent
   to**, optionally deleting their on-disk data (per-action checkbox, default
   off).
2. **Delete all `topic_deliveries` rows** for the topic.
3. **Clear the topic's check state** and queue an immediate re-check.

The state clear is last because it is the step that arms the scheduler. If an
earlier step fails outright, the topic simply has not been reset yet and the
action can be retried; the scheduler is never released against a half-cleaned
topic.

### Fail-open on removal

Removal failures — client unreachable, torrent already gone, plugin without
`registry.WithRemoval` (e.g. `downloadfolder`), client plugin not installed,
config decrypt failure — do **not** abort the reset. State is wiped and the
re-check is queued regardless; each failure is returned as a warning and
rendered in the UI.

Rationale: the client being broken is the very reason the user is resetting. A
fail-closed reset would be unusable in the case it exists to serve.

Consequence, accepted: if the client kept a torrent Marauder has now forgotten,
the re-delivery re-adds the same infohash. Clients treat that as a duplicate and
either error (surfaced as a normal check error) or no-op.

### Paused topics

A paused topic has its state wiped but **stays paused**. `status` is only
normalised `error` → `active`. This lets a user prepare a batch of topics and
release them with Resume once the client is fixed, and prevents a bulk reset
over a mixed selection from silently un-pausing topics.

### What reset does not touch

Configuration and identity survive: `client_id`, `notifier_id`, `category`,
`download_dir`, `check_interval_sec`, `replace_on_update`,
`replace_delete_data`, `display_name`, `image_url`, and the `extra` keys
`quality` / `start_season` / `start_episode` / `source`.

`topic_events` history survives. It is append-only by design (only the retention
pruner deletes from it), and a `topic.reset` marker in the timeline explains the
vanished deliveries better than a hole would.

## Architecture

### Data layer

**`repo.Topics.ResetCheckState(ctx, id, userID uuid.UUID) error`** — new method
beside `RecordCheckResult`, whose write-set it inverts. One statement:

```sql
UPDATE topics SET
    last_hash          = NULL,
    last_checked_at    = NULL,
    last_updated_at    = NULL,
    consecutive_errors = 0,
    last_error         = NULL,
    last_error_code    = '',
    next_check_at      = now(),
    status             = CASE WHEN status = 'paused' THEN 'paused' ELSE 'active' END,
    extra              = extra - 'downloaded_episodes',
    updated_at         = now()
WHERE id = $1 AND user_id = $2
```

Zero rows affected → `ErrNotFound`.

Notes:

- `extra - 'downloaded_episodes'` is a targeted JSONB key delete, so the
  capability keys in `extra` survive. The existing `UpdateExtra` (whole-blob
  overwrite) and `Update` (which deliberately preserves `downloaded_episodes`)
  are both wrong for this.
- No migration is required — every column already exists.
- Clearing `last_hash` is what forces re-detection: `runCheck` computes
  `updated := check.Hash != "" && check.Hash != t.LastHash`.
- Setting `next_check_at = now()` *is* "check now". The scheduler's due query is
  `WHERE status IN ('active','error') AND next_check_at <= now()`; there is no
  manual-trigger API and none is added.

**`repo.Deliveries.DeleteForTopic(ctx, topicID, userID uuid.UUID) (int64, error)`**
— deletes every row for the topic.

This is mandatory, not cosmetic. `topic_deliveries` has a unique index on
`(topic_id, infohash)` and `Record` uses `ON CONFLICT DO NOTHING`; a surviving
row makes the re-delivery record silently vanish, leaving the status endpoint
permanently empty for that torrent.

The DELETE joins `topics` and keys on the owner (`d.topic_id = t.id AND
t.id = $1 AND t.user_id = $2`), so ownership is enforced by the statement rather
than resting on caller discipline. The handler still checks ownership up front
via a user-scoped `Topics.GetByID` so it can return 404 instead of a silent
no-op; the join is the second line of defence.

### `internal/clientremove`

The client-removal logic currently lives only in the scheduler
(`removeFromClient`). The reset handler needs identical behaviour, so it is
extracted into a package both callers share, rather than copied.

```go
type Result struct {
    ClientID   uuid.UUID
    ClientName string
    Hashes     []string
    OK         bool
    Reason     string // "" | "no_plugin" | "unsupported" | "decrypt" | "error"
    Err        error
}

func (r *Remover) Remove(ctx context.Context, userID uuid.UUID,
    byClient map[uuid.UUID][]string, deleteData bool) []Result
```

The package does the client lookup, plugin lookup, `WithRemoval` assertion,
config decrypt, and the bounded (`TrackerHTTPTimeout`) `Remove` call. It does
**not** log or meter — each caller labels its own metric, since the scheduler's
is `marauder_scheduler_replaced_previous_total{client,result}` and reset needs a
distinct one.

Deliveries are grouped by their own `client_id`, not the topic's current client:
a topic reassigned to a different client must still have its old torrents
removed from where they actually went. Rows with a NULL `client_id` are
unaddressable and skipped.

`Scheduler.replacePrevious` keeps its current semantics — it prunes only rows
whose client confirmed removal. Reset ignores `OK` when pruning (fail-open) but
reports every non-OK `Result` as a warning.

### API

`POST /api/v1/topics/{id}/reset`, registered after `/topics/{id}/resume` in the
authenticated route group.

```jsonc
// request
{ "delete_data": false }

// 200 OK
{
  "removed": 3,
  "warnings": ["Transmission (main): connection refused"]
}
```

- `removed` is the number of torrents **confirmed removed from a client**, not
  the number of delivery rows deleted. The two differ whenever a removal fails,
  because rows are deleted regardless (fail-open).
- Returns 200 with a body rather than 204 because the warnings must reach the
  user.
- `delete_data` defaults to **false** when omitted. Deleting on-disk data is
  irreversible, so it is opt-in. It is a per-action choice, deliberately
  independent of the topic's stored `replace_delete_data` policy.
- 404 for a topic the caller does not own (`user_id` is part of the WHERE
  clause).

The handler needs two new methods on its consumer seams: `ResetCheckState` on
`topicStore`, `DeleteForTopic` on `deliveriesStore`, plus a `Remover` field
(nil-safe: a nil remover skips client removal and warns).

**No bulk endpoint.** The router has zero bulk endpoints today; bulk pause,
resume and delete are all frontend fan-outs of N single-id calls. Reset follows
that pattern.

### Events

New `events.Type` `topic.reset` with policy `Persist: true, Notifiable: false,
SSE: true`.

Persisted so the per-topic timeline records the action. Not notifiable — the
user performing the reset does not need to be told they did it — and therefore
must not appear in the notifier `EventPicker` list.

Frontend additions: the type in `lib/events.ts`, a label in the `en` and `ru`
dictionaries, and a routing arm in `applyEvent` that invalidates the topic
queries.

### Frontend

**`components/topics/ResetTopicCard.tsx`** — new, shared by the row action and
the bulk bar. `Topics.tsx` is already 425 lines against the project's 250-line
limit (tracked tech debt), so it does not go inline.

Not a modal: `components/ui/` holds only `button`, `input`, `label`, `card` and
`badge` — there is no shadcn `Dialog` primitive in this project. The confirm
follows the established `AddTopicCard` / `EditTopicCard` pattern instead — an
inline `<Card>` animated into the page by the existing `AnimatePresence` block
above the topic list.

Card contents:

- Title naming the target: the topic's display name, or the count for a bulk
  reset.
- Body explaining what is discarded: delivery records, downloaded-episode
  progress, error state; and that the topic will be re-checked immediately
  (or, when paused, remains paused).
- One checkbox: **"Also delete downloaded files from the client"**, default
  unchecked.
- On confirm, the card switches to a **result view** — `Removed N torrents.
  Queued for re-check.` plus any warnings — with an explicit Close. It does not
  auto-dismiss.

  This frontend has no toast library (no `sonner`, no `useToast`), so the card
  is the only place a warning can be surfaced. For a destructive action this is
  preferable anyway: a client failure cannot scroll past unnoticed.

**`pages/Topics.tsx`**:

- A third ghost icon button (`RotateCcw`, lucide) in the per-row hover action
  group alongside Pencil and `DeleteConfirm`.
- `BulkActionBar` gains an `onReset` prop and a Reset button; `bulk()` gains a
  `"reset"` arm.
- Bulk fan-out reuses the existing `Promise.all` shape; per-topic results are
  aggregated into a single result view (total removed, warnings labelled by
  topic).

**`lib/api.ts`**: `resetTopic(id, deleteData)` returning
`{ removed: number; warnings: string[] }`.

**Cache invalidation on success**: `QK.topics`, `QK.topicStatus(id)`,
`QK.topicEvents(id)`, and `useCheckStatus.getState().clear(id)` — mirroring what
the delete mutation already does.

All new strings added to both the `en` and `ru` dictionaries.

## Concurrency: a reset landing mid-check

A check can run for tens of seconds (10–20s for a Cloudflare-gated tracker going
through FlareSolverr), and reset sets `next_check_at = now()`, so a worker being
in flight while a reset lands is ordinary, not exotic.

Writes that carry a finished check's result back to `topics` are guarded by
optimistic concurrency on the **pair** `(last_checked_at, next_check_at)` — the
version token for "the check state as this worker saw it at dispatch". Exactly
two statements write those columns, `RecordCheckResult` and `ResetCheckState`.
Both columns are needed: `ResetCheckState` sets `last_checked_at` to NULL, so on
its own that column cannot tell two consecutive resets apart, and a worker
dispatched by the first would still match after the second and restore the
pre-reset hash. `next_check_at = now()` at microsecond resolution is what makes
each reset distinct.

`RecordCheckResult` and `MarkEpisodeDownloaded` both carry the token and both
return `repo.ErrStaleCheckResult` when it no longer matches. The scheduler logs
that at Info and drops the write; for the episode mark it also stops draining
the batch, so a reset cannot be followed by further episode submissions.

### Known limitation: the mid-check submission window

**This is narrowed but not fully closed, and deliberately so.**

The reset's removal snapshot (`Deliveries.ListForTopic`, step 1) runs at one
instant. A torrent that a worker hands to the client *after* that snapshot is
taken is not in it, so the reset does not remove it. Concretely:

1. worker calls `tr.Download`, then `clientPlugin.Add` — the torrent is now in
   the client;
2. reset snapshots the delivery rows (this torrent has no row yet), removes what
   it found, deletes those rows, clears the check state;
3. worker inserts the delivery row for the torrent from step 1.

The topic still **converges**: the post-reset check re-detects the release,
re-delivers it (the client dedups on infohash), and the row is already there or
is written idempotently under the `(topic_id, infohash)` unique index. Status
and progress tracking end up correct.

What is **not** guaranteed is `delete_data`. That one torrent's files were never
deleted, and because clients recheck-and-resume against existing files at the
save path, a user who reset specifically to purge corrupt data may still have
it. The torrent is already in the client by the time any database write happens,
so no write-side guard can retract it.

#### What the pre-submit token check does and does not buy

`sendViaClient` calls `Topics.VerifyCheckState` immediately before
`clientPlugin.Add` — the read-only form of the same `(last_checked_at,
next_check_at)` guard the two writes carry. A stale token aborts the tick
before anything reaches the client, returning `repo.ErrStaleCheckResult`, which
`downloadAllPending` treats exactly like the episode-mark abort: stop draining,
keep and report whatever was already delivered, and do not report an error.

What it removes is the **unbounded** case. A worker can sit in `tr.Download` for
up to `MARAUDER_TRACKER_HTTP_TIMEOUT` (30s by default), *per episode*. Before
this check, such a worker submitted no matter how long ago the reset had
finished. Now it aborts.

What it does **not** do is reduce the window to the `Add` round-trip. The reset
writes its check state **last** — `ResetCheckState` is step 3, after the client
removal and the row delete — precisely because that is the step that arms the
scheduler. So the token does not change until the reset is essentially over,
and a worker that reads it at any point *during* the reset sees a valid token
and proceeds. The residual window is therefore the reset's own span, from its
delivery snapshot to its `ResetCheckState` write.

That span is not small. Each per-client `Remove` is bounded by
`TrackerHTTPTimeout` (the same 30s), and it is slowest exactly when the client
is unreachable — which is the most common reason to reset in the first place.
Moving `ResetCheckState` earlier would shrink it, but the ordering is load-
bearing for other reasons (see "step ordering" above) and was not changed here.

Summary: exposure goes from "any submission after the reset's snapshot,
unbounded into the future" to "a submission whose token read falls inside the
reset's own execution". Strictly better, still real.

Note the delivery row itself is **not** the problem and is deliberately left
unguarded. `recordDelivery` runs strictly after a successful `Add`, so the row
accurately describes a torrent that is in the client. `topic_deliveries` is a
mirror of client state, not check-scoped state — it has no "which check" column
— and it is the sole input to the status view, the progress watcher's
`ListInFlight`, the reset's own removal snapshot, and the replace-on-update
snapshot. Rejecting the insert would leave that torrent in the client with no
row, and therefore invisible to *every future removal* — including a later reset
with `delete_data`, and including replace-on-update. That converts a one-time
missed file into a permanently untracked torrent whenever no subsequent check
succeeds (a paused topic, a deleted topic, a tracker that stays down). Guarding
the insert would make this limitation worse, not better.

Fully closing the window still needs a claim-on-dispatch marker (the reset
refusing to proceed, or waiting, while a check holds the topic) or a per-topic
lock spanning submission and reset. Both add a blocking path to an endpoint that
bulk reset calls N times concurrently, and both need schema. Judged out of scope
for this change; the pre-submit check above is the no-migration mitigation.

## Testing

**`repo`** — the existing `topics_test.go` / `deliveries_test.go` use **pgxmock**,
not a live Postgres. They assert the SQL text and the bound arguments, not the
database's behaviour. So the tests pin:

- `ResetCheckState` issues one statement containing each required clause
  (`last_hash = NULL`, the `extra - 'downloaded_episodes'` key delete, the
  paused-preserving `status` CASE, `next_check_at = now()`), bound to
  `(id, userID)`.
- Zero rows affected → `ErrNotFound`; a pool error is wrapped.
- `DeleteForTopic` issues a delete scoped to `topic_id` and returns the row
  count.

The SQL *semantics* — that `-` really drops only that JSONB key, that the CASE
really preserves `paused` — are not covered by these tests. They are verified
once by hand against the dev stack (see the manual acceptance step) rather than
by introducing a Postgres test dependency this repo does not currently have.

**`clientremove`:** table-driven against a fake client plugin — success, plugin
not installed, plugin without `WithRemoval`, decrypt failure, `Remove` error.
Asserts `deleteData` is forwarded and that grouping is by delivery `client_id`.

**`handlers`:** with fake stores —

- `delete_data: true` reaches the remover; omitted field means false.
- A removal failure still wipes state, still deletes delivery rows, and returns
  the failure as a warning.
- Another user's topic returns 404 and mutates nothing.
- The `topic.reset` event is emitted.

**`frontend` (vitest + RTL):**

- Dialog renders the checkbox unchecked and forwards its value.
- Result view renders warnings and does not auto-close.
- Bulk reset issues one call per selected id and clears the selection.

## Success check

```
go build ./... && go vet ./... && go test -race ./...
tsc --noEmit && vitest run
```

Both green, run in the project's containerised toolchains per `CLAUDE.md`.

Manual acceptance: a topic with recorded deliveries, reset with the checkbox
ticked, loses its torrents from the client, shows an empty delivery list, and is
re-delivered on the next scheduler tick.

## Rejected alternatives

**Deferred reset via a `reset_requested` column**, with the scheduler doing the
removal and wipe on its next tick. Avoids touching the handler, but needs a
migration, delays the action, and leaves no request to report warnings on —
failures would only appear in the logs. The user explicitly needs to see which
removals failed.

**Duplicating the removal logic in the handler** instead of extracting
`clientremove`. Fastest to write, but leaves two copies of the decrypt /
capability-check / timeout sequence to drift apart.

**Re-pushing the recorded infohashes without clearing check state.** Cheaper,
but only works while the stored payload is still resolvable and does nothing for
per-episode trackers, whose progress lives in `downloaded_episodes`.

**Deleting `topic_events` history on reset.** Rejected: the table is append-only
by design, and a `topic.reset` marker is more informative than a gap.
