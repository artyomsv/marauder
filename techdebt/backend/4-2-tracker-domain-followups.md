# Tracker domain configuration (#126) — final-review follow-ups

| Field | Value |
|-------|-------|
| Criticality | Low |
| Complexity | Small |
| Location | `backend/internal/api/handlers/tracker_domains.go`, `backend/internal/plugins/trackers/{anidub,tapochek,toloka}`, `backend/internal/plugins/trackers/lostfilm/lostfilm_redirector.go`, `backend/internal/plugins/e2etest`, `backend/internal/scheduler/scheduler.go` |
| Found during | Final review of branch `126-allow-configurable-tracker-domains-and-automatic-mirror-fallback`, issue #126 |
| Date | 2026-07-22 |

## Issue

Five small, independent gaps spotted during final review of the tracker
domain configuration + automatic mirror fallback feature. None block
merge — each is scoped and low-risk — but they should not be forgotten.

### (a) `POST /system/trackers/{name}/domains/test` ignores `{name}`

`TrackerDomains.Test` (`tracker_domains.go:145-166`) never reads
`chi.URLParam(r, "name")` or calls `h.lookupTracker`, unlike `Update`
(`tracker_domains.go:98-100`). A probe request against a nonexistent or
misspelled tracker name currently still returns `200 {ok, detail}` for
whatever hostname was probed, instead of a `404` like `Update` gives for
an unknown tracker. Low impact (admin-only, no state is written), but
inconsistent with the sibling endpoint and worth aligning.

### (b) Missing Download-path domain rewrite tests + `vettedDialContext` direct test

Of the eight Task-7 "remaining fixed-domain" plugins, six
(`anidub`, `tapochek`, `toloka`)
build their `Download()` URL via `"https://" + p.effectiveDomain() + ...`
but have no test that exercises `Download()` under a non-default active
domain — only `Check()`-path domain-rewrite tests exist. `rutor`
(`TestDownload_RewritesToActiveDomain`) and `anilibria`
(`TestDownload_RelativeURLFallback_UsesActiveDomain`) already have this
coverage; the other six should get the equivalent test added, following
those two as the reference shape.

Separately, `vettedDialContext` (`tracker_domains.go:208`) — the dial-time
SSRF guard behind `DefaultDomainProbe` — has no direct unit test. It's
only exercised indirectly (its component pieces `isRoutableIP`,
`firstRoutableIP`, `denyRedirects`, `classifyProbeError` are each unit
tested; `tracker_domains_probe_test.go:103-116` explains why a real
end-to-end probe test can't distinguish "redirect not followed" from
"loopback dial refused"). A direct test of `vettedDialContext` (e.g. via
a custom `net.Resolver`/dialer seam) would close that gap without the
ambiguity the current comment documents.

### (c) `isRoutableIP` duplicated between the probe handler and LostFilm

`tracker_domains.go:182-184`'s `isRoutableIP` and
`lostfilm_redirector.go:98`'s inline check
(`ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
ip.IsUnspecified()`) implement the same non-routable-IP shape,
deliberately copied rather than shared (the handler's doc comment says so
explicitly, to avoid the handler depending on a tracker plugin package).
Neither copy rejects `ip.IsMulticast()`. Extracting one shared helper
(e.g. in a small new leaf package, or `plugins/registry`, that both the
`handlers` and `lostfilm` packages can import without creating a
handlers→plugin or plugin→plugin dependency) would remove the
duplication and add the missing multicast rejection in one place.

### (d) Plugin e2e fixtures where `domain == defaultDomain` fall into the resolver branch

Several Task-4/6/7 plugins' `effectiveDomain()`-style helpers special-case
`p.domain != defaultDomain` to decide "use the test-injected domain" vs.
"ask `registry.ActiveDomain(pluginName)`". An e2e fixture that happens to
set its `domain` field equal to the plugin's `defaultDomain` (e.g. testing
against `www.lostfilm.tv` itself) silently falls through to the resolver
branch instead of the intended test-injection branch, which fails open to
whatever the registry currently reports (empty in most test contexts, but
not deterministically so if a test registers a resolver). This is a
test-fragility trap, not a production bug — worth a guard comment or a
sentinel (`domain != "" && domain != defaultDomain`, or a separate
test-only override field) so a future fixture can't silently regress into
it.

### (e) Rotation trigger inherits `classifyError`'s broad "unreachable" bucket

`Scheduler.recordResult` (`scheduler.go:330-341`) triggers
`domains.Store.ReportFailure` whenever `classifyError` returns
`errCodeTimeout` or `errCodeUnreachable`. `classifyError`
(`scheduler.go:1031-1073`) maps HTTP `429` and *any* `5xx` (including
Cloudflare 520-526) into `errCodeUnreachable` — so a tracker temporarily
rate-limiting Marauder (`429`) or serving a transient `503` during a
deploy rotates the active domain to a mirror just as readily as a real
DNS/connect failure does. `ReportFailure`'s `RotateCooldown` (10 min)
limits the blast radius, but this mapping was inherited wholesale from
the pre-existing error-code taxonomy rather than designed for the
rotation trigger specifically. Revisit the classification (e.g. exclude
`429`/`5xx` from the rotation trigger, or give the rotator its own
narrower "should rotate" predicate) if users report surprise rotations
after a tracker's transient rate-limiting or maintenance window.

## Risks

- (a): a typo'd tracker name in a probe request silently "succeeds" against
  an unrelated hostname instead of surfacing a clear 404 — minor UX
  papercut for admins, no data risk.
