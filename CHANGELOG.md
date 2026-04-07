# Changelog

All notable changes to Marauder will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added (Phase 4f — LostFilm real packed-int v_search flow + per-episode state)
The previous draft of Phase 4f–h posted `c/s/e` form fields to
`/v_search.php` and "picked the latest episode per Check", which was
a plausible-but-wrong reverse-engineering of LostFilm. This entry
replaces that draft with the **actual** flow extracted from
`main.min.js`:

- **`lostfilm.go` rewritten end-to-end** around the real
  `PlayEpisode(a) { window.open("/v_search.php?a=" + a) }` JS shape:
  - **Series page parsing** now reads two redundant attributes per
    episode: `data-code="<show>-<season>-<episode>"` (canonical,
    hyphens not colons) and `data-episode="<show><sss><eee>"` (the
    packed integer used by `PlayEpisode`). Both forms are decoded;
    `data-episode` is the fallback when `data-code` is missing.
  - **`Check` is now stateful**: it parses every episode, applies
    the `start_season`/`start_episode` floor, subtracts the
    `topic.Extra["downloaded_episodes"]` set, and returns the
    pending list in `check.Extra["pending_episodes"]` (packed IDs)
    plus a deterministic hash of the form `eps:N/done:M/pending:K`.
    The hash flips both when new episodes appear AND when the user
    catches up, so the scheduler always re-evaluates.
  - **`Download` is now per-episode**: it pulls the **oldest**
    pending packed ID from `check.Extra`, GETs
    `/v_search.php?a=<packed>` (it is a GET, not a POST), captures
    the 302 `Location` (or meta-refresh body fallback), follows the
    redirect through external hosts (`retre.org` / `tracktor.in` /
    `lf-tracker.io`), parses the destination's per-quality `.torrent`
    buttons, picks the one matching `topic.Extra["quality"]`, and
    GETs the bencode bytes. Failure when the redirect lands on
    `/login` is surfaced explicitly as "session expired".
- **`qualityMatches` helper** locks down a sharp footgun: the naive
  `Contains(label, want)` test would have false-matched `1080p_mp4`
  for a user asking for plain `1080p`. The helper hard-codes the
  three known LostFilm tiers (`SD`, `1080p`, `1080p_mp4`/`mp4`) so
  each maps to a distinct, non-overlapping label substring. New
  `TestQualityMatcher` table-test pins all 11 cases.
- **Per-episode state tracking** lives in
  `topic.Extra["downloaded_episodes"]` (slice of packed IDs). The
  scheduler appends to this list after every successful submit and
  the next `Check` subtracts it from the pending set. JSON-roundtrip
  through the JSONB column produces `[]any` instead of `[]string`,
  so a new `extraStringSlice` helper handles both shapes.
- **`TestRedirectorFlow`** rewritten to drive the new flow end-to-end
  against an `httptest.Server`: 3 episodes parsed, all 3 pending →
  Download fetches the **oldest** first → asserts `?a=791001005`
  hits the server with method=GET → asserts the resulting torrent
  path contains `1080p` but not `1080p_mp4` → marks the first
  episode downloaded → reruns Check → asserts pending shrinks to
  2 → applies start_season=3 filter → asserts pending is empty.
- **`backend/internal/db/repo/topics.go`** gains
  `Topics.UpdateExtra(ctx, id, extra)` so the scheduler can persist
  the growing `downloaded_episodes` list without touching the rest
  of the topic row.

### Added (Phase 4g — scheduler multi-download loop with no-pending sentinel)
- **`scheduler.runCheck` per-episode loop.** The scheduler now drains
  every pending episode in one tick instead of one-per-tick. The
  inner loop calls `tr.Download(ctx, t, check, creds)` repeatedly
  until either:
  - Download returns the `"no pending episodes"` sentinel error
    (matched via `isNoPendingError`) — graceful exit, all submitted
    work counted as success.
  - A real download/submit error occurs mid-loop — record the
    progress made so far AND record the error+backoff so the next
    tick retries.
  - The `maxPerTick = 25` safety guard fires — stops a misbehaving
    plugin from burning unbounded download bandwidth per tick.
- **`Scheduler.markEpisodeDownloaded`** pops the first pending packed
  ID off `check.Extra["pending_episodes"]`, appends it to
  `topic.Extra["downloaded_episodes"]`, and persists via
  `Topics.UpdateExtra`. After persisting, the scheduler re-runs
  `tr.Check` so the next loop iteration sees the shrunken pending
  list (and a fresh hash).
- **Backwards-compatible**: every existing tracker plugin returns
  one payload from `Download` and then errors on the second call
  ("nothing to download"). The loop's `i > 0 + isNoPendingError`
  branch breaks cleanly after one iteration, so single-payload
  plugins keep their old semantics for free.
- **Tech debt logged**: `techdebt/2-3-scheduler-no-unit-tests.md`
  notes that the scheduler still has zero unit-test coverage and
  that the "no pending" contract is currently a stringly-typed
  `errors.Is`-incompatible sentinel.

