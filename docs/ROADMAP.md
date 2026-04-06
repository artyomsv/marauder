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
- [ ] E2E smoke test: add magnet, it appears in qBittorrent, topic event logged
- [ ] CONTRIBUTING.md + first unit tests for `crypto`, `auth`, and the
      plugin registry

**Done when:** the five-minute quick-start from the README actually works on
a clean Linux host.

---

## v0.2.0 — "Feels like a product"

**Goal:** enough polish, observability, and documentation that an external
homelab user could reasonably install it.

- [ ] `generictorrentfile` tracker plugin (HTTP URL → `.torrent` file,
      monitored by SHA-1)
- [ ] `downloadfolder` client plugin
- [ ] `telegram` notifier plugin
- [ ] System status page (scheduler state, last run, per-tracker errors)
- [ ] Prometheus `/metrics` endpoint (token-gated)
- [ ] `/health` and `/ready`
- [ ] Audit log UI (admin-only page)
- [ ] User management UI (admin-only page)
- [ ] Topic detail side-sheet with full event history
- [ ] Empty states, error states, loading states on every screen
- [ ] Russian (`ru`) translation of the UI
- [ ] CI: lint, unit, integration, frontend e2e (Playwright), trivy scan
- [ ] Published to GHCR with `:0.2.0` and `:latest-rc` tags

**Done when:** running the soak test for 48 hours at 200 topics @ 15 min
interval shows no memory growth and no unhandled errors.

---

## v0.3.0 — "Real trackers, real users"

**Goal:** monitorrent feature parity starts here — this is the milestone that
makes Marauder a credible replacement.

- [ ] `rutracker` tracker plugin (login + topic page scraping)
- [ ] `kinozal` tracker plugin
- [ ] `nnmclub` tracker plugin
- [ ] Cloudflare solver sidecar (`chromedp`-based) + `WithCloudflare` plugin
      capability wired through
- [ ] Per-topic quality selection UI (for trackers that support it)
- [ ] `transmission` client plugin (transmission-rpc)
- [ ] `deluge` client plugin (deluge-rpc)
- [ ] Keycloak OIDC login (authorization code + PKCE)
- [ ] Test with a live Keycloak instance documented in `docs/oidc.md`
- [ ] "Add topic" URL auto-detection with live preview
- [ ] Per-tracker credential verification ("Test login" button)

**Done when:** a user can log in via Keycloak, add a RuTracker topic, and
receive a Telegram notification when the topic updates — all without
touching config files.

---

## v0.4.0 — "The long tail"

**Goal:** close the gap on the remaining monitorrent trackers and clients.

- [ ] `lostfilm` tracker plugin (with quality selection)
- [ ] `anilibria` tracker plugin
- [ ] `anidub` tracker plugin
- [ ] `rutor`, `toloka`, `unionpeer`, `tapochek`, `hdclub`, `freetorrents` —
      ship as many as can be validated against a live site in a single release
- [ ] `utorrent` client plugin
- [ ] `email` notifier (SMTP)
- [ ] `webhook` notifier (POST JSON to arbitrary URL)
- [ ] `pushover` notifier
- [ ] Per-user theme + density preferences persisted server-side
- [ ] Compact topic table density for 200+ topics
- [ ] Bulk-edit: pause/resume/delete multiple topics

**Done when:** at least 8 of the original 12 monitorrent trackers and all
4 legacy clients have working Marauder equivalents.

---

## v1.0.0 — "Stable release"

**Goal:** a release we are willing to recommend to strangers.

- [ ] All v1.0 Definition of Done criteria from the PRD are met
- [ ] `marauder.cc` docs site published (static, from `docs/`)
- [ ] CONTRIBUTING.md + "How to write a tracker plugin" guide
- [ ] Sample GitHub Actions workflow for contributors to test their plugin
      against recorded fixtures
- [ ] Security review self-check against OWASP ASVS L2
- [ ] GHCR images signed with cosign
- [ ] SBOM published with every release
- [ ] CHANGELOG.md with full 1.0.0 notes
- [ ] Migration guide from monitorrent (how to import topics + credentials)

**Done when:** we have shipped and a small group of external beta testers
has run it for a week without reporting a release-blocker.

---

## Post-1.0 — stretch ideas (not committed)

- **FlareSolverr integration** as an alternative Cloudflare solver.
- **Key rotation** for `MARAUDER_MASTER_KEY`.
- **MFA** for local login (TOTP).
- **iCal feed** of upcoming/expected topic updates.
- **Per-user bandwidth / concurrency limits** on scheduler work.
- **Optional browser extension** ("send this page to Marauder").
- **Prowlarr bridge** — consume Prowlarr as one more "tracker" source.
- **Mobile-first responsive layout** (v1.x is desktop/tablet-first).
- **Grafana dashboard JSON** shipped with the repo.
- **Helm chart** for Kubernetes users.
- **Backup/restore CLI** (`marauder backup > dump.tar.gz`).
- **Import from monitorrent** — one-click migration from an existing
  monitorrent SQLite database.