- (b): a regression in the six plugins' domain-rewrite logic inside
  `Download()` specifically (as opposed to `Check()`) would not be caught
  by the existing test suites. `vettedDialContext` is the actual SSRF
  guard for the domain probe — its absence of a direct test means a
  future refactor could weaken the dial-time re-resolution check without
  any test failing.
- (c): the duplicated check is a paper cut today (both copies are
  currently identical), but future edits to one (e.g. adding CGNAT
  `100.64.0.0/10` or multicast rejection) can silently drift from the
  other, reopening the exact SSRF-adjacent gap either copy is meant to
  close.
- (d): low — would only manifest as a confusing test failure/pass
  depending on registry state, not a production issue.
- (e): could cause an unexpected mirror switch (and an admin notification)
  triggered by a tracker's own rate-limiting rather than an actual outage,
  which is surprising but self-correcting (the ring only has a few
  entries and the admin can revert via Settings → Tracker domains).

## Suggested Solutions

- (a): add the same `chi.URLParam` + `h.lookupTracker` lookup `Update`
  already does at the top of `Test`, returning the same 404 shape on an
  unknown/non-`WithDomains` tracker before probing.
- (b): add one `Download()`-path domain-rewrite test per remaining
  plugin (anidub, tapochek, toloka),
  mirroring `rutor`'s/`anilibria`'s existing tests; add a focused
  `vettedDialContext` test using an injectable resolver/dialer seam.
- (c): extract a single `isRoutableIP` (plus multicast rejection) into a
  small shared location both `handlers` and `lostfilm` can import without
  new package-direction dependencies; delete both duplicates.
- (d): guard the domain-injection branch with an explicit sentinel (empty
  string default) instead of relying on `!= defaultDomain`, or document
  the trap directly at each `effectiveDomain()` so a future fixture author
  sees the warning before hitting it.
- (e): narrow the rotation trigger's error-code set (e.g. a dedicated
  `errCode == errCodeUnreachable && !isRateLimitOr5xx` check, or split
  `classifyError`'s bucket into `errCodeUnreachable` vs. a new
  `errCodeRateLimited`/`errCodeServerError` that `recordResult` treats
  differently) once real-world rotation telemetry shows this matters.

## Addendum (final re-review, 2026-07-22)

- (f) ~~`domains.Store.ReportFailure`'s persist phase re-reads `Custom`
  fresh under `s.mu`, but the rotation target `Active` (`to`) is a local
  captured before the persist phase. A concurrent admin `Set` that lands
  a different `active` inside that window leaves memory and DB briefly
  divergent on `Active`.~~ **Resolved** (PR #127, flagged P1 by Greptile):
  the persist phase now re-reads the whole cached `DomainConfig` (active +
  custom) under the RLock and persists that, never the stale local target.
  Covered by `TestStore_ReportFailure_ConcurrentSetKeepsFreshConfig`.

## Addendum (code-review round 1, 2026-07-22)

Deferred findings from the `/code-review` pass on this branch. Fixed in the
same session: rutracker Check/Download now route through `effectiveDomain()`
(feature bug — the override/rotation was inert on rutracker's check loop);
rotation notify moved off the scheduler worker goroutine; rotation now gated
by `RotateFailureThreshold` consecutive failures; `CanonicalTopicURL`
scheme-less guard; `validateHostname` rejects numeric-TLD IP-likes; frontend
duplicate-domain guard + clear-on-success + disable-while-pending. Deferred:

- (g) **Plugin fetch paths trust `effectiveDomain()`/custom domains without
  dial-time IP vetting** (SEC-L1). An admin-added custom domain resolving to
  an internal/loopback/link-local IP would be dialed by the plugin's session
  (unlike the `/domains/test` probe, which vets IPs at dial time). Admin-only,
  self-hosted, and admins already configure arbitrary client/Sonarr URLs the
  backend connects to — low priority. Fix: run the probe's `vettedDialContext`
  shape inside the shared plugin HTTP transport, or vet custom domains on
  `Update` before persisting.
- (h) **Two SSRF-guard depths across plugins** — `rutor`/`nnmclub` re-validate
  the host inside `fetch` via `DomainAllowed`; `kinozal`/`rutracker`/`anilibria`/
  `lostfilm` trust `effectiveDomain()`'s (trusted) output directly. Both safe
  today; standardize on the defense-in-depth `fetch`-level check to keep it that
  way under future edits. Pairs naturally with (g).
- (i) **Rotation alert reuses `events.CheckFailed`** (CQ-L1). A domain rotation
  isn't a check failure; an admin subscribed only to `session.expired` won't get
  rotation alerts, and one subscribed to `check.failed` gets them mixed into
  per-check noise. Add a dedicated `domain.rotated` event (ripples through the
  events taxonomy/policy, i18n, and the frontend timeline — hence deferred).
- (j) **`validateHostname` internal error text** (SEC-L3) and the `Update`
  `problem.ErrInternal(err.Error())` path echo raw store/DB errors to the admin
  client. Matches the existing handler-package convention (`clients.go`,
  `credentials.go`), admin-only — accepted as-is, noted for a future
  package-wide error-sanitization pass.
- (k) **`/domains/test` probe has no retry** (R-M1) — intentional: it's a
  single-shot manual diagnostic; an automatic retry would mask a real transient
  failure the admin asked to see. Documented as won't-fix, not a gap.

Note: (e) above (rotation inherits `classifyError`'s broad 429/5xx→unreachable
bucket) is now partially mitigated by the `RotateFailureThreshold` gate — a
single 429/5xx no longer rotates — but narrowing the bucket itself remains open.
