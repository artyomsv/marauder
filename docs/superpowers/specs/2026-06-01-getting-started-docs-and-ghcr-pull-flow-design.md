# Getting-started docs + GHCR pull-flow — design

- **Date:** 2026-06-01
- **Status:** Approved (design); pending spec review
- **Author:** Claude (brainstorming session)

## Problem

Marauder's only documented way to start the service is **clone the repo →
`docker compose up` → Docker compiles three images locally**. That contradicts
the "the only thing you need on the host is Docker — no Go, Node, or Postgres
toolchain" promise we already advertise on `site/install.astro` and in the
README: a from-source build still needs the full repo and a multi-minute
compile on first boot.

We already publish prebuilt, multi-arch, cosign-signed images to GHCR on every
`v*` tag (`release.yml`), and `v1.0.0` is tagged — so the images exist. They are
just not consumable yet (private + no pull-based compose + no user-facing doc).

## Goals

1. A proper user-facing getting-started guide that leads with a **no-clone,
   pull-prebuilt** path and keeps **build-from-source** as a secondary path.
2. A compose file that makes the pull path actually work as a single download.
3. Reconcile the README quick-start and `site/install.astro` to lead with the
   pull path.
4. Document the one manual prerequisite (making the GHCR packages public).

## Non-goals / out of scope

- **Automating GHCR package visibility.** Flipping the three packages to Public
  is a one-time GitHub UI/owner action the maintainer performs; we only
  document it.
- **Publishing a dedicated `marauder-gateway` image** (the "Approach A" from
  brainstorming). Deferred — would strand the pull-flow until a new release is
  cut. Revisit at the next release; noted in "Future work".
- **Changing the existing `deploy/docker-compose.yml`**, the dev/sso overlays,
  or the contributor workflow. All stay build-based and untouched.
- **Folding the gateway into the frontend image** or any auth/scheduler change.

## Decisions (locked during brainstorming)

| Decision | Choice |
|---|---|
| Documented paths | **Both**, recommend pull-prebuilt |
| Task scope | **Full wire-up** (docs + compose + README + site + GHCR note) |
| Gateway config in pull flow | **B: inline `configs:` block** in the pull-flow compose |

## Background facts (verified in repo)

- `release.yml:72-91` pushes `ghcr.io/<owner>/marauder-{backend,frontend,cfsolver}`
  for `linux/amd64,linux/arm64` with tags `latest`, `{version}`, `{major}.{minor}`,
  cosign-signed + SBOM. Trigger: `v*` tag (and `workflow_dispatch`).
- `docker.yml` only **builds + Trivy-scans** (`push: false`); it never publishes.
- `v1.0.0` is the only tag → `:1.0.0`, `:1.0`, `:latest` images should exist.
- Anonymous pull of `ghcr.io/artyomsv/marauder-backend:1.0.0` returns **HTTP 403**
  → packages are currently **private**.
- `deploy/docker-compose.yml` services use `build:` contexts (no `image:` pulls).
- The **gateway** service (`deploy/docker-compose.yml`) is a vanilla
  `nginx:1.27-alpine` that **bind-mounts `./nginx/gateway.conf`** — the only
  on-disk dependency that breaks a no-clone flow.
- The **frontend** image bakes its own `nginx/default.conf` (serves the SPA on
  `:8081`); it does not proxy `/api`. Routing + security headers live in the
  gateway.
- `deploy/nginx/gateway.conf` is ~30 lines: `/api/` → `backend:8679`,
  `/health` + `/ready` passthrough, `/` → `frontend:8081`, plus 4 security
  headers. Container listens on `6688`; host port default `34080`.

## Manual prerequisite (maintainer, one-time)

For each of `marauder-backend`, `marauder-frontend`, `marauder-cfsolver`:

1. GitHub → profile/repo **Packages** → select package → **Package settings**.
2. **Change visibility → Public**.
3. **Connect repository** (`artyomsv/marauder`) so future pushes inherit access
   and the package links to the repo.

Verify afterward (should return HTTP `200`):

```bash
tok=$(curl -s "https://ghcr.io/token?scope=repository:artyomsv/marauder-backend:pull" \
  | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
curl -s -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer $tok" \
  https://ghcr.io/v2/artyomsv/marauder-backend/manifests/1.0.0
```

## Architecture / file plan

### 1. `deploy/docker-compose.ghcr.yml` (new) — the end-user pull stack

Standalone, self-contained, **image-only** mirror of the production stack. Key
differences from the build-based base:

- Each app service uses `image: ghcr.io/artyomsv/marauder-<svc>:${MARAUDER_VERSION:-1.0.0}`
  instead of a `build:` block. `db` stays `postgres:18-alpine`.
- `name: marauder` (project name) so it composes/teardowns cleanly when run by
  filename from an arbitrary directory.
- Gateway config supplied **inline** via the Compose top-level `configs:` key
  (Compose Spec `content:`; requires Docker Compose **v2.23.1+**) mounted at
  `/etc/nginx/conf.d/default.conf` — no bind mount, no extra file:

  ```yaml
  configs:
    gateway_conf:
      # KEEP IN SYNC with deploy/nginx/gateway.conf (build-from-source stack).
      content: |
        upstream marauder_backend  { server backend:8679; }
        upstream marauder_frontend { server frontend:8081; }
        server {
          listen 6688;
          ... (verbatim copy of deploy/nginx/gateway.conf body) ...
        }
  services:
    gateway:
      image: nginx:1.27-alpine
      configs:
        - source: gateway_conf
          target: /etc/nginx/conf.d/default.conf
  ```

