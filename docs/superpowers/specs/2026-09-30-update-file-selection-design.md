# Issue #205 — add updates paused / download only new files: design

Ref #205. Requested in discussion #149.

## 1. Problem

A user watches RuTracker season packs that grow over time: the uploader adds a
new episode to the same torrent, so its infohash changes. The user's server has
little disk space. qBittorrent removes each episode after it reaches its seed
ratio, because the file is already copied to a NAS. When the torrent updates,
Marauder hands the new version to the client and the client downloads every
old episode again.

## 2. Decisions (agreed 2026-09-30)

| # | Question | Decision |
|---|---|---|
| Scope | Option 1 only, or both | **Both**: "add updates paused" and "download only new files" |
| Q1 | Old file list unknown (old topic, magnet, client cannot skip, client call fails) | **Add paused.** Nothing downloads; the user picks files by hand |
| Q2 | When does "add paused" apply | **Updates only.** The first delivery of a topic (and the first after a reset) starts normally |
| Q3 | "Only new files" + replace-on-update with delete data | **Blocked.** The form locks "delete files" off; the API rejects the mix |
| Q4 | When is a file "the same" | **Same relative path AND same size.** The torrent's top folder is ignored |
| Approach | How files are skipped | **Add paused, read the client's own file list, match by path + size, skip, then start** |

Rejected approaches:

- *Send skip indices with the add* (Transmission `files-unwanted`, Deluge
  `file_priorities`). qBittorrent has no add-time file priority parameter, and
  Marauder's file indices do not reliably equal the client's: hybrid (v1+v2)
  torrents carry BEP 47 padding files in `info.files` that libtorrent-based
  clients hide from their file list, which shifts every later index. A shifted
  index skips the wrong episode.
- *Read the old torrent's files from the client.* The old torrent is often gone
  — the user's seed-ratio rule removed it, or replace-on-update did.

## 3. Settings and storage

### 3.1 Topic settings

Two new per-topic booleans, both default `false` so existing topics are unchanged:

- `add_paused_on_update` — "Add updates paused".
- `only_new_files` — "Download only new files".

An **update** is a delivery made while the topic has a known release:
`t.LastHash != ""`. The first check of a topic has an empty `last_hash`, and
`Topics.ResetCheckState` clears it, so the first delivery and the first after a
reset start normally. The failed-download path persists the old hash, so a
retried first delivery is still a first delivery.

If both settings are on, Marauder skips the old files **and** leaves the torrent
paused.

Both settings are **ignored for per-episode trackers** (`isEpisodic(tr)`,
LostFilm) — each of their deliveries is one new episode already — and for
notify-only topics, which never deliver. The form hides them in both cases,
matching replace-on-update.

### 3.2 Migration `0017_add_topic_update_policy.sql`

```sql
ALTER TABLE topics
    ADD COLUMN add_paused_on_update BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN only_new_files       BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE topic_deliveries
    ADD COLUMN files JSONB;
```

`files` is a JSON array of `{"path": "Season 1/E01.mkv", "size": 1234}`. `path`
is relative to the torrent's top folder: for a multi-file torrent it is
`info.files[i].path` joined with `/` (without `info.name`); for a single-file
torrent it is `info.name`. `NULL` means "unknown": a magnet delivery, a delivery
made before this migration, a torrent with more than `maxStoredFiles` (5000)
files, or a torrent whose file list could not be read.

Down migration drops the three columns.

### 3.3 Domain and repository

- `domain.TorrentFile{Path string; Size int64}`.
- `domain.Topic` gains `AddPausedOnUpdate`, `OnlyNewFiles`.
- `domain.TopicDelivery` gains `Files []TorrentFile` (nil = unknown).
- `repo.TopicFlags` gains the two booleans. Every `Topics` SELECT/INSERT/UPDATE
  that lists the existing flags lists these too. The Sonarr poller carries the
  existing topic's values through, as it does for the current flags.
- `Deliveries.Record` writes `files` (NULL when nil). `ON CONFLICT DO NOTHING`
  stays: the first recorded file list for an infohash is the file list of that
  infohash, so there is nothing to update.
- New `Deliveries.LatestFiles(ctx, topicID) ([]domain.TorrentFile, error)`:
  `files` of the newest row (`delivered_at DESC`) whose `files IS NOT NULL`;
  `(nil, nil)` when there is none. Replace-on-update prunes prior rows only
  after the new row is written, and the new row carries the full new list, so
  the baseline survives replacement. A topic reset deletes every row, so the
  first delivery after a reset has no baseline — consistent with it being a
  first delivery.

### 3.4 API

