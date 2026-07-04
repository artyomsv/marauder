# Tracker HTTP fetches have no per-call retry or circuit breaker

| Field | Value |
|-------|-------|
| Criticality | Low |
| Complexity | Medium |
| Location | all tracker plugins (`backend/internal/plugins/trackers/*`) — every `Check`/`Download`/`ResolveMetadata`/`AuthorComment` HTTP fetch |
| Found during | Code review of issue #110 (author comment in notifications), rules-compliance agent |
| Date | 2026-07-04 |

## Issue

`~/.claude/rules/resilience-patterns.md` mandates timeout + retry policy +
circuit breaker + bulkhead on every outbound call. Tracker plugin HTTP
fetches have explicit timeouts (context-bounded by the scheduler's
`TrackerHTTPTimeout`) and body-size caps (`io.LimitReader`), but no
per-call retry-with-backoff on transient failures and no circuit breaker
around a repeatedly-failing tracker host.

This is a **codebase-wide pattern**, not specific to #110: none of the 16
tracker plugins retry at the HTTP layer. The scheduler's per-topic
exponential backoff (`next_check_at`, capped 6h) is the de facto
resilience layer — a failed check retries on the next scheduled tick
rather than immediately.

## Why this is tracked, not fixed in the #110 change-set

Adding retry/circuit-breaker to just the two new `AuthorComment` fetches
would make the two newest calls inconsistent with the other ~50 fetch
sites while leaving the actual hot paths (Check/Download) unprotected.
Per `reference-pattern-adoption.md`, the right unit of work is a shared
HTTP-client wrapper (retry-on-connect-error/5xx with jitter, per-host
breaker) adopted by all plugins at once — likely in `forumcommon` /
a shared `trackerhttp` package.

## Mitigating factors

- Every fetch is context-timeout-bounded; nothing can hang a worker
  indefinitely (I/O-wise).
- The scheduler's tick-level exponential backoff already spaces retries
  per topic, and `AuthorComment` is fail-open (a transient failure costs
  one notification excerpt, never a check).
- Tracker hosts are user-configured third parties; a breaker would mainly
  save wasted egress, not protect Marauder's own availability.

## Suggested fix

Introduce a shared tracker HTTP helper with: bounded retry (max 3,
exponential + jitter, retry on connect error / 5xx / 429 only, GETs
only), a simple per-host circuit breaker, and the existing
LimitReader/timeout discipline. Migrate plugins package-by-package.
