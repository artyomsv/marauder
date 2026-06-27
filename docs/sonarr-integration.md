# Sonarr integration

Marauder can **automatically take over monitoring** of updateable forum-tracker
topics that Sonarr grabs. This closes the one gap in the *arr workflow for
forum trackers like RuTracker, Kinozal, and NNM-Club: a "release" there is an
**updateable forum topic** whose `.torrent`/infohash changes over time as new
episodes are added, and Sonarr — built around the Torznab "one release =
one immutable file" model — can grab the initial release but can't keep
watching that topic for future updates.

With the integration enabled, the ownership model is clean:

- **Seerr / Sonarr** stay the entry point and do the initial grab.
- **Marauder** automatically takes over monitoring of the grabbed forum topic
  and pushes future torrent updates to the **same** download client and
  category Sonarr imports from — so Sonarr keeps importing completed files with
  no category/path drift.

It is configured at **Integrations → Sonarr** in the Marauder UI (admin only).

## How it works

1. A background poller reads Sonarr's grab history
   (`GET /api/v3/history/since?eventType=grabbed`) every poll interval.
2. For each grab, it reads the tracker **topic URL** from `data.nzbInfoUrl`
   (the info/details page — *not* `guid`, which for some trackers is a download
   link).
3. If the URL matches an installed Marauder tracker plugin (and is in the
   allowed-trackers list, if you set one), Marauder auto-creates a monitored
   **topic** owned by the admin, using the configured default client / category
   / download directory.
4. From then on Marauder's scheduler monitors that topic for hash changes and
   delivers updated torrents — exactly like a manually-added topic. Auto-created
   topics carry a **Sonarr** badge in the Topics list.

The poll is deliberately conservative:

- **Go-forward on first enable** — enabling the integration stamps the cursor
  to "now" and imports **nothing** historical, so you don't get a topic per
  past grab. New grabs flow from that point on.
- **Idempotent** — deduped by topic URL (a season-pack grab that fans out into
  N episode-history rows still creates exactly one topic), with a
  unique-constraint backstop.
- **Fail-open** — if Sonarr is unreachable the poll logs a warning and retries
  next tick; the cursor never advances past unprocessed grabs.

## Multiple instances

Marauder supports **any number of Sonarr instances** — e.g. one Sonarr for
regular TV and a separate one for anime, each with its own download client,
category, save path, allowed-tracker set, and **independent history cursor**.
Each instance polls on its own interval and is enabled/disabled independently.

In the Marauder UI, open **Integrations → Sonarr** (admin only). You'll see a
card per configured instance with its status (**Active**, **Paused**, or
**Draft**), and an **Add instance** button. Each card lets you enable/disable,
test, edit, or delete that instance.

A new instance defaults to **disabled** ("Draft") so you can fill it in and
**Test connection** before it starts polling — flip **Enable** when ready.

## Configuration

Each instance has these fields:

| Field | What it does |
|---|---|
| **Name** | A label to tell instances apart (e.g. `TV`, `Anime`). |
| **Enable integration** | Turns this instance's poller on. |
| **Sonarr URL** | This Sonarr's base URL, reachable from the Marauder backend (e.g. `http://sonarr:8989` on the same Docker network). |
| **API key** | Sonarr → Settings → General → API Key. Stored encrypted at rest; never returned by the API (the UI shows only whether one is set). Leave blank when editing other fields to keep the stored key. |
| **Test connection** | Verifies the URL + key against Sonarr's `system/status`. |
| **Poll interval** | How often to check this Sonarr's grab history (minimum 60s). |
| **Allowed trackers** | Optional allow-list of tracker plugins to auto-import. Leave all unchecked to allow every supported tracker. |
| **Default client / category / download dir** | Applied to every topic this instance auto-creates. Point these at the **same** qBittorrent + category this Sonarr imports from (e.g. `tv-sonarr`) so Sonarr keeps importing Marauder's future downloads. |

Each instance lives as its own row in the database (encrypted API key) and is
editable at runtime — no restart, no env vars. Changes take effect on the next
poll tick. **Go-forward on first enable** applies per instance: enabling an
instance stamps *its* cursor to "now" and imports nothing historical.

## Trying it out

The dev overlay bundles a ready-to-use *arr stack
(`deploy/docker-compose.dev.yml` starts Prowlarr + Sonarr + FlareSolverr), and
`deploy/docker-compose.arr.yml` attaches the same stack to an **already-running**
Marauder so you can add it without rebuilding. See
[deploy/docker-compose.arr.yml](../deploy/docker-compose.arr.yml) for the
attach-to-existing flow.

## Notes

- **Sonarr must actually grab.** The integration reacts to Sonarr's *grabbed*
  history — it doesn't search or grab on its own. In the normal Seerr → Sonarr
  flow, "search on add" produces the grab automatically.
- **Russian-only / anime releases sometimes fail Sonarr's auto-grab** with
  "Unable to parse release" (the title doesn't map to Sonarr's episode model).
  Those releases still exist on the tracker — use Sonarr's **Interactive
  Search** and grab one manually (Sonarr will ask to override the rejection).
  Marauder picks up the resulting grab the same way.
- **Credentials.** Auto-created topics for trackers that need a login (e.g.
  Kinozal) won't be able to download until you add that tracker account under
  **Accounts**; the topic is still created and the scheduler surfaces the
  missing-credential state. RuTracker downloads work anonymously.
- **Owner.** Auto-created topics are owned by the admin who saved the instance
  (falling back to the first admin), since the poller runs headless.
