# Marauder — Project Notes for Claude

This file gives Claude sessions a fast structural snapshot of the
repository so they don't waste tokens re-discovering layout. Update it
in the same commit as any structural change (per
`~/.claude/rules/documentation-maintenance.md`).

## What this is

Marauder is a self-hosted torrent tracker monitor — a Go rewrite of the
abandoned `monitorrent` Python project. Users add tracker URLs (forum
threads, indexer feeds), Marauder polls them on a schedule, detects
new releases, and pushes the resulting torrents into one of several
download clients (qBittorrent, Transmission, Deluge, µTorrent). v1.0.0
shipped 2026-04-07.

Public site: https://marauder.cc · GitHub: artyomsv/marauder

## Top-level layout

```
backend/        Go services (main backend + cfsolver sidecar)
frontend/       React 19.2 + Vite + Tailwind 4 + shadcn admin UI
cfsolver/       Standalone Go service: chromedp-based Cloudflare solver
deploy/         docker-compose stacks (base + dev + sso overlays)
docs/           ROADMAP, PRD, VISION, COMPETITORS, per-feature guides
site/           Astro 5 marketing site published to marauder.cc
techdebt/       Debt-tracking files (one per issue, see global rule)
.github/        Workflows: ci, docker, e2e, release, codeql
```

## Backend (`backend/internal/...`)

| Package | Responsibility |
|---|---|
| `api` / `api/handlers` / `api/middleware` | chi router, REST handlers, JWT middleware |
| `audit` | append-only audit log writer |
| `auth` | local password (Argon2id) + JWT issuance/refresh + OIDC (Keycloak) |
| `cfsolver` | in-process client to the standalone `cfsolver/` service |
| `config` | env-driven config struct (caarlos0/env) — **add new env vars here** |
| `crypto` | AES-256-GCM for tracker credentials and client config blobs |
| `db` / `db/repo` | pgxpool wrapper + repository structs (`Topics`, `Clients`, `Notifiers`, `Users`, `TrackerCredentials`, `Deliveries`, `Audit`, `Settings`). `TrackerCredentials` carries encrypted `secret_enc` (password) **and** `session_enc`/`session_nonce` (cookie-session blob; migration `0002`), plus a nullable `session_expired_at` marker (migration `0003`) the scheduler uses to dedupe expiry notifications (atomic `MarkSessionExpired`, cleared by `SetSession` on re-auth). `Deliveries` (migration `0006`, `topic_deliveries`) records every torrent pushed to a client — `{topic_id, infohash, label, client_id, delivered_at}`, unique on `(topic_id, infohash)` so `Record` is idempotent (`ON CONFLICT DO NOTHING`) |
| **`notify`** | reusable notification dispatcher — `Send(userID, domain.Message)` fans out to all of a user's configured notifiers (best-effort, metered). First/only consumer: scheduler session-expiry alerts. The single event→notifier fan-out point |
| `domain` | core types: `Topic` (incl. per-topic `ClientID`, `DownloadDir`, `Category`, `ImageURL`), `Check`, `Payload`, `TrackerCredential`, `TopicDelivery`, `AddOptions` (`DownloadDir` + `Category`) |
| **`infohash`** | derives the BitTorrent v1 infohash (lowercase hex) from a `Payload` — `FromMagnet` (parses `xt=urn:btih:`, hex or base32) / `FromTorrent` (SHA-1 of the bencoded `info` dict via a length-based scanner) / `FromPayload`. The universal key linking a delivery to a client's live torrent status |
| **`extra`** | shared `extra.Int / StringSlice / String` helpers for the untyped `map[string]any` blobs in `Topic.Extra` and `Check.Extra` (added 2026-04-07; **use this instead of writing local helpers**) |
| `logging` | zerolog setup (JSON in prod, pretty in dev) |
| `metrics` | Prometheus collectors (HTTP, scheduler, tracker, client) |
| `plugins/registry` | plugin interfaces + global registry + tracker capability interfaces (`WithQuality`, `WithEpisodeFilter`, `WithCredentials`, `WithCloudflare`, `WithInteractiveLogin`, `WithSeasonCatalog`, `WithMetadata`) + client capability `WithStatus` (`Status(ctx, rawConfig, hashes) []TorrentStatus` — live download status by infohash; normalised `State*` vocabulary) + the `registry.EffectiveDownloadDir(base, override, category)` save-path helper + typed sentinels **`registry.ErrNoPendingEpisodes`**, `ErrCaptchaRequired`, `ErrSessionExpired` |
| **`plugins/captchalogin`** | reusable human-in-the-loop interactive captcha-login engine (`Begin`/`Complete`/`Refresh` + TTL pending-session store). A tracker supplies a `Config` (LoginURL, CaptchaURL, CookieNames, BuildForm, Classify); first consumer is LostFilm. See `WithInteractiveLogin` |
| `plugins/trackers/<name>` | one package per tracker plugin (16 plugins as of v1.0.0+) |
| `plugins/clients/<name>` | one package per torrent client (qBittorrent, Transmission, Deluge, µTorrent, downloadfolder). qBittorrent + Transmission also implement `registry.WithStatus` for live progress |
| `plugins/notifiers/<name>` | telegram, email, webhook, pushover |
| `plugins/torznabcommon` / `torznab` / `newznab` | shared scaffolding for the Torznab/Newznab indexer adapters |
| `plugins/forumcommon` | shared cookie-jar `Session` type for forum-tracker plugins |
| `plugins/e2etest` | shared `HostRewriteTransport` test helper for tracker e2e tests |
| `problem` | RFC-7807 error responses |
| `scheduler` | per-topic check loop with bounded worker pool, exponential backoff, per-episode multi-download loop, and unit tests |
| `version` | build-time version stamping (`-ldflags -X`) |

