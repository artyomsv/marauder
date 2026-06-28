# Replace previous version on update

Some releases get re-uploaded: a movie repacked with a fix, a season bundled
again, a single torrent that the tracker bumps to a new version. By default
Marauder treats each new version as an additional download — it pushes the new
torrent to your client and leaves the old one in place. Over time that stacks
duplicate copies of large releases and fills the disk.

The per-topic **Replace previous version on update** option fixes that: when the
topic is updated, Marauder removes the previously delivered torrent from its
download client before/after adding the new one, instead of accumulating copies.

## Enabling it

Per topic, in the **Add topic** or **Edit topic** form:

1. Tick **Replace previous version on update**.
2. Leave **Also delete the old files from disk** ticked (the default) to free the
   disk space, or untick it to drop only the torrent entry and keep the files.

That's it — the change applies from the next detected update onward.

## Behaviour

- **Default is unchanged.** New and existing topics keep every version unless you
  explicitly enable the option. Upgrading Marauder never deletes anything on its
  own.
- **Delete-data default.** Once you enable replacing, deleting the old files is
  the default (that is the point — reclaiming disk space). Untick the sub-option
  if you want to keep them.
- **Single-release topics only.** The option is a no-op for per-episode trackers
  (e.g. LostFilm): there, each new infohash is an *additional* episode, not a
  replacement, so Marauder never removes prior episodes.
- **Best-effort.** If the client can't be reached or doesn't support removal, the
  new release is still delivered; the old torrent is simply left in place and
  retried on the next update. A failed removal never fails the check.

## Supported clients

Removal is supported on every networked client:

| Client | Removes torrent | Deletes data |
|---|---|---|
| qBittorrent | ✅ | ✅ |
| Transmission | ✅ | ✅ |
| Deluge | ✅ | ✅ |
| µTorrent | ✅ | ✅ |
| Download folder | — | — |

The non-networked **Download folder** client has no concept of a torrent to
remove, so replacing is skipped for it and the old files are left untouched.
