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
| `db` / `db/repo` | pgxpool wrapper + repository structs (`Topics`, `Clients`, `Notifiers`, `Users`, `TrackerCredentials`, `Audit`, `Settings`) |
| `domain` | core types: `Topic`, `Check`, `Payload`, `TrackerCredential` |
| **`extra`** | shared `extra.Int / StringSlice / String` helpers for the untyped `map[string]any` blobs in `Topic.Extra` and `Check.Extra` (added 2026-04-07; **use this instead of writing local helpers**) |
| `logging` | zerolog setup (JSON in prod, pretty in dev) |
| `metrics` | Prometheus collectors (HTTP, scheduler, tracker, client) |
| `plugins/registry` | plugin interfaces + global registry + capability interfaces (`WithQuality`, `WithEpisodeFilter`, `WithCredentials`, `WithCloudflare`) + **`registry.ErrNoPendingEpisodes`** typed sentinel |
| `plugins/trackers/<name>` | one package per tracker plugin (16 plugins as of v1.0.0+) |
| `plugins/clients/<name>` | one package per torrent client (qBittorrent, Transmission, Deluge, µTorrent, downloadfolder) |
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

The scheduler depends on small **consumer-side interfaces** (`topicsRepo`,
`markEpisodeDownloader`, `clientsRepo`, `credentialsRepo`, `decryptor`)
plus two lookup-fn seams (`trackerLookupFn`, `clientLookupFn`) so it's
unit-testable without DB or registry. Tests live in `scheduler_test.go`.

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
docker run --rm -v "E:/Projects/Stukans/Prototypes/torrent/backend:/backend" -w //backend golang:1.23 sh -c "go build ./... && go vet ./... && go test -race ./..."

# Frontend
docker run --rm -v "E:/Projects/Stukans/Prototypes/torrent/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test && npm run build"

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
`WithCredentials`, `WithCloudflare`. The frontend AddTopic form
discovers them via `GET /api/v1/trackers/match?url=`.

For per-episode trackers (currently only LostFilm), `Download` must
return `fmt.Errorf("...: %w", registry.ErrNoPendingEpisodes)` when the
pending list is empty so the scheduler's per-episode loop terminates.