### Scheduler design (post-2026-04-07 refactor)

`runCheck` is now a thin orchestrator. The core flow:

1. `loadCredentials` — fetches and decrypts tracker credentials (if the
   plugin implements `WithCredentials`).
2. `tr.Check` — single round-trip to the tracker.
3. If `check.Hash` differs from the topic's last hash:
   - `downloadAllPending` — drains every pending episode in one tick.
     - Per-iteration `context.WithTimeout(ctx, TrackerHTTPTimeout)`.
     - Calls `tr.Download(iterCtx, ...)` until `errors.Is(err, registry.ErrNoPendingEpisodes)`.
     - After each successful submit, calls `Topics.MarkEpisodeDownloaded`
       (atomic SQL JSONB array append) to persist progress.
     - Caps at `cfg.SchedulerMaxEpisodesPerTick` (default 25, env
       `MARAUDER_SCHEDULER_MAX_EPISODES_PER_TICK`). Cap-hit logs a Warn
       and increments `marauder_scheduler_episodes_per_tick_capped_total{tracker_name}`.
   - Mid-loop failures preserve progress: `recordResult` is called with
     `updated || anySubmitted`.
4. `recordResult` — persists `next_check_at` (with exponential backoff
   on errors, capped at 6h) and writes the run summary metrics.

**Per-topic delivery:** `sendViaClient` passes `domain.AddOptions{DownloadDir:
t.DownloadDir, Category: t.Category}` to the client plugin. Category is a
**path segment, not a client-native label**: each client config carries an
optional base `download_dir`, and every client resolves the save path via
`registry.EffectiveDownloadDir(base, opts.DownloadDir, opts.Category)` —
`topic.DownloadDir` (explicit full path) overrides, else
`path.Join(clientBase, category)`. This works uniformly across qBittorrent,
Transmission, Deluge and downloadfolder (µTorrent still can't set a per-add
path). qBittorrent's old native-`category` config field was removed. Both
fields are editable via `PUT /topics/{id}`.

