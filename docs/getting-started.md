# Getting started with Marauder

Marauder runs as a small Docker Compose stack — PostgreSQL, the Go backend,
the React frontend, and an nginx gateway that fronts them on a single host
port. The only thing you need on the host is Docker.

There are two ways to start it:

- **[Option A — prebuilt images](#option-a--prebuilt-images-recommended)**
  pulls published images from GitHub Container Registry. No clone, no build.
  This is the recommended way to *run* Marauder.
- **[Option B — build from source](#option-b--build-from-source)** clones the
  repository and builds the images locally. Use this if you want to modify
  Marauder, add a plugin, or run an unreleased commit.

Both bring up the same stack and open on **<http://localhost:34080>**.

---

## What you need

- A Linux, macOS, or Windows host with **Docker 24+** and
  **Docker Compose v2.23.1+** (`docker compose version`). The prebuilt stack
  ships the gateway config inline via Compose `configs:`, which needs that
  Compose version.
- `openssl` (or any tool that can emit 32 random bytes) to generate the master
  encryption key.
- Optional: an existing torrent client (qBittorrent / Transmission / Deluge /
  µTorrent) reachable from the host. You can also add one later from the UI.

---

## Option A — prebuilt images (recommended)

Marauder publishes multi-arch (amd64 + arm64), cosign-signed images to
`ghcr.io/artyomsv/marauder-{backend,frontend,cfsolver}` on every release. The
prebuilt stack pulls those instead of compiling anything locally.

From an empty directory — no `git clone`:

```bash
mkdir marauder && cd marauder

# 1. Grab the compose file and the example env
curl -fsSLO https://raw.githubusercontent.com/artyomsv/marauder/main/deploy/docker-compose.ghcr.yml
curl -fsSL  https://raw.githubusercontent.com/artyomsv/marauder/main/deploy/.env.example -o .env

# 2. Generate the required master encryption key
sed -i "s|MARAUDER_MASTER_KEY=.*|MARAUDER_MASTER_KEY=$(openssl rand -base64 32)|" .env

# 3. Pull and start the stack
docker compose -f docker-compose.ghcr.yml --env-file .env up -d

# 4. Open the UI
#    http://localhost:34080   (admin / pleasechangeme)
```

The compose file pins to a specific release via `MARAUDER_VERSION` (default
`1.0.0`). See [Pinning a version](#pinning-a-version) below.

> **macOS note:** the BSD `sed` in step 2 needs `sed -i ''` instead of `sed -i`.
> Or just open `.env` in an editor and paste the output of `openssl rand -base64 32`
> into `MARAUDER_MASTER_KEY`.

---

## Option B — build from source

For contributors and anyone running an unreleased commit. This compiles the
backend, frontend, and cfsolver images locally on first start.

```bash
git clone https://github.com/artyomsv/marauder.git
cd marauder/deploy
cp .env.example .env

# Generate the master key
sed -i "s|MARAUDER_MASTER_KEY=.*|MARAUDER_MASTER_KEY=$(openssl rand -base64 32)|" .env

# Build + start (first run compiles the images — a few minutes)
docker compose --env-file .env up -d
```

The source stack also ships two overlays you can layer on top:

| Overlay | Command | Adds |
|---|---|---|
| Dev | `-f docker-compose.yml -f docker-compose.dev.yml up` | publishes db/backend/frontend ports + real qBittorrent & Transmission for end-to-end testing |
| SSO | `-f docker-compose.yml -f docker-compose.sso.yml up -d` | Keycloak 26 with a pre-imported realm and an `alice / marauder` test user |

The Cloudflare-solver sidecar is opt-in on both stacks via the `cfsolver`
profile: append `--profile cfsolver` to any `up` command.

---

## The master key and secrets

`MARAUDER_MASTER_KEY` is a **required** 32-byte, base64-encoded AES-256 key.
Marauder uses it to encrypt every secret it stores at rest — tracker
credentials, torrent-client configs, and JWT signing keys.

```bash
openssl rand -base64 32
```

> **Save this key somewhere safe.** If you lose it, every stored credential is
> permanently unrecoverable and you will have to re-enter all of them.

Optionally set `MARAUDER_METRICS_TOKEN` (`openssl rand -hex 32`) to gate the
Prometheus `/metrics` endpoint; leave it empty to disable the endpoint.

---

## First login

On the very first start — and only while the users table is empty — Marauder
creates an admin account from these env vars:

```bash
MARAUDER_ADMIN_INITIAL_USERNAME=admin
MARAUDER_ADMIN_INITIAL_PASSWORD=pleasechangeme
```

Sign in at <http://localhost:34080>, then:

1. **Change the admin password** from Settings.
2. **Clear both `MARAUDER_ADMIN_INITIAL_*` vars** in `.env` and restart the
   backend so the bootstrap can't run again with the default password.

Changing the values in `.env` *after* the first boot has no effect — the admin
already exists. Rotate the password from the UI instead.

---

## Verify it's healthy

```bash
# Gateway + backend are up
curl -fsS http://localhost:34080/health        # -> 200, body "."

# Backend reports its version
curl -sS http://localhost:34080/api/v1/system/info | jq .version

# All containers healthy
docker compose -f docker-compose.ghcr.yml ps    # (drop -f ... for the source stack)
```

You can also log in from the command line:

```bash
curl -sS -X POST http://localhost:34080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"pleasechangeme"}'
```

---

## Pinning a version

The prebuilt stack reads `MARAUDER_VERSION` from `.env` (default `1.0.0`):

```bash
# .env
MARAUDER_VERSION=1.0.0     # pinned — reproducible
# MARAUDER_VERSION=latest  # floats to the newest release
```

Pin to a released version for reproducible deployments. `latest` always points
at the most recent release and will change under you on the next `docker
compose pull`. To upgrade a pinned deployment:

```bash
# edit MARAUDER_VERSION in .env, then:
docker compose -f docker-compose.ghcr.yml --env-file .env pull
docker compose -f docker-compose.ghcr.yml --env-file .env up -d
```

---

## Next steps

- **[Connect a torrent client](clients.md)** — qBittorrent, Transmission,
  Deluge, µTorrent, or a watch folder.
- **[Add tracker credentials](trackers.md)** — logins for the supported
  trackers; some use an in-app captcha login.
- **[Enable OIDC sign-in](oidc.md)** — Keycloak / Authentik / any OIDC provider.
- **[Connect Torznab / Newznab](torznab-newznab.md)** — reach 500+ indexers via
  Jackett, Prowlarr, or NZBHydra2.

---

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `docker compose pull` → `denied` / `manifest unknown` / `403` | The GHCR packages are not public yet (or the tag/`MARAUDER_VERSION` doesn't exist) | The maintainer must make the packages public (see below). Confirm the tag exists in the [GitHub releases](https://github.com/artyomsv/marauder/releases). |
| `unsupported Compose file version` or the `configs.content` field is ignored | Docker Compose older than v2.23.1 | `docker compose version`; upgrade Docker / the Compose plugin. |
| Login fails with `Unexpected token '<' ... is not valid JSON` | You opened a service directly instead of the gateway | Use the gateway at `http://localhost:34080`, not an individual container port. |
| `MARAUDER_MASTER_KEY is required` on startup | Key not set in `.env` | Run `openssl rand -base64 32` and put it in `MARAUDER_MASTER_KEY`. |
| Port 34080 already in use | Another process holds the port | Set `MARAUDER_HOST_PORT` in `.env` to a free port in the 30000–49999 range. |

### Making the GHCR packages public (maintainer, one-time)

Images pushed by CI with the default `GITHUB_TOKEN` start out **private**, even
for a public repository. To let anyone pull them, the maintainer flips each
package to public once — for `marauder-backend`, `marauder-frontend`, and
`marauder-cfsolver`:

1. GitHub → **Packages** → select the package → **Package settings**.
2. **Change visibility → Public**.
3. **Connect repository** (`artyomsv/marauder`) so future releases inherit the
   setting.

Verify an anonymous pull works (expect HTTP `200`):

```bash
tok=$(curl -s "https://ghcr.io/token?scope=repository:artyomsv/marauder-backend:pull" \
  | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
curl -s -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer $tok" \
  https://ghcr.io/v2/artyomsv/marauder-backend/manifests/1.0.0
```
