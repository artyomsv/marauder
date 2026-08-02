# Topic recheck: retry a failing topic without destroying its state

**Date:** 2026-08-02
**Status:** approved design

## Problem

A topic that fails a check backs off exponentially, capped at six hours
(`MARAUDER_CHECK_MAX_BACKOFF`). That is the right behaviour for a tracker that
is down — it stops Marauder hammering it — but it is the wrong behaviour once
the operator has *fixed* the cause.

The concrete case, observed on 2026-08-01: RuTracker's origin was serving 520s,
several checks failed, and the topic landed on a six-hour backoff. The
credential was then repaired and the credential **Test** button confirmed login
worked — but the topic kept showing

> Login failed or the session expired — check this tracker's credentials

for another three hours, because nothing re-ran the check. The stale error was
also the *pre-fix* wording, so it actively contradicted the code that was
running.

There is no way to say "try again now". The only actions available are:

| Action | Why it doesn't fit |
|---|---|
| Wait | Up to six hours, with a misleading error on screen the whole time |
| **Reset** | Removes the already-delivered torrents from the client (optionally deleting their files) and clears delivery history. Enormously destructive for "just retry the check" |
| Pause + Resume | Does not touch `next_check_at`, so the backoff survives |
| Edit + Save | Does not touch `next_check_at` either |

Diagnosing this required a hand-written `UPDATE topics SET next_check_at =
now()`. A timestamp nudge should not require database access.

## Goal

One action — "Check now" — that queues a topic for its next scheduler tick,
changing nothing else.

Non-goals: checking paused topics, cancelling an in-flight check, any change to
how backoff itself is computed.

## Design

### Repository

```go
// QueueRecheck brings a topic's next check forward to now, so the scheduler
// picks it up on its next tick.
//
// Deliberately narrow: it writes next_check_at and nothing else. status and
// last_error are the scheduler's to clear, and it does so on the next
// successful check — leaving the old error visible until then is honest, since
// it IS still the last thing that happened.
//
// last_checked_at is untouched for a second reason: see "Interaction with the
// check-state token" below.
//
// Ownership is enforced by the statement rather than by caller discipline,
// matching ResetCheckState. Paused topics are excluded because DueForCheck
// ignores them, so writing next_check_at would silently do nothing.
func (r *Topics) QueueRecheck(ctx context.Context, id, userID uuid.UUID) (bool, error)
```

```sql
UPDATE topics
SET    next_check_at = now()
WHERE  id = $1 AND user_id = $2 AND status <> 'paused'
```

The `bool` is "a row was updated". False means *either* not-found/not-yours
*or* paused; the handler distinguishes them.

### Handler

`POST /api/v1/topics/{id}/recheck`, shaped like the existing `setStatus`
helper — parse the id, call the repo, return `204 No Content`.

The zero-row path costs one extra `Get` to produce an honest status code:

| Case | Response |
|---|---|
| Updated | `204 No Content` |
| Topic is paused | `409 Conflict` — "topic is paused; resume it first" |
| Not found / not the caller's | `404 Not Found` |

A `404` for a paused topic would be a lie, and a `204` would be worse: the UI
would report success for something the scheduler then ignores. The extra `Get`
runs only on the rare failing path.

No audit entry and no `topic_events` row.

Worth being explicit, because the neighbouring handlers differ: `Reset` writes
an audit entry (`topic_reset`) while `Pause` and `Resume` write none. Reset
audits because it destroys state — it removes torrents from a client and can
delete files, so "who did that, and when" is a question someone will eventually
ask. Recheck destroys nothing; it moves a timestamp the scheduler rewrites on
every tick anyway. It belongs with pause/resume.

The check that follows emits `check.started` / `check.completed` /
`check.failed` through the normal bus, which is a truer record of what happened
than "a user pressed a button".

### Frontend

- `TopicRow` gains a `RefreshCw` action beside the existing `RotateCcw` reset,
  **hidden when `status === "paused"`** — per the design decision above, a
  control that cannot work should not be shown.
- `BulkActionBar` gains a "Check now" entry, fanning out one request per
  selected topic through `mapWithConcurrency(topics, RECHECK_CONCURRENCY, …)` —
  the same bounded helper bulk reset uses (`RESET_CONCURRENCY`, 4), for the same
  reason: an unbounded `Promise.all` over a large selection points the whole
  burst at the backend at once. A named constant rather than a literal, matching
  the reset precedent.
- Both invalidate `QK.topics` on success.
- No confirmation dialog. The action is cheap and reversible by doing nothing;
  `DeleteConfirm`-style arming would be friction without a payoff.
- No bespoke progress UI. `TopicCheckStatus` already renders a "Checking…"
  chip driven by `check.*` SSE events, so the feedback path exists.
- i18n: new keys in both `en` and `ru` dictionaries.

### Interaction with the check-state token

`next_check_at` is half of the optimistic-concurrency token — `(last_checked_at,
next_check_at)` — that `RecordCheckResult`, `MarkEpisodeDownloaded` and
`VerifyCheckState` guard on, and which exists so a Reset landing mid-check is
not silently undone.

Writing `next_check_at` therefore invalidates the token for any check already in
flight, exactly as `ResetCheckState` does. That worker's result is dropped with
`repo.ErrStaleCheckResult`, the scheduler logs it at Info, and the queued check
runs fresh.

This is accepted rather than engineered around:

- It is the same window `Reset` already documents and accepts.
- It is bounded — one check cycle.
- It is self-correcting. The dropped write means `last_hash` keeps its old
  value, so the fresh check may re-detect the same release and re-submit it;
  `Deliveries.Record` is idempotent on `(topic_id, infohash)` and torrent
  clients reject a duplicate infohash, so the outcome is a redundant add, not a
  duplicate download.
- Closing it properly needs claim-on-dispatch, which is the same open item the
  Reset design already records.

`QueueRecheck` not writing `last_checked_at` matters here: leaving it alone
keeps the disruption to the token minimal and keeps "when was this last
checked?" truthful, since the recheck has not checked anything yet.

## Testing

Repository (integration, `//go:build integration` — the existing harness, since
the guard is in SQL and pgxmock only pins query text):

- updates `next_check_at` to approximately now for an `active` topic
- updates it for an `error` topic — the whole point of the feature
- leaves a `paused` topic untouched and reports false
- another user's topic is untouched and reports false
- `status`, `last_error`, `last_error_code` and `last_checked_at` are unchanged

Handler (unit, fake store):

- `204` on success
- `409` for a paused topic
- `404` for an unknown or unowned topic
- `400` for a malformed id

Frontend (Vitest):

- the row action is absent for a paused topic and present for `active`/`error`
- clicking it POSTs to the right URL and invalidates the topics query
- the bulk action issues one request per selected topic

## Alternatives considered

**A bulk endpoint** taking an id array. Rejected: every other bulk action in
the product (pause, resume, delete, reset) fans out from the client, and one
endpoint breaking that pattern buys nothing measurable at these sizes.

**A "check once" flag the scheduler consumes**, which would let paused topics
be checked without resuming. Rejected with the paused-topic decision — it adds
scheduler state to serve a case we decided not to support.

**Clearing `status` and `last_error` on recheck**, so the UI goes clean
immediately. Rejected: it would claim the topic is healthy before anything has
verified that. The error is still the last thing that happened until the next
check says otherwise.