- Preserves the base stack's `depends_on` + `condition: service_healthy`
  ordering, healthchecks, `restart: unless-stopped`, json-file logging caps,
  the `marauder_pgdata` named volume, and the full backend env block.
- `cfsolver` included under its `profiles: ["cfsolver"]` guard, image-based.
- Host port via `${MARAUDER_HOST_PORT:-34080}:6688`.

Usage (documented):

```bash
mkdir marauder && cd marauder
curl -fsSLO https://raw.githubusercontent.com/artyomsv/marauder/main/deploy/docker-compose.ghcr.yml
curl -fsSL  https://raw.githubusercontent.com/artyomsv/marauder/main/deploy/.env.example -o .env
# set MARAUDER_MASTER_KEY:
sed -i "s|MARAUDER_MASTER_KEY=.*|MARAUDER_MASTER_KEY=$(openssl rand -base64 32)|" .env
docker compose -f docker-compose.ghcr.yml --env-file .env up -d
# open http://localhost:34080
```

**Drift mitigation:** the inline block carries a `KEEP IN SYNC` comment pointing
at `deploy/nginx/gateway.conf`. (A future CI assertion that diffs the two is
noted under Future work, not built here.)

### 2. `docs/getting-started.md` (new) — canonical user guide

Sections:

1. **What you need** — Docker 24+ / Compose v2.23+, `openssl`; optional torrent
   client reachable from the host.
2. **Option A — Quick start with prebuilt images (recommended)** — the
   `curl` + `docker compose -f docker-compose.ghcr.yml up -d` flow above.
3. **Option B — Build from source (contributors / customizers)** — the existing
   `git clone` + `docker compose --env-file .env up -d` flow.
4. **The master key & secrets** — `MARAUDER_MASTER_KEY` is required; losing it
   makes stored credentials unrecoverable.
5. **First login** — `admin` / `pleasechangeme` from
   `MARAUDER_ADMIN_INITIAL_*`; change password, then clear both vars.
6. **Verify it's healthy** — `/health`, `/api/v1/system/info`.
7. **Pinning vs latest** — recommend pinning `MARAUDER_VERSION=1.0.0`; `latest`
   exists and floats.
8. **Optional overlays** — SSO (Keycloak) and cfsolver profiles (cross-link to
   `docs/oidc.md`); note overlays target the source/base stack.
9. **Next steps** — link clients, trackers, Torznab/Newznab, OIDC docs.
10. **Troubleshooting** — `pull access denied / 403` → packages still private;
    `unsupported Compose file / configs.content` → upgrade Compose; "login
    returns HTML / not valid JSON" → hitting the frontend container instead of
    the gateway.

### 3. `README.md` — rework quick start

- Lead with the pull path; keep build-from-source as a clearly-labeled
  secondary block; link to `docs/getting-started.md`.
- **Fix advertised drift while here** (own logical edit, called out in commit):
  - "11 tracker plugins" / "seven CIS forum-tracker plugins" → reconcile to the
    **16** used by the badges/site (or restate accurately).
  - Go badge `Go 1.23` → align with the actual toolchain (`1.25`).
  - Note the `1.1.0-dev` image tag vs advertised `v1.0.0` (informational).

### 4. `site/src/pages/install.astro` — reorder to pull-first

- Primary steps → prebuilt-image flow; secondary → build-from-source.
- Update the `howToSchema` steps to match the new primary flow.
- Keep prerequisites, master-key warning, verify, and "next steps" cards.

## Success criteria (verifiable)

1. From an empty directory with **no repo clone**, the documented Option A
   commands bring up four healthy containers and `curl http://localhost:34080/health`
   returns `200`. *(Validatable only after the packages are made public; until
   then, validate the compose file by running it against locally-tagged images
   built from the base stack — `docker compose -f docker-compose.yml build`,
   `docker tag ... ghcr.io/artyomsv/marauder-<svc>:1.0.0`.)*
2. `docker compose -f deploy/docker-compose.ghcr.yml config` parses with no
   errors (validates the `configs.content` block + image refs).
3. The build-from-source path in `docs/getting-started.md` matches the existing,
   still-working `deploy/docker-compose.yml` flow verbatim.
4. `site/` still builds: `npm run build` (in node:22 container) passes
   `astro check`.
5. The inline gateway config is byte-identical (modulo the `KEEP IN SYNC`
   comment) to `deploy/nginx/gateway.conf`.

## Risks

- **Packages stay private** → every pull-flow example fails with 403. Mitigated
  by the explicit prerequisite section + troubleshooting entry; cannot be fully
  validated by us until the maintainer flips visibility.
- **Compose version skew** → `configs.content` needs Compose v2.23.1+.
  Mitigated by the prerequisite line + troubleshooting entry.
- **Config duplication drift** (gateway.conf vs inline) → mitigated by the
  sync comment now; CI diff deferred.

## Future work (not in this task)

- Publish a `marauder-gateway` image (Approach A) and switch the pull-flow
  compose to it → removes the inline duplication entirely. Requires adding
  gateway to `docker.yml` + `release.yml` matrices and cutting a release.
- CI assertion diffing the inline gateway config against
  `deploy/nginx/gateway.conf`.
