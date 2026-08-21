# Getting started with Marauder

Marauder runs as a small Docker Compose stack — PostgreSQL, the Go backend,
the React frontend, and an nginx gateway that fronts them on a single host
port. The only thing you need on the host is Docker.

There are three ways to start it:

- **[Option A — prebuilt images](#option-a--prebuilt-images-recommended)**
  pulls published images from GitHub Container Registry. No clone, no build.
  This is the recommended way to *run* Marauder.
- **[Option B — build from source](#option-b--build-from-source)** clones the
  repository and builds the images locally. Use this if you want to modify
  Marauder, add a plugin, or run an unreleased commit.
- **[Option C — Kubernetes](#option-c--kubernetes-advanced)** deploys Marauder
  to your own cluster via a Helm chart (or Kustomize / plain manifests), with a
  choice of a simple single-pod Postgres or a CloudNativePG + S3-backup tier.
  For advanced users running k3s/k8s.

Options A and B bring up the same Docker Compose stack on
**<http://localhost:34080>**.

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
`1.0.1`). See [Pinning a version](#pinning-a-version) below.

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

## Option C — Kubernetes (advanced)

For advanced users running their own cluster. Marauder ships a Helm chart as
the source of truth, with derived Kustomize overlays and pre-rendered plain
manifests — pick whichever fits your workflow. Two database tiers are offered:

- **`simple`** — a single Postgres pod (no HA, no backups). Good for one user.
- **`cnpg`** — [CloudNativePG](https://cloudnative-pg.io/) with automatic
  failover and Barman → S3 point-in-time backups (requires the CNPG operator).

Every persistent volume (DB data, app config, downloads/media) takes a uniform
`type` (`existingClaim` / `pvc` / `nfs` / `hostPath` / `emptyDir` / `raw`), so
you decide exactly how storage is provided. The gateway defaults to a
`ClusterIP` Service, so the chart is load-balancer-agnostic (LoadBalancer,
NodePort and Ingress are all opt-in).

```bash
git clone https://github.com/artyomsv/marauder.git
helm install marauder marauder/deploy/helm/marauder -n marauder --create-namespace \
  -f marauder/deploy/helm/marauder/values-simple-db.yaml \
  --set persistence.downloads.type=pvc \
  --set persistence.downloads.pvc.storageClass=YOUR_SC \
  --set database.simple.persistence.pvc.storageClass=YOUR_SC \
  --set secrets.masterKey="$(openssl rand -base64 32)" \
  --set secrets.dbPassword="$(openssl rand -base64 24)" \
  --set initialAdmin.password="change-me"
```

See the **[Kubernetes deployment guide](kubernetes.md)** for the CNPG tier,
the full volume menu with per-backend examples, exposure options, secrets
handling, optional bundled clients / *arr stack, and the Kustomize and
plain-manifest install paths.

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

The prebuilt stack reads `MARAUDER_VERSION` from `.env` (default `1.0.1`):

```bash
# .env
MARAUDER_VERSION=1.0.1     # pinned — reproducible
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

## Using RuTracker? Start the Cloudflare solver

Every tracker Marauder supports works with the stack you just started, with
one exception: **RuTracker** sits behind a Cloudflare challenge and needs a
solver alongside Marauder. Without one, adding a RuTracker account or topic
fails with *"No Cloudflare solver is configured"*.

Run these from the same directory as your `up` command above.

**Option A (prebuilt images)** — grab the overlay next to your compose file:

```bash
curl -fsSLO https://raw.githubusercontent.com/artyomsv/marauder/main/deploy/docker-compose.solver.yml
docker compose -f docker-compose.ghcr.yml -f docker-compose.solver.yml --env-file .env up -d
```

**Option B (build from source)** — it is already in `deploy/`:

```bash
docker compose -f docker-compose.yml -f docker-compose.solver.yml up -d
```

That one file starts a FlareSolverr container **and** points Marauder at it.
On Kubernetes, set `arr.flaresolverr.enabled=true`.

It's opt-in because it runs a full Chrome and costs a few hundred MB of RAM —
skip it unless you use RuTracker. Details, including the same-public-IP
requirement, are in **[trackers.md](trackers.md#rutracker-needs-a-cloudflare-solver)**.

> **Already had RuTracker topics failing?** They are parked on a retry backoff
> of up to 6 hours, so they will keep showing the old error until their next
> check. Don't assume the solver didn't work — select them on the Topics page
> and use **Check now** to retry immediately.

---

## Next steps

- **[Connect a torrent client](clients.md)** — qBittorrent, Transmission,
  Deluge, µTorrent, or a watch folder.
- **[Add tracker credentials](trackers.md)** — logins for the supported
  trackers; some use an in-app captcha login.
- **[Enable OIDC sign-in](oidc.md)** — Keycloak / Authentik / any OIDC provider.
- **[Connect Torznab / Newznab](torznab-newznab.md)** — reach 500+ indexers via
  Jackett, Prowlarr, or NZBHydra2.
- **[Deploy on Kubernetes](kubernetes.md)** — Helm / Kustomize / manifests, the
  simple and CloudNativePG + S3-backup tiers, and the volume model.

---

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `docker compose pull` → `denied` / `manifest unknown` / `403` | The GHCR packages are not public yet (or the tag/`MARAUDER_VERSION` doesn't exist) | The maintainer must make the packages public (see below). Confirm the tag exists in the [GitHub releases](https://github.com/artyomsv/marauder/releases). |
| `unsupported Compose file version` or the `configs.content` field is ignored | Docker Compose older than v2.23.1 | `docker compose version`; upgrade Docker / the Compose plugin. |
| Login fails with `Unexpected token '<' ... is not valid JSON` | You opened a service directly instead of the gateway | Use the gateway at `http://localhost:34080`, not an individual container port. |
| `MARAUDER_MASTER_KEY is required` on startup | Key not set in `.env` | Run `openssl rand -base64 32` and put it in `MARAUDER_MASTER_KEY`. |
| Port 34080 already in use | Another process holds the port | Set `MARAUDER_HOST_PORT` in `.env` to a free port in the 30000–49999 range. |
| RuTracker fails with `No Cloudflare solver is configured` | RuTracker is challenge-gated and no solver is running | Add `-f docker-compose.solver.yml` to your `up` command (see above). |

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
  https://ghcr.io/v2/artyomsv/marauder-backend/manifests/1.0.1
```
