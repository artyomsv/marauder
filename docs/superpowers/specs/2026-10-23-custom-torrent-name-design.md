# Custom torrent folder name — design (issue #225)

Status: draft, decisions agreed 2026-10-23. Reviewed once (independent review,
findings folded in below).

## Why

Users rename a torrent's top folder in their client to keep a clean media
library. Marauder does not know about the rename. When the tracker updates
the topic, Marauder adds the new `.torrent` with the tracker's default folder
name. The client then puts the update in a second folder and downloads the
whole release again, next to the renamed copy.

The user wants one stable folder per topic, across every update.

## Decisions

| # | Question | Decision |
|---|---|---|
| 1 | How does `keep` know the name? | Live read of the previous torrent's folder in the client first; if that fails, the **last name Marauder saw**, saved on the topic. |
| 2 | Magnets | No rename. Normal add, with a note. |
| 3 | What is renamed | The **folder only**. Marauder does not set the client's list name. (Transmission changes its list name by itself when the root is renamed; that is fine.) |
| 4 | qBittorrent shortcut (`savepath` + `NoSubfolder`) | No. One flow for every client, so one set of tests. |
| 5 | Single-file torrents | No rename. Normal add, with a note. |

## Goals

1. A per-topic **folder name** setting, in the UI and the API, on create and
   on edit, with three modes:
   - `default` — the tracker's name. Today's behaviour, and the default.
   - `custom` — a name the user types.
   - `keep` — give each update the folder name the previous version has in
     the client, so a rename done by hand in the client survives updates.
2. Marauder applies the name on the first delivery (`custom` only) and on every
   update.
3. Existing data in that folder is reused: the client verifies it, so only the
   pieces that changed download.

## Non-goals

- Per-episode trackers (LostFilm). Each episode is its own torrent; one shared
  name would merge them. Ignored and hidden.
- Notify-only topics. Nothing reaches a client. Hidden.
- Magnets and single-file torrents (decisions 2 and 5).
- µTorrent and the download-folder client.
- The client's list name (decision 3), and renaming files inside the folder.
- Moving data between save paths. `download_dir` / category already do that.

## Anti-goals (a failure even if it "works")