`POST /topics` and `PUT /topics/{id}` accept `add_paused_on_update` and
`only_new_files`; the topic view returns them. Validation (both endpoints):
`only_new_files && replace_on_update && replace_delete_data` → **400**
("download only new files cannot be combined with deleting the previous
version's files: the old files would be lost"). This is checked on the values
the request would store, so a `PUT` that turns on `only_new_files` on a topic
that already deletes data is rejected too.

`GET /system/info` gains `client_plugins: [{name, supports_file_selection}]`.
The existing `clients` name list stays for compatibility.

## 4. Delivery flow

### 4.1 Reading a `.torrent` file list — new package `internal/torrentmeta`

`Files(data []byte) ([]domain.TorrentFile, error)`:

- Its own small bencode decoder with the same depth bound as `infohash`
  (`maxBencodeDepth` 32) and a bound on list length, because the input is a
  tracker response.
- Multi-file: `info.files`; each entry's `path` list joined with `/`, `length`
  as size. Entries that are BEP 47 padding files (`attr` contains `p`, or the
  first path component is `.pad`) are dropped.
- Single-file: one entry `{info.name, info.length}`.
- A v2-only torrent (`file tree`, no `files`/`length`) returns an error; the
  caller treats that as "unknown".
- Path components are UTF-8 as given; `path.utf-8` is preferred when present.

`SkipSet(prev, next []domain.TorrentFile) []domain.TorrentFile` returns the
entries of `next` whose `(Path, Size)` pair is in `prev` — the files to skip.

`MatchesClientFile(clientPath string, clientSize int64, f domain.TorrentFile) bool`
— true when sizes are equal and `clientPath == f.Path` or `clientPath` ends
with `"/" + f.Path`. Suffix matching absorbs the client's own top folder
(qBittorrent "Original"/"Create subfolder" content layouts, a renamed root).
Used by all three client plugins so the rule exists once.

### 4.2 Client capability — `registry.WithFileSelection`

```go
type WithFileSelection interface {
    // SkipFiles sets every file of the torrent `hash` that matches an entry of
    // skip (torrentmeta.MatchesClientFile) to "do not download". It waits,
    // bounded by ctx, until the client knows the torrent's file list. It
    // returns how many entries of skip matched at least one client file.
    SkipFiles(ctx context.Context, rawConfig []byte, hash string, skip []domain.TorrentFile) (int, error)
    // Start resumes a torrent that was added paused.
    Start(ctx context.Context, rawConfig []byte, hash string) error
}
```

| Client | Wait for file list | Skip | Start |
|---|---|---|---|
| qBittorrent | poll `GET /api/v2/torrents/files?hash=` until non-empty | `POST /api/v2/torrents/filePrio` `id=i|j|…&priority=0` (client `index` values) | `POST /api/v2/torrents/start` (5.x), falling back to `/resume` (4.x) on 404 |
| Transmission | `torrent-get` `fields: [files]` until non-empty | `torrent-set` `files-unwanted: [i, j, …]` | `torrent-start` |
| Deluge | `core.get_torrent_status(hash, ["files"])` until non-empty | `core.set_torrent_file_priorities(hash, [..])` — full list, 0 for skipped, current priority kept for the rest | `core.resume_torrent([hash])` |

The wait polls every 500 ms and stops at ctx's deadline. A `.torrent` add is
known to the client almost at once; the wait exists because qBittorrent's add
is asynchronous.

µTorrent and downloadfolder do not implement it.

### 4.3 Scheduler

In `sendViaClient` (single-release topics only; per-episode trackers skip all of
this):

1. `isUpdate := t.LastHash != ""`.
2. `files, filesErr := torrentmeta.Files(payload.TorrentFile)` when the payload
   is a `.torrent`; nil for a magnet. Kept for step 6 whatever the settings.
3. Build the plan:
   - `paused := isUpdate && (t.AddPausedOnUpdate || t.OnlyNewFiles)`.
   - When `isUpdate && t.OnlyNewFiles`, the file selection is **possible** only
     if: the payload is a `.torrent`, `files` was read, `Deliveries.LatestFiles`
     returned a baseline, and the client plugin implements
     `WithFileSelection`. Otherwise the torrent stays paused and the reason is
     kept for the notification (`magnet link`, `no earlier file list`,
     `client cannot select files`, `could not read the torrent's file list`).
   - `skip := torrentmeta.SkipSet(baseline, files)`.
4. `clientPlugin.Add(..., AddOptions{…, Paused: paused})`. Unchanged error path.
5. When the selection is possible:
   - `len(skip) == len(files)` (no new file): no client call, stays paused,
     note "no new files".
   - else `matched, err := SkipFiles(ctx, cfg, hash, skip)`. On error, or
     `matched != len(skip)` (an old file the client did not show — skipping
     less than planned would re-download it), the torrent stays paused with
     note "could not select files: …".
   - else, unless `t.AddPausedOnUpdate`, `Start`. A `Start` error leaves it
     paused with a note.
   - Success note: "Downloading N new files of M".
6. `recordDelivery` stores `files` (nil when unknown or above
   `maxStoredFiles`).

Every failure after a successful `Add` is **fail-safe and non-fatal**: the
torrent is in the client, paused, so the delivery is recorded and the check
succeeds. Nothing in this flow can make the client download an old file that
Marauder meant to skip; the worst case is "paused, pick by hand". Each outcome
is logged and metered:
`marauder_scheduler_file_selection_total{client, result}` with `result` in
`selected`, `no_new_files`, `paused_no_baseline`, `paused_magnet`,
`paused_unsupported`, `paused_unreadable`, `failed`.

`add_paused_on_update` on a client that cannot pause (µTorrent, downloadfolder)
adds normally — the plugin ignores `Paused`, as today — and logs a Warn. The
form states that these clients are not supported, so this is only reached by an
API caller or a changed default client.

The note travels back to `downloadAllPending` with the delivery label and is
appended to the `download.submitted` event body, one line per delivery.

`VerifyCheckState` still runs immediately before `Add`. `SkipFiles`/`Start` run
after it; they act only on the torrent this tick just added.

### 4.4 Interaction with replace-on-update (#101)

Order is unchanged: the new torrent is delivered (and its files selected), then
`replacePrevious` removes the old torrent. With `only_new_files` the delete-data
flag is forced off (§3.4), so replace removes the old torrent from the client
but leaves its files on disk.

## 5. Frontend

- Extract the "when the topic updates" block of `TopicForm.tsx` (replace-on-update
  and its delete-data box) into `components/topics/UpdatePolicyFields.tsx`, then
  add the two new checkboxes there with help text. `TopicForm.tsx` shrinks
  (565 lines today, limit 250, tracked in `techdebt/frontend/`).
- The block stays hidden for per-episode trackers and notify-only topics.
- When "Download only new files" is checked: the delete-data box is unchecked,
  disabled, and a one-line note explains why.
- When the selected client — or the user's default client when "Default" is
  selected — has `supports_file_selection: false` in `/system/info`
  `client_plugins`, a note under the two new checkboxes says they are not
  supported by that client.
- `lib/api.ts` types, `AddTopicCard`/`EditTopicCard` payloads, en/ru strings.

## 6. Testing

- `torrentmeta`: single-file, multi-file, hybrid with padding files,
  `path.utf-8`, v2-only (error), malformed/deep bencode (error, no panic),
  `SkipSet` and `MatchesClientFile` table tests (root rename, subfolder layout,
  size change, suffix that is not at a `/` boundary).
- Scheduler: first delivery, update with only paused, update with selection
  success, both settings on, magnet, no baseline, unsupported client, SkipFiles
  error, partial match, no new files, Start error, per-episode tracker
  (settings ignored), recorded `files` on every `.torrent` delivery.
- Client plugins: `httptest` servers for qBittorrent (incl. `/start` 404 →
  `/resume`), Transmission, Deluge — wait, skip, start.
- Repo integration tests (real Postgres): flags round-trip; `Record` with and
  without `files`; `LatestFiles` picks the newest non-NULL row.
- Handlers: the §3.4 400 on create and update; `/system/info` `client_plugins`.
- Frontend: checkboxes, locked delete-data box, unsupported-client note.
- **Real-client check** on the `deploy/docker-compose.test-clients.yml` matrix
  (qBittorrent 5.2.1 and 5.1.4, Transmission 4.1.2, Deluge 2.2.0): add a
  self-made two-version multi-file `.torrent` (version 2 = version 1 + one file)
  and confirm the client skips exactly the old file and starts. This also
  answers whether qBittorrent 5.x still honours `paused` on `torrents/add` or
  needs `stopped`; if it needs `stopped`, the plugin sends both. Test torrents
  use obviously synthetic names (`TEST-205-…`) and are removed afterwards.

Done when: `go build`, `go vet`, `go test -race ./...`, the `integration`
tests, `golangci-lint`, `gofmt` (via `test -z`), frontend `tsc --noEmit` and
`vitest run` all pass, and the real-client check shows the right files skipped
on all four clients.

## 7. Docs

- `CHANGELOG.md` `[Unreleased]`.
- New user guide `docs/update-policy.md` (both settings, the paused fallbacks,
  the replace interaction, supported clients).
- `CLAUDE.md`: scheduler section (update policy), `registry` capability list,
  new `torrentmeta` package row, migration `0017` in the `db/repo` row.
