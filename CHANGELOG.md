# Changelog

All notable changes to Marauder will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added (v0.4 work in progress)
- **Long-tail tracker plugins** (all alpha — structurally complete with
  fixture-based tests where applicable, validation against live sites
  pending):
  - `lostfilm` — series tracking with `WithQuality` capability (SD,
    1080p_mp4, 1080p), AJAX-based login, episode-marker hash detection
  - `anilibria` — uses the public Anilibria v3 JSON API; no
    authentication needed
  - `anidub` — phpBB-derived; `WithQuality` (HDTVRip, HDTVRip-AVC, BDRip)
  - `rutor` — public no-account tracker, magnet-only
  - `toloka` — Ukrainian phpBB tracker
  - `unionpeer` — phpBB tracker
  - `tapochek` — phpBB-derived cartoons tracker
- **`utorrent` client plugin:** token-based WebUI flow (GET
  `/gui/token.html`, then `/gui/?token=&action=add-url|add-file`),
  basic-auth, mocked-server tests for Test/Add-magnet/Add-file/auth-fail
- **`email` notifier:** SMTP via `net/smtp` with PLAIN auth.
  `sender` field is overridable so tests can substitute a fake instead
  of hitting a real mailserver.
- **`webhook` notifier:** POSTs JSON `{source, title, body, link}` to
  any URL. httptest-based tests for happy path, non-2xx, and empty URL.
- **`pushover` notifier:** form POST to api.pushover.net/1/messages.json.
  httptest-based tests verify all form fields are sent.
- All eight new trackers and four new client/notifier plugins are wired
  into `cmd/server/main.go` via blank imports. **Total bundled: 11
  trackers, 5 clients, 4 notifiers.**

### Added (v0.3 work in progress, batch 2)
- **`forumcommon` helper:** tiny shared `SessionStore` that holds an
  `http.Client` with its own cookie jar per `(tracker_name, user_id)`
  pair. Forum-style tracker plugins use it to keep login cookies hot
  across concurrent topic checks without each plugin reimplementing
  the wheel.
- **`rutracker` plugin** (`internal/plugins/trackers/rutracker`):
  parses `/forum/viewtopic.php?t=NNN`, posts the login form to
  `login.php`, scrapes the magnet URI and infohash from the topic
  page, falls back to the `dl.php?t=NNN` endpoint when the magnet is
  missing. Fixture-based unit tests cover Parse / Check / Download /
  Login / Verify. Marked alpha — needs validation against a real
  account.
- **`kinozal` plugin** (`internal/plugins/trackers/kinozal`): same
  shape, targeting `details.php?id=NNN` with the `Инфо хэш` regex and
  the dl.kinozal.tv subdomain. Fixture-tested.
- **`nnmclub` plugin** (`internal/plugins/trackers/nnmclub`): same
  shape, with `WithCloudflare` opt-in so the scheduler will route
  through the cfsolver sidecar when the site returns a Cloudflare
  challenge. Fixture-tested.
- All three plugins are wired into `cmd/server/main.go` via blank
  imports so they self-register on process start.

### Added (v0.3 work in progress)
- **Keycloak / OIDC end-to-end:** `auth.OIDCProvider` is wired into the
  router. New handlers `OIDCLogin` (begin auth-code flow with state
  cookie) and `OIDCCallback` (exchange + verify ID token + provision
  user + issue Marauder JWT pair). Frontend `OIDCCallbackPage` parses
  the access/refresh tokens out of the URL fragment and lands on the
  dashboard. New `deploy/docker-compose.sso.yml` overlay starts
  Keycloak 26.0 with a pre-imported `marauder` realm and an
  `alice/marauder` test user. `docs/oidc.md` walks through the full
  E2E flow plus troubleshooting.
- **Transmission client plugin** (`internal/plugins/clients/transmission`):
  full RPC client with the `X-Transmission-Session-Id` 409-retry dance,
  basic auth support, magnet + base64 .torrent submission, and mocked
  test server.
