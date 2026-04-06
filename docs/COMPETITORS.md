# Marauder — Competitive Landscape

This document maps the space Marauder sits in, identifies the projects that users
are most likely to reach for *instead of* Marauder, and explains where Marauder
stops, where it competes, and where it complements.

The short version: **nothing currently in active development does what Marauder
does well**. Sonarr/Radarr/Prowlarr dominate the Torznab/Newznab world, but have
never supported forum-style trackers with login. FlexGet can technically do it,
but requires users to write YAML. Monitorrent *was* the right shape — it is no
longer maintained.

---

## At a glance

| Project | Focus | Covers forum trackers (RuTracker, LostFilm, NNM-Club)? | Actively maintained? | License | UI |
|---|---|---|---|---|---|
| **Marauder** *(this project)* | Forum-tracker monitoring + client delivery | ✅ Yes, first-class | ✅ | MIT | Modern React 19 |
| [monitorrent](https://github.com/werwolfby/monitorrent) | Forum-tracker monitoring + client delivery | ✅ (historically) | ❌ Stalled since mid-2024 | MIT | Legacy Aurelia |
| [Sonarr](https://sonarr.tv/) | TV show automation | ❌ Torznab only | ✅ | GPLv3 | Good |
| [Radarr](https://radarr.video/) | Movie automation | ❌ Torznab only | ✅ | GPLv3 | Good |
| [Lidarr](https://lidarr.audio/) | Music automation | ❌ Torznab only | ✅ | GPLv3 | Good |
| [Prowlarr](https://prowlarr.com/) | Indexer aggregation for *arr stack | ❌ Torznab/Newznab only | ✅ | GPLv3 | Good |
| [Jackett](https://github.com/Jackett/Jackett) | Tracker → Torznab shim | ⚠️ Partial (search only, no monitoring) | ✅ | GPLv2 | Basic |
| [FlexGet](https://flexget.com/) | General content automation | ⚠️ Via YAML + community plugins | ✅ | MIT | Minimal webui |
| [TorrentMonitor](https://hub.docker.com/r/alfonder/torrentmonitor) | monitorrent-lite fork | ✅ partial | ⚠️ Sporadic | Unclear | Legacy |

---

## The *arr stack: Sonarr, Radarr, Lidarr, Readarr

### What it is

The "Servarr" family is the mainstream solution for English-speaking homelab
users. Each *arr handles a different media type:

- **Sonarr** — TV series.
- **Radarr** — movies.
- **Lidarr** — music albums.
- **Readarr** — books.
- **Prowlarr** — indexer manager that syncs tracker credentials across all of them.

They share a codebase, a UI, and a philosophy: you tell them what you *want*,
they watch a set of Torznab/Newznab indexers, and they pick the best quality
release matching your profile.

### Why they don't solve Marauder's problem

Sonarr, Radarr, and Prowlarr are built around the **Torznab/Newznab** API
contract. A "tracker" in their world exposes a structured search endpoint that
returns JSON or XML, categorised by content type, with a standard field set
(title, size, seeders, category, publish date).

**CIS forum trackers do not work this way.** RuTracker is a phpBB-style forum.
LostFilm is a bespoke CMS with per-series threads. NNM-Club is a phpBB variant
wrapped in Cloudflare. Kinozal is yet another bespoke phpBB skin. What they
have in common:

- Authentication by **session cookie**, obtained via an HTML login form.
- Content discovery by **browsing topic threads**, not by querying an API.
- **Updates in place** — the same topic URL will have a new `.torrent` attachment
  when a new episode is released. You detect the update by watching the topic
  page, not by polling a search feed.
- **Cloudflare** interstitials that need to be solved to even reach the login page.
- **Per-user download quotas, hit-and-run rules, and ratio limits** that mean
  the client needs to actually keep the file seeding, not just grab-and-go.

Jackett exists to bolt a Torznab shim onto forum trackers for the *arr stack,
but the shim only does **search**, not **topic monitoring**. You cannot use
Jackett + Sonarr to watch a specific RuTracker topic for updates — that's not
what the contract supports.

Marauder is built for the monitoring case. It is a **complement** to Sonarr /
Radarr / Prowlarr, not a replacement. If you need to auto-download the latest
Hollywood movies from a Torznab indexer, keep using Radarr. If you need to
watch a specific forum thread on RuTracker and get the `.torrent` file the
moment the uploader posts an updated version, Marauder is the tool.

### Where Marauder beats them

- **Direct support for forum trackers.** No Torznab shims, no workarounds.
- **Topic-level monitoring**, not search-based.
- **OIDC / Keycloak out of the box** — *arr apps have no native SSO; you bolt on
  Authelia in front.

### Where they beat Marauder

- **Battle-tested.** Millions of installs, years of polish.
- **Quality-profile matching** across many releases of the same content.
- **Metadata integration** with TMDB, TVDB, MusicBrainz.
- **File renaming and library organization.**

---

## Jackett

### What it is

A translation layer. Jackett takes a list of forum/private trackers and exposes
them over the Torznab API so that Sonarr, Radarr, and friends can search them.
It supports ~500 trackers through a YAML-definition format.

### Why it doesn't solve Marauder's problem

Jackett is a **search proxy**, not a **monitor**. You can ask it "find me the
latest Rick and Morty 1080p releases across my 40 trackers" and get a merged
list back. You cannot ask it "watch this specific topic URL and tell me when
the attached `.torrent` changes."

Jackett also **does not hand torrents to clients** — it just returns search
results. The *arr app on top has to do that.

### Where Marauder beats it

- Topic-level monitoring is first-class.
- Direct delivery to qBittorrent/Transmission/Deluge, no intermediary *arr.
- Structured plugin interface in Go, not YAML definitions that silently break
  when trackers change their HTML.
- Per-user credential isolation — Jackett is single-user by design.

### Where it beats Marauder

- Breadth of tracker support — Jackett covers hundreds.
- Integrated with the *arr ecosystem.

---

## FlexGet

### What it is

FlexGet is a general-purpose content automation engine written in Python. It
has ~300 plugins, its own YAML DSL, and can orchestrate RSS feeds, HTML
scraping, quality profiles, and delivery to torrent clients. A community plugin
adds RuTracker support. It has been actively maintained for over a decade
(latest release January 2026).

### Why it doesn't solve Marauder's problem (for most users)

FlexGet is **a DSL, not an application**. To use it for the "watch RuTracker
topic XYZ and send updates to qBittorrent" job, you write something like:

```yaml
tasks:
  rutracker-watch:
    rutracker:
      username: "{? rutracker.username ?}"
      password: "{? rutracker.password ?}"
    rutracker_topic: 1234567
    accept_all: yes
    transmission:
      host: localhost
      username: user
      password: pass
```

That is genuinely powerful — and genuinely inaccessible to the user who wants to
paste a URL into a web UI and walk away. There is no first-class web interface
for non-technical users; the official webui is minimal and most users run
FlexGet from the CLI. Credentials live in YAML files on disk. There's no
multi-user story. Troubleshooting means reading Python stack traces.

### Where Marauder beats it

- **Zero YAML.** Paste URL, save, done.
- Web UI, multi-user, RBAC, OIDC.
- Credentials encrypted at rest, not stored in flat YAML.
- Observability built in (Prometheus metrics, structured logs, system status
  page).

### Where it beats Marauder

- **Breadth.** FlexGet does RSS, HTML scraping, Usenet, podcasts, and so on.
  Marauder is deliberately focused on forum-tracker monitoring.
- **Scripting.** For users who want a programmable pipeline with arbitrary
  quality logic, FlexGet is more expressive.

---

## monitorrent (the incumbent)

### What it is

The project Marauder is built to replace. A Python 3 / Aurelia application that
monitors forum trackers and hands torrents to clients. MIT-licensed. 12 tracker
plugins, 5 client plugins, 5 notification backends.

### Why it no longer solves the problem

- **Stalled.** No release since 1.4.0 (July 2023). No active maintainer.
- **Broken trackers.** LostFilm, RuTracker, NNM-Club all have months-old open
  issues about broken scraping.
- **Broken clients.** qBittorrent ≥ 4.5 API changes are not handled (#402).
- **Cloudflare bypass is outdated.** The built-in solver uses old Playwright
  heuristics that Cloudflare now detects (#363, #407).
- **Memory leaks and crashes** (#393, #397).
- **Unicode issues** with Cyrillic paths — marked *wontfix* because of a library
  dependency.
- **Aurelia frontend** is a paradigm most front-end developers have never touched,
  making contribution difficult. The build toolchain is frozen in ~2019.
- **No SSO story.** Single hard-coded login, plain password, no MFA.

### Where it still wins (today)

- **Works for users whose trackers still work.** Inertia has value.
- **Familiar UI** for existing users.

### Why Marauder is a clean rewrite, not a fork

- The underlying libraries (old Aurelia, old Python scraping stack, outdated
  Cloudflare bypass) are a dead end, not a foundation.
- The plugin architecture (mixins, abstract classes) is tangled with the ORM
  and hard to modernize incrementally.
- A Go rewrite gives us a single static binary per platform, a tenth of the
  memory footprint, and a plugin model that is actually pleasant to extend.
- Starting clean lets us get **security, OIDC, observability, and multi-user
  support right from day one**, rather than bolting them onto a codebase that
  didn't anticipate them.

---

## Other / smaller / archived

- **[TorrentMonitor](https://hub.docker.com/r/alfonder/torrentmonitor)** — a
  stripped-down monitorrent-adjacent project focused on RuTracker, RuTor, and
  LostFilm. Updates are irregular and documentation is thin.
- **pyMediaManager** — Django-based personal project, not maintained as a
  product; effectively abandonware.
- **Custom shell scripts** (`curl` + `cron` + `rtorrent` watch folder) — the
  "zero-dependency" approach many power users fall back to when monitorrent
  breaks. It works, but there is no UI, no state, no error reporting, no
  updates-when-the-HTML-changes.
- **[Sonarr Profilarr](https://corelab.tech/sonarr-radarr-profilarr-native-hunting-protocol/)**
  — a 2026 tool for managing quality profiles across Sonarr/Radarr. Complementary,
  not competitive.

---

## How Marauder positions itself

> **Marauder is the forum-tracker monitor that the *arr stack never was.**
>
> If your trackers expose Torznab, use Prowlarr + Sonarr + Radarr.
> If your trackers are forum threads that need login, scraping, and topic
> monitoring — that's Marauder.

And where the two worlds overlap, Marauder is built to **co-exist**, not
compete: the same qBittorrent instance can accept downloads from Sonarr,
Radarr, and Marauder simultaneously. Use the right tool for each source.