### Added (Phase 4h — shared DeleteConfirm safety component)
- **`frontend/src/components/shared/DeleteConfirm.tsx`** (new) — a
  one-click trash icon that swaps in place to a `Delete? ✓ ✗` row
  on the first click and only fires `onConfirm` after the second.
  Auto-cancels after 4s of inactivity. No modal, no portal, no
  layout shift — the confirm row replaces the trash icon at the
  same position. Click events are stopped from bubbling so it can
  live inside row-level click handlers. Spinner state via
  `isPending`.
- **Wired into 4 pages** (`Topics.tsx`, `Clients.tsx`,
  `Credentials.tsx`, `Notifiers.tsx`), replacing the previous
  `<Button onClick={() => del.mutate(id)}>` patterns. Removes the
  accidental-deletion footgun without adding a modal dialog —
  consistent with the "no JS confirm() / no blocking modals" rule.

### Fixed (Phase 4i — real version from /system/info on AppShell + Settings)
- **`AppShell.tsx` and `Settings.tsx`** now render the live build
  version from `GET /api/v1/system/info` instead of hardcoded
  `v0.1` / `v0.4.0-alpha` strings. Settings additionally renders
  the build commit and date when present (suppressed if either is
  literally `"unknown"`). 5-minute React Query stale time.
- **`deploy/docker-compose.yml`** dev marker bumped from
  `0.1.0-dev` → `1.1.0-dev` for backend / frontend / cfsolver, so
  local images built off `main` post-1.0.0 no longer wear the
  pre-v0.1 label.

### Maintenance
- **`.gitignore`** now excludes `.claude/` (per-machine Claude Code
  settings). Stops `settings.local.json` from leaking into commits.

### Added (Phase 4e — tracker credentials surface end-to-end)
The `tracker_credentials` table existed in the schema since v0.1
but had no REST handler and no frontend UI — it was unreachable.
This phase wires the entire surface end-to-end so users can finally
add LostFilm / RuTracker / Kinozal accounts.

- **`backend/internal/db/repo/tracker_credentials.go`** (new) —
  Create / GetByID / GetForTracker / ListForUser / Update / Delete
  methods. Mirrors the existing Clients repo pattern with a unique
  `(user_id, tracker_name)` constraint enforced at the DB layer.
- **`backend/internal/api/handlers/credentials.go`** (new) — REST
  handler exposing five endpoints under `/api/v1/credentials`:
  `GET /` (list, no secrets), `POST /` (validates by calling the
  plugin's `Login` before saving), `PUT /{id}` (rotate username and
  optionally password), `DELETE /{id}`, `POST /{id}/test` (decrypts
  the secret and re-runs `Login` + `Verify`).
- **`Credentials` handler struct** wired into `router.go` Deps and
  constructed in `cmd/server/main.go` with the existing master key
  + audit logger. Every create / update / delete is audit-logged.
- **Scheduler now passes credentials into `Check`/`Download`.** The
  scheduler at `backend/internal/scheduler/scheduler.go` gains a
  `creds *repo.TrackerCredentials` field. Before each topic check,
  if the tracker implements `WithCredentials` and the user has a
  stored credential, the scheduler decrypts the secret, calls
  `Login`, and passes the in-memory credential into both `Check`
  and `Download`. Login failures are recorded as `auth_error` in
  the metric and as a backoff-retry on the topic.
- **`frontend/src/pages/Credentials.tsx`** (new) — `/accounts` page.
  Lists existing accounts grouped by tracker (display name, username,
  test/edit/delete buttons). Add form filters out trackers that
  already have a credential to honour the unique constraint.
  Validates by attempting Login on submit; if Login fails the
  credential is not stored.
- **New nav entry** "Accounts" in `AppShell.tsx` and corresponding
  i18n keys in `en.ts` / `ru.ts`.
- **Plugin contract clarified**: `WithCredentials.Login` receives
  a `*TrackerCredential` whose `SecretEnc` field carries the
  **plaintext** password in-memory after decryption. The persisted
  blob is the AES-256-GCM ciphertext.

### Added (Phase 4a–d — tracker capability discovery + quality / episode in AddTopic)
- **`WithEpisodeFilter` capability interface** in
  `backend/internal/plugins/registry/registry.go`. Tracker plugins
  that implement `SupportsEpisodeFilter() bool` promise to honour
  `topic.Extra["start_season"]` / `topic.Extra["start_episode"]`
  in their `Check`/`Download` methods.
- **`GET /api/v1/trackers/match?url=<encoded>`** — new endpoint in
  `backend/internal/api/handlers/trackers.go`. Looks up the tracker
  plugin that claims a URL via `registry.FindTrackerForURL`, then
  type-asserts every optional capability and returns the snapshot:
  `tracker_name`, `display_name`, `qualities`, `default_quality`,
  `supports_episode_filter`, `requires_credentials`,
  `uses_cloudflare`. 404 if no plugin matches. Used by the
  AddTopic form.