- **Deluge client plugin** (`internal/plugins/clients/deluge`): Web
  JSON-RPC client (`/json` endpoint), auth.login + web.connected +
  web.connect handshake, magnet + base64 .torrent submission, and
  mocked test server.
- **Cloudflare solver sidecar** (`cfsolver/`): standalone Go service
  using `chromedp` + Debian-slim chromium. Exposes
  `POST /solve {url}` returning `{user_agent, cookies}`. Built as a
  separate Docker image and started via the `cfsolver` compose
  profile. The main backend talks to it via `internal/cfsolver/client.go`.

### Added (v0.2 work in progress, batch 2)
- **Audit log:** `internal/audit` package with an async logger backed
  by a 256-deep buffered channel — handlers Record() entries and the
  background goroutine drains into Postgres so the request path is
  never blocked. Login success / failure / logout all write entries
  with IP and User-Agent. Admin-only `GET /api/v1/system/audit` lists
  the most recent N entries.
- **Notifiers CRUD:** `repo.Notifiers`, `handlers.Notifiers`,
  routes `GET/POST/DELETE/POST-test /api/v1/notifiers`. Same
  encrypt-on-write/validate-via-Test-on-create pattern as Clients.
- **Frontend Notifiers page:** matches the Clients page UX with
  per-plugin field hints (telegram, email, webhook, pushover) and a
  Send-test button per row.
- **Frontend Audit log page** (admin-only): infinite-refresh table
  styled like the topics list, shows action / actor / target / IP /
  user-agent / timestamp.
- **Frontend System page:** live status tiles (scheduler running/
  paused, goroutines, heap, GC cycles), last-run summary card with
  checked/updated/errors counters, run history list, build info card.
  Auto-refreshes every 5 seconds via React Query.
- **i18n:** tiny zustand-backed module with `en` and `ru`
  dictionaries, a `useT()` hook, and a header-bar locale switcher.
  Login screen, dashboard tiles, navigation, and primary section
  headings translated.
- **Plugin tests:** unit tests for `generictorrentfile` (httptest
  fixture-based hash detection), `downloadfolder` (temp-dir add),
  `telegram` (mocked Bot API via custom RoundTripper), and
  `qbittorrent` (login + add via a stand-in WebUI v2 server).

### Added
- Initial project documentation set: `VISION.md`, `COMPETITORS.md`, `PRD.md`,
  `ROADMAP.md`, `README.md`, MIT `LICENSE`, and this `CHANGELOG.md`.
- Decision recorded: Go chosen as the backend language (see PRD §2 rationale).
- Target tech stack locked: Go 1.23+, React 19.2, Vite 8, Tailwind 4.2,
  shadcn/ui 4.1.2, PostgreSQL 18.
- Target deployment: Docker + docker-compose, with an optional Cloudflare
  solver sidecar and an optional Keycloak profile for SSO.
- Target public URL: `https://marauder.cc`.
- **Backend (Go):** `chi` HTTP router, `pgx` connection pool,
  `goose`-managed migrations, zerolog structured logging, RFC 7807 error
  responses, security-headers middleware, request-id middleware.
- **Auth:** Argon2id password hashing, ES256 JWT access tokens, opaque
  refresh tokens with reuse-detection rotation, AES-256-GCM secret
  encryption keyed by `MARAUDER_MASTER_KEY`, optional OIDC via
  `coreos/go-oidc` (Keycloak / Authentik / any OIDC provider).
- **Plugin registry:** `Tracker`, `Client`, and `Notifier` interfaces with
  `init()`-based self-registration.
- **Bundled plugins:** `genericmagnet` and `generictorrentfile` trackers,
  `qbittorrent` (WebUI v2) and `downloadfolder` clients, `telegram`
  notifier.
- **Scheduler:** bounded worker pool, exponential backoff with a 6-hour cap,
  end-to-end pipeline from Check -> Download -> client submission.
- **Frontend (React 19.2 + Vite 8 + Tailwind 4 + shadcn/ui):** dark-first
  design language with glass cards, animated login, dashboard with live
  status tiles, topics list with inline add-topic card, placeholder screens
  for Clients / Notifiers / Settings, zustand auth store, TanStack Query
  wiring, API client with RFC 7807 error mapping.
