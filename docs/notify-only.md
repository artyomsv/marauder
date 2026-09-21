# Notify-only topics

Sometimes you want to watch a release without downloading it automatically:
wait for a new episode, follow a repack, or decide for yourself when to fetch
the files. The per-topic **Notify only — do not download** option keeps checking
the tracker on the topic's normal schedule and announces new releases.
Nothing is sent to a torrent client, and no download client is required.

## Enabling it

Per topic, in the **Add topic** or **Edit topic** form:

1. Tick **Notify only — do not download**.
2. Pick a notifier, or make sure you have a default notifier on the
   **Notifiers** page.
3. Optionally tick **Also tell me about the release that is there now** to
   announce the release already on the page at the first check.

The sub-option is off by default, so a new notify-only topic's first check
quietly records what is already there. Later changes are announced.

## Behaviour

- **Default is unchanged.** New and existing topics keep downloading unless
  you explicitly enable notify-only.
- **You need a notifier.** With no per-topic notifier selected and no default
  notifier, the topic is completely silent: it keeps checking but tells nobody.
  Missing notification routing alone does not fail checks, turn the topic red,
  or produce an error. The form warns about this once the notifier list loads.
- **Download settings are kept.** The client, download directory, category,
  and replace-on-update policy are hidden while notify-only is on. Their values
  stay stored, so turning the mode off restores them without re-entry.
- **Seen episodes stay seen.** For per-episode trackers such as LostFilm,
  pending episodes are recorded as seen even though nothing is downloaded.
- **Reset makes the next check a first check again.** Reset clears the topic's
  recorded state. If notify-only remains enabled, **Also tell me about the
  release that is there now** applies again after Reset: ticked announces the
  current release; unticked records it silently.

## Notifications

The notification includes the topic name, episode labels for per-episode
trackers, the release author's latest comment when the tracker supplies one,
and a link to the tracker page so you can download it yourself. Long episode
lists show the first ten labels and a count of the remaining episodes.
The message makes clear that the release was not downloaded.

These alerts use the **`release.found`** event, not `download.submitted`.
Make sure your chosen or default notifier subscribes to `release.found`.
A notifier using the legacy **`updated`** subscription still receives it.

## Switching back to downloading

Untick **Notify only — do not download** in **Edit topic**. The stored download
settings reappear, and only the **next** detected change is downloaded.
Releases seen while notify-only was on are not fetched, including the episode
backlog recorded as seen.

**Warning — per-episode trackers:** for LostFilm, Reset also clears all recorded
episode progress. After switching back to downloading, this makes the entire
available back catalogue at or above your configured start season/episode
eligible for download again, not just the latest release. Check that starting
point before using Reset: it can queue dozens of episodes.

If you want the current release fetched now, turn notify-only off, save the
topic, then use **Reset**. Reset clears the recorded state and queues a fresh
check; a paused topic must also be resumed. Reset while notify-only is still
on keeps the topic in notify-only mode and follows the first-check sub-option
described above.
