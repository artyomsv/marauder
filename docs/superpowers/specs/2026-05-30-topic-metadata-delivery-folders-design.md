# Topic Metadata, Delivery Tracking & Uniform Folders — Design

**Date:** 2026-05-30
**Status:** Approved (brainstorm), pending implementation
**Goal:** Make topics self-describing (real title + poster image, auto-resolved per
tracker), show what each topic has delivered to its client (with optional live
download progress), and give every client a base download folder that topic
*categories* nest under uniformly.

This bundles five user-requested adjustments into three independently shippable
stages (PR-A, PR-B, PR-C).

---

## Unifying model

Three structural decisions tie the five requests together:

1. **One tracker capability resolves human metadata.** A new optional interface
   `WithMetadata` returns `{Title, ImageURL}` for a URL. It serves the "real
   display name" request (#3), the "show an image" request (#2), and the
   "redundant source in name" request (#5) — because once the title is the real
   show/release name, the existing source badge makes any tracker prefix
   obsolete. Each tracker plugin owns its own extraction (the "abstraction"
   the user asked for).

2. **One deliveries record underpins both progress tiers.** A new
   `topic_deliveries` table stores, per push, `{topic_id, infohash, label,
   client_id, delivered_at}`. Tier 1 (always) reads it to list "delivered"
   items with human-readable labels (`S01E05`). Tier 2 (where supported) feeds
   those infohashes to the client's status query for a live percent/finished
   badge. The **BitTorrent infohash is the universal key**: Marauder already
   parses the BTIH of every torrent it pushes; qBittorrent queries by `hashes=`
   and Transmission's `torrent-get` accepts `hashString` — same identifier.

3. **Category is a path segment, not a client-native label.** Each client config
   gains an optional base `download_dir`. The effective save path is computed
   once, in the scheduler:
   `effective = topic.DownloadDir (explicit override) || join(client.download_dir, topic.Category)`.
   Every client that already honors `opts.DownloadDir` then behaves identically,
   including Transmission (which has no native category). qBittorrent's
   native-category config field is **removed** in favor of this model.

---

## Stage PR-A — Uniform client folder + category subfolder (#4)

**Scope:** smallest, pure wiring; `Topic.DownloadDir`/`Topic.Category` and
`AddOptions{DownloadDir, Category}` already exist.

### Backend
- **Client config schema:** add optional `download_dir` (base folder) to each
  plugin's `ConfigSchema()` and config struct. Remove qBittorrent's `category`
  config field.
- **Scheduler `sendViaClient`:** compute the effective save path
  `topic.DownloadDir || path.Join(clientBaseDir, topic.Category)` (forward
  slashes; `path.Join`, not `filepath`, since these are remote client paths) and
  pass it as `opts.DownloadDir`. Stop relying on per-client category.
- **Clients that honor DownloadDir:** qBittorrent (`savepath`), Transmission
  (`download-dir`), Deluge (`download_location`), downloadfolder (`path`). µTorrent
  still cannot set a path per add — document that it ignores the base folder.
- The base folder lives inside the encrypted config blob; no new column.

### Frontend
- **Client form (`Clients.tsx` `fieldsForPlugin`):** add a "Base download folder"
  field to every plugin's field list. Remove qBittorrent's category field.
- **Topic form:** `category` input stays; relabel its helper text to "nested
  under the client's base folder".
- **i18n:** new keys `clients.field.downloadDir(.hint)`, updated
  `topics.add.category` hint.

### Success check
qBittorrent + Transmission both deliver into `<base>/<category>`; a topic with an
explicit `DownloadDir` overrides; backend `go test -race ./...` green; client
config round-trips the new field.

---

## Stage PR-B — Auto title + poster image (#2, #3, #5)

### Backend
- **DB `0005`:** `ALTER TABLE topics ADD COLUMN image_url TEXT;` Add
  `domain.Topic.ImageURL string` (no json tag, PascalCase-read like its siblings).
  Update `scanTopic`, insert, and update SQL/repo.
- **Registry `WithMetadata`:**
  ```go
  type Metadata struct { Title string; ImageURL string }
  type WithMetadata interface {
      ResolveMetadata(ctx context.Context, rawURL string, creds *domain.TrackerCredential) (*Metadata, error)
  }
  ```
- **Trackers:** RuTracker (first-post `<title>` cleaned of ` :: RuTracker.org`;
  first content `<img>` / `<var class="postImg">` src) and LostFilm (series
  `<title>` cleaned; poster image from the series page) implement `WithMetadata`.
  Reuse the page-fetch helpers they already call in `Check`.
- **Create handler:** after `Parse`, if the tracker implements `WithMetadata`,
  call it with a short `context.WithTimeout` (best-effort). On success seed
  `DisplayName` (unless the user supplied one) and `ImageURL`. On failure keep the
  Parse placeholder — never block topic creation.
- **Scheduler self-heal:** persist `check.DisplayName` back to the topic when it
  differs (fixes the existing latent bug where the real title is parsed but never
  saved). Image is resolved once at add-time; not re-fetched per check.
- **New endpoint `GET /api/v1/trackers/preview?url=`:** runs `ResolveMetadata`
  (no creds), returns `{title, image_url}` for the add-form preview. Trackers
  without the capability return an empty body / 204.

### Frontend
- **`lib/api.ts`:** add `ImageURL: string` to `Topic`; add `previewTracker(url)`
  → `{title, image_url}`.
- **Topic row (`Topics.tsx`):** render an `<img>` poster thumbnail with
  `onError` → hide; show the real `DisplayName` (no source prefix); the existing
  tracker badge already conveys source (#5 satisfied).
- **Add form:** debounced (`useDebouncedValue`) call to `previewTracker` after a
  valid URL; show poster + resolved title as a preview card; prefill the display
  name field when empty.
- **i18n:** `topics.preview.*`, image alt text.

### Success check
Adding a RuTracker URL shows the real torrent title + image immediately; a
LostFilm series shows show title + poster; existing placeholder topics upgrade to
real titles after one scheduled check; broken image URLs disappear cleanly;
frontend `npm run typecheck && npm test && npm run build` green.

---

## Stage PR-C — Delivery tracking + live progress (#1)

### Backend
- **DB `0006`:** `topic_deliveries (id, topic_id FK CASCADE, infohash TEXT,
  label TEXT, client_id UUID, delivered_at TIMESTAMPTZ)`, index on `topic_id`.
- **Scheduler:** on each successful `Add`, capture the pushed torrent's infohash
  (from the `Payload`/`check.Hash`) and a human label — for episodic, decode the
  packed episode id into `S{season:02}E{episode:02}`; for single-torrent, the
  release/display name — and insert a `topic_deliveries` row. This is **Tier 1**
  and is never blocked.
- **Registry `WithStatus` (clients):**
  ```go
  type TorrentStatus struct { Hash string; PercentDone float64; State string }
  type WithStatus interface {
      Status(ctx context.Context, rawConfig []byte, hashes []string) ([]TorrentStatus, error)
  }
  ```
  Implement for **Transmission** (`torrent-get` with `fields:[hashString,
  percentDone, status]`, request `ids` by hash) and **qBittorrent**
  (`/api/v2/torrents/info?hashes=<joined>`). Other clients omit it.
- **New endpoint `GET /api/v1/topics/{id}/status`:** loads the topic's
  deliveries, and if its client implements `WithStatus`, queries live status by
  infohash; returns per-item `{label, percent_done, state, delivered_at}`.
  Clients without the capability → items with `state:"delivered"` only. On-demand;
  no background poller.

### Frontend
- **Topic row:** delivered count + decoded `S01E05` chips (Tier 1, always). When
  the topic's client supports status, a React Query with `refetchInterval` polls
  `GET /topics/{id}/status` while the page is open and renders a live percent /
  "finished" badge. Degrades to "delivered" labels otherwise.
- **i18n:** `topics.delivery.*` (delivered, downloading, finished, count).

### Caveat (verify during implementation)
Per-episode **live** status needs each episode's infohash captured from its
`Payload` at delivery. If an episodic `Payload` doesn't expose a derivable
infohash, that topic degrades to Tier 1 chips while single-torrent topics get full
live percent. Tier 1 is never blocked by this.

### Success check
A RuTracker topic shows live download percent from Transmission that advances on
poll and reads "finished" at 100%; a LostFilm topic shows decoded delivered
episodes (live percent if infohash available); a client without `WithStatus`
shows delivered labels with no error; backend + frontend test suites green.

---

## Cross-cutting

- **Conventions:** PascalCase-read / snake_case-write asymmetry preserved
  (`Topic` fields untagged; request structs json-tagged). React Query keys from
  `QK`. `interface` over `type` for TS object shapes. 4-space Go is gofmt-managed.
- **No synthetic data:** images and titles come only from real tracker pages;
  empty/uncertain → placeholder or hidden, never fabricated.
- **Docs:** update `CLAUDE.md` (domain row: `ImageURL`; new `WithMetadata` /
  `WithStatus` capabilities; deliveries table; category-as-subfolder) in the same
  commits as the structural changes.
- **Each stage is its own PR** and must be green on CI before the next.