- **`POST /api/v1/topics`** now accepts three optional fields:
  `quality`, `start_season`, `start_episode`. They are validated
  against the plugin's `WithQuality.Qualities()` list (where
  applicable) and overlaid onto the Extra map the plugin's
  `Parse()` returned, then persisted in the existing `topics.extra`
  JSONB column. No DB schema change.
- **AddTopic form is now capability-driven**
  (`frontend/src/pages/Topics.tsx`). After the user pastes a URL the
  form debounces 350 ms then calls `/trackers/match`. If the
  response includes `qualities`, a quality `<select>` appears
  (defaulting to `default_quality`). If `supports_episode_filter`
  is true, two number inputs ("Start season", "Start episode") are
  rendered. If `requires_credentials` is true, a yellow notice
  invites the user to add a tracker account. The detected tracker
  display name is shown inline as a green confirmation.

### Added (Phase 3 — edit torrent clients + per-plugin URL guidance)
- **`GET /api/v1/clients/{id}`** in
  `backend/internal/api/handlers/clients.go` — returns the client row
  with the **decrypted** config blob, scoped to the calling user.
  Audit-logged on every read. Used by the frontend Edit Client form
  so the user can see (and rotate) what they previously saved.
- **`PUT /api/v1/clients/{id}`** — overwrites the mutable fields
  (`display_name`, `is_default`, `config`) on an existing client.
  Calls `plugin.Test()` before persistence, so a bad config never
  overwrites a good one. Plugin name (`client_name`) cannot be
  swapped via PUT — delete and re-add to switch from Transmission to
  qBittorrent. Audit-logged.
- **`Clients.Update(ctx, id, userID, displayName, isDefault, configEnc, configNonce)`**
  in `backend/internal/db/repo/clients.go`.
- **Frontend Edit button** on every client card in
  `frontend/src/pages/Clients.tsx`. Opens a new `EditClientCard`
  component that fetches the decrypted config via the new GET, hydrates
  every field (URL, username, password), and PUTs the result on save.
- **Inline help text** under every URL field — `Field` type gains an
  optional `helpText`. Transmission's URL field now reads
  *"Use the full RPC URL ending in /transmission/rpc. Default
  Transmission Web UI port is 9091; some packages (e.g.
  transmission-daemon) use 8083 or 9091. Example:
  http://192.168.2.65:8083/transmission/rpc"*. Same treatment for
  qBittorrent, Deluge, µTorrent, and the download-folder plugin.
- **`docs/clients.md`** — new per-client setup guide. One section per
  supported client showing the exact URL format, default port,
  required fields, and the most common gotchas. The Add Client form
  now links to this doc inline.
- **`api.put<T>(path, body)`** added to `frontend/src/lib/api.ts` —
  the wrapper previously only had `get / post / patch / del`.

### Added (Phase 2 — real Settings page + change-password endpoint)
- **`frontend/src/pages/Settings.tsx`** replaces the v0.4 placeholder
  with a real Settings page. Three sections, single column:
  - **Appearance** — segmented controls for theme (light/dark),
    language (English/Русский), and table density
    (comfortable/compact). All three are persisted in
    `marauder-prefs` localStorage via the existing `usePrefs` Zustand
    store. Server-side persistence is deferred.
  - **Account** — username + email read-only, plus a three-field
    change-password form (current / new / confirm) wired to the new
    backend endpoint. Sign-out button revokes the refresh token and
    clears the auth store.
  - **About** — version (`v0.4.0-alpha`), license, links to
    marauder.cc, GitHub, CHANGELOG, ROADMAP.
- **`POST /api/v1/auth/me/password`** in
  `backend/internal/api/handlers/auth.go` — change-password handler
  for local accounts. Verifies the current password with Argon2id,
  enforces an 8-char minimum on the new password, hashes with
  Argon2id, persists via the new
  `Users.UpdatePasswordHash(ctx, id, hash)` repo method, audit-logs
  every attempt (success and failure). OIDC-only accounts are
  rejected with 400 because they have no local password to change.
- The route registration in `frontend/src/App.tsx` now points
  `/settings` at `<SettingsPage>` instead of the generic placeholder.
- New i18n keys under `settings.*` in both `en.ts` and `ru.ts`.

### Changed (Phase 1 — visual & interaction polish across app + site)
- **Brand palette switched from violet/cyan to blue/amber/slate.** Only
  CSS tokens were touched; every component reads
  `hsl(var(--primary))` so no JSX changes were needed.
  - `frontend/src/index.css`: `--primary` 265→217 (Tailwind blue),
    `--accent` 192→38 (Tailwind amber), `--ring` mirrors primary,
    body radial gradients + glass-card shadow rebalanced.
  - `site/src/styles/global.css`: same tokens swapped to keep the
    marketing site brand-consistent with the app.
