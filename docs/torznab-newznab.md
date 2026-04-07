# Torznab and Newznab Indexers

Marauder ships with first-class support for the two indexer protocols
that drive the entire English-speaking self-hosted scene:

- **Torznab** — used by Jackett, Prowlarr, NZBHydra2 (in torrent
  mode), and Sonarr/Radarr to talk to torrent indexers
- **Newznab** — used by NZBGeek, NZBPlanet, DOGnzb, NZBHydra2 (in
  Usenet mode), and any other Usenet indexer

These two plugins together unlock **several hundred** indexers without
Marauder having to ship a scraper for any of them. If your tracker is
already exposed by Jackett or Prowlarr, Marauder can monitor it.

---

## How the model fits

Marauder's tracker abstraction is "watch a URL, detect updates by
hash". Torznab/Newznab are search-based: you give the indexer a query
and it returns a list of releases newest-first.

The two map cleanly:

| Marauder concept | Torznab/Newznab equivalent |
|---|---|
| Topic URL | The indexer search URL with `?q=...` |
| Hash | The GUID (or `infohash`) of the newest item in the feed |
| "Update detected" | A new release matching the search has appeared |
| Download payload | The first item's `<enclosure>` URL |

So a single Marauder topic pointing at
`torznab+https://prowlarr/.../?q=Some+Show` becomes a permanent
"follow this show" subscription that grabs every new episode that
your indexer publishes — the same UX as Sonarr's wanted list, but
driven by Marauder's existing scheduler and client routing.

**One Marauder topic per show or movie you want to follow.**

---

## URL format

Both plugins use an explicit URL prefix so the auto-detected tracker
selection is unambiguous:

```
torznab+https://prowlarr.example.com/api/v2.0/indexers/rutracker/results/torznab?apikey=KEY&t=search&q=Some+Show
newznab+https://nzbgeek.info/api?apikey=KEY&t=tvsearch&q=Some+Show&season=1
```

The `torznab+` / `newznab+` prefix is stripped before the actual
HTTPS request is made.

---

## Step-by-step: Torznab via Prowlarr

1. **In Prowlarr**, configure the indexer you want to follow (e.g.
   RuTracker via the Prowlarr indexer definition).
2. Open the indexer's "Test" or "Sync" page and copy the **Torznab
   feed URL**. It looks like:

   ```
   https://prowlarr.example.com/12/api?apikey=YOUR_KEY&t=search
   ```

3. Append your search terms:

   ```
   https://prowlarr.example.com/12/api?apikey=YOUR_KEY&t=search&q=Some+Show+S01
   ```

4. Open the Marauder UI, click **Add topic**, and paste:

   ```
   torznab+https://prowlarr.example.com/12/api?apikey=YOUR_KEY&t=search&q=Some+Show+S01
   ```

5. Marauder detects the `torznab` plugin from the prefix, parses the
   indexer URL, and on the next scheduler tick fetches the feed.
6. Configure a **qBittorrent** (or Transmission/Deluge) client and
   set it as default. New releases are sent there automatically.

---

## Step-by-step: Newznab via NZBGeek

1. Sign in to **NZBGeek** (or any Newznab indexer) and copy your API
   key from the profile page.
2. Build the search URL:

   ```
   https://nzbgeek.info/api?apikey=YOUR_KEY&t=tvsearch&q=Some+Show&season=1
   ```

3. Open the Marauder UI, click **Add topic**, and paste:

   ```
   newznab+https://nzbgeek.info/api?apikey=YOUR_KEY&t=tvsearch&q=Some+Show&season=1
   ```

4. Configure a **downloadfolder** client pointed at your SABnzbd or
   NZBGet watch directory:

   ```
   /path/to/sabnzbd/watch
   ```

5. When a new release matches the search, Marauder downloads the
   `.nzb` file and writes it to the watch folder. SABnzbd / NZBGet
   pick it up automatically.

