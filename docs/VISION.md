# Marauder — Vision

> *"Set it, forget it, and never miss an episode."*

## The one-sentence pitch

**Marauder** is a self-hosted application that watches torrent-tracker topics for
new releases and hands them off to your torrent client automatically — built from
the ground up in Go with a modern React 19 interface, first-class security, and a
plugin architecture that makes it easy to keep adding new trackers, clients, and
notification targets as the ecosystem evolves.

## The problem

There is a very specific niche the *arr stack does not cover: monitoring
**forum-style torrent trackers** (RuTracker, Kinozal, NNM-Club, LostFilm,
Anilibria, Toloka, and similar CIS-oriented trackers) for updates, and
automatically re-downloading torrents whose content has changed — a new episode,
a new quality re-encode, a re-seed with extra languages. These trackers are
*forum threads*, not API-driven indexers, so Sonarr / Radarr / Prowlarr cannot
reach them.

Tools that historically filled this niche have stalled. The community trackers
keep moving — Cloudflare interstitials get tougher, qBittorrent's Web API
changes shape between releases, NNM-Club moves behind a CDN, LostFilm rotates
its CMS — and the Python-era tools haven't kept up. Users who relied on them are
now **copy-pasting magnet links by hand**.

Meanwhile, the ecosystem Marauder targets has kept moving:

- Torrent clients have evolved (qBittorrent 5.x, Transmission 4.x, Deluge 2.x).
- Cloudflare's bot detection has become significantly more aggressive.
- OIDC / SSO has become the default for self-hosted homelab setups — nobody wants
  yet another username/password pair.
- React, the frontend ecosystem, and CSS tooling have been through two major
  paradigm shifts (React 19, Tailwind 4, shadcn/ui).

That is the gap Marauder is built to fill.

## The users

Marauder is built primarily for a **single persona**, with two secondary ones:

### Primary: the self-hosting homelab enthusiast

- Runs a home server or NAS with Docker.
- Already runs qBittorrent / Transmission / Deluge.
- Already runs Keycloak, Authentik, or another SSO provider for their homelab.
- Watches TV series and anime, cares about re-encodes and multi-language releases.
- Is comfortable with `docker compose up -d` but does **not** want to compile
  anything, fight Python dependencies, or read a 40-page wiki to get started.
- Reads Russian well enough to navigate the tracker forums, or doesn't, and
  just pastes topic URLs from their browser.

### Secondary: the small private-tracker community

- Runs a single shared instance for a handful of users.
- Needs **per-user isolation** (topics, credentials, download targets) and
  **proper access control**, not just "one admin password for everybody."

### Secondary: the library archivist

- Tracks long-running shows, documentaries, or course series over years.
- Wants a **system of record** of what they've been watching and when updates
  arrived — not just a fire-and-forget downloader.

## The change we want to make

1. **Own the forum-tracker niche.** Sonarr/Radarr/Prowlarr are excellent for
   Torznab/Newznab indexers, but they cannot monitor RuTracker, LostFilm, or
   NNM-Club — those trackers are *forum threads*, not API-driven indexers. Marauder
   is the dedicated tool for that world, done right.
2. **Make it boring to keep running.** One `docker compose up -d`. Healthchecks.
   Structured logs. No crashes at 3 AM. Bounded memory. Restart-clean.
3. **Make it pleasant to look at.** A genuinely modern UI — dark-first, glass
   accents, motion — not another Bootstrap 3 admin template. The kind of interface
   that a user *wants* to open, not one they tolerate.
4. **Make it secure by default.** Argon2id for local passwords, JWT refresh-token
   rotation, OIDC/Keycloak as a first-class login mode, CSRF protection, per-user
   data isolation at the database level, and encrypted tracker credentials at rest.
5. **Make it extensible.** A tracker is a 300-line Go file that implements one
   interface. A client is the same. Adding the 13th tracker or a new client
   should be a weekend project for a first-time contributor, not an archaeological
   dig through undocumented Python mixins.
6. **Make observability first-class.** Prometheus metrics, structured JSON logs
   with request IDs, a `/health` and `/ready` endpoint, and a UI "system status"
   page that tells you exactly which tracker runs failed, why, and when.

## Non-goals

These things are explicitly **out of scope**:

- **Marauder is not a torrent client.** It does not speak the BitTorrent protocol,
  does not seed, does not manage peers. It hands `.torrent` files or magnet links
  off to an external client (qBittorrent, Transmission, Deluge, uTorrent).
- **Marauder is not a media library / transcoder.** It will not rename files to
  Plex/Jellyfin conventions, will not transcode, will not scrape metadata from
  TMDB/TVDB. It stops when the torrent is handed to the client.
- **Marauder is not a Torznab/Newznab aggregator.** That's Prowlarr's job. Marauder
  focuses on **forum-style trackers with login and scraping**, which Prowlarr
  fundamentally cannot handle.
- **Marauder is not a general web scraper.** Trackers are first-class plugins, not
  user-defined CSS-selector config blobs. New trackers ship as code, reviewed and
  versioned.
- **Marauder is not a piracy-enabling tool.** It does not host content, does not
  ship a bundled index of copyrighted material, does not bypass DRM. It is a
  personal automation tool — whether what users download is lawful in their
  jurisdiction is their responsibility, and the `README` will say so explicitly.

## What success looks like

At 12 months post-MVP, Marauder is successful if:

- A new user can go from `git clone` to *"the first episode of the show I was
  waiting for just appeared in qBittorrent"* in **under 10 minutes**.
- The container's RSS memory stays **under 150 MB** for a user tracking 200 topics.
- Marauder ships **at least 12 forum-tracker plugins** covering the major CIS
  community trackers, plus Torznab and Newznab adapters for everything else.
- **All four** legacy clients (qBittorrent, Transmission, Deluge, uTorrent) are
  supported, plus a "download-to-folder" fallback.
- The project has accepted at least **5 external contributors** — the plugin
  architecture is pulling its weight.
- A homelab user can log in via **Keycloak** without Marauder ever touching a
  password field.
- The `marauder.cc` documentation site answers *"how do I add a new tracker?"*
  clearly enough that a motivated Go beginner can do it.
