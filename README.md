<div align="center">

# 🦉 Marauder

### *The forum-tracker monitor that stayed up to date.*

**A modern, self-hosted torrent topic monitor — successor to monitorrent.**

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Status: alpha](https://img.shields.io/badge/status-alpha-orange.svg)](#project-status)
[![Go](https://img.shields.io/badge/backend-Go-00ADD8.svg)](backend/)
[![React 19](https://img.shields.io/badge/frontend-React%2019-61DAFB.svg)](frontend/)
[![Postgres 18](https://img.shields.io/badge/database-Postgres%2018-336791.svg)](deploy/)

[Vision](docs/VISION.md) · [Competitors](docs/COMPETITORS.md) · [PRD](docs/PRD.md) · [Roadmap](docs/ROADMAP.md) · [Changelog](CHANGELOG.md)

</div>

---

## What is Marauder?

Marauder watches torrent tracker topics (RuTracker, LostFilm, Kinozal, NNM-Club,
Anilibria, and friends) for updates and **automatically hands the new
`.torrent` file or magnet link to your torrent client** — qBittorrent,
Transmission, Deluge, uTorrent, or a simple download folder.

It is a ground-up rewrite of the abandoned
[monitorrent](https://github.com/werwolfby/monitorrent) project, built in 2026
with the tools and practices that monitorrent couldn't have had in 2015:

- **Go** backend — single static binary, ~50 MB image, bounded memory.
- **React 19 + Vite 8 + Tailwind 4 + shadcn/ui** frontend — genuinely modern,
  dark-first, keyboard-friendly.
- **PostgreSQL 18.4** for state.
- **Internal JWT + OIDC (Keycloak / Authentik / any OIDC provider)** for auth.
- **Plugin architecture** for trackers, clients, and notifiers.
- **Observable** from day one — Prometheus metrics, structured logs,
  `/health`, `/ready`.

---

## Why another one?

The short version:

> Sonarr, Radarr, and Prowlarr dominate the Torznab/Newznab world. None of
> them can monitor a RuTracker forum thread, because RuTracker isn't a
> Torznab indexer — it's a forum. That's the niche monitorrent filled, and
> since mid-2024 **it stopped working** for most users. Marauder picks up
> where monitorrent left off.

Read the full rationale: [VISION.md](docs/VISION.md) ·
[COMPETITORS.md](docs/COMPETITORS.md).

---

## Project status

> ⚠️ **Alpha.** Marauder is under active development. The vision, PRD, and
> roadmap are stable; the code is not yet. Expect breaking changes until
> **v1.0.0** is tagged. See [ROADMAP.md](docs/ROADMAP.md) for the current
> milestone.

---

## Quick start

> The full stack is designed to run on any machine with Docker installed.
> No Go, Node, or Postgres toolchains required on the host.

```bash
# 1. Clone the repo
git clone https://github.com/artyomsv/marauder.git
cd marauder

# 2. Generate a master encryption key and set up env
cp deploy/.env.example deploy/.env
# Fill MARAUDER_MASTER_KEY with the output of:
openssl rand -base64 32

# 3. Bring the stack up
docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d

# 4. Open the UI
open http://localhost:6688
```

On first start, Marauder creates an admin user from
`MARAUDER_ADMIN_INITIAL_USERNAME` / `MARAUDER_ADMIN_INITIAL_PASSWORD` in the
`.env` file. **Change the password after first login and unset those
variables.**

---

## Architecture at a glance

```
         ┌──────────────┐
         │  React 19    │  shadcn/ui + Tailwind 4 + TanStack Query
         │  frontend    │
         └──────┬───────┘
                │ JSON over HTTPS
                ▼
         ┌──────────────┐       ┌──────────────┐
         │   Go backend │──────►│  PostgreSQL  │
         │   chi + pgx  │       │  18.4        │
         └──┬────┬──────┘       └──────────────┘
            │    │
            │    └──────────┐
            ▼               ▼
     ┌────────────┐   ┌──────────────┐
     │  Tracker   │   │   Torrent    │
     │  plugins   │   │   clients    │
     │ (rutracker,│   │ (qBittorrent,│
     │  lostfilm, │   │  Transmission│
     │  nnm-club, │   │  Deluge,     │
     │   ...)     │   │  ...)        │
     └─────┬──────┘   └──────────────┘
           │
           │ (Cloudflare-protected trackers only)
           ▼
     ┌────────────┐
     │ cfsolver   │  sidecar container: chromium + chromedp
     │ sidecar    │
     └────────────┘

     Optional: ────────────────────────────────────────
     OIDC (Keycloak / Authentik / Authelia) for SSO.
     Prometheus + Grafana for metrics.
     Telegram / Email / Webhook / Pushover for notifications.
```

---

## Tech stack

| Layer | Choice | Why |
|---|---|---|
| Backend | Go 1.23+ | Fast, simple concurrency, single binary, mature HTTP/scraping ecosystem, friendly to contributors. See PRD §2. |
| HTTP router | `chi` | Stdlib-idiomatic, middleware composable, minimal. |
| DB driver | `pgx` v5 + `sqlc` | Type-safe queries generated from SQL. |
| Migrations | `goose` | Embedded, runs at startup, simple. |
| Logging | `zerolog` | Structured, allocation-free. |
| Config | `envconfig` | 12-factor, no YAML. |
| Frontend | React 19.2 + TypeScript | Latest stable, Server Components optional. |
| Build | Vite 8.0.2 | Fast HMR, plugin ecosystem. |
| Styling | Tailwind CSS 4.2 | New `@tailwindcss/vite` plugin, no PostCSS headaches. |
| UI kit | shadcn/ui 4.1.2 | Copy-in components, no vendor lock-in. |
| State | TanStack Query v5 + Zustand | Server state + minimal global UI state. |
| Forms | react-hook-form + zod | |
| Database | PostgreSQL 18.4 | |
| Auth | Internal JWT (ES256) + OIDC (Keycloak etc.) | |
| Secrets | AES-256-GCM at rest | |
| Observability | Prometheus + structured JSON logs | |
| Packaging | Docker + docker-compose | No host dependencies. |
| CI | GitHub Actions | Lint, unit, integration, e2e, trivy, govulncheck. |

---

## Repository layout

```
marauder/
├── backend/            Go backend (chi, pgx, sqlc, goose)
├── frontend/           React 19 + Vite 8 + Tailwind 4 + shadcn
├── deploy/             docker-compose files, .env.example, nginx configs
├── docs/               VISION / COMPETITORS / PRD / ROADMAP / guides
├── CHANGELOG.md        Keep a Changelog format
├── LICENSE             MIT
└── README.md
```

---

## Contributing

Marauder is meant to be easy to extend. Adding a new tracker is a single Go
file implementing the [`Tracker`](docs/PRD.md#51-tracker-plugin-contract)
interface plus a recorded-HTTP-fixture test. The full contribution guide is in
[CONTRIBUTING.md](CONTRIBUTING.md) (coming in v0.2).

For now:

- **Open issues** for bugs, feature ideas, or tracker breakage reports.
- **Discuss design** before large PRs — a quick issue saves a lot of rebasing.
- **Don't** submit PRs adding hard-coded tracker URLs pointing at copyrighted
  content. Marauder is an automation tool, not an index.

---

## License

Marauder is released under the [MIT License](LICENSE) — same as monitorrent.

---

## Legal notice

Marauder is a general-purpose automation tool. It does not host content and
does not ship with any pre-configured tracker URLs. What you choose to
monitor and download is **your responsibility** and subject to the laws of
your jurisdiction and the terms of service of the trackers you use.