- **Dark/light mode toggle now actually works.** Previously
  `frontend/index.html` hardcoded `class="dark"` on `<html>` and the
  header showed a static Moon icon labelled "dark" with no handler.
  - Added `theme: "light" | "dark"` + `setTheme` to the existing
    `usePrefs` Zustand store at `frontend/src/lib/prefs.ts`. The
    setter toggles `.dark` on `document.documentElement` and
    `onRehydrateStorage` re-applies the persisted theme on store
    rehydrate.
  - Removed the hardcoded `class="dark"` from `frontend/index.html`
    and added an inline boot script that reads
    `localStorage["marauder-prefs"]` synchronously and applies the
    `.dark` class before React mounts — no FOUC flash.
  - `AppShell.tsx` header now renders a real Sun/Moon toggle button
    next to the locale switcher.
- **Language switcher dropdown rewritten** at
  `frontend/src/components/layout/LocaleSwitcher.tsx`. The bare native
  `<select>` (whose `<option>` styling is browser-controlled and
  ignores Tailwind) is replaced with a small custom popover: trigger
  button + click-outside handler + Escape-to-close + glass-card panel
  + Check icon on the active locale. ~85 LOC, no new dependency.
- **Sitewide alpha disclaimer banner on marauder.cc.** Inserted a
  warning-tinted banner immediately after `<Header />` in
  `site/src/layouts/Page.astro` so every page shows it. New
  `.alpha-banner` rule in `site/src/styles/global.css`. Banner text:
  *"Alpha release. Marauder is in early alpha. Most plugins are
  structurally complete but have not been validated against live
  services yet — expect rough edges. See plugin status →"*
- **Version label dropped from `1.0.0` to `0.4.0-alpha`** in
  `site/src/data/seo.ts` (the home hero pill picks this up
  automatically). Hero pill recoloured from green-pulse to
  warning-pulse to match the alpha framing. README badges updated
  from `violet.svg` to `blue.svg`. PRD §9.1 design language paragraph
  rewritten to describe the new palette.

### Changed (marauder.cc visual & content polish)
- **Replaced emoji icons with inline lucide SVG icons** via a new
  `site/src/components/Icon.astro` component. Six feature-card icons
  on the home page (radio-tower, globe, send, shield-check, activity,
  blocks), the install warning callout (triangle-alert), and inline
  arrow-right / github icons all render as zero-JS inline `<svg>` —
  no `astro-icon` or `@iconify-json/lucide` dependency added.
- **Dialled back the violet color usage** across the site. The
  brand violet remains on the primary CTA buttons and the Marauder
  logo gradient, but is no longer used for section header labels,
  hover borders, link underlines, step number circles, or
  background radial glows. Section labels now use
  `text-muted-foreground`, hover borders use `foreground/30`, and
  link underlines use `foreground/40`. The body background is a
  single subtle violet ellipse instead of two stacked violet/cyan
  glows.
- **Removed all `monitorrent` mentions from the marketing site and
  internal documentation** except for one credits line in the
  README. Deleted `site/src/pages/vs/monitorrent.astro` and
  `docs/migrating-from-monitorrent.md`. Reworded copy in
  `docs/VISION.md`, `docs/COMPETITORS.md`, `docs/PRD.md`,
  `docs/ROADMAP.md`, and `CONTRIBUTING.md` to describe the
  forum-tracker monitoring niche on its own terms. Cleaned up the
  same comments in `backend/internal/plugins/trackers/lostfilm/lostfilm.go`
  and `backend/internal/plugins/registry/registry.go`. The single
  remaining mention is in `README.md` under "License & credits".

