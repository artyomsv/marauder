# Add updates paused / download only new files

Two per-topic settings decide what happens when a torrent you already
downloaded is **updated** on the tracker: **Add updates paused** and
**Download only new files**. Both are off by default, so existing topics behave
exactly as before.

## What it is for

Some releases grow over time. A season pack on RuTracker is the usual case: the
uploader adds each new episode to the same torrent, so the torrent changes and
Marauder sends the new version to your client. By default the client then
downloads the whole pack again, including every episode you already have.

That is a problem when disk space is short, or when your client removes
finished episodes after seeding (for example because they were already copied
to a NAS): each update brings every old episode back.

- **Add updates paused** adds the new version to the client paused. You pick the
  files you want and start it yourself.
- **Download only new files** skips the files the previous version already had
  and downloads only the new ones.

Both are in the **Add topic** and **Edit topic** forms, under the
"Replace previous version on update" option.

## Add updates paused

When the topic is updated, Marauder adds the new version to the client paused
and nothing downloads until you start it. The notification ends with
`Added paused.`

Only **updates** are affected. The first download of a topic starts normally,
because there is nothing to compare it with. After a **Reset**, the next
download counts as a first download again and also starts normally.

## Download only new files

When the topic is updated, Marauder:

1. adds the new version to the client paused,
2. waits until the client lists the torrent's files,
3. marks every file the previous version already had as "do not download",
4. starts the torrent.

The notification then says, for example, `Downloading 1 new of 12 files.`

What counts as the "same file":

- **Same name and same size.** The name is the file's path inside the torrent;
  the torrent's top folder is ignored, so a renamed folder does not matter.
- **A re-encoded episode downloads again.** If the uploader replaced an episode
  with a new encode, it keeps its name but its size changes, so Marauder treats
  it as new. That is deliberate: it is a different file.
- **A skipped file can still get a small piece on disk.** Torrents are
  downloaded in pieces, and a piece can cover the end of one file and the start
  of the next. When that happens, the client writes the shared piece, so a
  small partial file (or a hidden part file) can appear next to the new
  episode. The skipped file is not downloaded.

If you turn on both settings, Marauder skips the old files **and** leaves the
torrent paused, so you can check the selection before it starts. The
notification says, for example, `Added paused with 1 new of 12 files selected.`

If the update has no new file at all (only old files, for example a torrent
that was re-registered without changes), nothing is started and the torrent is
left paused.

## When Marauder adds paused instead

Marauder never guesses. When it cannot tell which files are new, it adds the
torrent **paused** and tells you, so you can pick the files in your client. It
never lets an old file download because of an error. The download itself still
counts as successful: the topic does not turn red.

| Situation | Notification text |
|---|---|
| The tracker gave a **magnet link**, which carries no file list | `Added paused: a magnet link has no file list. Pick the new files in your client.` |
| **No earlier file list.** Marauder stores each torrent's file list when it downloads it, starting with this version. The first update of a topic whose last download happened before you upgraded — for example, an old topic you just turned the setting on for — has nothing to compare with. Later updates do. | `Added paused: no earlier file list to compare with. Pick the new files in your client.` |
| The torrent file **could not be read** (or has more than 5000 files) | `Added paused: could not read the torrent's file list. Pick the new files in your client.` |
| A **client call failed**: the client did not list the files within 15 seconds, did not show all the old files, or refused to skip or start | `Added paused: file selection did not finish (<step>). Check the torrent in your client.` — `<step>` names what failed, for example `skipping the old files failed` |
| The update has **no new files** | `Added paused: this update has no new files.` |

The client's own error message is written to Marauder's log, not to the
notification.

## With "Replace previous version"

"Replace previous version on update" removes the old torrent from the client
when the new one arrives. Its sub-option **Also delete the old files from disk**
would delete the very files that "Download only new files" skips, so they would
be gone for good.

So when **Download only new files** is on:

- **Also delete the old files from disk** is unticked and locked off in the form,
  and the API refuses the combination (HTTP 422).
- Replace still removes the old torrent from the client, but **keeps its files**
  on disk. The new torrent downloads only the new episodes next to them.

## Supported clients

| Client | Add updates paused | Download only new files |
|---|---|---|
| qBittorrent | yes | yes |
| Transmission | yes | yes |
| Deluge | yes | yes |
| µTorrent | no | no |
| Download folder | no | no |

These were checked against real qBittorrent 5.2.1 and 5.1.4, Transmission 4.1.2
and 4.0.6, and Deluge 2.2.0.

µTorrent and the download folder cannot pause a torrent or select its files.
The form shows a note when the topic's client (or your default client) is one
of them. If the settings are on anyway, the torrent is added normally and all
files download; with **Download only new files** the notification says
`This client cannot pause or select files, so all files download.`

## Not for per-episode trackers

On trackers that deliver one torrent per episode (LostFilm), every download is
already just the new episode. Both settings are hidden in the form for such
topics and ignored if set through the API. They are hidden for notify-only
topics too, which never download.

## Metric

Each outcome of **Download only new files** is counted in
`marauder_scheduler_file_selection_total{client, result}`:

| `result` | Meaning |
|---|---|
| `selected` | The old files were skipped; the torrent started (or was left paused because "Add updates paused" is on too) |
| `no_new_files` | The update had no new file; left paused |
| `paused_no_baseline` | No earlier file list; left paused |
| `paused_magnet` | Magnet link; left paused |
| `paused_unreadable` | The torrent's file list could not be read; left paused |
| `unsupported` | The client cannot pause or select files; every file downloads |
| `failed` | A client call failed; left paused |

Every result except `selected` means you may have to act in the client.
