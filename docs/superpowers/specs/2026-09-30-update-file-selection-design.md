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
- New `Deliveries.LatestFiles(ctx, topicID, excludeInfohash) ([]domain.TorrentFile, error)`
  (*amended after review*): `files` of the newest row (`delivered_at DESC`)
  whose `infohash <> excludeInfohash` — the update's own infohash — whether or
  not that row has a list. `(nil, nil)`, meaning no baseline, when there is no
  such row **or its `files` is NULL**. The first version returned the newest
  non-NULL row and was wrong twice:
  - *A retry compared the torrent with itself.* The delivery row is recorded
    before file selection (§4.3). If the tick's check result is then discarded
    — a shutdown mid-selection, `ErrStaleCheckResult` after a recheck or an
    interval edit, a failed `RecordCheckResult` — `last_hash` stays on the old
    release and the next tick delivers the same one again. Its own row was the
    newest, so every file was "old" and the new episode never downloaded.
  - *A magnet in between made an older version the baseline.* v1 (.torrent) →
    v2 (magnet, added paused, the user picked files by hand) → v3 (.torrent)
    compared v3 with v1 and re-downloaded v2's files, contradicting Q1: the old
    file list is unknown, so the update is added paused.

  Replace-on-update prunes prior rows only after the new row is written, and
  the new row carries the full new list, so the baseline survives replacement
  (a retry of that same release then finds no other row and is added paused).
  A topic reset deletes every row, so the first delivery after a reset has no
  baseline — consistent with it being a first delivery.

### 3.4 API

`POST /topics` and `PUT /topics/{id}` accept `add_paused_on_update` and
`only_new_files`; the topic view returns them. Validation (both endpoints):
`only_new_files && replace_on_update && replace_delete_data` → **422**
("download only new files cannot be combined with deleting the previous
version's files: the old files would be lost"; `topics.ValidUpdatePolicy`,
sentinel `topics.ErrOnlyNewFilesDeletesData`). This is checked on the values
the request would store, so a `PUT` that turns on `only_new_files` on a topic
that already deletes data is rejected too.

*Amended after implementation:* the status is 422, matching the other
create-validation failures (`ErrQualityUnsupported`, the check-interval range),
not 400. And on **create**, an omitted `replace_delete_data` defaults to
**false** when `only_new_files` is on (it stays true otherwise), so a client
that sends only `replace_on_update` + `only_new_files` gets the safe
combination instead of a rejection; only an explicit `true` is refused.

`GET /system/info` extends each entry of the existing `clients` list with
`supports_file_selection` (*amended:* no separate `client_plugins` list — the
`clients` entries already carried `name` and `display_name`, so a second list
would have repeated them).

## 4. Delivery flow

### 4.1 Reading a `.torrent` file list — new package `internal/torrentmeta`

`Files(data []byte) ([]domain.TorrentFile, error)`:

- Its own small bencode decoder with the same depth bound as `infohash`
  (`maxBencodeDepth` 32) and a bound on list length, because the input is a
  tracker response. *Amended after review:* also a cap on the total number of
  values decoded per call (`maxValues` 250 000; a 5000-file torrent needs about
  25 000). Each value costs tens of bytes of heap for one byte of input, and
  this runs on every `.torrent` delivery, so without it an 8 MiB torrent of
  tiny values decodes into hundreds of MB. Exceeding it is an error, which the
  scheduler treats as an unreadable file list.
- Multi-file: `info.files`; each entry's `path` list joined with `/`, `length`
  as size. Entries that are BEP 47 padding files (`attr` contains `p`, or the
  first path component is `.pad`) are dropped.
- Single-file: one entry `{info.name, info.length}`.
- A v2-only torrent (`file tree`, no `files`/`length`) returns an error; the
  caller treats that as "unknown".
- Path components are UTF-8 as given; `path.utf-8` is preferred when present.

`SkipSet(prev, next []domain.TorrentFile) []domain.TorrentFile` returns the
entries of `next` whose `(Path, Size)` pair is in `prev` — the files to skip.

`MatchesClientFile(c domain.ClientFile, f domain.TorrentFile) bool`
— true when sizes are equal and the client path equals `f.Path`, or equals it
after dropping **exactly** its first component. That absorbs the client's own
top folder (qBittorrent "Original"/"Create subfolder" content layouts, a
renamed root) without the looser suffix match, which would also let
`A/Season 1/E01.mkv` match a file listed as `Season 1/E01.mkv` under a
different parent.

