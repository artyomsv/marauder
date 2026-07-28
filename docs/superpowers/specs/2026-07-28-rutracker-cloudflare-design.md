# RuTracker Cloudflare: honest classification + a working solver path

**Date:** 2026-07-28
**Status:** approved design, not yet implemented

## Problem

Two RuTracker topics sit in `status=error` with:

```
last_error:      auth failed: rutracker login failed: invalid credentials
                 (no logged-in marker in response)
last_error_code: auth
```

The credentials are not the problem. RuTracker now serves a Cloudflare
interstitial to every plain HTTP client:

| Probe from `deploy-backend-1` (no credentials used) | Result |
|---|---|
| `rutracker.org/forum/login.php` | 403, `Cf-Mitigated: challenge` |
| `rutracker.org/forum/viewtopic.php?t=6854635` | 403, `Cf-Mitigated: challenge` |
| `rutracker.net` | 403, `Cf-Mitigated: challenge` |
| `rutracker.nl` | resolves, no HTTPS response |
| `rutracker.cr` | NXDOMAIN — dead mirror |
| `nnmclub.to` (contrast) | 200 OK |

`rutracker.go:159` tests for `id="logged-in-username"` and, when absent,
returns `invalid credentials`. That positive-indicator test can only answer
"am I authenticated?", never "why not?" — so a network-layer block is
reported as an auth failure. The stored credential (`Nossp`) was never
actually tested.

The anonymous-download fallback does not help: the block is at the HTTP
layer, so `Check` cannot fetch the topic page with or without an account.

