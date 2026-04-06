# Changelog

All notable changes to Marauder will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
