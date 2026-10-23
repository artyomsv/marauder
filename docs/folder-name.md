# Folder name in the client

A per-topic setting decides what the torrent's **top folder** is called in your
torrent client. It is off by default ("Tracker's name"), so existing topics
behave exactly as before.

## What it is for

Torrent clients let you rename a torrent's folder, for example to keep a clean
media library. Marauder did not know about that rename. When the tracker updated
the topic, the new `.torrent` came with the tracker's folder name, so the client
put the update in a **second** folder and downloaded the whole release again
next to your renamed copy.

With a folder name set, every version of the topic goes into the same folder,
and the client reuses the files already there.

The setting is in the **Add topic** and **Edit topic** forms, under the update
settings.

## The three choices

- **Tracker's name** — the default. The folder gets the name the tracker gives it.
- **Custom name** — the folder gets the name you type, on the first download and
  on every update.
- **Keep the name from my client** — before an update, Marauder reads the folder
  name the previous version has in your client and gives the new version the same
  name. Rename the folder in your client once, and updates follow it. Marauder
  also remembers the last name it saw, so this still works after the previous
  torrent was removed from the client.

## What happens on a download

1. Marauder adds the torrent to the client **paused**.
2. It waits until the client lists the torrent's files.
3. It renames the torrent's top folder.
4. It asks the client to **recheck** the torrent, so the client finds the files
   already in that folder. Only the parts that changed download.
5. It starts the torrent (unless "Add updates paused" keeps it paused).

The notification ends with `Saved in folder "<name>".`

If a step fails, the torrent stays **paused** and the notification says which
step failed, for example
`Added paused: renaming the folder failed (renaming failed). Rename it in your client and start it.`
The check itself does not fail: the torrent did reach your client.

## When the name is not used

The torrent then downloads into the tracker's folder, and the notification says
why (`Could not use folder name "<name>": ...`):

- **Magnet links.** A magnet has no file list until the client fetches it.
- **Single-file torrents.** There is no folder to rename.
- **A client set to "no subfolder".** The client does not put the torrent in a
  folder (qBittorrent's "Don't create subfolder" content layout).
- **A torrent that already has data on disk.** Marauder never renames a torrent
  that has written files: the rename moves files and could overwrite the
  previous version's files. This happens only on a retry, after you started the
  torrent by hand.
- **µTorrent and the download-folder client.** They cannot rename folders. The
  form shows a warning when one of them receives the topic.

Per-episode trackers (LostFilm) and notify-only topics do not show the setting:
each episode is its own torrent, and a notify-only topic never reaches a client.

## Use it with "Replace previous version on update"

Turn **Replace previous version on update** on, with **Also delete the old
files from disk** off. Then the old torrent is removed from the client and only
one torrent owns the folder. Without it, two torrents share the same files, and
the old one shows errors when the update changes a file.

"Also delete the old files from disk" is switched off and locked while a folder
name is set: the old and the new version share the folder, so deleting the old
version's files would delete the new version's files too. The API refuses that
combination with **422**.

Be careful with two other actions that delete data:

- Removing the old torrent **with its data** by hand in your client deletes the
  shared folder.
- A topic **Reset** with "delete data" deletes the shared folder too.

## Supported clients

qBittorrent, Transmission and Deluge.