**Delivery tracking:** after each successful `Add`, `recordDelivery` logs the
push into `topic_deliveries` — the payload's infohash (via the `infohash`
package), a human label (episodic: `pending_human[0]` e.g. `s01e06`, kept
aligned with `pending_episodes` as the loop consumes; single-torrent: the
topic display name), and the resolving client's id. Best-effort/fail-open: a
missing recorder, undecodable payload, or DB error is logged and never fails
the check. `GET /api/v1/topics/{id}/status` reads these rows and, when the
topic's client implements `registry.WithStatus`, augments each with live
percent/state by infohash (10s timeout, fail-open to "delivered" labels);
no background poller — the scheduler stays a pure monitor.

**Errored-topic retry:** `DueForCheck` selects `WHERE status IN
('active','error')`, so a topic that errors keeps retrying on its already-
persisted backoff `next_check_at` (≤6h) instead of parking permanently. A
successful check flips the status back to `active` (`paused` stays excluded).

The scheduler depends on small **consumer-side interfaces** (`topicsRepo`,
`markEpisodeDownloader`, `clientsRepo`, `credentialsRepo`, `deliveriesRecorder`,
`decryptor`) plus two lookup-fn seams (`trackerLookupFn`, `clientLookupFn`) so
it's unit-testable without DB or registry. Tests live in `scheduler_test.go`.

## Frontend (`frontend/src/...`)

```
src/
├── App.tsx                    Route table + Suspense boundary
├── main.tsx                   ReactDOM entrypoint
├── components/
│   ├── layout/AppShell.tsx    Header + sidebar + outlet
│   ├── shared/                Reusable across pages
│   │   ├── DeleteConfirm.tsx  Two-click destructive confirm (uses useArmedConfirm)
│   │   └── ResourceCard.tsx   Slot-based card chrome for list pages
│   └── ui/                    shadcn primitives — DO NOT hand-edit
├── hooks/                     (legacy folder, mostly empty — prefer lib/hooks)
├── i18n/                      en/ru dictionaries + useT hook
├── lib/
│   ├── api.ts                 Typed fetch wrapper, ApiError, SystemInfo
│   ├── auth-store.ts          zustand store: tokens, user, login/logout
│   ├── prefs.ts               zustand store: theme, locale, density
│   ├── queryKeys.ts           Centralised React Query key factory (QK)
│   ├── utils.ts               cn() helper
│   └── hooks/
│       ├── useArmedConfirm.ts  Two-state idle⇄armed machine with timeout
│       ├── useDebouncedValue.ts Generic debounce for query inputs
│       ├── useLogout.ts        Revoke refresh token + clear store + nav
│       └── useSystemInfo.ts    Shared /system/info query (5-min stale)
├── pages/
│   ├── Login.tsx
│   ├── Dashboard.tsx
│   ├── Topics.tsx              Topics list + AddTopicCard + BulkActionBar
│   ├── Clients.tsx             Torrent client CRUD
│   ├── Credentials.tsx         Tracker account CRUD
│   ├── Notifiers.tsx           Notifier CRUD
│   ├── Settings.tsx            Theme/locale/density + change password + about
│   ├── Audit.tsx               Audit log viewer (admin-only)
│   ├── System.tsx              System status page
│   └── OIDCCallback.tsx        Keycloak authorization-code redirect target
└── test/
    └── setup.ts                Vitest + RTL global setup
```

### Conventions

- **Server state**: React Query (`@tanstack/react-query`). Always use
  keys from `lib/queryKeys.ts` (`QK.topics`, `QK.client(id)`, …) not
  inline string literals.
- **Global UI state**: zustand stores in `lib/`.
- **Forms**: `react-hook-form` + `zod` for validation.
- **Animations**: `framer-motion` (`AnimatePresence`, `motion.div`).
- **Icons**: `lucide-react` exclusively.
- **i18n**: `useT()` from `i18n/`. English + Russian dictionaries.
- **Component size**: max 250 lines per file (currently breached by
  `Topics.tsx` and `Clients.tsx` — pre-existing tech debt).
