# Interactive login cannot see a Cloudflare interstitial

| Field | Value |
|-------|-------|
| Criticality | Medium |
| Complexity | Medium |
| Location | `backend/internal/plugins/captchalogin/engine.go` (`post`), `backend/internal/plugins/trackers/rutracker/interactive.go` (`classifyLogin`) |
| Found during | Code review of PR #138 (RuTracker clearance replay), code-reviewer agent |
| Date | 2026-08-02 |

## Issue

`captchalogin.Engine.post` discards the `*http.Response` and returns only the
body, so nothing on the interactive-login path can tell a Cloudflare 403
interstitial from a real login response. The interstitial contains neither
`id="logged-in-username"` nor `cap_sid`, so `classifyLogin` returns
`OutcomeFailed` and `Begin` surfaces `"interactive begin: login rejected"`.

Two consequences:

1. **The dead clearance is never invalidated.** `retryOnChallenge` wraps the
   fetch path and `Login`, but not `BeginLogin`/`CompleteLogin`/
   `RefreshChallenge`. The interactive path self-heals only if some other
   caller (a scheduler check) happens to invalidate first.
2. **The user is told their login was rejected** — i.e. "your password is
   wrong" — which is precisely the misdiagnosis the rest of PR #138 works to
   eliminate (`loginOnce`'s pre-marker status guard, the rewritten
   `ErrCloudflareChallenge` doc, `explainLoginFailure`'s Cloudflare case).

Note the pattern: `retryOnChallenge` was extracted *because* the fetch path had
the retry and `Login` did not. The interactive path is the third path that
still does not.

## Why this is tracked, not fixed in PR #138

The fix needs a new `Challenged func(*http.Response) bool` on
`captchalogin.Config` so `post` can return `registry.ErrCloudflareChallenge`,
plus wrapping the three `WithInteractiveLogin` methods in `retryOnChallenge`.
That touches the shared engine and its other consumer (LostFilm), and PR #138
already carries a large surface. The practical exposure is small: the clearance
is warmed at boot and re-minted by every other path, so an interactive login
meeting a dead clearance is a narrow window.

## Fix sketch

```go
// captchalogin.Config
Challenged func(resp *http.Response) bool // RuTracker passes isCloudflareChallenge

// post: after Do, before reading the body
if e.cfg.Challenged != nil && e.cfg.Challenged(resp) {
    return nil, registry.ErrCloudflareChallenge
}
```

Then wrap `BeginLogin`/`CompleteLogin`/`RefreshChallenge` in the plugin's
`retryOnChallenge`, exactly as `Login` and `fetchBytes` already are.

## Impact if left

An interactive login attempted while the cached clearance is dead reports
"login rejected", sending the user to re-check a password that is fine. It
recovers on its own once any other request invalidates the clearance.