`MatchSkip(client []domain.ClientFile, skip []domain.TorrentFile) (indices []int, matched int)`
maps the skip set onto the client's list: the sorted, de-duplicated client
indices to skip, and how many skip entries matched at least one client file.
A caller that gets `matched < len(skip)` must not start the torrent.

*Amended after implementation:* matching runs **once, in the scheduler**, not
inside each client plugin (see §4.2).

### 4.2 Client capability — `registry.WithFileSelection`

*Amended after implementation:* the capability is three thin calls. Waiting
and matching live in the scheduler, so each exists once instead of three
times, and the plugins only translate to their client's API.

```go
type WithFileSelection interface {
    Client
    // Files lists the torrent's files the way the client numbers them. An
    // empty list, not an error, while the client does not know the torrent
    // or its file list yet — a qBittorrent add is asynchronous.
    Files(ctx context.Context, rawConfig []byte, hash string) ([]domain.ClientFile, error)
    // SkipFiles marks the given client file indices "do not download".
    SkipFiles(ctx context.Context, rawConfig []byte, hash string, indices []int) error
    // Start resumes a torrent that was added paused.
    Start(ctx context.Context, rawConfig []byte, hash string) error
}
```

`domain.ClientFile{Index, Path, Size, Wanted}`: `Index` is the client's own file
id, `Wanted` is false for a file marked "do not download".

| Client | Files | SkipFiles | Start |
|---|---|---|---|
| qBittorrent | `GET /api/v2/torrents/files?hash=` (404 → empty list; `index`, else position, for pre-4.4) | `POST /api/v2/torrents/filePrio` `id=i\|j\|…&priority=0` | `POST /api/v2/torrents/start`, falling back to `/resume` on 404 (pre-5.0) |
| Transmission | `torrent-get` `fields: [files, fileStats]` (unknown hash → `torrents: []`) | `torrent-set` `files-unwanted: [i, j, …]` | `torrent-start` |
| Deluge | `core.get_torrent_status(hash, ["files", "file_priorities"])` (unknown id → `{}`) | `core.set_torrent_options([hash], {file_priorities: [..]})` — full list, 0 for skipped, current priority kept for the rest; Deluge 2 dropped `set_torrent_file_priorities` | `core.resume_torrent(hash)` |

The scheduler polls `Files` every 500 ms within a 15 s `fileSelectionTimeout`.

qBittorrent's paused add sends **both** `paused=true` and `stopped=true`
(5.0 renamed the field; each version ignores the name it does not know).

µTorrent and downloadfolder do not implement it.

**Real-client results (2026-09-30)**, from the build-tagged
`backend/internal/plugins/clients/fileselectioncheck` test: qBittorrent 5.2.1
and 5.1.4, Transmission 4.1.2 and 4.0.6, and Deluge 2.2.0 each honoured the
paused add, listed both files, skipped exactly the old one, started, and
answered an unknown hash with an empty list. qBittorrent 5.1.4 and 5.2.1 answer
`torrents/start` 200 and `torrents/resume` 404, so the fallback is only for
older servers.

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
`unsupported`, `paused_unreadable`, `failed`.

`add_paused_on_update` on a client that cannot pause (µTorrent, downloadfolder)
adds normally — the plugin ignores `Paused`, as today — and logs a Warn. The
form states that these clients are not supported, so this is only reached by an
API caller or a changed default client.

The note travels back to `downloadAllPending` with the delivery label and is
appended to the `download.submitted` event body, one line per delivery.

`VerifyCheckState` still runs immediately before `Add`. `SkipFiles`/`Start` run
after it; they act only on the torrent this tick just added.

*Amended after implementation* (the flow above is the plan; these are the
differences that shipped, in `scheduler/update_policy.go`):

- The result is **`unsupported`**, not `paused_unsupported`: a client without
  `WithFileSelection` cannot pause either, so the torrent is added **normally**
  (not paused) and all its files download. The plan does not mark it paused,
  and with `only_new_files` the note says `This client cannot pause or select
  files, so all files download.` *Amended after review:* with only
  `add_paused_on_update` the note says `This client cannot pause, so the update
  started.` (no metric — there is no file-selection outcome), so the user is
  not left believing the update waits for them.
- `planDelivery` (steps 1-3, including the `LatestFiles` read) runs **before**
  `VerifyCheckState`, so its database read does not widen the gap between that
  guard and `Add`. An infohash failure or a file list above `maxStoredFiles` is
  `paused_unreadable`; a `LatestFiles` error degrades to `paused_no_baseline`.
