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

## Defaults for new topics

Admins can set **Settings → Topic defaults → Replace previous version on
update** and **Also delete the old files from disk**. The Add topic form starts
with those values, which can still be changed for that topic. API clients that
omit `replace_on_update` or `replace_delete_data` inherit the corresponding
global default; an explicit `true` or `false` takes precedence.

Each **Integrations → Sonarr** instance can override either default with **On**
or **Off**, or select **Use global default** (`null` in the API). Each field
inherits independently: the instance override wins, then the global default,
then the built-in value if settings cannot be read. The built-in policy is
replace **off**, delete data **on**, preserving the behaviour of an unconfigured
installation.

These values are copied onto a topic only when it is created. Changing global
or Sonarr defaults never changes existing topics, including when Sonarr
realigns an existing topic's client, category, or download directory.

For growing **Sonarr season packs** that reuse a folder, choose replace **on**
and delete data **off**. Marauder removes the old torrent entry while retaining
the files for the new torrent to recheck and reuse.

When an API create omits delete-data, **Download only new files** or a custom /
kept folder name forces the inherited delete-data value off. An explicit
delete-data **on** with replace **on** and either of those options is still
rejected. The form shows the delete checkbox locked off for these combinations.

## Behaviour

- **Built-in behaviour is unchanged.** Without configured defaults, new topics
  keep every version. Existing topics keep their stored policy on upgrade.
- **Delete-data default.** The built-in default deletes the old files when
  replacing. Global or Sonarr defaults can change it for new topics; each topic
  can also keep its files by unticking the sub-option.
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
