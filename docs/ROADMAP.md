# Marauder — Roadmap

> Version-numbered, outcome-oriented, not calendar-bound.
> Each milestone has a **"done when"** bar, not an ETA.

---

## v0.1.0 — "Hello, world" (MVP skeleton)

**Goal:** a developer can clone the repo, run `docker compose up -d`, log in,
add a generic magnet URI, and see it land in qBittorrent.

- [x] Project vision, competitors, PRD, roadmap, README, MIT license
- [x] Go backend skeleton: `chi` router, `pgx` pool, zerolog, envconfig
- [x] Postgres 18 + goose migrations (users, refresh_tokens, jwt_keys,
      topics, topic_events, tracker_credentials, clients, notifiers,
      audit_log, settings)
- [x] Internal JWT auth: local login, refresh rotation, logout
- [x] First admin user auto-created from env on first start
- [x] `genericmagnet` tracker plugin (accepts magnet URIs, no monitoring)
- [x] `generictorrentfile` tracker plugin (SHA-1 monitoring of a .torrent URL)
- [x] `qbittorrent` client plugin (WebUI API v2)
- [x] `downloadfolder` client plugin
- [x] `telegram` notifier plugin
- [x] Scheduler loop with bounded worker pool and exponential backoff
- [x] React 19.2 + Vite 8 + Tailwind 4 + shadcn/ui frontend skeleton
- [x] Login screen + dashboard + topics list + add-topic inline card
- [x] docker-compose.yml (db + backend + frontend + gateway)
- [x] End-to-end smoke test: stack up -> login as admin -> JWT issued
- [x] **E2E smoke test: add magnet -> appears in qBittorrent, topic event
      logged** (see `docs/test-e2e-magnet.md`)
- [x] Clients CRUD endpoints with master-key config encryption
- [x] First unit tests for `crypto` and the plugin registry
- [x] CONTRIBUTING.md and unit tests for `auth` (JWT round-trip)
- [x] Frontend Clients CRUD page (replaces placeholder)

**Done when:** the five-minute quick-start from the README actually works on
a clean Linux host.

---

## v0.2.0 — "Feels like a product"

**Goal:** enough polish, observability, and documentation that an external
homelab user could reasonably install it.

- [x] `generictorrentfile` tracker plugin (HTTP URL → `.torrent` file,
      monitored by SHA-1)
- [x] `downloadfolder` client plugin
- [x] `telegram` notifier plugin
- [x] System status page (frontend + backend endpoint)
- [x] Prometheus `/metrics` endpoint with HTTP, scheduler, tracker, and
      client collectors (token-gated)
- [x] `/health` and `/ready`
- [x] Audit log UI (admin-only page) + writes from auth handlers
- [x] Russian (`ru`) translation of the UI (en + ru dictionaries)
- [x] Notifiers CRUD API + frontend page
- [x] Plugin unit tests (`generictorrentfile`, `downloadfolder`,
      `telegram`, `qbittorrent`)
- [ ] User management UI (admin-only page)
- [ ] Topic detail side-sheet with full event history
- [ ] CI: lint, unit, integration, frontend e2e (Playwright), trivy scan
- [ ] Published to GHCR with `:0.2.0` and `:latest-rc` tags

**Done when:** running the soak test for 48 hours at 200 topics @ 15 min
interval shows no memory growth and no unhandled errors.

---

## v0.3.0 — "Real trackers, real users"

**Goal:** the first batch of real CIS forum-tracker plugins, plus a credible
multi-user / SSO story. This is the milestone that turns Marauder from a
scaffold into a usable product.

- [x] `rutracker` tracker plugin (login + topic page scraping) — alpha,
      structurally complete with fixture-based tests
- [x] `kinozal` tracker plugin — alpha, fixture-tested
- [x] `nnmclub` tracker plugin — alpha, fixture-tested, opts into
      `WithCloudflare`
- [x] Cloudflare solver sidecar (`chromedp`-based) — separate Go service
      `cfsolver/`, Debian-slim image with chromium, exposed via the
      `cfsolver` compose profile, in-process client at
      `backend/internal/cfsolver`
- [ ] Per-topic quality selection UI (for trackers that support it)
- [x] `transmission` client plugin (transmission-rpc) — handles the
      X-Transmission-Session-Id 409 dance, supports magnet + .torrent +
      basic auth, mocked-server tests
- [x] `deluge` client plugin — Web JSON-RPC, auth.login + web.connect +
      core.add_torrent_magnet/file, mocked-server tests
- [x] Keycloak OIDC login (authorization-code flow) — `docker-compose.sso.yml`
      profile with realm pre-import + alice/marauder test user, OIDCLogin/
      OIDCCallback handlers, /oidc-callback frontend page, `docs/oidc.md`
- [x] Test with a live Keycloak instance documented in `docs/oidc.md`
- [ ] "Add topic" URL auto-detection with live preview
- [ ] Per-tracker credential verification ("Test login" button)

**Done when:** a user can log in via Keycloak, add a RuTracker topic, and
receive a Telegram notification when the topic updates — all without
touching config files.

---

## v0.4.0 — "The long tail"

**Goal:** close the gap on the remaining CIS forum trackers and legacy clients.

- [x] `lostfilm` tracker plugin (with quality selection via WithQuality
      capability) — alpha, structurally complete
- [x] `anilibria` tracker plugin — uses the public Anilibria v3 JSON API
- [x] `anidub` tracker plugin — alpha, with WithQuality
- [x] `rutor` tracker plugin — public, no auth
- [x] `toloka` tracker plugin — alpha
- [x] `unionpeer` tracker plugin — alpha
- [x] `tapochek` tracker plugin — alpha
- [x] `hdclub` — TBDev/Gazelle-style private tracker plugin (alpha)
- [x] `freetorrents` — phpBB Free-Torrents.org plugin (alpha)
- [x] **E2E test harness + 14 per-tracker E2E tests** — every tracker
      now has a `<name>_e2e_test.go` that exercises the full pipeline
      through a fake qBittorrent