- **Path alias**: `@/` maps to `src/`.
- **Tests**: Vitest + `@testing-library/react` + `userEvent` + jsdom.
  Co-locate `*.test.tsx` next to the component. Run with `npm test`.

### Common dev commands

```bash
# Backend (Docker — never install Go locally)
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go vet ./... && go test -race ./..."

# Frontend
docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test && npm run build"

# Stack up (compose)
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml up -d
```

## Ports (per `~/.claude/rules/local-port-ranges.md` — host ports must be 30000-49999)

Host-facing ports — all in the 34xxx range, overrideable via env vars:

| Service | Host port | Env var | Container-internal |
|---|---|---|---|
| Gateway (nginx, prod stack) | `34080` | `MARAUDER_HOST_PORT` | 6688 |
| Vite dev server (`npm run dev`) | `34000` | n/a (vite.config.ts) | n/a |
| Backend (dev overlay only) | `34081` | `MARAUDER_DEV_BACKEND_PORT` | 8679 |
| Frontend container (dev overlay only) | `34001` | `MARAUDER_DEV_FRONTEND_PORT` | 8081 |
| Postgres (dev overlay only) | `34432` | `MARAUDER_DEV_DB_PORT` | 5432 |
| qBittorrent (dev overlay only) | `34611` | `MARAUDER_DEV_QBIT_PORT` | 6611 |
| Transmission (dev overlay only) | `34091` | `MARAUDER_DEV_TRANSMISSION_PORT` | 9091 |
| Keycloak (sso overlay only) | `34643` | `MARAUDER_KEYCLOAK_HOST_PORT` | 8643 |

In the production stack (`docker-compose.yml` only) **only the gateway**
is published to the host. Everything else stays inside the docker
network. The dev (`docker-compose.dev.yml`) and sso
(`docker-compose.sso.yml`) overlays publish additional ports for direct
access during development. Container-internal ports (right column) keep
their conventional values — only the host-side mappings (left column)
must stay in the safe 34xxx range.

### Local dev access (dev overlay) — default hosts & credentials

**Open the app at the gateway, not the frontend container.** The
frontend container (`34001`) serves only the static SPA and does **not**
proxy `/api` — hitting it directly makes login fail with
`Unexpected token '<' ... is not valid JSON` (the API call falls through
to `index.html`). Use the gateway, which proxies both `/` and `/api`:

| What | URL | Credentials |
|---|---|---|
| Marauder app (login here) | http://localhost:34080 | `admin` / `pleasechangeme` |
| Frontend container (SPA only, no API) | http://localhost:34001 | — (don't use for login) |
| qBittorrent WebUI | http://localhost:34611 | `admin` / temp password — see below |
| Transmission WebUI | http://localhost:34091 | no auth (`rpc-authentication-required: false`) |

App login comes from `MARAUDER_ADMIN_INITIAL_USERNAME` /
`MARAUDER_ADMIN_INITIAL_PASSWORD` in `deploy/.env`, seeded **only when
the users table is empty** (`ensureAdmin`, `cmd/server/main.go`).
Changing `.env` after the first boot has no effect — rotate via the UI.

qBittorrent has no fixed password: the linuxserver image mints a random
temporary one on every start until you set a permanent one in the WebUI.
Retrieve the current value with:

```bash
docker logs deploy-qbittorrent-1 2>&1 | grep "temporary password"
```

**Torrent-client config inside Marauder must use Docker service DNS, not
the host ports.** The `localhost:34xxx` mappings only work from your
browser on the host; the backend container reaches the clients on the
internal network by service name + container-internal port:

| Client | RPC/Host URL to enter in Marauder |
|---|---|
| qBittorrent | `http://qbittorrent:6611` |
| Transmission | `http://transmission:9091/transmission/rpc` |

Entering `http://localhost:34611` / `:34091` here fails with a
connection-refused error because `localhost` inside the backend
container is the backend itself.

