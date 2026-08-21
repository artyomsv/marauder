# Dead `cfsolver` sidecar is still shipped, and Helm enables it unconditionally

| Field | Value |
|-------|-------|
| Criticality | Medium |
| Complexity | Small |
| Location | `deploy/docker-compose.yml`, `deploy/docker-compose.ghcr.yml`, `deploy/helm/marauder/templates/configmap.yaml:9`, `deploy/helm/marauder/templates/cfsolver.yaml`, `backend/internal/cfsolver/` |
| Found during | Fixing issue #158 (RuTracker Cloudflare solver never wired up) |
| Date | 2026-08-21 |

## Issue

`cfsolver` is documented in `CLAUDE.md` as **unused** — superseded by the
FlareSolverr clearance minter — yet it is still shipped and, on Kubernetes,
still switched on:

- Both production compose stacks define a profile-gated `cfsolver` service that
  builds/pulls a `marauder-cfsolver` image.
- `deploy/helm/marauder/templates/configmap.yaml` sets
  `MARAUDER_CFSOLVER_ENABLED: "true"` and a `MARAUDER_CFSOLVER_URL`
  **unconditionally** — not gated on any value — so every chart install tells
  the backend a solver sidecar exists.
- `backend/internal/cfsolver/` and the `cfsolver/` service remain in the tree
  and are built by CI (`nightly-build` builds all three images).

The concrete cost surfaced in #158: the GHCR stack shipped the **dead** solver
sidecar while shipping neither the **live** one nor even its environment
passthrough. A user reading that compose file could reasonably conclude a
Cloudflare solver was already present.

## Risks

- **Misleads operators.** Two solver mechanisms are visible, only one works.
  Anyone debugging a Cloudflare failure will find `cfsolver` first — it is the
  one named in the compose file — and lose time on it.
- **Wasted build/release surface.** A third image is built, signed, SBOM'd and
  published nightly for every release, and its Chrome base has repeatedly
  produced CVE noise.
- **Config drift risk.** `MARAUDER_CFSOLVER_ENABLED: "true"` is ungated; if the
  backend ever starts honouring it again, every chart install changes behaviour
  silently.

## Suggested Solutions

1. **Remove it** (preferred). Delete `cfsolver/`, `backend/internal/cfsolver/`,
   the compose services, the Helm template + configmap keys, and the image from
   the release/nightly workflows. Note `docs/trackers.md` already tells users
   the sidecar is obsolete, and the issue template still offers "docker compose
   (cfsolver profile)" as a deployment option — both need updating.
2. **Gate it** as a minimum: make the configmap keys conditional on a values
   flag defaulting to false, so a chart install stops asserting a solver exists.
3. Keep it only if there is a concrete plan to use it — in which case say so in
   `CLAUDE.md`, which currently says the opposite.
