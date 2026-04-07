# Torrent client setup guide

Marauder hands torrents off to an external client when a topic updates.
This page lists the URL formats, ports, and gotchas for every client
plugin that ships with Marauder. Open **Clients → Add client** in the
UI and paste the URL that matches your setup.

The encrypted credentials never leave the database — even your admin
session only sees them when you click **Edit**, and every read is
written to the audit log.

---

## qBittorrent

**Plugin name:** `qbittorrent`
**API:** Web UI v2 (`/api/v2/...`)
**Default port:** `8080`
**URL format:** `http://host:port` (no trailing path, no `/api`)

| Field | Example |
|---|---|
| URL | `http://192.168.1.10:8080` |
| Username | `admin` |
| Password | (your Web UI password) |
| Category (optional) | `tv-marauder` |

**Common gotchas**

- **Don't include `/api/v2/` in the URL.** The plugin appends it
  automatically. If you paste `http://host:8080/api/v2/auth/login`
  the test will fail with a 404.
- **Self-signed HTTPS** — qBittorrent's built-in HTTPS uses a
  self-signed cert by default. Either disable HTTPS, install a real
  cert, or front qBittorrent with nginx + Let's Encrypt.
- **Whitelist the Marauder backend container** in qBittorrent's
  *Web UI → Bypass authentication for clients in whitelisted IP
  subnets* if you want to skip the password roundtrip on every
  request.
- The **category** field, if set, tags every torrent Marauder
  submits — useful for sorting in the qBittorrent UI and for
  per-category save paths.

---

## Transmission

**Plugin name:** `transmission`
**API:** Transmission RPC (JSON over HTTP)
**Default port:** `9091` (some packages use `8083`)
**URL format:** `http://host:port/transmission/rpc` (path is mandatory)

| Field | Example |
|---|---|
| RPC URL | `http://192.168.2.65:8083/transmission/rpc` |
| Username (optional) | (only if Transmission's RPC password is enabled) |
| Password (optional) | |

**Common gotchas**

- **The path `/transmission/rpc` is required.** Without it you get
  HTTP 405 Method Not Allowed because Transmission's webroot serves
  the Web UI, not the RPC endpoint. Double-check it's there.
- **The 409 / X-Transmission-Session-Id dance** is handled
  transparently by Marauder. The first request gets a 409 with a
  session ID header, Marauder stores it, retries — you never see
  the dance, but if your reverse proxy strips the
  `X-Transmission-Session-Id` header you will.
- **Custom port?** transmission-daemon installed via `apt` on
  Debian/Ubuntu typically listens on `9091`. Some Synology DSM
  packages use `9091`, while certain Docker images
  (e.g. `linuxserver/transmission`) default to `9091` for the
  Web UI but expose `51413` for peers — use the **Web UI port**, not
  the peer port.
- **Authentication** is optional. Leave both fields blank if your
  Transmission has no RPC password. If you set a password,
  Marauder uses HTTP Basic auth, which Transmission accepts in
  addition to its native auth challenge.

---

## Deluge

**Plugin name:** `deluge`
**API:** Deluge Web JSON-RPC
**Default port:** `8112`
**URL format:** `http://host:port` (the plugin appends `/json` itself)

| Field | Example |
|---|---|
| Web URL | `http://192.168.1.10:8112` |
| Password | (the Web UI password, not the daemon password) |

**Common gotchas**

- The **Web UI password** and the **daemon password** are different.
  Marauder talks to the Web UI, so use the password you type when
  visiting `http://host:8112` in a browser.
- The Web UI must be **connected to the daemon** before Marauder can
  add a torrent. The plugin calls `web.connect` automatically, but
  if you have multiple daemons configured, the first one is used.
- **Transitive failures**: if Deluge restarts the daemon mid-session,
  Marauder will see a `web.is_connected: false` and re-authenticate
  on the next request. No action needed on your side.

---

## µTorrent

**Plugin name:** `utorrent`
**API:** Web UI token + form auth
**Default port:** `8080`
**URL format:** `http://host:port/gui/` (the trailing slash matters)

| Field | Example |
|---|---|
| Web UI URL | `http://192.168.1.10:8080/gui/` |
| Username | `admin` |
| Password | (your Web UI password) |

**Common gotchas**

- **Enable the Web UI** in µTorrent → *Options → Preferences → Web
  UI* and set a username + password before adding the client to
  Marauder. The default install ships with the Web UI off.
- The trailing slash on `/gui/` is required — without it µTorrent
  serves a 404.
- µTorrent's Web UI is **not actively maintained** upstream. If
  Transmission or qBittorrent works for your setup, prefer them.

---

## Download folder (watch folder fallback)

**Plugin name:** `downloadfolder`
**API:** filesystem write
**URL format:** absolute path on the backend container

| Field | Example |
|---|---|
| Folder path | `/downloads/sabnzbd-watch` |

**Use cases**

- **SABnzbd / NZBGet watch folder** — Marauder writes the
  `.torrent` file (or NZB, in the case of Newznab indexers) into
  the folder, and SABnzbd / NZBGet picks it up on its next scan.
  Make sure the folder is mounted into both containers via the
  same bind-mount.
- **Headless seedbox** — point at a directory the seedbox watches
  via FTP / SFTP / `inotify` and you have a "drop and go" pipeline.
- **Plain archival** — sometimes you just want the files on disk.
  Use a dedicated archive folder.

**Common gotchas**

- **The path is from the backend container's perspective**, not
  your host. If you bind-mount `~/downloads` into the backend at
  `/downloads`, the value here is `/downloads`, not `~/downloads`.
- **Permissions matter.** The backend runs as `marauder` (UID
  10001 in the bundled Dockerfile). The mounted directory must be
  writable by that UID, or you'll get `permission denied` on
  every write. Either `chown 10001` the host directory or fix the
  bind-mount with `:rw` and a permissive umask.
- The plugin **does not** speak the BitTorrent protocol — it only
  drops files. The downstream tool (SABnzbd, qBittorrent watch
  folder, FlexGet, etc.) is responsible for the actual transfer.

---

## Editing an existing client

Click the **pencil icon** on a client card to open the edit form.
Marauder fetches the decrypted config from `GET /api/v1/clients/{id}`
(audit-logged) and pre-fills every field, including the password.
Saving runs the same `Test` validation as create — a bad config
never overwrites a good one.

You **cannot change the plugin type** of an existing client. To
switch from Transmission to qBittorrent, delete and re-add.

---

## Testing without saving

The "Test connection" button on each card re-runs the plugin's
`Test()` method against the stored config. It does not modify the
database — it's safe to click as often as you like.

If the test fails, the error message is the literal Go error
returned by the plugin (e.g. `transmission status 401:
Unauthorized`). That's usually enough to diagnose the issue.

---

## I want to add a new client type

Marauder's client interface is one Go file implementing five
methods (`Name`, `DisplayName`, `ConfigSchema`, `Test`, `Add`). See
[`docs/plugin-development.md`](plugin-development.md) for the
walkthrough — adding a new client is a weekend project for a
first-time contributor.