### Added (marauder.cc marketing site)
- **New `site/` directory** containing the Astro 5 + Tailwind 4 +
  Shiki marketing site for `https://marauder.cc`. Designed for
  **100% Lighthouse SEO** with zero React/JS hydration:
  - 8 routes: home (`/`), `/install`, `/features`, `/trackers`,
    `/integrations`, `/docs`, `/vs/sonarr`, `/legal`, plus a friendly 404
  - Per-page **unique title, meta description, canonical URL**
  - **Open Graph + Twitter Card** on every page (8 OG tags + 4
    Twitter Card tags) generated centrally by `BaseHead.astro`
  - **JSON-LD structured data** on every page via `JsonLd.astro` with
    XSS-safe `</script>` escape:
    - sitewide: `Organization` + `WebSite`
    - home: `SoftwareApplication` (with version/license/category) +
      `FAQPage` (8 Q&A pairs)
    - `/install`: `HowTo` with 5 numbered steps (triggers Google's
      "How to" rich result)
    - inner pages: `BreadcrumbList`
  - **Sitemap** auto-generated at `/sitemap-index.xml` via
    `@astrojs/sitemap`, excluding the 404 page
  - **`robots.txt`** allowing all crawlers and pointing to the sitemap
  - **`CNAME` file** with `marauder.cc` for GitHub Pages custom domain
  - **Favicon SVG** + Apple touch icon SVG matching the app's
    violet/cyan brand
  - **OG image** at `/og/default.svg` (1200×630) with brand text
  - One long-form **comparison page** for SEO long-tail:
    `/vs/sonarr` (Sonarr-Radarr-Prowlarr feature matrix +
    explanation of why the *arr stack can't see forum trackers)
  - **Performance budget:** 0 JS frameworks shipped (Astro outputs
    pure HTML by default), only 2.25 KB of Astro's prefetch helper.
    Total HTML max 40 KB per page, single CSS bundle 35 KB
  - **Visual identity** matching the app: dark-first slate base,
    deep-violet primary, electric-cyan accent, glass cards, Inter
    + JetBrains Mono fonts, generous spacing
- **`.github/workflows/site.yml`** — Pages deploy workflow:
  - Triggers on push to main when `site/**` or the workflow itself
    changes, plus `workflow_dispatch`
  - Runs `npm ci && npm run build` in `site/` with the Node 22 cache
  - Asserts `dist/index.html`, `dist/sitemap-index.xml`,
    `dist/robots.txt`, `dist/CNAME`, the `<title>` tag, and the
    JSON-LD block are all present before deploying
  - Uploads the `dist/` directory as a Pages artifact and deploys
    via `actions/deploy-pages@v4`
  - `concurrency: pages` ensures only one deploy in flight at a time
  - Validated with `actionlint` (clean)
- **`docs/site-deploy.md`** — full guide for the one-time setup
  (Pages source toggle + DNS records at the registrar, with the
  exact 4 A records and CNAME GitHub Pages requires) plus the
  ongoing edit workflow, troubleshooting matrix, and Lighthouse
  validation steps.

### Added (CI / GitHub Actions)
- **Five GitHub Actions workflows** under `.github/workflows/`:
  - **`ci.yml`** — fast-feedback PR pipeline (under 3 min budget):
    `go vet`, race-detector tests, `golangci-lint`, `govulncheck`,
    cfsolver build/vet, frontend `tsc --noEmit` and `npm run build`,
    bundle-size summary. Cancels in-flight runs on the same ref.
  - **`docker.yml`** — builds backend, frontend, and cfsolver images
    on every push to main and on every tag. Trivy scan with HIGH/
    CRITICAL fail-on, SARIF uploaded to the GitHub Code Scanning view.
    Does NOT push images.
  - **`e2e.yml`** — heavyweight nightly + on-tag end-to-end test that
    brings up the full compose stack (db + backend + frontend +
    gateway + qBittorrent), then runs the magnet → qBittorrent
    walkthrough from `docs/test-e2e-magnet.md` end-to-end. Includes
    backend log capture on failure and a clean teardown step.
  - **`release.yml`** — tag-pushed release pipeline. Multi-arch
    (amd64 + arm64) build via QEMU + buildx, push to `ghcr.io/
    artyomsv/marauder-{backend,frontend,cfsolver}` with semver tags,
    cosign keyless signing via OIDC, CycloneDX SBOM per image, GitHub
    Release with the auto-extracted CHANGELOG section. Pre-release
    detection from `-rc`/`-alpha`/`-beta` tag suffixes.
  - **`codeql.yml`** — GitHub CodeQL SAST for Go and TypeScript with
    the `security-extended` query pack. Runs on PR + push + weekly.
- **`.github/dependabot.yml`** — automated dependency updates across
  Go modules (backend + cfsolver), npm (frontend), GitHub Actions,
  and Docker base images. Weekly Monday cadence, minor/patch updates
  grouped per ecosystem to reduce PR noise. React 19 / Vite 8 /
  Tailwind 4 majors are pinned per the v1.0 tech-stack lock.
- **PR + Issue templates**:
  - `.github/PULL_REQUEST_TEMPLATE.md` — checklist mirroring CONTRIBUTING.md
  - `.github/ISSUE_TEMPLATE/bug.yml` — structured bug report
  - `.github/ISSUE_TEMPLATE/feature.yml` — structured feature request
  - `.github/ISSUE_TEMPLATE/tracker_breakage.yml` — special-case
    template for forum-tracker plugin breakage with HTML excerpt
    upload, scrubbing checkboxes, and a tracker dropdown
- **`backend/.golangci.yml`** — golangci-lint v2 config covering 12
  linters (errcheck, govet, ineffassign, staticcheck, unused,
  bodyclose, rowserrcheck, sqlclosecheck, errorlint, gosec, misspell,
  unconvert) plus gofmt + goimports formatters. Includes principled
  exclusions for test files, init-based plugin registration,
  `defer .Body.Close()` and `defer tx.Rollback()` patterns, and
  SHA-1 used as a content hash (G401/G505) which is the same hash
  BitTorrent uses internally.
- **`docs/ci.md`** — full CI/CD documentation: per-workflow
  description, when each runs, what to do when it fails, how to
  cut a release, how to validate locally with the same Docker
  commands the workflows use.

### Fixed (lint pass over the existing codebase)
- `internal/crypto/crypto_test.go`: replace tautological
  `HashToken("x") != HashToken("x")` comparison with two assigned
  variables so staticcheck SA4000 stops (correctly) flagging it.
- `internal/plugins/trackers/kinozal/kinozal_test.go`: replace
  `if HasPrefix { TrimPrefix }` with the unconditional
  `TrimPrefix` (S1017).
- `internal/plugins/clients/transmission/transmission_test.go`:
  remove the unused `mu sync.Mutex` field on `fakeServer`.
- `internal/crypto/crypto.go`: bound-check `len(want)` before the
  uint32 conversion in `VerifyPassword`, with a `#nosec G115`
  annotation explaining the bound is enforced.
- `internal/plugins/clients/downloadfolder/downloadfolder.go`: file
  permissions tightened from `0o640` to `0o600` per gosec G306, with
  a comment explaining the trade-off for shared-group setups.
- `internal/plugins/e2etest/qbitfake.go`: bound the test server's
  form-parsing body size with `http.MaxBytesReader` to satisfy gosec
  G120 even on a fake server.
- `gofmt -w` applied across the backend.

### Verified
- `golangci-lint run --timeout=5m`: **0 issues**.
- `go build ./...` and `go vet ./...`: clean.
- `go test ./...`: 29 packages, 0 failures.
- `actionlint` over all 5 workflow files: clean.

### Added (Torznab + Newznab support)
- **Torznab and Newznab indexer plugins** — opens Marauder up to
  several hundred indexers without writing scrapers. Sonarr, Radarr,
  Prowlarr, Jackett, and NZBHydra2 collectively cover 500+ indexers
  via these two protocols, and Marauder now speaks both.
  - `torznab` — for any Torznab indexer (Jackett, Prowlarr,
    NZBHydra2 in torrent mode, or a direct Torznab feed). Uses the
    explicit `torznab+https://...` URL prefix so CanParse never
    collides with forum-tracker plugins. The hash is the newest
    item's `infohash` (or `guid` fallback). New releases at the top
    of the feed trigger a Marauder "update" the same way a forum-
    thread re-upload does. Enclosure magnet URIs route directly to
    the user's torrent client.
  - `newznab` — for any Usenet indexer (NZBGeek, NZBPlanet,
    DOGnzb, NZBHydra2). Uses `newznab+https://...` prefix. Marauder
    downloads the `.nzb` and hands the bytes to a `downloadfolder`
    client pointed at a SABnzbd / NZBGet watch directory — the
    Usenet handoff is unchanged from the *arr stack workflow.
  - Shared `torznabcommon` parser package handles the common
    RSS+attr XML shape (both protocols share it). 4 parser unit
    tests cover the Torznab feed, the Newznab feed, empty input,
    and malformed XML.
- **Per-plugin tests** for both new plugins:
  - `torznab`: 7 tests (CanParse, Parse, Check happy path with
    infohash, Check fallback to GUID when no infohash, Check on
    empty feed, Check on HTTP 500, safeFilename helper) plus an
    E2E test that runs the full pipeline against a fake indexer
    and submits to a fake qBittorrent.
  - `newznab`: 4 tests (CanParse, Parse, Parse rejects bad scheme)
    plus an E2E test that runs the full pipeline through a fake
    NZB indexer that serves both the RSS feed and the .nzb bytes.
- **Bundled tracker count: 16** (was 14).
- **`docs/torznab-newznab.md`** — full integration guide explaining
  the model fit, the URL prefix scheme, step-by-step Prowlarr and
  NZBGeek walkthroughs, category numbers, and the validation
  procedure.

### Added (previous push — full tracker E2E coverage)
- **Two new tracker plugins** completing the original monitorrent
  catalog:
  - `freetorrents` — phpBB-derived Free-Torrents.org. Login form,
    `viewtopic.php` scrape, magnet + dl.php fallback. Alpha (needs
    live-account validation).
  - `hdclub` — HD-Club.org TBDev/Gazelle-style private tracker.
    `details.php` scrape, `download.php` torrent fetch. Alpha.
  - **Bundled tracker count: 14** (was 12 in v1.0.0).
- **`internal/plugins/e2etest` package** — shared E2E test harness:
  - `QBitFake` — httptest-backed stand-in for the qBittorrent WebUI v2
    API that captures every torrent submission for assertions
  - `RunFullPipeline(t, Case)` — generic runner that drives a tracker
    plugin through CanParse → Parse → Login → Verify → Check →
    Download → submit-to-fake-qbit → assertions
  - `HostRewriteTransport` — `http.RoundTripper` that rewrites a
    production hostname to a local httptest.Server host. Lets the
    plugin's regex URL patterns and CanParse keep matching against
    canonical hostnames while HTTP traffic transparently routes to
    the test server. **Production code is unmodified between unit
    tests and E2E.**
- **End-to-end tests for all 14 trackers** (one `<name>_e2e_test.go`
  per package, in-package so it can construct the plugin with private
  fields). Every test exercises the complete pipeline including the
  fake-qBit submission step:
  - `genericmagnet`, `generictorrentfile`
  - `rutracker`, `kinozal`, `nnmclub`
  - `lostfilm`, `anilibria`, `anidub`
  - `rutor`, `toloka`, `unionpeer`, `tapochek`
  - `freetorrents`, `hdclub`
- **`lostfilm` Download** is now wired to extract a magnet URI from
  the series page if one is present, instead of returning a stub
  error. The redirector flow for paid users is still pending live
  validation, but the magnet path is real and exercised in E2E.

### Changed
- `freetorrents` and `hdclub` are wired into `cmd/server/main.go`
  via blank imports.

### Verified
- `go build ./...` and `go vet ./...` clean.
- `go test ./...`: **26 test packages, all green**, including
  14 fresh tracker E2E tests.

## [1.0.0] — 2026-04-07

The initial production release. The full feature set landed across the
v0.1 → v0.4 development branches and is collected here.

### Architecture

- **Backend:** Go 1.23, `chi` HTTP router, `pgx` v5 connection pool,
  `goose`-managed embedded migrations, `zerolog` structured JSON logging,
  RFC 7807 problem-details error responses, security-headers middleware,
  request-id middleware, recovery middleware that turns panics into
  500s with trace IDs.
- **Frontend:** React 19.2 + Vite 8 + Tailwind CSS 4.2 + shadcn/ui 4.1.2,
  TanStack Query for server state, zustand for local UI state,
  framer-motion for entry animations, lucide-react for icons. Dark-first
  design language with deep-violet primary, electric-cyan accent, glass
  cards, and radial gradients.
- **Database:** PostgreSQL 18 (currently 18.3 alpine; rolls forward
  automatically when 18.4 publishes).
- **Deployment:** Docker + docker-compose, four-service production stack
  (postgres + backend + frontend + nginx gateway), `cfsolver` profile
  for the optional Cloudflare-bypass sidecar, `sso` profile for the
  optional Keycloak realm, `dev` overlay for end-to-end testing with
  real qBittorrent and Transmission containers.

### Auth

- **Local accounts:** Argon2id password hashing
  (`time=3, memory=64 MiB, parallelism=4`), ES256-signed JWT access
  tokens, opaque refresh tokens stored as SHA-256 hashes server-side,
  refresh-token rotation with reuse detection that revokes the entire
  token family on misuse.
- **OIDC:** auth-code flow via `coreos/go-oidc/v3`. Provisions new
  users on first sign-in. Pre-built `docker-compose.sso.yml` overlay
  brings up Keycloak 26.0 with a `marauder` realm and an
  `alice/marauder` test user. Documented in `docs/oidc.md`.
- **Master key:** AES-256-GCM at-rest encryption for tracker
  credentials, client configs, notifier configs, and JWT signing
  keys, all keyed by `MARAUDER_MASTER_KEY` (32-byte base64).
- **Audit log:** async logger (256-buffered channel + background
  drainer) that records login success/failure/logout to a
  Postgres-backed audit_log table. Admin-only `GET /api/v1/system/audit`
  + frontend page exposes recent entries.

### Plugin architecture

A plugin is one Go file plus its tests. `init()` self-registers with
the global `registry` package on process start. Three kinds of plugin:

| Kind | Interface | Optional capabilities |
|---|---|---|
| Tracker | `Tracker` | `WithCredentials`, `WithQuality`, `WithCloudflare` |
| Client  | `Client`  | — |
| Notifier | `Notifier` | — |

See [`docs/plugin-development.md`](docs/plugin-development.md) for the
full guide.

**Total bundled in v1.0:** 11 trackers, 5 clients, 4 notifiers.

#### Trackers (11)

| Plugin | Site | Status |
|---|---|---|
| `genericmagnet` | any magnet URI | ✅ E2E validated |
| `generictorrentfile` | any HTTP(S) `.torrent` URL | ✅ unit-tested |
| `rutracker` | RuTracker.org | 🟡 alpha (fixture-tested, needs live validation) |
| `kinozal` | Kinozal.tv | 🟡 alpha |
| `nnmclub` | NNM-Club.to (with `WithCloudflare`) | 🟡 alpha |
| `lostfilm` | LostFilm.tv (with `WithQuality`) | 🟡 alpha |
| `anilibria` | Anilibria.tv (uses public v3 API) | 🟡 alpha |
| `anidub` | tr.anidub.com (with `WithQuality`) | 🟡 alpha |
| `rutor` | Rutor.org | 🟡 alpha |
| `toloka` | Toloka.to | 🟡 alpha |
| `unionpeer` | Unionpeer.org | 🟡 alpha |
| `tapochek` | Tapochek.net | 🟡 alpha |

> **Alpha** means the plugin is structurally complete with fixture-based
> unit tests and follows the same patterns as the validated plugins, but
> has not been validated against a live site by the maintainer because
> doing so requires a real account on each site. The next release moves
> any plugin that a community member validates to "stable".

#### Clients (5)

| Plugin | Status |
|---|---|
| `downloadfolder` | ✅ unit-tested |
| `qbittorrent` (WebUI v2) | ✅ E2E validated against real qBittorrent docker container |
| `transmission` (RPC) | ✅ unit-tested with mocked-server |
| `deluge` (Web JSON-RPC) | ✅ unit-tested with mocked-server |
| `utorrent` (token-based WebUI) | 🟡 unit-tested with mocked-server, no live µTorrent docker image to validate against |

#### Notifiers (4)

| Plugin | Status |
|---|---|
| `telegram` (Bot API) | ✅ unit-tested via custom RoundTripper |
| `email` (SMTP, PLAIN auth) | ✅ unit-tested with injected sender |
| `webhook` (POST JSON) | ✅ unit-tested with httptest |
| `pushover` (form POST) | ✅ unit-tested with httptest |

### Cloudflare bypass

A separate `cfsolver/` Go service uses `chromedp` + Debian-slim
chromium to drive a target URL through any Cloudflare interstitial
and return the resulting cookies + user-agent. Runs as its own Docker
image and is gated behind the `cfsolver` compose profile so it doesn't
start unless the user opts in. Tracker plugins that opt into the
`WithCloudflare` capability automatically route through it via the
`internal/cfsolver` client package.

### Scheduler

- Single dispatch goroutine on a configurable tick (default 60s)
- Bounded worker pool (default 8) draining a buffered job channel
- Per-topic check pipeline: load → call tracker `Check` → compare hash
  → if changed, call `Download` → decrypt client config with master
  key → call client `Add`
- Exponential backoff on errors, capped at 6 hours
- Falls back to the user's default client if a topic has no explicit
  `client_id`
- In-memory ring buffer of the last 50 run summaries, exposed via
  `GET /api/v1/system/status` for the live System page
- Records detailed Prometheus metrics for every check, update, and
  client submit

### Observability

- **`/health`** — always 200 if the process is up
- **`/ready`** — 200 only when the database is reachable
- **`/metrics`** — Prometheus exposition, gated by a static bearer
  token (`MARAUDER_METRICS_TOKEN`). Includes:
  - `marauder_http_requests_total{method,route,status}`
  - `marauder_http_request_duration_seconds{method,route}`
  - `marauder_scheduler_runs_total{result}`
  - `marauder_scheduler_topic_checks_total{tracker,result}`
  - `marauder_scheduler_topic_check_duration_seconds{tracker}`
  - `marauder_tracker_updates_total{tracker}`
  - `marauder_client_submit_total{client,result}`
  - default `go_*` and `process_*` collectors
- **System status page** in the frontend showing the scheduler state,
  last-run summary, run history, and a Go runtime snapshot, all
  auto-refreshing every 5 seconds

### Frontend pages

- **Login** — animated card with local form + "Sign in with Keycloak"
  button (if OIDC is configured)
- **Dashboard** — four live status tiles + recent activity feed
- **Topics** — full CRUD with checkboxes, bulk pause/resume/delete,
  comfortable/compact density toggle, inline add card with auto-detect
  preview
- **Clients** — full CRUD with per-plugin field hints, Test-connection
  button per row, default-client toggle
- **Notifiers** — full CRUD with per-plugin field hints, Send-test
  button per row
- **System** (any user) — live scheduler + runtime status, run history
- **Audit log** (admin only) — append-only event table with action,
  actor, target, IP, user-agent, result
- **OIDC callback** — picks up tokens from the URL fragment and lands
  the user on the dashboard

### i18n

Tiny zustand-backed module with English and Russian dictionaries plus
a `useT()` hook. Locale is persisted in `localStorage` and switchable
from a header dropdown.

### Testing

- **18 unit-test packages** covering crypto, auth, plugin registry,
  every bundled tracker (where fixtures are available), every bundled
  client, and every bundled notifier
- **End-to-end magnet → qBittorrent walkthrough** documented and
  validated in [`docs/test-e2e-magnet.md`](docs/test-e2e-magnet.md)
- `go build ./... && go vet ./...` clean
- `npm run build` produces ~470 KB / ~146 KB gzipped frontend bundle

### Deployment

- Multi-stage Dockerfiles for backend and frontend, both running as
  non-root users with healthchecks
- `deploy/docker-compose.yml` — production stack
- `deploy/docker-compose.dev.yml` — overlay that exposes ports and
  starts real qBittorrent + Transmission containers
- `deploy/docker-compose.sso.yml` — overlay that adds Keycloak with a
  pre-imported realm
- All host ports are non-standard to avoid colliding with other
  services on the developer machine: gateway 6688, backend 8679,
  frontend dev 8680, Vite HMR 5174, Postgres dev 55432, Keycloak 8643

### Documentation

- `README.md` — top-level project overview
- `docs/VISION.md` — what we're building and why
- `docs/COMPETITORS.md` — how Marauder relates to Sonarr/Radarr/Prowlarr/
  Jackett/FlexGet/monitorrent
- `docs/PRD.md` — full product requirements document
- `docs/ROADMAP.md` — phased plan with v1.0 status
- `docs/plugin-development.md` — guide to writing tracker / client /
  notifier plugins
- `docs/oidc.md` — Keycloak OIDC walkthrough
- `docs/test-e2e-magnet.md` — reproducible end-to-end smoke test
- `docs/migrating-from-monitorrent.md` — migration guide
- `CONTRIBUTING.md` — local dev, test running, PR checklist
- `CHANGELOG.md` — this file

[Unreleased]: https://github.com/artyomsv/marauder/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/artyomsv/marauder/releases/tag/v1.0.0
