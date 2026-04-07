# Migrating from monitorrent

This document is for users coming from `werwolfby/monitorrent`. The
short version: Marauder doesn't auto-import your monitorrent state in
v1.0 — you'll need to re-add your topics manually. The good news is
that the topic URLs you used in monitorrent should work unchanged in
Marauder.

---

## What carries over

| monitorrent feature | Marauder equivalent | Notes |
|---|---|---|
| RuTracker.org topics | rutracker plugin | Same `viewtopic.php?t=N` URL |
| LostFilm.tv series | lostfilm plugin | Same `/series/<slug>` URL |
| Kinozal.tv | kinozal plugin | Same `details.php?id=N` URL |
| NNM-Club | nnmclub plugin | Now uses the cfsolver sidecar for Cloudflare |
| Anilibria | anilibria plugin | Now uses the public v3 JSON API |
| Anidub | anidub plugin | |
| Rutor | rutor plugin | |
| Toloka, Unionpeer, Tapochek | yes | |
| qBittorrent | qbittorrent plugin | WebUI API v2 (qBit 4.5+) |
| Transmission | transmission plugin | RPC, with the 409 session-id dance |
| Deluge | deluge plugin | Web JSON-RPC at `/json` |
| uTorrent | utorrent plugin | Token-based WebUI |
| Local folder downloads | downloadfolder plugin | |
| Telegram notifications | telegram plugin | |
| Email notifications | email plugin | SMTP PLAIN |
| Pushover notifications | pushover plugin | |
| Webhooks | webhook plugin | New in Marauder |

---

## What does NOT carry over

- **Sessions and refresh tokens.** Marauder uses ES256 JWT, monitorrent
  used Flask sessions. Everyone has to log in again the first time.
- **Stored credentials.** monitorrent's SQLite encrypted credentials are
  not portable to Marauder's AES-256-GCM master-key encryption. You'll
  re-enter your tracker passwords once.
- **Hit-and-run tracking** and other quality-profile metadata that
  monitorrent stored separately. Marauder relies on the torrent client
  itself for that information in v1.0.
- **Pushbullet and Pushall** notification backends. We dropped them
  because both services have shut down or significantly degraded since
  monitorrent's last release.

---

## Step-by-step migration

### 1. Inventory your monitorrent topics

If you still have a working monitorrent install:

```bash
sqlite3 monitorrent.db \
  "SELECT t.url, p.display_name FROM topics t JOIN plugins p ON p.id = t.plugin_id;" \
  > monitorrent-topics.txt
```

If your monitorrent doesn't start, you can pull the database file
straight off disk and inspect it on another machine.

### 2. Stand up Marauder

```bash
git clone https://github.com/artyomsv/marauder.git
cd marauder/deploy
cp .env.example .env
# Generate the master key:
sed -i "s|MARAUDER_MASTER_KEY=.*|MARAUDER_MASTER_KEY=$(openssl rand -base64 32)|" .env
docker compose --env-file .env up -d
open http://localhost:6688
```

### 3. Re-create your tracker credentials

For each tracker that monitorrent had logged in to, configure the
credential through the Marauder UI under `Trackers`. Marauder validates
the credential by attempting a login before saving.

> **Note (v1.0):** the per-tracker credential UI is in development.
> Until it ships, configure RuTracker etc. by inserting rows directly
> into the `tracker_credentials` table — see `docs/PRD.md §7.1`.

### 4. Re-create your torrent client connections

Add your qBittorrent / Transmission / Deluge under `Clients`. Use the
**same hostname your monitorrent used**, on the **same port**. Marauder
talks to all of them inside the docker network if you're running them
in the same compose stack.

### 5. Import topics

For each topic from your inventory, paste the URL into the Marauder
"Add topic" sheet. Marauder auto-detects the tracker from the URL.

If you have hundreds of topics, you can also `POST /api/v1/topics`
directly:

```bash
TOK=$(curl -sS -X POST http://localhost:6688/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"yours"}' | \
  awk -F'"access_token":"' '{print $2}' | awk -F'"' '{print $1}')

while IFS= read -r url; do
  curl -sS -X POST http://localhost:6688/api/v1/topics \
    -H "Authorization: Bearer $TOK" \
    -H "Content-Type: application/json" \
    -d "{\"url\":\"$url\"}"
done < monitorrent-topics.txt
```

### 6. Decommission monitorrent

Once Marauder is checking your topics happily, stop the monitorrent
container, archive the SQLite file somewhere safe, and reclaim the
port/disk space.

---

## What's better in Marauder

- **Memory.** Marauder stays under 150 MB RSS for 200 topics.
  monitorrent regularly reached 1+ GB and crashed.
- **Modern qBittorrent.** Marauder speaks the v2 WebUI API that
  qBittorrent 4.5+ shipped — monitorrent's last release predates it.
- **Modern Cloudflare bypass.** The cfsolver sidecar uses recent
  chromedp + Chromium, not the 2019-era Playwright heuristics
  monitorrent shipped.
- **OIDC.** Sign in with Keycloak, Authentik, Auth0, or any OIDC
  provider. monitorrent has one local password for everybody.
- **Multi-user.** Each user has their own topics, credentials, and
  download clients, isolated at the database level.
- **Audit log.** See every login, logout, configuration change.
  Useful for shared instances.
- **Prometheus metrics.** Monitor your monitor.

---

## What's still rough

Marauder is v1.0, not v3.0. Things that still need work:

- The forum-tracker plugins (RuTracker, Kinozal, NNM-Club, LostFilm,
  Anilibria, Anidub, Rutor, Toloka, Unionpeer, Tapochek) are shipped
  as **alpha** — structurally complete and unit-tested with HTML
  fixtures, but not validated against live sites in the original
  development cycle. If you have a real account on any of these and
  hit a problem, please open an issue with a minimal reproducer and
  the relevant DOM snippet.
- The credential management UI doesn't yet support per-tracker
  configuration through the web interface — coming in v1.1.
- The OIDC flow is auth-code only. PKCE lands in v1.1.
- The "topic detail" side-sheet with full event history is on the
  v0.2 backlog.

The full status is in [`ROADMAP.md`](ROADMAP.md).
