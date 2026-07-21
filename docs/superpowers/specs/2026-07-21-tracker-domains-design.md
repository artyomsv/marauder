# Configurable Tracker Domains + Automatic Mirror Fallback — Design

**Issue:** [#126](https://github.com/artyomsv/marauder/issues/126)
**Date:** 2026-07-21
**Branch:** `126-allow-configurable-tracker-domains-and-automatic-mirror-fallback`

## Problem

Tracker domains are compile-time constants baked into each plugin
(`defaultDomain` set in `init()`). When a tracker's primary domain dies
(kinozal.tv → mirrors kinozal.me / kinozal.guru still up), every topic on
that tracker stops checking and there is no way to switch without a code
release. Users cannot point Marauder at a working mirror.

Key existing behavior that shapes the design: the canonicalizing plugins
(kinozal, rutracker) deliberately **discard the stored topic URL's host**
and rebuild every fetch URL from `p.domain` + the parsed topic ID (SSRF
hardening). So existing topics need **no migration** — the moment
`p.domain` becomes runtime-configurable, all topics follow the new domain.

## Scope decisions (agreed)

- **Approach:** phased — Phase 1 config + UI, Phase 2 lazy error-driven
  fallback. **No** background health-check daemon (probes are unreliable
  behind Cloudflare/geo-blocks; error-driven rotation covers the need).
- **Custom domains:** known plugin-declared mirrors in a dropdown **plus**
  admin-only validated free-text custom domains.
- **Coverage:** all 16 tracker plugins adopt the seam in Phase 1.
- **UI home:** admin-only section on the existing Settings page.

## Phase 1 — configurable domains

### 1. Storage — migration `0015`, `tracker_settings`

One row per tracker, created lazily on first save:

```sql
CREATE TABLE tracker_settings (
    tracker_name   text PRIMARY KEY,
    active_domain  text,                          -- NULL ⇒ plugin default
    custom_domains jsonb NOT NULL DEFAULT '[]',   -- ordered = priority
    updated_at     timestamptz NOT NULL DEFAULT now()
);
```

New repo `backend/internal/db/repo/tracker_settings.go` with
`List / Get / Upsert`, mirroring existing repo patterns (pgxmock-tested).
No changes to the `topics` table.

### 2. Runtime seam — in-memory `domains.Store`

New package `backend/internal/domains`:

- RWMutex-guarded map `tracker_name → {active string, custom []string}`.
- Loaded from `tracker_settings` at boot; updated in place by the PUT
  handler (single-process assumption, same as `sse.Hub`).
- Registry hook: `registry.SetDomainResolver(fn)` — one function seam, set
  once in `cmd/server/main.go` wiring.
- Plugins resolve via a helper: `p.effectiveDomain()` = resolver override
  if set, else the compile-time `defaultDomain`. All internal URL builders
  (`canonicalDetailsURL`, download URLs, login URLs, API bases) switch from
  `p.domain` to `p.effectiveDomain()`.

New optional capability in `plugins/registry`:

```go
// WithDomains declares the domains a tracker is known to operate on.
// First entry is the canonical/default domain; the rest are mirrors.
type WithDomains interface {
    Domains() []string
}
```

All 16 plugins implement it, seeded from today's regex TLD alternations
(e.g. kinozal → `kinozal.tv, kinozal.me, kinozal.guru`; single-domain
plugins → one entry).

**Host acceptance becomes a runtime allowlist.** `CanParse` and the SSRF
host allowlists (nnmclub `fetch` guard, lostfilm `allowedRedirectHosts`)
change from hardcoded regex/map membership to:
`host ∈ Domains() ∪ configured custom_domains` (plus `www.` variants where
plugins accept them today). The barrier remains a strict allowlist — just
runtime-extensible by admins instead of compile-time only. This keeps the
rationale of the dismissed CodeQL `go/request-forgery` finding intact.

### 3. Custom domain validation

- Input is a **bare hostname**: no scheme, port, path, userinfo, or IP
  literal. Lowercased, RFC-1123 label validation.
- Admin-only endpoint (same gating as sonarr instance management).
- Plugins always build `https://` URLs — scheme never user-controlled.
- Optional "Test" action per domain: run the plugin's cheapest anonymous
  GET against that host, report reachability. Manual, on-demand — no
  background probing.

### 4. API

Admin-gated, beside the sonarr system endpoints:

- `GET /api/v1/system/trackers/domains` — array of
  `{name, default_domain, known_domains, custom_domains, active_domain}`.
- `PUT /api/v1/system/trackers/{name}/domains` — body
  `{active_domain, custom_domains}`. Validates
  `active_domain ∈ known ∪ custom` (or empty ⇒ revert to default),
  validates each custom hostname, upserts the DB row and patches the
  in-memory store atomically.
- `POST /api/v1/system/trackers/{name}/domains/test` — body `{domain}`,
  returns reachability result (the Test button).

### 5. Frontend — Settings page, admin-only section

- New section "Tracker domains" on `pages/Settings.tsx`, rendered only for
  `role=admin` (Settings is otherwise per-user; this section is
  instance-wide — labeled as such).
- Component `components/settings/TrackerDomains.tsx` (≤250 lines; split a
  `TrackerDomainRow` child if needed): per tracker a domain `Select`
  (known + custom entries, default marked), an add-custom-domain input
  with inline validation, and the Test button.
- React Query keys via `QK` factory; i18n strings in both en and ru.

## Phase 2 — lazy error-driven fallback

Rotation-on-failure converging on the next scheduled check (no in-tick
retry, no plugin signature changes):

- The scheduler already classifies check errors (`classifyError` →
  `timeout` / `unreachable` / `auth` / `parse` / …). On **only** the
  `timeout` and `unreachable` classes it calls
  `domains.ReportFailure(trackerName)`.
- `domains.Store` rotates `active` to the next candidate in priority order
  (ring over known + custom), guarded by a **per-tracker cooldown**
  (one rotation per ~10 min) so a burst of failing topics can't spin the
  ring past the working mirror. The new active is persisted via the repo
  and metered: `marauder_tracker_domain_rotations_total{tracker}`.
- Success sticks — no rotation back to the "primary". The errored topic
  retries on its already-persisted backoff `next_check_at` against the new
  domain; recovery is automatic within minutes with zero probe traffic.
- On rotation, send an admin notification "Tracker X switched to mirror Y"
  through the existing `notify` dispatcher (one-shot per rotation event).

## Edge cases & explicit non-goals

- **Cross-mirror dedup:** `topics` has `UNIQUE(user_id, url)`. At topic
  create, canonicalize the stored URL's host to the tracker's **default**
  domain (first `Domains()` entry — stable identity independent of the
  currently-active mirror). Existing rows are left untouched; on
  canonicalizing trackers their host is ignored at fetch anyway. A
  pre-existing topic stored with a mirror host can still duplicate against
  a new canonical add — accepted, rare, not worth a data migration.
- **Cookie sessions** (LostFilm interactive captcha login): cookies are
  domain-scoped, so a domain switch likely invalidates the stored session.
  The existing expiry → notify → re-auth loop handles it. Documented
  behavior, not engineered around.
- **Password credentials:** re-login runs against the effective domain
  automatically — no change needed.
- **`SourceURL` in notifications** keeps the stored topic URL (may point
  at a dead domain during an outage). Rewriting notification links to the
  active mirror is a possible follow-up, out of scope.
- **No background health checks** — explicit non-goal (see scope).
- **Multi-replica:** the in-memory store assumes a single backend process,
  consistent with `sse.Hub`. Redis-backed store is the escape hatch if
  that ever changes.

## Testing

- **Repo:** pgxmock tests for `tracker_settings` (upsert semantics, list,
  scan of `custom_domains` jsonb).
- **`domains.Store`:** resolution precedence (custom active > default),
  rotation ring order, cooldown gating, persistence callback.
- **Plugins:** one representative table test per plugin that `CanParse` /
  host allowlist / `effectiveDomain()` honor injected custom domains;
  extend kinozal's `TestCheck_CanonicalizesMirrorHost` to assert fetches
  target a switched active domain.
- **Handler:** hostname validation rejections (scheme/port/path/IP),
  non-admin → 403, active-not-in-allowlist → 422.
- **Scheduler:** fake store asserting `ReportFailure` fires only for
  `timeout`/`unreachable` classifications.
- **Frontend:** Vitest + RTL for the Settings section — renders tracker
  list, select changes fire the mutation, custom-domain input validates.

## Success criteria

1. With kinozal's active domain set to `kinozal.me`, an existing topic
   created against `kinozal.tv` checks and downloads successfully without
   being recreated (unit-level: URL builders target the active domain).
2. Adding a topic by mirror URL dedups against the same topic added by
   canonical URL.
3. A `timeout`-classified failure rotates the active domain once per
   cooldown window and persists it; `auth`/`parse` failures never rotate.
4. Non-admin users neither see the Settings section nor can call the API.
5. Full suites pass: backend `go build && go vet && go test -race ./...`,
   frontend `npm run typecheck && npm test && npm run build`.