- [x] `utorrent` client plugin — token-based WebUI flow with mocked tests
- [x] `email` notifier (SMTP) — net/smtp PLAIN auth, mocked-sender tests
- [x] `webhook` notifier — POST JSON, httptest-based tests
- [x] `pushover` notifier — form POST, httptest-based tests
- [ ] Per-user theme + density preferences persisted server-side
      (currently localStorage; v0.5)
- [x] Compact topic table density for 200+ topics — toggle in Topics page
- [x] Bulk-edit: pause/resume/delete multiple topics via checkboxes +
      bulk action bar

**Done when:** Marauder ships at least 12 forum-tracker plugins and all
4 legacy clients (qBittorrent, Transmission, Deluge, µTorrent) work end-to-end.

---

## v1.0.0 — "Stable release" ✅

**Goal:** a release we are willing to recommend to strangers. **Tagged.**

- [x] All v1.0 Definition of Done criteria from the PRD are met
      (auth, scheduler, plugin architecture, observability, docker
      stack, end-to-end magnet pipeline, multi-user data isolation)
- [x] `marauder.cc` marketing site built and deployed via GitHub
      Pages — Astro 5 + Tailwind 4, 9 routes, 100% Lighthouse SEO
      target (zero JS shipped, JSON-LD on every page, sitemap, OG
      tags). See [`docs/site-deploy.md`](site-deploy.md) for the DNS
      setup.
- [x] CONTRIBUTING.md + plugin development guide
      ([`docs/plugin-development.md`](plugin-development.md))
- [x] Sample GitHub Actions workflow for contributors — `ci.yml`,
      `docker.yml`, `e2e.yml`, `release.yml`, `codeql.yml` (see
      [`docs/ci.md`](ci.md))
- [ ] Security review self-check against OWASP ASVS L2 — v1.1
- [x] GHCR images signed with cosign — `release.yml` keyless via OIDC
- [x] SBOM published with every release — CycloneDX per image via
      `anchore/sbom-action`
- [x] CHANGELOG.md with full 1.0.0 notes

**Done when:** we have shipped and a small group of external beta testers
has run it for a week without reporting a release-blocker. **Released
2026-04-07.**

---

## Post-1.0 — landed

- [x] **Torznab + Newznab indexer plugins** (v1.0.x patch) — Marauder
      now speaks both protocols and reaches every indexer that
      Sonarr / Radarr / Prowlarr / Jackett / NZBHydra2 collectively
      support (500+). See [`docs/torznab-newznab.md`](torznab-newznab.md).
- [x] **GitHub Actions CI/CD** — five workflows + Dependabot config +
      PR/Issue templates + golangci-lint config. Per-workflow doc at
      [`docs/ci.md`](ci.md).
- [x] **Real Settings page** — replaces the v0.4 placeholder. Theme,
      language, density, change password, sign out, build info.
      `POST /api/v1/auth/me/password` for local-account password
      rotation.
- [x] **Edit torrent client config** — `GET/PUT /api/v1/clients/{id}`
      with audit-logged config decrypt. Inline help text per plugin
      (Transmission `/transmission/rpc` gotcha, qBit Web UI port,
      etc.) plus a new [`docs/clients.md`](clients.md) setup guide.
- [x] **Tracker capability discovery** — new
      `GET /api/v1/trackers/match` returns the optional capabilities
      of the matching plugin (qualities, episode filter, credentials
      requirement, Cloudflare hint). The AddTopic form uses it to
      render quality dropdowns and start-from-episode inputs only
      where the plugin supports them.
- [x] **`WithEpisodeFilter` capability** — new optional interface in
      the registry. Plugins implementing it honour
      `topic.Extra["start_season"]` / `topic.Extra["start_episode"]`
      in `Check`/`Download`. LostFilm is the first consumer.
- [x] **Tracker credentials surface end-to-end** — backend repo +
      handler + scheduler wiring + frontend `/accounts` page. The
      `tracker_credentials` table existed in the schema since v0.1
      but was unreachable until this release. Now users can add
      LostFilm / RuTracker / Kinozal accounts and the scheduler
      passes the decrypted credential into every `Check`/`Download`.
- [x] **LostFilm redirector flow live-implemented** — `lostfilm.go`
      `Check` parses every `data-code="<show>:<season>:<episode>"`
      marker; `Download` POSTs to `/v_search.php`, follows the
      redirector chain through `retre.org` / `tracktor.in`, picks
      the matching-quality `.torrent` and submits it. New
      `TestRedirectorFlow` exercises the full chain end-to-end via
      httptest. Live-validation against a real LostFilm account
      happens the first time a contributor adds a credential and
      runs a topic check.
- [x] **Per-tracker setup guide** — new
      [`docs/trackers.md`](trackers.md) covering required accounts,
      quality options, episode filter usage, and the most common
      selector-drift failure modes (with the regex line to update).

## Post-1.0 — stretch ideas (not committed)

- **FlareSolverr integration** as an alternative Cloudflare solver.
- **Key rotation** for `MARAUDER_MASTER_KEY`.
- **MFA** for local login (TOTP).
- **iCal feed** of upcoming/expected topic updates.
- **Per-user bandwidth / concurrency limits** on scheduler work.
- **Optional browser extension** ("send this page to Marauder").
- **Mobile-first responsive layout** (v1.x is desktop/tablet-first).
- **Grafana dashboard JSON** shipped with the repo.
- **Helm chart** for Kubernetes users.
- **Backup/restore CLI** (`marauder backup > dump.tar.gz`).
