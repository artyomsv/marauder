# Tracker setup guide

Marauder's tracker plugins are how it watches a topic page for updates.
Some plugins work with no setup (public RSS, Newznab, magnet links);
others need a per-user account so they can log in and reach the
content. This page describes the per-tracker requirements and the
optional fields the AddTopic form will surface for each.

The matching companion is [`docs/clients.md`](clients.md), which
covers the *delivery* side (qBittorrent, Transmission, etc.).

---

## How the AddTopic form decides what to show

When you paste a URL on the **Topics → Add topic** form, the frontend
debounces 350 ms and calls `GET /api/v1/trackers/match?url=...`. The
backend looks up the matching plugin and returns a snapshot of every
optional capability:

```json
{
  "tracker_name": "lostfilm",
  "display_name": "LostFilm.tv",
  "qualities": ["SD", "1080p_mp4", "1080p"],
  "default_quality": "1080p",
  "supports_episode_filter": true,
  "requires_credentials": true,
  "uses_cloudflare": false
}
```

The form then renders:

| Capability | UI |
|---|---|
| `qualities` non-empty | Quality `<select>` defaulting to `default_quality` |
| `supports_episode_filter: true` | Two number inputs ("Start season", "Start episode") |
| `requires_credentials: true` | Yellow notice + link to `/accounts` |
| `uses_cloudflare: true` | (Reserved for v0.5 — Cloudflare profile hint) |

---

## Generic plugins (no account required)

### Generic magnet
**Plugin name:** `genericmagnet`
**URL format:** `magnet:?xt=urn:btih:...`

Catch-all for raw magnet URIs. The hash is the infohash itself, so
the topic never "updates" — it's a one-shot ingest. Useful for
bootstrapping your client without going through a tracker.

### Generic .torrent
**Plugin name:** `generictorrentfile`
**URL format:** `https://example.com/path/to/file.torrent`

Catch-all for direct `.torrent` URLs. Marauder downloads the bytes
and submits them. No tracker semantics — same one-shot model as the
magnet plugin.

### Torznab
**Plugin name:** `torznab`
**URL format:** `torznab+https://prowlarr.example.com/api?apikey=...`

Indexer protocol used by Sonarr, Radarr, Prowlarr, Jackett, and
NZBHydra2. Marauder polls the search endpoint and treats new
results as topic updates. See [`docs/torznab-newznab.md`](torznab-newznab.md)
for the full guide.

### Newznab
**Plugin name:** `newznab`
**URL format:** `newznab+https://api.nzbgeek.info/api?apikey=...`

Usenet indexer protocol. Same semantics as Torznab. Pair with the
`downloadfolder` client to drop NZBs into a SABnzbd or NZBGet
watch folder.

---

## CIS forum trackers (account required)

Every plugin in this section implements `WithCredentials` and
expects you to add an account on `/accounts` before adding any
topics. Marauder validates the credential by attempting Login when
you save the account — bad credentials are rejected immediately.

### LostFilm.tv

| | |
|---|---|
| **Plugin name** | `lostfilm` |
| **Account required** | Yes (paid or trial) |
| **Quality selection** | Yes — SD / 1080p_mp4 / 1080p |
| **Episode filter** | Yes |
| **Cloudflare** | No |
| **URL format** | `https://www.lostfilm.tv/series/<slug>/` |

LostFilm is the show-tracking site for Russian-dubbed TV episodes.
The site gates content behind a session cookie, and the actual
.torrent files live behind a multi-stage redirector.

**Marauder's flow when a topic checks:**

1. The scheduler looks up your stored LostFilm credential and POSTs
   it to `/ajaxik.php` to refresh the session cookie.
2. `Check` fetches the series page and parses every
   `data-code="<show>:<season>:<episode>"` marker. The hash is
   derived from the highest `(season, episode)` tuple, so the
   scheduler detects new episodes as soon as the uploader posts them.
3. When a new episode appears, `Download`:
   - POSTs to `/v_search.php` with the `c=<show>&s=<season>&e=<episode>`
     params and the session cookie.
   - Captures the `Location` header (or meta-refresh) — it points
     at an external redirector page (`retre.org`, `lf-tracker.io`,
     etc.).
   - Fetches the destination page and parses the per-quality
     download buttons.
   - Picks the link matching `topic.Extra["quality"]` (defaulting
     to `1080p`).
   - GETs the `.torrent` body and submits it to your default client.

**Episode filter usage:** set `Start season` to 2 and `Start episode`
to 5 on the AddTopic form to skip every episode before S02E05.
Marauder will only download episodes ≥ S02E05.

