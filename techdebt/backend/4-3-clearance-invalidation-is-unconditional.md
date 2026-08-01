# Clearance invalidation is unconditional and can discard a fresh cookie

| Field | Value |
|-------|-------|
| Criticality | Low |
| Complexity | Medium |
| Location | `backend/internal/flaresolverr/minter.go` (`InvalidateClearance`), `backend/internal/plugins/trackers/rutracker/rutracker.go` (`retryOnChallenge`) |
| Found during | Code review of PR #138 (RuTracker clearance replay), code-reviewer agent |
| Date | 2026-08-02 |

## Issue

`retryOnChallenge` invalidates by probe URL with no notion of *which* clearance
was rejected, and `InvalidateClearance` clears whatever is cached regardless of
whether it is the one that failed.

Worker A gets a 403 on clearance `C0`, invalidates, and mints `C1` (10-55s).
Worker B was already in flight with `C0`; its 403 arrives after `C1` has
landed, and it wipes the good `C1` and mints `C2`. FlareSolverr serialises
solves, so every surplus mint is 10-55s of blocked solver for every other
tracker.

It is self-limiting — it stops once all in-flight `C0` requests drain — but
with 8 scheduler workers plus the search fan-out it can multiply cold solves
several-fold. `marauder_flaresolverr_clearance_total`'s own help text names a
sustained `minted` rate as the alarm signal, so this can trip the alarm it
documents.

## Why this is tracked, not fixed in PR #138

The fix is a compare-and-clear: stamp an opaque generation on the clearance in
`mint`, return it through `applyClearance`, and have
`InvalidateClearance(probeURL, version)` clear only when the cached version
matches. That changes the `registry.ClearanceProvider` interface, which PR #138
had already settled. Deferred because the behaviour is correct — only wasteful
— and the boot warm-up plus the year-long cookie lifetime make repeated
invalidation rare in practice.

## Fix sketch

```go
type Clearance struct {
    Cookies   map[string]string
    UserAgent string
    Version   uint64 // 0 = unversioned; callers without one invalidate unconditionally
}

func (t *Transport) InvalidateClearance(probeURL string, version uint64)
```

## Impact if left

Under concurrent 403s, several redundant Cloudflare solves where one would do.
Each blocks the shared solver for its duration, slowing unrelated trackers.