> **Note:** Marauder does NOT speak the Usenet protocol itself. The
> Newznab plugin is purely a "watch the indexer feed and drop the
> .nzb in a folder" pipeline. The actual Usenet download is handled
> by your existing SABnzbd / NZBGet install.

---

## Categories

Both Torznab and Newznab use category numbers to filter results.
Append `&cat=...` to your URL:

| Category | Number |
|---|---|
| Movies (any) | `2000` |
| Movies HD | `2040` |
| TV (any) | `5000` |
| TV HD | `5040` |
| TV UHD | `5045` |
| Anime | `5070` |
| Music (any) | `3000` |

Example — TV HD only:

```
torznab+https://prowlarr.example.com/12/api?apikey=KEY&t=search&q=Some+Show&cat=5040
```

---

## What Marauder uses from the response

The XML feed shape Marauder reads is the standard Torznab/Newznab
RSS+attr layout:

```xml
<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed">
  <channel>
    <item>
      <title>The Show S01E12 1080p WEB-DL</title>
      <guid>https://example.com/details/9001</guid>
      <pubDate>Mon, 06 Apr 2026 18:00:00 +0000</pubDate>
      <enclosure url="magnet:?xt=urn:btih:..." type="application/x-bittorrent"/>
      <torznab:attr name="seeders" value="42"/>
      <torznab:attr name="infohash" value="..."/>
    </item>
  </channel>
</rss>
```

For the **hash**, Marauder uses (in priority order):

1. The `torznab:attr name="infohash"` value if present
2. Otherwise the `<guid>` field

For the **download**, Marauder uses the `<enclosure>` URL of the
first item:

- Torrent indexers: this is usually a `magnet:` URI; Marauder hands
  it directly to qBittorrent / Transmission / Deluge / uTorrent
- Newznab: this is a `.nzb` URL; Marauder downloads the bytes and
  passes them through to a `downloadfolder` client

For the **display name**, Marauder uses the first item's `<title>`
in the topic detail view, and extracts the `?q=` value from the URL
for the topic list summary.

---

## Validation

Both plugins are **E2E tested** against fixture-driven httptest
servers using the canonical RSS+attr response format. The test
files are at:

- [`backend/internal/plugins/trackers/torznab/torznab_e2e_test.go`](../backend/internal/plugins/trackers/torznab/torznab_e2e_test.go)
- [`backend/internal/plugins/trackers/newznab/newznab_e2e_test.go`](../backend/internal/plugins/trackers/newznab/newznab_e2e_test.go)
- [`backend/internal/plugins/trackers/torznabcommon/parser_test.go`](../backend/internal/plugins/trackers/torznabcommon/parser_test.go)

For **live validation** against your own indexer:

1. Bring the dev stack up (`docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d`).
2. Add a Torznab topic via the UI as described above.
3. Wait for one scheduler tick (default 60 s).
4. Open the **System** page in the Marauder UI and look for the
   topic in the run history — it should show `checked: 1, updated: 1, errors: 0`.
5. Open your qBittorrent / SABnzbd UI and confirm the latest matching
   release was added.

If anything goes wrong, the per-topic error message in the topic
list will tell you exactly what failed.

---

## Why this is a big deal

Sonarr, Radarr, Prowlarr, Jackett, and NZBHydra2 collectively
support **more than 500 indexers**. Marauder's two plugins reach
all of them by speaking the same RSS protocol the *arr stack speaks.

This means a Marauder install can simultaneously:

- Monitor Russian forum trackers (RuTracker, Kinozal, NNM-Club, etc.)
  via the dedicated forum-tracker plugins
- Monitor any Torznab indexer (anywhere in the world) for new releases
- Monitor Usenet indexers (NZBGeek, etc.) and drop NZBs into a
  watch folder

…all from a single dashboard, with one scheduler, one set of
notification rules, and one access-controlled UI.