- **Deleting or overwriting the user's data.**
  - Replace-on-update with "delete data" would delete the shared folder. That
    combination is refused (see Validation).
  - A rename onto a folder that already holds the previous version's files
    can overwrite them if the new torrent has already written its own files
    (libtorrent's per-file rename is a plain `rename()`). Marauder renames
    **only a torrent that has downloaded 0 bytes**. Otherwise it does not
    rename.
- **Downloading into the tracker's folder name after the user asked for
  another, without saying so.** If Marauder cannot rename, the torrent stays
  paused (failure) or the note says why (cannot apply). Same rule as the
  update policy (#205).
- A behaviour change for existing topics. Mode `default` changes nothing.

## Data

Migration `0018_add_topic_folder_name.sql`:

```sql
ALTER TABLE topics
  ADD COLUMN folder_name_mode text NOT NULL DEFAULT 'default'
    CHECK (folder_name_mode IN ('default', 'custom', 'keep')),
  ADD COLUMN folder_name text NULL,
  ADD COLUMN folder_name_seen text NULL;
```

- `folder_name` — the typed name. Set only in `custom` mode; NULL otherwise.
- `folder_name_seen` — `keep` mode's memory: the last folder name Marauder
  saw or set for this topic. Written by the scheduler only (see Delivery flow).
  Not exposed for editing. Cleared by a topic reset and when the mode changes.

`domain.Topic` gets `FolderNameMode`, `FolderName`, `FolderNameSeen`. The
scheduler writes `folder_name_seen` with its own small statement
(`Topics.SetFolderNameSeen(ctx, id, name)`), not through `Topics.Update`.
Best-effort: a DB error is a Warn log.

## API and validation

`POST /topics` and `PUT /topics/{id}` take `folder_name_mode` and
`folder_name`. They follow the handler's existing pointer-field pattern: an
omitted field on `PUT` keeps the stored value. When the resulting mode is not
`custom`, `folder_name` is stored as NULL.

Name rules (`topics` package):

- Trim spaces. Then 1..255 **bytes** of UTF-8. Stored as typed (no Unicode
  normalisation — builder may revisit).
- Not `.`, `..` or `.pad`.
- No `/`, `\`, control characters, or `< > : " | ? *`.
- No trailing `.` or space.
- Not a Windows reserved name, case-insensitive, with or without an extension:
  `CON`, `PRN`, `AUX`, `NUL`, `COM1`–`COM9`, `LPT1`–`LPT9`.

Policy rule beside `ValidUpdatePolicy`: mode not `default` **and**
`replace_on_update` **and** `replace_delete_data` → **422**. Checked on create
and update, on the values the topic would store (as the only-new-files rule
is). On create, an omitted `replace_delete_data` defaults to `false` when the
mode is not `default`.

## Client capability

New `registry.WithRename`, implemented by qBittorrent, Transmission and Deluge
(each already implements `WithFileSelection`, which the flow also needs):

```go
type WithRename interface {
	// RenameRoot renames the torrent's top folder from oldName to newName.
	RenameRoot(ctx context.Context, rawConfig []byte, hash, oldName, newName string) error
	// Recheck asks the client to verify the torrent's data on disk.
	Recheck(ctx context.Context, rawConfig []byte, hash string) error
	// BytesDone reports how much of the torrent the client has written.
	BytesDone(ctx context.Context, rawConfig []byte, hash string) (int64, error)
}
```

| Client | RenameRoot | Recheck | BytesDone |
|---|---|---|---|
| qBittorrent | `torrents/renameFolder` (`oldPath`, `newPath`) | `torrents/recheck` | `torrents/info` `downloaded` |
| Transmission | `torrent-rename-path` (`path` = old root, `name` = new) | `torrent-verify` | `torrent-get` `downloadedEver` |
| Deluge | `core.rename_folder(id, "Old/", "New/")` — both with a trailing `/` | `core.force_recheck([id])` — takes a **list** | `core.get_torrent_status` `total_done` |

The folder name a client uses now is read through the existing
`WithFileSelection.Files`: the first path component shared by every client
path (the "rooted" layout `torrentmeta.MapClientFiles` already infers). No
new read method.

`/system/info` `clients[]` gains `supports_rename`.

## Delivery flow (scheduler)

New file `scheduler/folder_name.go`, wired into `planDelivery` and
`finishDelivery` beside the update policy.

### Plan (before `VerifyCheckState`, like `planDelivery`)

1. Mode `default`, episodic tracker, or notify-only → nothing to do.
2. Magnet payload → cannot apply (`magnet`), note.
3. Single-file `.torrent` (from `torrentmeta.Files`) → cannot apply
   (`single_file`), note.
4. Client without `WithRename` or `WithFileSelection` → cannot apply
   (`unsupported`), Warn log, note. The form warns before this.
5. Work out the wanted name:
   - `custom` → `FolderName`.
   - `keep` →
     a. the newest earlier delivery of this topic, **excluding the payload's
        own infohash** (a retried tick has already recorded its own row — the
        same rule as `LatestFiles`), on the client that holds it: read its
        live root with `Files`. If found, save it to `folder_name_seen`;
     b. else `folder_name_seen`;
     c. else nothing to do (first delivery, or nothing known).
6. Wanted name equals the payload's `info.name` → nothing to do.
7. Otherwise → add **paused**, and run the rename after `Add`.

### After `Add`

One time budget (`folderSelectionTimeout`) covers these steps **and** the
only-new-files selection. It nests inside the check's context, like
`fileSelectionTimeout` does today.

1. Wait for the client's file list (`waitForFiles`). A qBittorrent add is
   asynchronous.
2. Read the live root from that list.
   - No top folder (client set to "no subfolder") → cannot apply (`no_root`),
     note, continue to step 6.
   - Live root already equals the wanted name → `same_name`: skip steps 3–5
     and continue to step 6. This is the retry case: `qbittorrent.Add` treats
     a duplicate as success and does not pause an existing torrent, so the
     torrent may already be renamed and running. Do not claim "paused".
3. `BytesDone`. Not 0 → cannot apply (`has_data`), note, continue to step 6.
   This is the overwrite guard (Anti-goals).
4. `RenameRoot(live root → wanted name)`. Then read the file list once more
   (no wait) for the selection.
5. `Recheck`, so the client finds the data already in the folder.
6. Only-new-files selection, if the topic has it. `selectNewFiles` is changed
   to take a client file list instead of calling `waitForFiles` itself, and
   gets the list read after the rename. `MapClientFiles` already accepts a
   renamed root.
7. `Start`, unless "add updates paused" or the selection keeps it paused. All
   three clients defer a start until a running recheck ends (to be proven by
   the real-client test).

After a successful rename (or `same_name`) in `keep` mode, save the wanted
name to `folder_name_seen`. In `keep` mode with "nothing to do" at plan step
6, save the payload's `info.name`.

**Failure** in steps 1, 3 (read error), 4 or 5: the torrent stays **paused**,
the check does not fail (the delivery succeeded), and the note names the step.
The raw client error goes to the log only.

### Notes

Appended to `download.submitted`. When the update policy also has a note, the
update-policy note comes first, then the folder note, joined by one space.

- success: `Saved in folder "<name>".`
- cannot apply: `Could not use folder name "<name>": <reason>. Saved in the tracker's folder.`
- failure: `Added paused: renaming the folder failed (<step>). Rename it in your client and start it.`

### Metric

`marauder_scheduler_folder_rename_total{client,result}`, results `renamed`,
`same_name`, `magnet`, `single_file`, `no_root`, `has_data`, `unsupported`,
`failed`.

## Frontend

- New `components/topics/FolderNameFields.tsx`, used by `TopicForm` near
  `UpdatePolicyFields`. A select (Tracker's name / Keep the name from my
  client / Custom name) and a text input for `custom`. The component owns all
  of its logic: `TopicForm.tsx` already breaks the 250-line limit and must not
  grow by more than the call site.
- Hidden for per-episode trackers and notify-only; sent as `default` there.
- Warning when the effective client has `supports_rename: false` (same pattern
  as `useUnsupportedClient`).
- When the mode is not `default`, lock "delete data" in replace-on-update, the
  same way only-new-files locks it now.
- en + ru strings. Row badge: builder's call.

## Docs

- `docs/folder-name.md` (user guide). It must say:
  - Keep replace-on-update **on** (with "delete data" off), so the old torrent
    is removed and only one torrent owns the folder. Without it, two torrents
    share the files, and the old one shows errors when files change.
  - Deleting the old torrent **with its data** by hand, or a topic reset with
    "delete data", deletes the shared folder.
  - Magnets and single-file torrents keep the tracker's name.
- `CLAUDE.md`: the new columns, capability and scheduler step.
- `CHANGELOG.md` `[Unreleased]`.

## Verification

- Unit tests: name validation table, the policy 422, the plan decision table
  (each "nothing to do" and "cannot apply" branch, own-infohash exclusion,
  live-read then saved-name fallback), the after-Add order (rename before
  selection, recheck before start, `same_name` skips rename and recheck,
  `BytesDone > 0` skips rename), note joining, and each failure leaving the
  torrent paused.
- Client plugin tests against `httptest` servers for each new RPC, including
  Deluge's list argument and trailing slashes.
- **Real clients.** Extend the build-tagged `plugins/clients/fileselectioncheck`
  test (own Compose project, `-p marauder225check`, torrents named
  `TEST-225-*`) to prove, on qBittorrent 5.2.1/5.1.4, Transmission 4.1.2/4.0.6
  and Deluge 2.2.0:
  1. rename of a paused, 0-byte torrent works;
  2. a second torrent renamed into a folder that holds the first one's data
     rechecks and finds those pieces — for Transmission also with
     `incomplete-dir-enabled: true` (the linuxserver default);
  3. a start sent during a recheck is not lost;
  4. renaming to the name it already has is handled (for the `same_name` path).
  If Deluge cannot pass these before merge, drop Deluge from v1 (no
  `WithRename` on it) rather than ship it unproven.
- CI checks green on the PR (no local builds).

## Left to the builder

- Struct for the new topic fields (`repo.TopicFlags` or a sibling).
- `folderSelectionTimeout` value.
- Unicode normalisation of names.
- Row badge and wording.