## Key environment variables

- `MARAUDER_MASTER_KEY` — AES-256 key for credential/config encryption (REQUIRED)
- `MARAUDER_DB_URL` — pgx connection string
- `MARAUDER_SCHEDULER_WORKERS` — worker pool size (default 8)
- `MARAUDER_SCHEDULER_MAX_EPISODES_PER_TICK` — per-episode loop cap (default 25)
- `MARAUDER_OIDC_*` — Keycloak settings (optional, gated by `MARAUDER_OIDC_ENABLED`)
- See `deploy/.env.example` for the full list.

## Plugin development

See `docs/plugin-development.md`. The pattern: implement the
`registry.Tracker` interface (or `Client` / `Notifier`), register via
`registry.RegisterTracker(...)` in `init()`, write a fixture-based
unit test plus an e2e test using `plugins/e2etest.HostRewriteTransport`.

Optional capability interfaces: `WithQuality`, `WithEpisodeFilter`,
`WithCredentials`, `WithCloudflare`, `WithInteractiveLogin`,
`WithSeasonCatalog`, `WithMetadata`. The frontend AddTopic form discovers most via
`GET /api/v1/trackers/match?url=`; `supports_interactive_login` is
**also** surfaced per-tracker in `GET /api/v1/system/info` because the
add-credential form selects a tracker by name and has no URL.
`WithSeasonCatalog` (LostFilm) enumerates a series' released
seasons/episodes from `GET /api/v1/trackers/seasons?url=` (fetches the
public `/series/<slug>/seasons` page, reuses the episode parser); the
AddTopic form uses it to constrain the "start from" season/episode
selectors to released values.

`WithMetadata` (RuTracker, LostFilm) resolves a real title + poster image
from a topic URL. It is called best-effort (fail-open, short timeout) at add
time so a new topic shows a real name + image immediately instead of a
"RuTracker topic 123" placeholder, and powers `GET /api/v1/trackers/preview?url=`
for the AddTopic form's preview card. The scheduler additionally self-heals the
title on each check (`displayNamePersister` → `Topics.UpdateDisplayName`) when a
plugin's `Check` reports a `DisplayName` that differs from what's stored. The
image is stored in `topics.image_url` (migration `0005`) and rendered as a
graceful `<img>` (hidden on load error — no fake fallback).

For per-episode trackers (currently only LostFilm), `Download` must
return `fmt.Errorf("...: %w", registry.ErrNoPendingEpisodes)` when the
pending list is empty so the scheduler's per-episode loop terminates.

### Interactive (captcha) login

Trackers that gate login behind a captcha implement
`registry.WithInteractiveLogin` (`BeginLogin`/`CompleteLogin`/
`RefreshChallenge`) — easiest by embedding a `captchalogin.Engine` built
from a `captchalogin.Config`. The user solves the captcha in-app via
`POST /api/v1/credentials/interactive/{begin,complete,refresh}`; the
harvested session cookie is persisted (encrypted) in
`tracker_credentials.session_enc`. The password is **also** persisted
(encrypted `secret_enc`) on the interactive add so the session can later
be re-established without re-entering credentials. Such a plugin's
`Login` rehydrates the stored cookie into its session jar and validates
it via `Verify`, returning `registry.ErrSessionExpired` when the cookie
is missing/dead. LostFilm is the reference implementation.

**Expiry → notify → re-auth loop:** when `Login` returns
`ErrSessionExpired`, the scheduler atomically marks the credential
(`session_expired_at`) and fires a one-shot notification via `notify`
(deduped — only the check that wins the NULL→now() transition notifies).
The credential view exposes `session_expired`; the UI shows a badge and a
**captcha-only** re-auth dialog backed by
`POST /api/v1/credentials/{id}/reauth/{begin,complete}` — these decrypt
the stored password to fetch a fresh captcha (no credential re-entry) and
`SetSession` clears the expiry marker on success.
