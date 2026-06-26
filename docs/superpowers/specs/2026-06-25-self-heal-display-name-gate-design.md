# Design — Placeholder-only self-heal gate (issue #90)

**Date:** 2026-06-25
**Issue:** [#90](https://github.com/artyomsv/marauder/issues/90) — Sonarr-imported topics show forum main-page title instead of release name (name disappears after first check)
**Parent:** #86 (Sonarr integration), shipped in v1.4.0

## Problem

For topics auto-created via the Sonarr integration, the Topics page initially
shows the correct release title, then after the first scheduler check it reverts
to the forum main-page / category title.

Two independent defects combine to produce this:

1. **Unconditional self-heal (the mechanism, all trackers).** The scheduler's
   display-name self-heal at `backend/internal/scheduler/scheduler.go:404`
   overwrites the stored title with `check.DisplayName` whenever the two differ:

   ```go
   if check.DisplayName != "" && check.DisplayName != t.DisplayName {
       p.UpdateDisplayName(ctx, t.ID, check.DisplayName)
   }
   ```

   There is no notion of "better vs worse" — any differing `Check` title
   permanently clobbers a good add-time title. This is the cross-tracker
   chokepoint; Kinozal is merely the first plugin to trip it.

2. **Kinozal title-host inconsistency (the source, kinozal only).** Kinozal's
   `Check` fetches the title from the **raw** `topic.URL` host
   (`kinozal.go:189`, e.g. `kinozal.guru`), while every other request in the
   plugin (`Login`, `fetchInfohash`, `ResolveMetadata`, `Download`) is rebuilt
   from the hardcoded `p.domain` (`kinozal.tv`). Session cookies are bound to
   `kinozal.tv`, so the title request to a mirror host is effectively
   anonymous and can return a different (main-page) `<title>`. Add-time
   `ResolveMetadata` canonicalizes to `kinozal.tv` (`kinozal.go:254`), so the
   two title sources disagree.

`p.domain` is a fixed constant (`defaultDomain = "kinozal.tv"`), set once in
`init()` and never derived from the topic URL. So a `.guru`/`.me` topic already
routes login/infohash/download to `kinozal.tv`; the title fetch is the lone
exception.

## Goals

- A topic that has a real, resolved title keeps it across repeated scheduler
  checks — for **every** tracker, present and future.
- Preserve the legitimate purpose of self-heal: upgrading a generated
  placeholder (`"Kinozal topic 123"`) to a real title when add-time metadata
  was unavailable.
- Make Kinozal's `Check` title source consistent with the rest of the plugin.

## Non-goals

- Honoring the specific mirror domain (`.guru`/`.me`) a user's topic uses for
  login/infohash/download/metadata. The whole plugin currently pins
  `kinozal.tv`; changing that is a separate, larger concern (own ticket).
- A user-facing setting to choose how the title is displayed (floated in the
  ticket) — out of scope.
- Automatically repairing topics already showing the wrong title — see
  "Existing data".

## Design decisions

| Decision | Choice | Rationale |
|---|---|---|
| Title-change contract | **Placeholder-only heal** | Matches self-heal's actual purpose with least machinery; no quality heuristics. |
| Placeholder detection | **DB provenance flag** | The fact is known precisely once (at creation); store it rather than re-derive fragile per-tracker string patterns on every tick in a tracker-agnostic scheduler. |
| Existing rows | **Lock all existing** (default `false`) | Stops flapping immediately with zero risk to correct titles. Feature is weeks old, so the already-broken blast radius is tiny. |
| Kinozal host fix | **Folded into this spec (§ 7)** | Defense-in-depth at the source layer; closes the bug class, not just the instance. |

## § 1 — Data model

Migration `backend/internal/db/migrations/0010_topic_display_name_placeholder.sql`
(next after `0009_add_sonarr_settings.sql`):

```sql
ALTER TABLE topics
  ADD COLUMN display_name_is_placeholder BOOLEAN NOT NULL DEFAULT false;
```

- `false` = resolved/locked; `true` = generated placeholder, eligible for
  self-heal.
- Existing rows default `false` (**lock all existing**). New rows always get an
  explicit value written by the application.
- `domain.Topic` gains `DisplayNameIsPlaceholder bool`.
- `repo/topics.go`: `topicColumns`, the row scan, and the `INSERT` column list
  + values all include `display_name_is_placeholder`.

## § 2 — Creation (`topics/create.go`)

The flag is computed once, from the title's source:

| Source of the final stored name | Flag |
|---|---|
| Caller/user supplied (`in.DisplayName != ""`) | `false` |
| `ResolveMetadata` returned a non-empty `meta.Title` | `false` |
| Fell back to `Parse`'s generated name | `true` |

Implementation: a `resolved` bool initialised to `in.DisplayName != ""`. The
existing metadata helper (which sets `*displayName = meta.Title`) also flips
`resolved = true` when it assigns a non-empty title. Persist
`topic.DisplayNameIsPlaceholder = !resolved`.

This keeps the fail-open metadata behaviour unchanged: a metadata failure
leaves `resolved = false` → placeholder `true` → still eligible for self-heal,
exactly as today.

## § 3 — Scheduler gate (`scheduler.go:404`)

One added condition:

```go
if check.DisplayName != "" && check.DisplayName != t.DisplayName && t.DisplayNameIsPlaceholder {
    if p, ok := s.topics.(displayNamePersister); ok {
        if err := p.UpdateDisplayName(ctx, t.ID, check.DisplayName); err != nil {
            log.Warn().Err(err).Msg("UpdateDisplayName failed")
        }
    }
}
```

Self-heal fires only while the stored name is a placeholder; the first heal
locks it (see § 4). Best-effort/fail-open semantics are unchanged.

## § 4 — Repository (`repo/topics.go`)

`UpdateDisplayName` resolves the name and locks the flag atomically in one
statement:

```sql
UPDATE topics
   SET display_name = $2,
       display_name_is_placeholder = false,
       updated_at = now()
 WHERE id = $1
```

`Update` (user rename via `PUT /topics/{id}`) locks **only on an actual name
change**, so editing an unrelated field (category, client) on a
still-placeholder topic does not accidentally freeze the placeholder:

```sql
... display_name_is_placeholder = CASE WHEN display_name <> $3
        THEN false ELSE display_name_is_placeholder END ...
```

### Sources that set the flag to `false` (resolved/locked)

1. Caller/user-supplied name at create (§ 2).
2. Add-time `ResolveMetadata` title (§ 2).
3. First scheduler self-heal (§ 3 → § 4).
4. A genuine user rename (§ 4).

Only `Parse`'s generated name leaves the flag `true`.

## § 5 — Kinozal `Check` title-host canonicalization

Extract a small helper and reuse it where the id → canonical-URL logic is
currently duplicated (`ResolveMetadata`, `fetchInfohash`):

```go
// canonicalDetailsURL rebuilds the details URL from the trusted host
// (p.domain) + the numeric id parsed from rawURL — never the raw user URL.
// Avoids request forgery (CodeQL go/request-forgery) and pins the request to
// p.domain so Check's title matches ResolveMetadata's.
func (p *plugin) canonicalDetailsURL(rawURL string) (string, error) { ... }
```

`Check` fetches `canonicalDetailsURL(topic.URL)` for the title instead of the
raw `topic.URL` (`kinozal.go:189`). Effects:

- Add-time and check-time titles come from the same host → consistent.
- Closes the minor `go/request-forgery` gap on the current line 189 (the other
  call sites were written specifically to avoid it).

Scope guard: this makes `Check` internally consistent with the plugin only; it
does not honor the `.guru`/`.me` mirror (non-goal above).

## Error handling

- Self-heal remains best-effort: an `UpdateDisplayName` failure logs a Warn and
  never affects the check result (unchanged).
- Add-time metadata remains fail-open: failure → placeholder `true`, topic
  created with the `Parse` name (unchanged).
- Kinozal `canonicalDetailsURL` returns an error for a non-parseable URL;
  `Check` propagates it exactly as the current title path would on a fetch
  failure (the infohash is the load-bearing field and already gates success).

## Testing

| Area | Test | Assertion |
|---|---|---|
| Creation | `topics/create_test.go` | Flag `true` when only `Parse` name; `false` for caller-supplied and for resolved metadata. |
| Scheduler (regression) | `scheduler/scheduler_test.go` | `is_placeholder=false` + differing `Check.DisplayName` ⇒ `UpdateDisplayName` **not** called. `is_placeholder=true` ⇒ called once. |
| Repo — heal | `repo` / fake | `UpdateDisplayName` sets name and clears the flag. |
| Repo — rename | `topics_handler_test.go` | User `Update` flips flag to `false` only when the name differs from stored. |
| Kinozal | `kinozal/kinozal_test.go` | With `p.domain` = test host and a `kinozal.guru` topic URL, `Check` hits the canonical host (ignores the raw mirror host). |
| Migration | apply check | Column added; pre-existing rows read back `false`. |

## Existing data

After migration, all current topics are locked (`false`). Topics already
showing the wrong title are **frozen** at that value and are repaired by a
manual rename (or delete + re-add). No automatic backfill — acceptable because
the Sonarr feature shipped only in v1.4.0, so few topics are affected.

## Files touched

- `backend/internal/db/migrations/0010_topic_display_name_placeholder.sql` (new)
- `backend/internal/domain` — `Topic.DisplayNameIsPlaceholder`
- `backend/internal/db/repo/topics.go` — columns, scan, insert, `UpdateDisplayName`, `Update`
- `backend/internal/topics/create.go` — compute + persist the flag
- `backend/internal/scheduler/scheduler.go` — gate condition
- `backend/internal/plugins/trackers/kinozal/kinozal.go` — `canonicalDetailsURL` + `Check`
- Tests alongside each of the above
- `CLAUDE.md` — note the new column + gate behaviour (per documentation rule)

## Success criterion

A Kinozal topic (including a `.guru` URL) keeps its release-name title across
repeated scheduler checks. Encoded as the scheduler regression test: a topic
with a resolved title (`is_placeholder=false`) whose `Check` returns a
different `DisplayName` is **not** overwritten; a placeholder topic
(`is_placeholder=true`) is upgraded exactly once and then locked.

## Follow-up (separate tickets)

- Honor the actual mirror domain (`.guru`/`.me`) across the kinozal plugin
  rather than pinning `kinozal.tv`.
- Optional per-topic "re-resolve metadata" action to repair already-broken
  topics on demand (brainstorm option C, deferred).
