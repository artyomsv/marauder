# Flaky test: TestRoundTrip_DestroysTheSessionItAbandons

| Field | Value |
|-------|-------|
| Criticality | Medium |
| Complexity | Trivial |
| Location | `backend/internal/flaresolverr/client_test.go:561`, `backend/internal/flaresolverr/client.go:274` |
| Found during | CI on PR #226 (issue #225); the branch does not touch this package |
| Date | 2026-10-24 |

## Issue

`Transport` destroys an abandoned FlareSolverr session in a background
goroutine (`client.go:274`, `go func() { ... "sessions.destroy" ... }()`), but
the test reads `f.cmdCount("sessions.destroy")` right after `RoundTrip`
returns, without waiting for that goroutine. When the goroutine has not run
yet, the count is 0 and the test fails:

```
client_test.go:561: sessions.destroy calls = 0, want 1 — the abandoned session leaks a browser otherwise
```

Seen on CI run for commit 26c53dc (PR #226, 2026-10-24) under `-race`; the
rerun of the same commit passed.

## Risks

A red `Backend` check on unrelated pull requests, and reruns that teach
everyone to ignore a red backend job.

## Suggested Solutions

1. In the test, poll `f.cmdCount("sessions.destroy")` until it is 1 or a short
   deadline passes (the repo already has `waitFor`-style helpers in other
   tests).
2. Or give the fake solver a channel that the `sessions.destroy` handler
   signals, and wait on it with a timeout.
