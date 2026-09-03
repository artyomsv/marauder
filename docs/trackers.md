# Tracker setup guide

> **FlareSolverr has no authentication.** Anything that can reach its port can
> drive a real browser to any URL from your network. The solver overlay puts it
> on its own Docker network so only Marauder's backend can reach it; if you host
> it yourself, keep it on a trusted network and never expose it publicly.
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

## Searching trackers from the AddTopic form

Since issue #129 you don't have to hunt down the topic URL in a browser
first: the **Topics → Add topic** card has a **Search trackers** tab.
Type a title, press Search, and Marauder queries every searchable
tracker concurrently (15 s budget each, first result page only, max 50
rows per tracker). Clicking a result switches back to the By-URL tab
with the topic URL prefilled — from there the normal match → preview →
create flow applies unchanged.

Searchable trackers today:

| Tracker | Account needed for search? | Notes |
|---|---|---|
| Rutor | No | Public search; works with zero configuration |
| RuTracker | **Yes** | `tracker.php` is login-gated. Add a RuTracker account under **Accounts** first; without one the search reports "needs a tracker account" and other trackers still return results |
| LostFilm | No | Searches the public series catalog (the site's own search box endpoint). Results are **series** to subscribe to, not individual releases — seeders show as "—" |
| Kinozal | No | Public `browse.php` search (works anonymously; a stored account's session is reused when present). Cyrillic queries are cp1251-encoded |
| Anilibria | No | Searches the AniLiberty v1 API. Results are **release pages** to subscribe to — seeders show as "—" |

Per-tracker failures never block the rest: a tracker that is down,
slow, or missing credentials shows a one-line notice under the results
while the others' results render normally. Searches are also
rate-limited to one in flight per user — every search triggers real
scraping requests against the tracker, and hammering them is how
instances get IP-banned.

Cyrillic queries work on RuTracker: Marauder transcodes the query to
the cp1251 encoding the site expects before sending it.

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

> **NNM-Club** is listed separately below — it is anonymous-only
> and does not require an account.

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
| **Account required** | Optional (free) — a magnet works anonymously, an account gets the full `.torrent` |
| **Quality selection** | No |
| **Episode filter** | No |
| **Cloudflare** | **Yes — a solver is required** |
| **URL format** | `https://rutracker.org/forum/viewtopic.php?t=<id>` |

Largest CIS public-private tracker. One thread = one topic.
Marauder polls the topic page and detects when the uploader
replaces the attached `.torrent`. Free accounts work; the only
gotcha is that the `pp` cookie expires after a few weeks of
inactivity, in which case Marauder transparently re-runs Login
on the next check.

#### RuTracker needs a Cloudflare solver

Since **2026-07-28** RuTracker answers every `/forum/` request from a plain
HTTP client with a Cloudflare challenge. Without a solver, checks, login,
search and downloads all fail, and Marauder reports:

> No Cloudflare solver is configured, and this tracker can't be reached
> without one.

Start one by layering the solver overlay onto whichever stack you run:

```bash
# source-build stack
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.solver.yml up -d

# prebuilt (GHCR) stack
docker compose -f deploy/docker-compose.ghcr.yml -f deploy/docker-compose.solver.yml \
  --env-file deploy/.env up -d
```

The overlay starts a FlareSolverr container **and** sets
`MARAUDER_FLARESOLVERR_URL` for the backend. Both halves matter: a solver
nothing points at is indistinguishable from no solver at all, which is what
[issue #158](https://github.com/artyomsv/marauder/issues/158) was.

Already running FlareSolverr elsewhere (an *arr stack, for instance)? Skip the
overlay and set `MARAUDER_FLARESOLVERR_URL=http://<host>:8191` in
`deploy/.env`. On Kubernetes, `arr.flaresolverr.enabled=true` deploys one and
wires the URL for you.

> **The solver must egress from the same public IP as the backend.**
> Cloudflare binds the clearance to the requesting address, so a solver behind
> a different VPN exit mints cookies Marauder cannot use — that shows up as
> "the solver's clearance was rejected" rather than as a missing solver.

> **Topics that were already failing won't recover instantly.** A missing
> solver takes the ordinary retry backoff, which reaches 6 hours, so topics
> that have been failing for a while keep showing the old error until their
> next scheduled check. Select them on the Topics page and use **Check now** to
> retry immediately rather than concluding the solver didn't work.
Marauder does **not** proxy its traffic through the browser. FlareSolverr
solves the challenge once and returns a `cf_clearance` cookie plus the
User-Agent it was issued for; Marauder replays that pair on its own requests.
Keeping the requests Marauder's own is what lets it submit a login and carry a
binary `.torrent` instead of degrading to a magnet.

### Kinozal

| | |
|---|---|
| **Plugin name** | `kinozal` |
| **Account required** | Yes (free) |
| **Quality selection** | No |
| **Episode filter** | No |
| **Cloudflare** | No |
| **URL format** | `https://kinozal.me/details.php?id=<id>` |

> **Default domain changed 2026-08-03.** The original `kinozal.tv` stopped
> resolving — both `8.8.8.8` and `1.1.1.1` return SERVFAIL — so the plugin
> default is now `kinozal.me`. `kinozal.tv` remains in the allowlist so
> topic URLs already stored against it keep working, and `kinozal.guru` is
> available as a mirror. Each mirror brands the page `<title>` after its
> own domain (`Кинозал.МЕ`, `Кинозал.GURU`), which the title cleaner now
> strips by pattern rather than by one hardcoded suffix.

Russian movie / TV tracker. Same one-thread-one-topic model as
RuTracker. The infohash is read from the authenticated
`get_srv_details.php?id=<id>&action=2` endpoint (the details page itself
doesn't expose it); the display title + poster come from the details
page `<title>` (cp1251-decoded) and `og:image`.

**Validation status:** verified end-to-end against a live Kinozal account
(2026-06) — login, infohash resolution, metadata, and download → client
delivery all confirmed.

---

## NNM-Club (anonymous, no account)

| | |
|---|---|
| **Plugin name** | `nnmclub` |
| **Account required** | No (anonymous) |
| **Quality selection** | No |
| **Episode filter** | No |
| **Cloudflare** | Site is behind Cloudflare, but no solver is needed in practice |
| **URL format** | `https://nnmclub.to/forum/viewtopic.php?t=<id>` |

phpBB tracker behind Cloudflare. Works anonymously — account login is
not supported because Cloudflare Turnstile blocks automated login
flows. No credentials needed; do **not** add an NNM-Club account entry
on `/accounts`.

**No sidecar required.** Earlier revisions of this page told you to start
a `cfsolver` profile. That is obsolete twice over: the `cfsolver` service
was superseded by the FlareSolverr clearance minter, and NNM-Club does
not currently serve an interstitial to Marauder at all — topic pages
return the real HTML to an ordinary Go request. Cloudflare policy is
dynamic and per-IP, so the clearance path remains available if your
egress does get challenged, but it is not part of normal setup.

**Validation status:** verified end-to-end against the live site on
2026-08-03 with no credentials and no solver — `Check` resolved a real
infohash and title from a public release topic, `Download` produced a
magnet, and `ResolveMetadata` returned the title and poster.

---

## Rutor (anonymous, no account)

| | |
|---|---|
| **Plugin name** | `rutor` |
| **Account required** | No (anonymous) |
| **Quality selection** | No |
| **Episode filter** | No |
| **Cloudflare** | No |
| **URL format** | `https://rutor.info/torrent/<id>/<slug>` |

Public Russian-language tracker. One page = one release. Marauder reads
the release magnet from the topic page, then upgrades it to the real
`.torrent` file from the mirror's download host
(`https://d.rutor.info/download/<id>`, falling back to
`https://rutor.info/download/<id>`) — a magnet needs peer discovery to
resolve its metadata, the file does not. The file is only accepted when
its infohash matches the page magnet; otherwise Marauder falls back to
the magnet rather than delivering a mismatched torrent. That fallback
magnet keeps the announce URLs the page published, so it does not depend
on DHT alone. When no mirror serves a usable file Marauder logs a
warning — a permanently broken download host would otherwise downgrade
every rutor delivery silently.

> **Default domain changed 2026-09-03.** The original `rutor.org` is a
> **stale clone**: its newest release was id 1087871 while the live mirrors
> were already at 1104882 — roughly 17,000 releases behind — and it answers
> current topic ids with `404 Раздача не существует`. The plugin default is
> now `rutor.info`, with `new-rutor.org` as the mirror. Both live mirrors
> share one id space, so a topic URL from either resolves against the other.
> `rutor.org` still parses so topics added before the change keep working,
> but Marauder no longer sends requests to it and domain rotation can no
> longer land there.
>
> Only `rutor.info` serves `.torrent` bytes, on both `d.rutor.info/download/<id>`
> and plain `rutor.info/download/<id>`. `new-rutor.org` serves neither:
> `d.new-rutor.org` presents a certificate that does not cover it, and its own
> `/download/<id>` answers 200 with an HTML page. A topic stored against
> `new-rutor.org` therefore fetches its file from `rutor.info` instead.

**Validation status:** verified end-to-end against the live site on
2026-09-03 with no account — change detection, a real `.torrent` delivered
for topics on **both** live mirrors, title and poster resolution, and
search. On every topic tested the page magnet's infohash matched the
downloaded `.torrent` exactly. The check is re-runnable:

```bash
docker run --rm -v "$PWD/backend:/backend" -w //backend golang:1.25 \
  go test -tags=live -run TestLive -v ./internal/plugins/trackers/rutor/...
```

---

## Other CIS trackers

| Plugin name | Display name | Account | Quality | Episode filter | Cloudflare |
|---|---|---|---|---|---|
| `anidub` | AniDub | Yes | Yes | No | No |
| `anilibria` | AniLiberty | No (public API) | No | No | No |
| `toloka` | Toloka | Yes | No | No | No |
| `tapochek` | Tapochek | Yes | No | No | No |

> **Removed 2026-08-03.** `hdclub`, `unionpeer` and `freetorrents` were
> deleted after each of their domains was probed and found not to serve a
> tracker any more: HD-Club shut down in 2017 and `hdclub.org` now
> redirects to an unrelated file-hosting affiliate site; every Unionpeer
> domain is either a parking page or NXDOMAIN; `free-torrents.org` has no
> DNS A record. Topics stored against them will report a missing plugin.

All of these implement `Login` and reach content through the same
session-cookie pattern as LostFilm. Selectors are in each plugin's
package — see [`docs/plugin-development.md`](plugin-development.md)
for the guide on adding new ones or fixing selector drift.

**Not every plugin can confirm a login.** `Verify` is the second,
independent signal that a session is really authenticated — it fetches
a page and looks for a positive logged-in marker. `toloka` has no known
marker yet, so it returns
`registry.ErrVerifyUnsupported` rather than claiming a check they did
not make. Adding or testing an account for those trackers saves the
credential and shows an amber **"could not be verified"** notice
instead of a green tick. The credential still works; Marauder is only
declining to imply it validated something it could not. See
[`docs/plugin-development.md`](plugin-development.md) if you can
identify a logged-in marker for one of them.

**AniDub validation status:** verified end-to-end against the live
tracker with a real account on 2026-08-02 — login rejection detection,
session verification, title/poster resolution, change detection, and a
real `.torrent` delivered to qBittorrent. Note that AniDub publishes no
infohash on its topic pages, so the plugin's change token is derived
from the torrent block (id, filename, size) rather than a page hash;
seeder counts are deliberately excluded because they would otherwise
make every check look like a new release.

AniLiberty is the public-API exception in this group. Current
`https://aniliberty.top/anime/releases/release/<alias>` URLs use the official
AniLiberty v1 API; legacy `https://anilibria.tv/release/<slug>.html` URLs
remain supported through the v3 API.

**Validation status:** verified read-only against the live AniLiberty v1 API
as of 2026-07 (release search and torrent listing endpoints; all torrent
records carry 40-hex infohashes, matching magnet `btih` values, and populated
`updated_at`). The full URL → API → magnet → qBittorrent pipeline is covered
by fixture-based e2e tests; Sonarr-created topics keep the grabbed
codec/quality variant across infohash changes.

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
| No Cloudflare solver is configured | RuTracker is challenge-gated and no solver was started | Layer `docker-compose.solver.yml` onto your stack (see [RuTracker](#rutracker-needs-a-cloudflare-solver)) |
| The Cloudflare solver did not answer | FlareSolverr is down, still launching Chrome (~9s), or unreachable | Check the container is running; a boot-time occurrence clears itself on the next check |
| Blocked by Cloudflare — the solver's clearance was rejected | Solver and backend egress from different public IPs | Put both behind the same exit, or neither |

The selector regexes are deliberately small and named so that a
contributor can fix drift in a single line. See the comments at
the top of `backend/internal/plugins/trackers/lostfilm/lostfilm.go`.
