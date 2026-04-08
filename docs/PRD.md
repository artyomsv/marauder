# Marauder — Product Requirements Document

**Version:** 1.0
**Status:** Draft → approved for implementation
**Owner:** @artyomsv
**Last updated:** 2026-04-06

> This document is the single source of truth for what Marauder is, what it does,
> what it explicitly does not do, and how it is built. It is written to be useful
> to three audiences: the person building it, the people reviewing it, and the
> person trying to figure out in 12 months *"why did we do it this way?"*.

---

## 1. Summary

Marauder is a self-hosted application that monitors torrent forum-tracker topics
for updates and automatically delivers the resulting `.torrent` files or magnet
links to the user's torrent client(s). It is built with a Go backend, a React 19
+ Tailwind 4 + shadcn/ui frontend, PostgreSQL 18.4, and a plugin architecture
designed to be easy to extend.

Public URL: **`https://marauder.cc`**
License: **MIT**
Repository: **[artyomsv/marauder](https://github.com/artyomsv/marauder)**

---

## 2. Goals and non-goals

### 2.1 Goals

1. **Broad coverage of CIS forum trackers and English-speaking indexers** —
   the most-used forum trackers (RuTracker, Kinozal, NNM-Club, LostFilm,
   Anilibria, etc.) plus Torznab/Newznab adapters for everything else, all on
   a modern, maintainable codebase.
2. **Security by default** — password hashing with Argon2id, refresh-token
   rotation, OIDC/Keycloak as a first-class login mode, per-user data isolation,
   encrypted tracker credentials at rest, CSRF protection on state-changing
   requests, strict CSP on the frontend.
3. **Easy to run** — `docker compose up -d` and a `.env` file is all it takes.
   The container must be observable, healthcheck-aware, and bounded in memory.
4. **Easy to extend** — a new tracker or client plugin is a single Go file
   implementing one interface, with a well-documented test harness.
5. **Pleasant to use** — a dark-first modern UI, not a Bootstrap admin template;
   meaningful empty states; informative error messages; keyboard-friendly.
6. **Observable** — Prometheus metrics, structured JSON logs, `/health`, `/ready`,
   per-task last-run status in the UI.

### 2.2 Non-goals

- Marauder will not implement the BitTorrent protocol.
- Marauder will not do file renaming, transcoding, Plex/Jellyfin integration, or
  media-library management.
- Marauder will not expose a Torznab/Newznab API.
- Marauder will not be a hosted SaaS product. It is exclusively self-hosted.
- Marauder will not ship a bundled index of tracker URLs or copyrighted content.
- Marauder will not support Windows or macOS desktop installers in v1. Docker
  only.

---

## 3. Personas

### 3.1 P1 — "Maks, the homelab enthusiast" (primary)

- Runs a Synology / Unraid / Proxmox box at home.
- Already runs Keycloak, Authelia, or Authentik.
- Has 3–8 active TV series he's following plus ~20 older archived topics.
- Wants one `docker compose` file he can drop into his stack.
- Expects SSO. Will not create yet another local password.
- Expects Prometheus metrics scraped into his Grafana stack.

### 3.2 P2 — "Anna, the small-community admin" (secondary)

- Runs a Marauder instance for a private group of 5–15 friends.
- Needs each user to have **their own** tracker credentials, topics, and
  download destinations — strict isolation.
- Needs basic RBAC: `admin` vs `user`.
- Needs the instance to be defensible against a single user misbehaving (one
  user's bad tracker password can't break another user's monitoring).

### 3.3 P3 — "Pavel, the archivist" (secondary)

- Follows long-running documentaries and course series over years.
- Wants a **history**: when did topic X update, what was the hash change, what
  did the client do with it.
- Rarely logs in, but when he does he wants to see *"here is every update in the
  last 6 months."*

---

## 4. User stories

### Authentication

- **US-AUTH-1** As a new user, I can log in with a local username + password
  created by the administrator.
- **US-AUTH-2** As a new user, I can log in via Keycloak (OIDC authorization code
  + PKCE) if the admin has configured it.
- **US-AUTH-3** As a logged-in user, my session refreshes automatically without
  forcing me to log in again every 15 minutes.
- **US-AUTH-4** As a user, I can log out, which revokes my refresh token
  server-side.
- **US-AUTH-5** As an admin, I can disable or delete a user; their sessions
  become invalid immediately.

### Trackers & topics

- **US-TRK-1** As a user, I can see the list of installed tracker plugins with
  their status (needs credentials / ready / error / disabled).
- **US-TRK-2** As a user, I can configure credentials for a tracker plugin (e.g.,
  RuTracker username/password), and the credentials are encrypted at rest.
- **US-TRK-3** As a user, I can add a topic by pasting its URL; Marauder detects
  which tracker plugin parses it and stores it.
- **US-TRK-4** As a user, I can see all my topics in a list with last-check time,
  last-update time, current hash, and status.
- **US-TRK-5** As a user, I can trigger a topic check manually (bypass the
  schedule).
- **US-TRK-6** As a user, I can pause / resume a topic.
- **US-TRK-7** As a user, I can delete a topic.
- **US-TRK-8** As a user, I can edit a topic's download destination (override
  default) and assigned client.
- **US-TRK-9** As a user, I can see per-topic history: every check, every
  update, every error.

### Clients

- **US-CLI-1** As a user, I can configure one or more torrent client connections
  (qBittorrent, Transmission, Deluge, uTorrent, local folder).
- **US-CLI-2** As a user, I can test a client connection from the UI before
  saving.
- **US-CLI-3** As a user, I can set a default client for new topics.
- **US-CLI-4** As a user, I can remove a client; all topics that used it prompt
  me to reassign before the removal completes.

### Notifications

- **US-NOT-1** As a user, I can configure one or more notification targets
  (Telegram, Email, Webhook, optionally Pushover).
- **US-NOT-2** As a user, I can choose which events trigger a notification
  (topic updated / topic error / all failures).
- **US-NOT-3** As a user, I can send a test notification.

### Scheduling & status

- **US-SCH-1** As a user, I can set a global check interval (default: 15 min).
- **US-SCH-2** As a user, I can set a per-topic override (e.g., check every
  hour for this archive topic).
- **US-SCH-3** As a user, I can see a global "last run" banner with summary
  (checked N, updated M, errors E).
- **US-SCH-4** As an admin, I can pause the entire scheduler.

### Admin

- **US-ADM-1** As an admin, I can create, disable, and delete users.
- **US-ADM-2** As an admin, I can see all users' topic counts and last-seen.
- **US-ADM-3** As an admin, I can view and download the audit log.
- **US-ADM-4** As an admin, I can view system metrics (DB row counts, memory,
  scheduler lag) without SSHing into the container.

---

## 5. Functional requirements

### 5.1 Tracker plugin contract

A tracker plugin is a Go package implementing the `Tracker` interface:

```go
type Tracker interface {
    // Name is a stable machine-readable identifier (e.g. "rutracker").
    Name() string

    // DisplayName is a human label for the UI (e.g. "RuTracker.org").
    DisplayName() string

    // CanParse decides whether a URL belongs to this tracker.
    CanParse(rawURL string) bool

    // Parse extracts the topic metadata from a URL. Called when the user
    // first adds a URL. Must be idempotent.
    Parse(ctx context.Context, rawURL string) (*Topic, error)

    // Check fetches the current state of a topic and returns a *Check result.
    // The scheduler compares the returned hash to the stored one to decide
    // whether to emit an "updated" event.
    Check(ctx context.Context, topic *Topic, creds *Credentials) (*Check, error)

    // Download returns the .torrent bytes or a magnet link for a specific
    // check result. Separate from Check so the scheduler can choose to
    // fetch the payload only when there is an actual update.
    Download(ctx context.Context, topic *Topic, check *Check, creds *Credentials) (*Payload, error)
}
```

Optional capability interfaces:

- `WithCredentials` — the tracker requires login; exposes `Login` and `Verify`.
- `WithQuality` — the tracker supports quality selection (e.g., LostFilm).
- `WithCloudflare` — the tracker may return a Cloudflare challenge; hints the
  HTTP client to use the shared browser-based solver.
- `WithProxy` — the tracker supports / requires a per-plugin HTTP proxy.

### 5.2 Client plugin contract

```go
type Client interface {
    Name() string
    DisplayName() string

    // Test pings the client and returns nil if the connection is usable.
    Test(ctx context.Context, cfg *ClientConfig) error

    // Add submits a payload (torrent file bytes or magnet URI) plus options
    // (download dir, category/label, paused state).
    Add(ctx context.Context, cfg *ClientConfig, payload *Payload, opts AddOptions) error

    // Optional: Remove a torrent by info hash.
    // Optional: List returns currently-active torrents; used for diagnostics.
}
```

### 5.3 Notifier plugin contract

```go
type Notifier interface {
    Name() string
    DisplayName() string
    Test(ctx context.Context, cfg *NotifierConfig) error
    Send(ctx context.Context, cfg *NotifierConfig, msg Message) error
}
```

### 5.4 Scheduler

- A single scheduler goroutine loop, driven by a cron-like ticker (default 1 min).
- On each tick, it selects topics whose `next_check_at <= now()` and dispatches
  them to a bounded worker pool (default 8 workers, configurable).
- Each worker owns a topic check end-to-end: fetch → parse → compare hash →
  download payload → hand to client → emit events.
- All work runs inside a per-topic **row-level lock** to prevent double-runs
  when the user also clicks "check now".
- Failures update `next_check_at` with exponential backoff capped at 6 hours.

### 5.5 MVP scope of plugins

Marauder's v0.1 MVP ships with:

- **Trackers (3):**
  - `rutracker` — RuTracker.org (login + scraping).
  - `genericmagnet` — a fallback plugin that accepts any magnet URI and treats
    it as a one-shot hand-off to the client (no monitoring, no hash change
    detection). Useful for "I just want Marauder to be my dropbox for magnet
    URIs from a browser bookmarklet".
  - `generictorrentfile` — accepts any HTTP(S) URL pointing at a `.torrent`
    file and monitors the SHA-1 of the file for changes.
- **Clients (2):**
  - `qbittorrent` — via WebUI API v2 (works with qBittorrent 4.5+ and 5.x).
  - `downloadfolder` — writes the `.torrent` to a watch directory on disk.
- **Notifiers (1):**
  - `telegram` — bot token + chat ID.

Post-MVP plugins (see `ROADMAP.md`) include: LostFilm, Kinozal, NNM-Club,
Anilibria, Transmission, Deluge, uTorrent, Email, Webhook, Pushover.

### 5.6 Credential encryption

- Each tracker credential (username/password/cookie), each client credential,
  and each notifier credential is encrypted at rest using **AES-256-GCM** with
  a master key loaded from an environment variable `MARAUDER_MASTER_KEY`
  (32-byte base64).
- The master key is **required at startup**; absence is a hard error.
- On first start, the user is shown instructions to generate one (`openssl rand
  -base64 32`) and is warned that losing it means all stored credentials are
  unrecoverable.
- A per-record random nonce is stored alongside the ciphertext.
- The UI displays credential fields as `••••••` once saved; updates require
  typing the full value again (no prefill).

### 5.7 Multi-user data isolation

- Every row in `topics`, `tracker_credentials`, `clients`, `notifiers`, and
  `topic_events` has a `user_id` column, foreign-keyed to `users.id`.
- Every API handler enforces `WHERE user_id = $1` — no exceptions.
- A repository-layer helper forces the user ID into the query; writing a raw
  query that bypasses it is a lint-time error (enforced by a custom
  `golangci-lint` ruleguard).
- An admin can see a global view of "topic counts per user" via a dedicated
  admin endpoint, but cannot read another user's credentials.

### 5.8 Cloudflare bypass

- Marauder exposes a pluggable `cfsolver.Solver` interface with two
  implementations:
  - `noopsolver` — default; returns a "not supported" error. Safe for builds
    without the optional dependency.
  - `chromedpsolver` — spins up a headless Chromium via
    [chromedp](https://github.com/chromedp/chromedp), waits for the Cloudflare
    JS challenge to resolve, extracts the `cf_clearance` cookie, and returns it.
- The solver runs in a **separate sidecar container** by default (so chromium
  is not bundled into the main Go binary). The main backend talks to the solver
  via a tiny HTTP API inside the compose network.
- Tracker plugins that opt into `WithCloudflare` automatically route their HTTP
  client through the solver when they receive an HTTP 403/503 with a
  Cloudflare challenge body.

### 5.9 HTTP client behavior

- All outbound tracker and client HTTP traffic goes through a shared `resty`
  (or `net/http` wrapper) client with:
  - Configurable per-plugin timeout (default 30s).
  - Max 3 redirects.
  - Configurable proxy (per-plugin, or global `HTTPS_PROXY`).
  - Custom User-Agent (`Marauder/<version> (+https://marauder.cc)`).
  - Request/response logging at DEBUG level with secret scrubbing.
  - Automatic retry on transient errors (DNS failure, 502/503/504) with
    exponential backoff (3 tries, jitter).

---

## 6. Non-functional requirements

### 6.1 Performance targets

| Metric | Target |
|---|---|
| Cold start (backend, empty DB) | < 2 s |
| Cold start (backend, 1 000 topics) | < 5 s |
| Topic check latency (single tracker, no CF) | < 3 s p95 |
| Memory footprint (100 topics, steady state) | < 150 MB RSS |
| Memory footprint (1 000 topics, steady state) | < 400 MB RSS |
| API response time (list topics, 1 000 topics) | < 200 ms p95 |
| Frontend Lighthouse performance (prod build) | ≥ 90 |

### 6.2 Reliability

- The backend container must run for **7 days** under a realistic load
  (200 topics, 15 min interval) without crashing or leaking memory beyond the
  400 MB target. CI runs a nightly "soak" test that validates this with
  accelerated time.
- Scheduler survives transient DB disconnects (retry + circuit breaker).
- Scheduler survives transient tracker timeouts (mark topic, backoff, continue).
- Database migrations are forward-only and transactional.

### 6.3 Security

- **OWASP ASVS L2** target for critical security controls (auth, session,
  access control, data protection).
- Password hashing: Argon2id with `time=3, memory=64 MiB, parallelism=4`.
- JWT signing: ES256 (ECDSA P-256) with a key generated on first start and
  persisted in the DB (encrypted with `MARAUDER_MASTER_KEY`).
- Refresh tokens: opaque random strings, stored hashed server-side, rotated
  on every use, 30-day max lifetime, revoked on logout or user disable.
- Access tokens: 15-minute lifetime, sent in `Authorization: Bearer`.
- CORS: tight allowlist from config; no wildcards in production.
- CSRF: state-changing endpoints use a double-submit cookie when the session
  is cookie-based (for the OIDC flow); pure JWT flows are exempt because they
  use `Authorization` headers.
- SQL injection: only `pgx` parameterized queries; no string concatenation.
  Enforced by lint rule.
- HTML injection: React 19 auto-escapes; any use of `dangerouslySetInnerHTML`
  must be justified in PR description.
- CSP: `default-src 'self'; img-src 'self' data:; script-src 'self'; style-src
  'self' 'unsafe-inline'` (the `unsafe-inline` is Tailwind-4's dev inline
  styles; production build ships without it).
- HTTP security headers: HSTS (2 years), X-Content-Type-Options, X-Frame-Options
  DENY, Referrer-Policy strict-origin-when-cross-origin, Permissions-Policy
  locked down.
- Rate limiting on `POST /api/v1/auth/login`: 5 attempts per IP per 15 minutes.
- Audit log captures: login (success/failure), user create/delete/disable,
  credential create/update/delete, topic create/delete, client create/delete.

### 6.4 Accessibility

- WCAG 2.1 AA target for the frontend.
- Keyboard navigable (no `onclick` handlers on non-interactive elements without
  `role` + `tabIndex`).
- Color contrast checked by `axe-core` in Playwright tests.
- Visible focus rings (not `outline: none`).

### 6.5 Internationalization

- UI strings live in a `src/i18n/{en,ru}.json` dictionary.
- Default locale is English.
- Russian (`ru`) ships in v1 because the core tracker audience is
  Russian-speaking.
- The frontend locale is user-selectable in a dropdown; persisted in
  `localStorage`.
- Dates and numbers use `Intl.DateTimeFormat` / `Intl.NumberFormat`.

### 6.6 Observability

- **Logs:** structured JSON via `zerolog` in the backend. Every request gets a
  request ID (`X-Request-ID` header, generated if absent). Every scheduler run
  gets a run ID. Secrets are scrubbed at the log layer.
- **Metrics:** Prometheus exposition at `GET /metrics` (protected by a static
  token env var, `MARAUDER_METRICS_TOKEN`). Key metrics:
  - `marauder_http_requests_total{method,route,status}`
  - `marauder_http_request_duration_seconds{method,route}`
  - `marauder_scheduler_runs_total{result}`
  - `marauder_scheduler_topic_checks_total{tracker,result}`
  - `marauder_scheduler_topic_check_duration_seconds{tracker}`
  - `marauder_tracker_updates_total{tracker}`
  - `marauder_client_submit_total{client,result}`
  - `marauder_db_pool_connections{state}`
  - `go_*` and `process_*` default collectors.
- **Health:** `GET /health` always returns 200 if the process is up.
  `GET /ready` returns 200 only when the DB is reachable and migrations are
  applied.
- **UI status page:** a "System" page in the UI surfaces: scheduler paused/running,
  last N runs and their summaries, last N per-tracker errors, DB pool stats,
  current goroutine count, memory usage.

---

## 7. Data model

All tables live in a single `marauder` schema in PostgreSQL 18.4.

### 7.1 Tables (abridged)

```sql
-- Users and auth
CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username        TEXT NOT NULL UNIQUE,
    email           TEXT UNIQUE,
    password_hash   TEXT,                   -- NULL when OIDC-only
    role            TEXT NOT NULL CHECK (role IN ('admin','user')),
    oidc_subject    TEXT UNIQUE,            -- NULL for local users
    oidc_issuer     TEXT,
    is_disabled     BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at   TIMESTAMPTZ
);

CREATE TABLE refresh_tokens (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash      TEXT NOT NULL,          -- SHA-256 of the opaque token
    issued_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    revoked_at      TIMESTAMPTZ,
    replaced_by     UUID REFERENCES refresh_tokens(id),
    user_agent      TEXT,
    ip              INET
);

-- JWT signing keys (rotated)
CREATE TABLE jwt_keys (
    id              TEXT PRIMARY KEY,       -- "kid"
    algo            TEXT NOT NULL,          -- "ES256"
    private_key_enc BYTEA NOT NULL,         -- encrypted with master key
    public_key_pem  TEXT NOT NULL,
    active          BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Tracker plugins and credentials
CREATE TABLE tracker_credentials (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tracker_name    TEXT NOT NULL,
    username        TEXT,
    -- password / session cookie stored encrypted:
    secret_enc      BYTEA,
    secret_nonce    BYTEA,
    extra           JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, tracker_name)
);

-- Torrent client configurations
CREATE TABLE clients (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_name     TEXT NOT NULL,          -- "qbittorrent"
    display_name    TEXT NOT NULL,          -- "Living room qBit"
    config_enc      BYTEA NOT NULL,
    config_nonce    BYTEA NOT NULL,
    is_default      BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Topics (the core monitoring unit)
CREATE TABLE topics (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tracker_name        TEXT NOT NULL,
    url                 TEXT NOT NULL,
    display_name        TEXT NOT NULL,
    client_id           UUID REFERENCES clients(id) ON DELETE SET NULL,
    download_dir        TEXT,               -- override
    extra               JSONB NOT NULL DEFAULT '{}'::jsonb, -- quality, etc.
    last_hash           TEXT,
    last_checked_at     TIMESTAMPTZ,
    last_updated_at     TIMESTAMPTZ,
    next_check_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    check_interval_sec  INTEGER NOT NULL DEFAULT 900,
    consecutive_errors  INTEGER NOT NULL DEFAULT 0,
    status              TEXT NOT NULL DEFAULT 'active',
                            -- active | paused | error
    last_error          TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, url)
);

CREATE INDEX idx_topics_next_check ON topics (next_check_at)
    WHERE status = 'active';
CREATE INDEX idx_topics_user ON topics (user_id);

-- Per-topic history
CREATE TABLE topic_events (
    id              BIGSERIAL PRIMARY KEY,
    topic_id        UUID NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type      TEXT NOT NULL, -- "checked","updated","error","submitted"
    severity        TEXT NOT NULL CHECK (severity IN ('info','warn','error')),
    message         TEXT,
    data            JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_topic_events_topic ON topic_events (topic_id, created_at DESC);
CREATE INDEX idx_topic_events_user ON topic_events (user_id, created_at DESC);

-- Notifier configurations
CREATE TABLE notifiers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notifier_name   TEXT NOT NULL,
    display_name    TEXT NOT NULL,
    config_enc      BYTEA NOT NULL,
    config_nonce    BYTEA NOT NULL,
    events          TEXT[] NOT NULL DEFAULT ARRAY['updated','error'],
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Audit log (append-only)
CREATE TABLE audit_log (
    id              BIGSERIAL PRIMARY KEY,
    user_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    actor           TEXT,                   -- "system" or username snapshot
    action          TEXT NOT NULL,          -- "login","user.create",...
    target_type     TEXT,                   -- "user","topic","client",...
    target_id       TEXT,
    result          TEXT NOT NULL,          -- "success"|"failure"
    ip              INET,
    user_agent      TEXT,
    details         JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Global singleton settings row
CREATE TABLE settings (
    id                      INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    scheduler_paused        BOOLEAN NOT NULL DEFAULT false,
    default_check_interval  INTEGER NOT NULL DEFAULT 900,
    oidc_enabled            BOOLEAN NOT NULL DEFAULT false,
    oidc_issuer             TEXT,
    oidc_client_id          TEXT,
    oidc_client_secret_enc  BYTEA,
    oidc_client_secret_nonce BYTEA,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### 7.2 Migrations

- Managed by `goose` (embedded in the binary, run on startup).
- Each migration is a single `.sql` file under `backend/internal/db/migrations/`
  named `NNNN_description.{up,down}.sql`.
- Down migrations exist but are not run in production; they're for test
  teardown only.

---

## 8. API surface (v1)

Base path: `/api/v1`
Content type: `application/json`
Auth: `Authorization: Bearer <access_token>` (except auth endpoints).

### Auth
- `POST   /auth/login`            — local username/password → tokens
- `POST   /auth/refresh`          — refresh token → new tokens
- `POST   /auth/logout`           — revoke refresh token
- `GET    /auth/oidc/login`       — begin OIDC flow (302 to Keycloak)
- `GET    /auth/oidc/callback`    — OIDC callback
- `GET    /auth/me`               — current user profile

### Users (admin only except `/me`)
- `GET    /users`                 — list
- `POST   /users`                 — create
- `PATCH  /users/:id`             — update (disable, change role)
- `DELETE /users/:id`             — delete

### Trackers (catalog + credentials)
- `GET    /trackers`              — list installed plugins
- `GET    /trackers/:name`        — plugin metadata
- `GET    /trackers/:name/credentials` — current user's credential (without secret)
- `PUT    /trackers/:name/credentials` — upsert credential
- `DELETE /trackers/:name/credentials`
- `POST   /trackers/:name/credentials/verify` — test login

### Topics
- `GET    /topics`                — list (filter, paginate)
- `POST   /topics`                — create from URL
- `GET    /topics/:id`
- `PATCH  /topics/:id`            — rename, reassign client, change interval, pause
- `DELETE /topics/:id`
- `POST   /topics/:id/check`      — trigger immediate check
- `GET    /topics/:id/events`     — history

### Clients
- `GET    /clients`
- `POST   /clients`
- `PATCH  /clients/:id`
- `DELETE /clients/:id`
- `POST   /clients/:id/test`

### Notifiers
- `GET    /notifiers`
- `POST   /notifiers`
- `PATCH  /notifiers/:id`
- `DELETE /notifiers/:id`
- `POST   /notifiers/:id/test`

### System
- `GET    /system/status`         — scheduler state, last run summary
- `POST   /system/scheduler/pause` (admin)
- `POST   /system/scheduler/resume` (admin)
- `GET    /system/audit` (admin)  — paginated audit log
- `GET    /system/info`           — version, build, features enabled

### Unauthenticated infrastructure (not under /api/v1)
- `GET    /health`
- `GET    /ready`
- `GET    /metrics`               — gated by `Authorization: Bearer <MARAUDER_METRICS_TOKEN>`

### Error responses

All errors follow **RFC 7807** (`application/problem+json`):

```json
{
  "type": "https://marauder.cc/errors/topic-url-not-recognized",
  "title": "No tracker plugin matches this URL",
  "status": 422,
  "detail": "The URL 'https://example.com/foo' is not parseable by any installed tracker plugin.",
  "instance": "/api/v1/topics",
  "trace_id": "7b2c..."
}
```

---

## 9. Frontend UX spec

### 9.1 Design language

- **Theme:** dark-first, with a polished light mode available.
- **Palette:** neutral slate base, **blue** as the primary accent
  (`hsl(217 91% 60%)`), warm amber as the secondary accent for "highlight /
  updated" callouts, and red for destructive actions.
- **Typography:** Inter for UI, JetBrains Mono for hashes, URLs, and technical
  fields.
- **Shape:** rounded-xl (12 px) for cards and inputs; rounded-2xl (16 px) for
  modals. Subtle inner shadow on inputs in dark mode.
- **Depth:** a very subtle glassmorphism treatment on the top-level nav and
  modals (`backdrop-blur-xl` + 4–6% white overlay) — used sparingly, not on
  every card, to avoid "Vista-style" excess.
- **Motion:** `framer-motion` for route transitions (150 ms fade+slide),
  list entry/exit (200 ms), and the "check now" spinner. No bounce, no
  parallax, no hero video.
- **Density:** comfortable by default, toggleable to compact for power users
  who have 200+ topics.

### 9.2 Screens

1. **Login** — minimalist centered card; local form + optional "Sign in with
   Keycloak" button when OIDC is enabled.
2. **Dashboard** — hero tiles: active topics, updates in last 24h, errors in
   last 24h, next check countdown. Below: the "recent activity" feed.
3. **Topics** — the workhorse screen. A responsive table (TanStack Table) with:
   - Filter pills across the top (All / Active / Paused / Errored).
   - A full-text search over display names and URLs.
   - Per-row: tracker badge, display name, last updated (relative), next check
     (relative), status icon, quick actions (check now, pause, edit, delete).
   - Row click opens a side-sheet with the full topic detail + event history.
4. **Add topic** — a single input that auto-detects the tracker as you paste,
   shows a preview of the parsed topic below, and lets you pick client +
   interval before saving.
5. **Trackers** — catalog view showing all installed plugins, with "configure
   credentials" and "check status" actions per plugin.
6. **Clients** — CRUD list with a "Test connection" button on each card.
7. **Notifiers** — CRUD list with "Send test" button.
8. **System** — scheduler state, metrics, audit log (admin only).
9. **Users** (admin only).
10. **Settings** — theme, language, density, global interval, OIDC config.

### 9.3 Component sourcing

- **shadcn/ui 4.1.2** provides the base primitives (Button, Input, Dialog,
  Sheet, Tabs, DropdownMenu, Table, Toast, Form). Copied into
  `src/components/ui/` not imported as a package, per shadcn convention.
- **TanStack Query v5** for server state.
- **TanStack Table v8** for the topics and users tables.
- **React Router v7** for routing.
- **Zustand** for a tiny amount of global UI state (theme, locale, sidebar
  collapsed). No Redux.
- **react-hook-form + zod** for forms and validation.
- **lucide-react** icons.

### 9.4 Empty states

Every list view has a custom empty state, never a blank page. Example for
Topics: "No topics yet. Paste a tracker URL to start watching." with a CTA
button that focuses the URL input in the Add modal.

### 9.5 Error states

- Network errors show a toast and a retry button.
- Validation errors show inline on the form field.
- Server 5xx errors show a full-page "something went wrong" view with the
  trace ID copy-to-clipboard button.

---

## 10. Deployment topology

### 10.1 Containers

```
┌─────────────────────────────────────────────────────────────┐
│                     docker compose network                  │
│                                                              │
│  ┌──────────┐   ┌──────────┐   ┌─────────────┐   ┌────────┐ │
│  │  nginx   │──►│ frontend │   │   backend   │──►│  db    │ │
│  │  (prod)  │   │ (static) │   │   (Go)      │   │ pg18.4 │ │
│  └──────────┘   └──────────┘   └──────┬──────┘   └────────┘ │
│      ▲                                 │                     │
│      │                                 │                     │
│  HTTPS 443 (in front of nginx via      ▼                     │
│  user's own reverse proxy / Traefik)   ┌─────────────┐       │
│                                        │ cfsolver    │       │
│                                        │ (chromedp)  │       │
│                                        └─────────────┘       │
│                                                              │
│  ┌────────────────── profile: sso ────────────────────┐      │
│  │                    keycloak                          │     │
│  └──────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────┘
```

### 10.2 Services

| Service | Image | Port | Notes |
|---|---|---|---|
| `db` | `postgres:18.4-alpine` | 5432 (internal) | Named volume `marauder_pgdata`, healthcheck with `pg_isready`. |
| `backend` | `ghcr.io/artyomsv/marauder-backend:<tag>` (multi-stage Go) | 8080 (internal) | Runs migrations on startup. Non-root user. |
| `cfsolver` | `ghcr.io/artyomsv/marauder-cfsolver:<tag>` (chromium + chromedp) | 9222 (internal) | Optional; only started when `MARAUDER_CFSOLVER_ENABLED=true`. |
| `frontend` | `ghcr.io/artyomsv/marauder-frontend:<tag>` (nginx + static bundle) | 8081 (internal) | Built with Vite, served by nginx unprivileged image. |
| `gateway` | `nginx:1.27-alpine` | 34080 (host) | Optional in dev; the user can put their own reverse proxy (Traefik, Caddy) in front. Provides `/api` → backend, `/` → frontend. Container-internal port stays at 6688. |
| `keycloak` | `quay.io/keycloak/keycloak:26.0` | 34643 (host, optional) | `profile: sso`. Not started by default. Container-internal port stays at 8643. |

### 10.3 Environment variables

Documented in `deploy/.env.example`. Critical ones:

- `MARAUDER_MASTER_KEY` — 32-byte base64, **required**, secrets fail to load
  without it.
- `MARAUDER_METRICS_TOKEN` — token required to hit `/metrics`.
- `MARAUDER_DB_URL` — postgres connection string.
- `MARAUDER_ADMIN_INITIAL_USERNAME` / `MARAUDER_ADMIN_INITIAL_PASSWORD` — used
  only on first start to create the first admin. Warns if left set after.
- `MARAUDER_OIDC_ISSUER`, `MARAUDER_OIDC_CLIENT_ID`,
  `MARAUDER_OIDC_CLIENT_SECRET`, `MARAUDER_OIDC_REDIRECT_URL` — optional.
- `MARAUDER_LOG_LEVEL` — `debug|info|warn|error`, default `info`.
- `MARAUDER_HTTPS_PROXY` — optional shared proxy for tracker HTTP.
- `MARAUDER_CFSOLVER_URL` — e.g., `http://cfsolver:9222`.

---

## 11. Build, test, and CI

### 11.1 Local dev

```bash
# Backend only (live reload with air, inside docker)
docker compose -f docker-compose.yml -f docker-compose.dev.yml up backend db

# Frontend dev server (Vite HMR, inside docker)
docker compose -f docker-compose.yml -f docker-compose.dev.yml up frontend-dev

# Full prod stack
docker compose up -d
```

### 11.2 Tests

| Layer | Tool | Target coverage |
|---|---|---|
| Go unit tests | `go test ./...` + `testify` | ≥ 70% for `internal/` |
| Go integration tests | `testcontainers-go` + real Postgres | Auth, repos, scheduler |
| Tracker plugin tests | Recorded HTTP fixtures (`httptest`) | Per plugin |
| Frontend unit tests | Vitest + React Testing Library | ≥ 60% for `src/` |
| Frontend E2E | Playwright | Happy paths for 5 core flows |
| Security | `gosec`, `govulncheck`, `trivy` on images | No HIGH/CRITICAL |
| Lint | `golangci-lint`, `eslint`, `prettier` | Clean on CI |

### 11.3 CI (GitHub Actions)

Pipelines (all run on PR and on push to `main`):

1. `backend-lint-test` — go vet, golangci-lint, unit tests, govulncheck.
2. `backend-integration` — testcontainers with Postgres 18.4.
3. `frontend-lint-test` — eslint, tsc, vitest.
4. `frontend-e2e` — Playwright against a compose stack.
5. `docker-build` — backend and frontend images; `trivy` scan; on tag push,
   also publish to GHCR.
6. `docs-lint` — markdownlint on `docs/`.

---

## 12. Risks and mitigations

| Risk | Probability | Impact | Mitigation |
|---|---|---|---|
| Cloudflare evolves faster than `chromedp` can keep up | High | High | Solver is a sidecar container; swap in a different solver (e.g., FlareSolverr) without backend changes. |
| RuTracker changes its HTML and breaks the plugin | High | Medium | Recorded fixture tests catch regressions; plugin API is small enough that a fix is a single PR. |
| qBittorrent WebUI API v2 changes again | Medium | Medium | Version-sniff on connect; document supported versions per release. |
| `MARAUDER_MASTER_KEY` rotation story is messy | Medium | High | v1 requires manual re-encrypt script; v1.1 adds key versioning on each ciphertext so keys can be rotated without downtime. |
| Memory growth under 1 000+ topics | Medium | Medium | Nightly soak test in CI; pprof endpoint (gated) for diagnostics. |
| User points Marauder at a tracker they don't have rights to use | N/A | Legal/ethical | README disclaimer; Marauder does not ship pre-configured URLs; behavior is user's responsibility. |
| Supply chain: a Go dep is compromised | Low | High | `govulncheck` in CI; `go.sum` committed; no auto-updates to `main`; renovate bot in dry-run mode. |
| Single-maintainer bus factor | Medium | High | MIT license, plugin-centric architecture, and first-class CONTRIBUTING guide lower the bar for others to take over. |

---

## 13. Open questions (accepted defaults)

The user has explicitly asked Marauder to be built without asking clarifying
questions, so the following are **decisions made by default** and can be
revisited post-v1:

- **Bundled trackers in MVP:** `rutracker`, `genericmagnet`, `generictorrentfile`.
  Others are post-MVP.
- **Default language:** English; Russian translation is in-scope for v1.
- **Default theme:** dark.
- **Session length:** 15-min access, 30-day refresh.
- **Scheduler default interval:** 15 minutes.
- **Password policy:** minimum 12 characters, no composition rules (follow
  NIST 800-63B).
- **Multi-tenancy:** single-instance, multi-user; no organization concept.
- **Notifications in MVP:** Telegram only.
- **UI component library:** shadcn/ui copied into repo; no external package.

---

## 14. Definition of done (v1.0)

v1.0 ships when:

- [ ] All MVP user stories (Sec. 4) are implemented and covered by tests.
- [ ] RuTracker plugin monitors a real topic end-to-end (login → check →
      update detection → qBittorrent submit → Telegram notify).
- [ ] `downloadfolder` client is fully functional.
- [ ] OIDC login against a Keycloak instance works.
- [ ] `/metrics`, `/health`, `/ready` are implemented and documented.
- [ ] Russian translation is ≥ 95% complete.
- [ ] CI is green on all pipelines.
- [ ] `docker compose up -d` on a clean Linux host produces a working stack
      within 2 minutes.
- [ ] `README.md` has a "first 10 minutes" quick-start that actually works.
- [ ] `CHANGELOG.md` has a `1.0.0` entry.
- [ ] Docker images are published to GHCR with semver tags.
