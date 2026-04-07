# Scheduler package has zero unit tests

| Field | Value |
|-------|-------|
| Criticality | High |
| Complexity | Medium |
| Location | `backend/internal/scheduler/scheduler.go` |
| Found during | Post-1.0 LostFilm per-episode rewrite — added a non-trivial multi-download loop with no test coverage |
| Date | 2026-04-07 |

## Issue

`backend/internal/scheduler/` has no `*_test.go` files. The scheduler is
the central nervous system of Marauder — it owns the worker pool, the
exponential backoff curve, the credential decryption path, and (after
the 2026-04-07 LostFilm rewrite) a multi-download per-episode loop with
several non-obvious branches:

- `i == 0` download failure → fatal, record error + backoff
- `i > 0` download failure with `isNoPendingError(err)` → graceful
  break, record success
- `i > 0` mid-loop failure of any other kind → record progress made
  AND record error + backoff
- `markEpisodeDownloaded` race: the topic.Extra map is mutated and
  persisted, then `tr.Check` is called again for the next iteration
- `maxPerTick = 25` runaway guard

None of these are exercised by automated tests today. Every change to
the scheduler has been validated by hand (or by inference from a
green build).

## Risks

- A regression in the per-episode loop could silently drop episodes
  (mark as downloaded, fail to submit, lose state) or re-download
  the same episode forever if the persistence step is broken.
- A regression in the backoff curve could DoS small trackers when
  errors stack.
- A regression in credential decryption could cause every authenticated
  tracker to fail at the same time, with no test signal until users
  report it.
- The "no pending episodes" sentinel is matched via `strings.Contains`
  on the error message — if a plugin author types the phrase
  differently the loop runs to maxPerTick and burns 25 download
  attempts per tick.

## Suggested Solutions

1. **Fake `Tracker` + `Topics` repo + minimal `Scheduler` constructor**.
   The scheduler already takes interfaces (`repo.Topics`, `registry.*`),
   so a fake-driven test does not need a database. Cover at minimum:
   - happy path: hash unchanged → no Download call
   - happy path: hash changed → Download called once → submit → record
   - per-episode loop: 3 pending episodes → 3 Download calls → 3
     submits → loop terminates on `errNoPending` → state persisted
   - mid-loop failure: 2 successful downloads then transport error →
     RecordCheckResult(success=true, err set, backoff applied)
   - maxPerTick: a misbehaving plugin that always returns a payload
     should stop after 25 iterations
2. **Replace the `strings.Contains("no pending episodes")` sentinel
   with a typed error** (`var ErrNoPendingEpisodes = errors.New(...)`)
   exported from `registry/` and matched via `errors.Is`. Removes the
   stringly-typed contract.
3. **Property-based test for the backoff curve** — `s.backoff(t, true)`
   has a closed-form expectation that's easy to verify across N
   consecutive failures.
