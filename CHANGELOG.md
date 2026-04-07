# Changelog

All notable changes to Marauder will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Nothing yet — see the [v1.0.0](#100--2026-04-07) section below for the
most recent release.

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