Domain rotation (#126) does not help either: `classifyError` rotates only on
`timeout`/`unreachable`, and a 403 is neither — and every live mirror is
challenged anyway.

### The Cloudflare path does not exist

`internal/cfsolver.Client.Solve` is referenced by nothing outside its own
package. `WithCloudflare` is consumed in exactly one place —
`handlers/trackers.go:86`, which surfaces a `uses_cloudflare` JSON flag. The
nnmclub doc comment claiming "the scheduler will route HTTP failures through
the cfsolver sidecar and re-try" describes code that was never written.

### The solver is not trustworthy

Built and tested the sidecar against live targets:

- **`main.go:192` sets `OK: true` unconditionally** after navigation. There
  is no check that the challenge cleared. The solver cannot report failure.
- **Wait strategy is `chromedp.Sleep(8*time.Second)`** (`main.go:177`) — a
  fixed sleep, not a condition.
- **UA reports `HeadlessChrome/150.0.0.0`**, which Cloudflare fingerprints.
- Observed zero cookies for both rutracker.org and nnmclub.to. Extraction may
  be fine and there may genuinely have been none (a challenge page withholds
  `cf_clearance`); this is **not** established as a defect, unlike the three
  above.

Wiring this in as-is would be worse than doing nothing: the plugin would
"solve", inject an empty cookie set, retry, get the same 403, and report the
same misleading error — at double the request rate against a site already
blocking us.

## Browser investigation (2026-07-28) — what is actually happening

Run from Chrome on the same residential network, same minute as the
container probes above:

| Client | Cookies | Result |
|---|---|---|
| Chrome | its own | **200**, no challenge, `#logged-in-username` = `Nossp` |
| Chrome, same tab | `credentials:'omit'`, `cache:'no-store'` | **403** `Cf-Mitigated: challenge` |
| container `wget`, Chrome UA | none | **403** `Cf-Mitigated: challenge` |
| container `wget`, Chrome UA, 2nd request replaying saved cookies | none issued | **403** — the 403 sets **no cookies at all** |

This rules out, with evidence, every cheap explanation:

- **Not the credentials.** The stored account is live and logged in.
- **Not the IP.** The same residential IP passes in Chrome.
- **Not the User-Agent.** A Chrome UA from the container still 403s.
- **Not the TLS/JA3 fingerprint.** The *same* Chrome, with cookies omitted,
  is challenged identically — so the differentiator cannot be the handshake.
- **Not retryable.** The challenge response issues no cookie, so a client
  that cannot execute the challenge never earns one, no matter how often it
  retries.

**Therefore:** RuTracker fronts requests with a Cloudflare **JS challenge**.
A browser executes it, earns a clearance cookie, and passes thereafter. Go's
HTTP client cannot execute JS, is never issued a cookie, and is challenged
forever.

This is exactly the problem the cfsolver was built for. The architecture was
right; the implementation never worked. Note also that the relevant cookies
(`__cf_bm`, `cf_clearance`) are **HttpOnly** — invisible to `document.cookie`
but readable via CDP `network.GetCookies()`, which is why the solver's
zero-cookie result is a genuine extraction bug rather than "nothing to get."

## Milestone 0 — prove portability before building anything

**One unproven link remains:** whether a clearance cookie minted by the
browser is accepted from a *different* client. Cloudflare binds
`cf_clearance` to IP and User-Agent; the solver shares the host's IP and can
match the UA, so this should hold — but it is not yet demonstrated.

Do this first, before sections B–F:

1. Fix the solver's cookie extraction (section A) only far enough to return
   cookies.
2. Solve `rutracker.org`, take the returned cookies, and replay a plain
   `wget`/Go request with them.
3. **200 → portability proven, build the rest. Still 403 → stop.** The
   cookie is fingerprint-bound and sections C/D are dead; ship only the
   honest-error work (E + the `.cr` prune) and close the path.

This costs a fraction of the full build and removes the project's only
remaining unknown.

### Milestone 0 RESULT (2026-07-28): NEGATIVE — cookie injection is dead

Executed. The solver, once repaired, **does** clear RuTracker's challenge and
returns a real clearance cookie:

```
cookies:    cf_clearance (661 chars), cf_chl_rc_ni
title:      "Just a moment..."   html_bytes: 27136
```

Replaying that cookie from a plain HTTP client on the **same host, same IP,
with the exact User-Agent the solver reported**:

| Replay attempt | Result |
|---|---|
| `cf_clearance` only | **403** `Cf-Mitigated: challenge` |
| both cookies + full browser headers (`Accept-Language`, `Sec-Fetch-*`, `Upgrade-Insecure-Requests`, …) | **403** `Cf-Mitigated: challenge` |

**Cloudflare binds the clearance to the TLS/HTTP2 fingerprint of the client
that earned it.** A cookie minted by Chrome is worthless to Go's HTTP client.

Per the stop condition above, **sections C and D as written are dead.** No
amount of cookie plumbing into `forumcommon`'s jar can work.

### The architecture that the evidence does support

The full evidence set is:

| Client | Cookies | Result |
|---|---|---|
| Chrome | its own | **200** |
| Chrome | omitted | 403 |
| plain HTTP client | none | 403 |
| plain HTTP client | Chrome's valid `cf_clearance` | 403 |

Only *Chrome's fingerprint **plus** the cookie* succeeds. So the request must
be **made by the browser**, not merely authorised by a cookie it earned.

**Revised design — browser-as-fetcher.** cfsolver grows a `/fetch` endpoint
that performs the request inside the browser and returns status + body. Once
a domain is cleared, an in-page `fetch()` inherits both the browser's TLS
fingerprint and its cookie jar, which covers all three things RuTracker needs:

- **Check** — GET the topic page, return HTML
- **Login** — POST the login form from in-page
- **Download** — fetch the `.torrent` bytes, return base64

This replaces sections C and D. Section A (solver correctness) and section E
(honest error classification) survive unchanged and are still required.

**Cost:** every RuTracker request goes through Chrome. For a poller running
on a 15-minute-to-6-hour cadence this is acceptable; for anything hot it
would not be.

## Non-goal

**Guaranteeing RuTracker access.** The mechanism is now understood and the
odds look good, but Cloudflare tuning is adversarial and headless detection
may still bite. The design's job is to attempt *once*, cache the outcome, and
fail **honestly and cheaply**. If Milestone 0 fails, sections E and the `.cr`
prune still pay for themselves by replacing a lie with an accurate error.

## Design

### A. `cfsolver/main.go` — make success mean something

Replace the fixed sleep and unconditional `OK` with an explicit outcome:

1. Navigate, then **poll** at ~1s intervals until `timeout_seconds`.
2. Classify the landing page each tick:
   - **Not challenged** → return immediately, `OK: true`, with whatever
     cookies exist. (Preserves today's behaviour for unchallenged sites.)
   - **Challenged** → keep polling until `cf_clearance` is present, then
     return `OK: true`.
3. **Timeout while still challenged** → `OK: false` with a real error. This
   is the crux of the section: the solver must be able to say "I failed."
4. Set a stock Chrome UA (no `Headless` token) and add
   `--disable-blink-features=AutomationControlled`.

**Testability.** chromedp cannot be meaningfully unit-tested, so the
*decisions* move into pure functions with table tests:

- `isChallengePage(html string) bool`
- `hasClearance(cookies []*network.Cookie) bool`
- `outcome(challenged, cleared bool, timedOut bool) (ok bool, err error)`

Note the two detectors are deliberately different and must not be unified:
inside the browser there is no access to the original response headers, so
`isChallengePage` inspects the rendered DOM, while section C's
`IsCloudflareChallenge` inspects the Go response's status and headers. They
answer the same question from opposite sides of the boundary.

The chromedp driving stays a thin, uncovered shell. `cfsolver/` is its own Go
module and needs its own CI test step.

### B. `registry` — the seam

- `ErrCloudflareChallenge` sentinel in `registry/errors.go`, alongside the
  existing `ErrCaptchaRequired` / `ErrSessionExpired`.
- `SetCloudflareSolver(s CloudflareSolver)` mirroring the existing
  `SetDomainResolver` exactly: package-level var, `sync.RWMutex`, nil-safe
  accessor that returns a zero value when unset, installed once in
  `main.go`.
- `registry` defines a **minimal** solver interface; `main.go` adapts
  `cfsolver.Client` to it. This keeps `registry` from importing the cfsolver
  package and keeps the dependency pointing inward.

### C. `forumcommon/cloudflare.go` — the shared retry

- `IsCloudflareChallenge(resp *http.Response) bool` — the detection
  primitive: 403/503 carrying a `Cf-Mitigated` header.
- A `Session` method that performs a request and, on challenge:
  solve → inject the returned cookies into the session's existing
  `Client.Jar` → replay the request **once** → if still challenged, return
  `ErrCloudflareChallenge`.
- **Single-flight + cooldown keyed by host**, so N topics on one tracker
  trigger one headless-Chrome run, not N. A `Solve` is a browser round-trip
  of up to 90s; this must never run per-request. Concretely:
  - A **successful** solve needs no TTL of its own — the cookies live in the
    session jar and expire naturally; the next challenge simply triggers a
    fresh solve.
  - A **failed** solve is cached for a cooldown window (10 min, matching
    `domains.RotateCooldown`) so a blocked tracker cannot spawn a browser run
    on every check. Without this, an unwinnable tracker is more expensive
    than a working one.
- **Fail-open**: no solver installed, or solver unreachable, returns the
  sentinel rather than wedging the check.

Placing this in `forumcommon` means NNM-Club — which already claims
`WithCloudflare` — inherits it without further work.

### D. `rutracker`

- Route Login / Check / Download / metadata fetches through the section-C
  helper.
- Return `ErrCloudflareChallenge` (wrapped) instead of `invalid credentials`
  when the response is a challenge.
- Remove `rutracker.cr` from `knownDomains` — NXDOMAIN, so it wastes a slot
  in the rotation ring.

### E. Honest surfacing

`classifyError` gains a `cloudflare` code, and **the check must run before
both existing passes**:

- the HTTP-status pass maps **403 → auth** (`scheduler.go:1058`)
- the keyword pass matches `"invalid credentials"` and `"login failed"`

Either would swallow a Cloudflare error back into `auth`. Ordering is the
whole point of this section, not an implementation detail.

Frontend: add a `cloudflare` entry to the `TopicError` code→i18n map, plus
`topics.error.cloudflare` in both `en.ts` and `ru.ts`.

### F. Deploy / config

`CFSolverEnabled` and `CFSolverURL` already exist in `config.go`, and the
`cfsolver` service already exists behind a compose profile. Remaining work is
documentation: add `MARAUDER_CFSOLVER_ENABLED` / `MARAUDER_CFSOLVER_URL` to
`deploy/.env.example` and document the `--profile cfsolver` opt-in.

## Testing

Test-first throughout; every test watched failing before implementation.

| Unit | Tests |
|---|---|
| `isChallengePage` / `hasClearance` / `outcome` | table tests incl. the timeout-while-challenged case |
| `IsCloudflareChallenge` | 403+header, 503+header, 403 without header, 200 |
| Session retry helper | solves once and replays; still-challenged returns sentinel; no solver installed returns sentinel; concurrent callers trigger **one** solve |
| `classifyError` | a Cloudflare error yields `cloudflare`, **not** `auth`, despite containing 403 / credential keywords |
| rutracker Login | challenge response returns the sentinel, not `invalid credentials` |
| `knownDomains` | `.cr` absent |

Live verification is explicitly **not** a success criterion — RuTracker may
remain blocked. Success is: the failure is correctly named, attempted once,
cached, and cheap.

## Risks

1. **The solver may still lose.** Accepted and designed for; see Non-goal.
2. **Retry amplification.** Mitigated by single-flight + TTL cache; a
   negative outcome must be cached too, or a blocked tracker triggers a
   browser run per check.
3. **`cfsolver` is a separate Go module** — its tests need their own CI step
   or they will silently not run.
4. **Solve cost.** Up to 90s of headless Chrome. Must never sit on a request
   path the scheduler blocks on beyond its existing timeout budget.