- **Deployment:** multi-stage Dockerfiles (backend + frontend) running as
  non-root users with healthchecks, `deploy/docker-compose.yml` with nginx
  gateway + postgres + backend + frontend, `deploy/docker-compose.dev.yml`
  overlay that adds real qBittorrent and Transmission containers for
  integration testing, `deploy/.env.example` with safe defaults.
- **Port scheme:** all host-exposed ports are non-standard
  (`6688` gateway, `8679` backend, `8680` frontend dev, `55432` postgres dev)
  to avoid colliding with other services on the developer box.

### Added (continued)
- **Clients API:** `GET /api/v1/clients`, `POST /api/v1/clients`,
  `DELETE /api/v1/clients/{id}`, and `POST /api/v1/clients/{id}/test`.
  Config JSON is validated by the plugin's `Test()` before being
  encrypted (AES-256-GCM) and persisted; list view never returns
  the decrypted config.
- **Scheduler wired to master key:** the scheduler now decrypts the
  stored client config on each submission so the plugin receives plain
  JSON. Falls back to the user's default client when a topic has no
  explicit `client_id`.
- **Tests:** table-driven `internal/crypto` tests for AES-GCM round-trip,
  Argon2id hash+verify, random-token generation, and SHA-256
  token-hash. Registry tests for register/list/find-for-url/duplicate-
  panic/get-not-found.
- **Docs:** `docs/test-e2e-magnet.md` — a step-by-step reproducer for
  the full magnet → qBittorrent pipeline using the dev compose overlay.

### Fixed
- `genericmagnet` plugin no longer double-prefixes "magnet:" in the
  stored `last_hash` field.

### Added (v0.2 work in progress)
- **`internal/auth` tests:** JWT manager round-trip, key generation +
  reuse-on-restart, refresh-token rotation, refresh-token reuse
  detection (which revokes all of the user's tokens), and revoke. The
  manager now depends on `JWTKeyStore` and `RefreshTokenStore`
  interfaces so the production repos and test fakes share the same
  abstraction.
- **`CONTRIBUTING.md`:** how to lay out a tracker / client / notifier
  plugin, the testing pattern, and the merge checklist.
- **Frontend Clients page** (replaces the placeholder): full CRUD with
  inline add card, per-plugin field hints (qBittorrent / Transmission /
  Deluge / downloadfolder), Test-connection button per row.
- **Prometheus metrics:** `marauder_http_requests_total`,
  `marauder_http_request_duration_seconds`, `marauder_scheduler_runs_total`,
  `marauder_scheduler_topic_checks_total`, `marauder_scheduler_topic_check_duration_seconds`,
  `marauder_tracker_updates_total`, `marauder_client_submit_total`. The
  HTTP middleware uses chi's matched route pattern as the metric label
  to keep cardinality bounded.
- **System status endpoint:** `GET /api/v1/system/status` returns
  scheduler paused state, last run summary (started/ended/checked/
  updated/errors), the last 50 run summaries, and a runtime snapshot
  (goroutines, alloc, sys, heap objects, GC cycles).
- **Scheduler ring buffer:** in-memory history of the last 50 ticks
  with start/end timestamps and per-tick counters of checked / updated
  / errored topics, plus mutex-guarded live counters that workers
  increment as they complete checks.

### Verified
- `go build ./...` and `go vet ./...` clean.
- `go test ./internal/crypto/... ./internal/plugins/registry/...`:
  **11 subtests pass**.
- `npm run build` produces a 437 KB / 139 KB gzipped bundle.
- Full `docker compose up -d` brings the stack healthy end-to-end.
- **Complete E2E verified:** login as admin -> create qBittorrent client
  (Test passes) -> add magnet topic -> wait 1 scheduler tick -> torrent
  appears in qBittorrent with correct `name`, `hash`, `magnet_uri`,
  and `state: metaDL`. Backend logs confirm
  `"message":"topic updated"`. This closes the last open item in the
  v0.1 Definition of Done.

[Unreleased]: https://github.com/artyomsv/marauder/commits/main