- The delivery is **recorded before** file selection (step 6 moves ahead of
  step 5): the selection can wait up to its 15 s budget, and a shutdown in that
  wait must not lose the row that is the next update's baseline and what reset,
  replace and the progress watcher act on. Because the row exists before the
  tick's result is persisted, a discarded result leads to the same release
  being delivered again with its own row present; `planDelivery` therefore
  computes the payload's infohash **before** the baseline read and passes it
  to `LatestFiles` as the row to leave out (§3.3).
- The scheduler **always** waits for the client's file list before `Start`,
  even when nothing is skipped: a `Start` sent before an asynchronous
  qBittorrent add has landed is lost, leaving the torrent paused behind a
  "Downloading" note.
- Step 5's `matched, err := SkipFiles(ctx, cfg, hash, skip)` is split to fit
  §4.2: the scheduler polls `Files`, calls `torrentmeta.MatchSkip`, refuses on
  `matched != len(skip)`, then `SkipFiles(indices)`.
- *Amended after review:* **no new file** (step 5's first bullet) no longer
  returns without touching the client, which left every file wanted, so a user
  pressing Start re-downloaded the whole pack. It waits for the client's file
  list, matches, and skips **every** file under the same rule (a partial match
  is `failed` and nothing is skipped), then does not start. The result is still
  `no_new_files`. Only a torrent with no content file at all skips the client
  calls, since there is nothing to skip.
- Every outcome is logged at Info; `failed` at Warn with the step.
- Notification notes name the failed step (`could not list the files`,
  `the client listed N of M old files`, `skipping the old files failed`,
  `starting the torrent failed`) but **never** the raw client error, which can
  carry an HTML error page; that goes to the log only.
- Note texts as shipped: `Downloading N new of M files.`, `Added paused with N
  new of M files selected.` (both settings on), `Added paused.` (paused only),
  `Added paused: this update has no new files; all its files are skipped.`,
  `This client cannot pause, so the update started.` (paused only, on a client
  that cannot pause), and `Added paused: <reason>. Pick the new files in your
  client.` for the fallbacks.

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
  disabled, and a one-line note explains why. *Amended after review:* the form
  submits what the box shows (`replaceDeleteData && !onlyNewFiles`), not the
  stored value behind it — a topic saved with replace-on-update off keeps
  `replace_delete_data` true, and turning replace-on-update on would otherwise
  send the combination the API rejects with 422.
- When the selected client — or the user's default client when "Default" is
  selected — has `supports_file_selection: false` in its `/system/info`
  `clients` entry, a note under the two new checkboxes says they are not
  supported by that client (`components/topics/useUnsupportedClient.ts`).
- *Amended:* for per-episode trackers the two flags are sent as `false`, so
  the form never saves state the user cannot see.
- `lib/api.ts` types, `AddTopicCard`/`EditTopicCard` payloads, en/ru strings.

## 6. Testing

- `torrentmeta`: single-file, multi-file, hybrid with padding files,
  `path.utf-8`, v2-only (error), malformed/deep bencode (error, no panic),
  `SkipSet` and `MatchesClientFile` table tests (root rename, subfolder layout,
  size change, suffix that is not at a `/` boundary).
- Scheduler: first delivery, update with only paused, update with selection
  success, both settings on, magnet, no baseline, unsupported client, SkipFiles
  error, partial match, no new files, Start error, per-episode tracker
  (settings ignored), recorded `files` on every `.torrent` delivery, a retry
  whose own row is already recorded, a magnet version in between.
- Client plugins: `httptest` servers for qBittorrent (incl. `/start` 404 →
  `/resume`), Transmission, Deluge — wait, skip, start.
- Repo integration tests (real Postgres): flags round-trip; `Record` with and
  without `files`; `LatestFiles` picks the newest row of another infohash,
  passes over the excluded infohash's own row, and returns no baseline when
  that newest row is NULL (a magnet in between).
- Handlers: the §3.4 422 on create and update; `/system/info`
  `clients[].supports_file_selection`.
- Frontend: checkboxes, locked delete-data box, unsupported-client note.
- **Real-client check** on the `deploy/docker-compose.test-clients.yml` matrix
  (qBittorrent 5.2.1 and 5.1.4, Transmission 4.1.2 and 4.0.6, Deluge 2.2.0): add a
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