**Validation status:** the redirector flow follows the public
reverse-engineered shape of the LostFilm site as of 2026-04. Selectors
are constants at the top of `lostfilm.go` so future drift is a one-line
fix. The unit tests cover both the magnet-on-page fallback (preserved
for the test fixture) and the full redirector chain via httptest.

### RuTracker.org

| | |
|---|---|
| **Plugin name** | `rutracker` |
| **Account required** | Yes (free) |
| **Quality selection** | No |
| **Episode filter** | No |
| **Cloudflare** | No |
| **URL format** | `https://rutracker.org/forum/viewtopic.php?t=<id>` |

Largest CIS public-private tracker. One thread = one topic.
Marauder polls the topic page and detects when the uploader
replaces the attached `.torrent`. Free accounts work; the only
gotcha is that the `pp` cookie expires after a few weeks of
inactivity, in which case Marauder transparently re-runs Login
on the next check.

### Kinozal.tv

| | |
|---|---|
| **Plugin name** | `kinozal` |
| **Account required** | Yes (free) |
| **Quality selection** | No |
| **Episode filter** | No |
| **Cloudflare** | No |
| **URL format** | `https://kinozal.tv/details.php?id=<id>` |

Russian movie / TV tracker. Same one-thread-one-topic model as
RuTracker. Hash is the modification timestamp on the page.

### NNM-Club

| | |
|---|---|
| **Plugin name** | `nnmclub` |
| **Account required** | Yes (free) |
| **Quality selection** | No |
| **Episode filter** | No |
| **Cloudflare** | **Yes** — requires the cfsolver sidecar |
| **URL format** | `https://nnmclub.to/forum/viewtopic.php?t=<id>` |

phpBB tracker wrapped in Cloudflare. Marauder routes through the
`cfsolver` sidecar profile (start it with
`docker compose --profile cfsolver up -d`) which uses headless
Chromium via chromedp to solve the Cloudflare interstitial and
hand the cookies back.

### Other CIS trackers

| Plugin name | Display name | Account | Quality | Episode filter | Cloudflare |
|---|---|---|---|---|---|
| `anidub` | AniDub | Yes | Yes | No | No |
| `anilibria` | Anilibria | No (public API) | No | No | No |
| `rutor` | Rutor | No | No | No | No |
| `toloka` | Toloka | Yes | No | No | No |
| `unionpeer` | Unionpeer | Yes | No | No | No |
| `tapochek` | Tapochek | Yes | No | No | No |
| `hdclub` | HD-Club | Yes | No | No | No |
| `freetorrents` | Free-Torrents.org | Yes | No | No | No |

All of these implement `Login`/`Verify` and reach content through
the same session-cookie pattern as LostFilm. Selectors are in
each plugin's package — see
[`docs/plugin-development.md`](plugin-development.md) for the
guide on adding new ones or fixing selector drift.

---

## Adding a tracker account

1. Open **Accounts** in the sidebar (`/accounts`).
2. Click **Add account**.
3. Pick the tracker from the dropdown (only trackers that need
   credentials are listed; trackers you've already configured are
   filtered out).
4. Enter your username/email and password.
5. Click **Login & save**. Marauder calls the plugin's `Login`
   method to validate the credentials before persisting them.
   If Login fails, the credential is **not** stored.

Stored passwords are AES-256-GCM-encrypted at rest with the
master key. Marauder admins cannot decrypt them without the
master key file. Test the credential at any time with the
"Test login" button — it decrypts the secret and re-runs
`Login` + `Verify`.

---

## When a topic check fails

The scheduler logs every check to the topic event history. Common
failure modes for credentialed trackers:

| Symptom | Likely cause | Fix |
|---|---|---|
| `auth failed: lostfilm login failed` | Password rotated externally, or trial expired | Update the credential on `/accounts` |
| `lostfilm: no data-code or data-episode markers found` | LostFilm changed its HTML | Update `dataCodeRe` in `lostfilm.go` and open a PR |
| `lostfilm v_search returned neither Location header nor meta-refresh` | Redirector signature changed | Inspect the v_search response in your browser, update `metaRefreshRe` |
| `lostfilm download page: no per-quality torrent links found` | Quality button selector drifted | Update `qualityLinkRe` |
| `lostfilm GET ... -> 403` | Cloudflare interstitial added | Wait for the cfsolver profile or pull updated cookies |

The selector regexes are deliberately small and named so that a
contributor can fix drift in a single line. See the comments at
the top of `backend/internal/plugins/trackers/lostfilm/lostfilm.go`.
