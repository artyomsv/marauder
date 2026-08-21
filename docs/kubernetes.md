# Running Marauder on Kubernetes

This guide is for **advanced users** who want to run Marauder on their own
Kubernetes cluster. It ships a single Helm chart (the source of truth) and two
derived consumption formats — Kustomize and plain manifests — so you can use
whichever fits your workflow.

> New to Marauder? Start with [getting-started.md](getting-started.md) and the
> docker-compose stacks under `deploy/`. Kubernetes is the advanced path.

## What you get

- **Core (always):** backend, frontend, `cfsolver`, and an nginx **gateway**
  (the single entrypoint), plus Postgres in one of two tiers.
- **Two database tiers:**
  - `simple` — a single Postgres pod. No HA, no backups. Great for one user.
  - `cnpg` — [CloudNativePG](https://cloudnative-pg.io/) with automatic
    failover and **Barman → S3** point-in-time backups.
- **A uniform volume model** — every persistent volume (DB data, app config,
  downloads/media, optional client/arr config) accepts the same six source
  types, so you decide exactly how storage is provided.
- **Optional extras (off by default):** bundled torrent clients
  (qBittorrent / Transmission) and a Sonarr / Prowlarr / FlareSolverr stack.

## Layout

```
deploy/
  helm/marauder/            # the Helm chart — SOURCE OF TRUTH
    values.yaml             # every option, documented
    values-simple-db.yaml   # preset: simple tier
    values-cnpg.yaml        # preset: cnpg tier
  kustomize/                # base + overlays that inflate the chart
    base/  overlays/simple-db/  overlays/cnpg/
  k8s/                      # plain manifests (generated from the chart)
    simple-db/marauder.yaml  cnpg/marauder.yaml
  render-manifests.sh       # regenerates deploy/k8s/** from the chart
```

## Prerequisites

- A Kubernetes cluster and `kubectl` access.
- A default StorageClass, **or** name one explicitly (see Volumes). Note: if
  your cluster has more than one StorageClass marked default, always set
  `storageClass` explicitly — leaving it blank is ambiguous.
- For the `cnpg` tier: the **CloudNativePG operator and the Barman Cloud
  plugin installed cluster-wide first** (the chart does not install operators).
- A stable `MARAUDER_MASTER_KEY` (base64-encoded 32 bytes) you keep safe — it
  encrypts tracker credentials and client configs and must not change.

---

## Install with Helm (recommended)

### Simple tier (quickstart)

```bash
helm install marauder deploy/helm/marauder -n marauder --create-namespace \
  -f deploy/helm/marauder/values-simple-db.yaml \
  --set image.tag=1.9.0 \
  --set persistence.downloads.type=pvc \
  --set persistence.downloads.pvc.storageClass=YOUR_SC \
  --set database.simple.persistence.pvc.storageClass=YOUR_SC \
  --set secrets.masterKey="$(openssl rand -base64 32)" \
  --set secrets.dbPassword="$(openssl rand -base64 24)" \
  --set initialAdmin.password="change-me"
```

Reach the UI (ClusterIP default):

```bash
kubectl -n marauder port-forward svc/marauder-gateway 8080:80
# open http://localhost:8080  — log in with admin / the password above
```

### CNPG tier (HA + S3 backups)

Install the operator + plugin once (versions per the CNPG docs):

```bash
kubectl apply --server-side -f \
  https://raw.githubusercontent.com/cloudnative-pg/cloudnative-pg/release-1.28/releases/cnpg-1.28.1.yaml
# then install the Barman Cloud plugin (see cloudnative-pg/plugin-barman-cloud)
```

Then:

```bash
helm install marauder deploy/helm/marauder -n marauder --create-namespace \
  -f deploy/helm/marauder/values-cnpg.yaml \
  --set image.tag=1.9.0 \
  --set database.cnpg.storage.storageClass=YOUR_SC \
  --set database.cnpg.backup.s3.destinationPath=s3://YOUR_BUCKET/marauder/ \
  --set database.cnpg.backup.s3.endpointURL=https://s3.amazonaws.com \
  --set database.cnpg.backup.s3.credentials.create=true \
  --set database.cnpg.backup.s3.credentials.accessKeyId=YOUR_KEY \
  --set database.cnpg.backup.s3.credentials.secretAccessKey=YOUR_SECRET \
  --set secrets.masterKey="$(openssl rand -base64 32)" \
  --set secrets.dbPassword="$(openssl rand -base64 24)" \
  --set initialAdmin.password="change-me"
```

The S3 endpoint is generic — AWS S3, MinIO, Backblaze B2, DO Spaces, etc. The
backend connects to the CNPG-managed `marauder-db-rw` Service, which always
points at the current primary (automatic failover).

---

## Install with Kustomize

The overlays inflate the chart via Kustomize's Helm support. Because the chart
lives in a sibling directory, you must allow it explicitly:

```bash
kubectl kustomize --enable-helm --load-restrictor LoadRestrictionsNone \
  deploy/kustomize/overlays/simple-db | kubectl apply -f -
```

Swap `simple-db` for `cnpg` for the other tier. Edit `values-*.yaml` (or the
overlay `valuesInline`) for your storage, S3, and secrets first.

> **Helm 4 note:** `kubectl kustomize --enable-helm` probes `helm version -c`,
> a flag Helm 4 removed. Use **Helm 3** for the kustomize path (the Helm chart
> itself works on both Helm 3 and 4).

## Install with plain manifests

`deploy/k8s/{simple-db,cnpg}/marauder.yaml` are pre-rendered from the chart
with placeholder secrets. Edit the placeholders (`REPLACE_ME*`), then:

```bash
kubectl create namespace marauder
kubectl -n marauder apply -f deploy/k8s/simple-db/marauder.yaml
```

These are generated — don't hand-maintain them long-term. Regenerate after
changing the chart with `deploy/render-manifests.sh` (CI enforces they stay in
sync).

---

## Volumes — the wide-options model

Every persistent volume takes a `type` plus a matching block. The same shape
applies to `persistence.config`, `persistence.downloads`,
`database.simple.persistence`, and each optional client/arr `config`.

```yaml
persistence:
  downloads:
    type: nfs   # existingClaim | pvc | nfs | hostPath | emptyDir | raw
```

| `type` | Use it for | Key fields |
|---|---|---|
| `existingClaim` | A PVC you already created (recommended for media) | `existingClaim: my-pvc` |
| `pvc` | Dynamic provisioning | `pvc.storageClass` (""=default), `pvc.size`, `pvc.accessModes` |
| `nfs` | A NAS export | `nfs.server`, `nfs.path` |
| `hostPath` | Single-node / homelab | `hostPath.path`, `hostPath.type` |
| `emptyDir` | Ephemeral scratch (not for media) | `emptyDir: {}` |
| `raw` | Anything else (CephFS, iSCSI, any CSI driver) | `raw: {<native volumeSource>}` |

Examples:

```yaml
# A NAS over NFS, shared by Marauder and any bundled clients:
persistence:
  downloads:
    type: nfs
    nfs: { server: 192.168.1.10, path: /export/downloads }

# Bring your own pre-created PVC:
persistence:
  downloads: { type: existingClaim, existingClaim: media-library }

# An exotic backend via the raw escape hatch:
persistence:
  downloads:
    type: raw
    raw:
      csi:
        driver: cephfs.csi.ceph.com
        volumeHandle: media
```

### ⚠ ReadWriteMany for shared downloads

The downloads volume is mounted by Marauder (for the `downloadfolder` client)
**and** by any bundled clients/arr. When **more than one pod** mounts it, the
volume must be **ReadWriteMany** (NFS, CephFS, Longhorn RWX, etc.). A plain
block volume (most `ReadWriteOnce` classes) can only be mounted by pods on one
node — use `hostPath` with node pinning, or a single mounting pod, in that case.

---

## Exposure — no load balancer assumed

`gateway.service.type` defaults to **`ClusterIP`** so the chart works on any
cluster. Pick what your environment provides:

| Environment | Setting |
|---|---|
| Any (port-forward / your own Ingress) | `gateway.service.type=ClusterIP` (default) |
| Cloud (EKS/GKE/AKS/DO…) — LB provisioned for you | `gateway.service.type=LoadBalancer` |
| Bare metal with MetalLB | `gateway.service.type=LoadBalancer` (+ optional `loadBalancerIP`) |
| Bare metal without an LB | `gateway.service.type=NodePort` (+ optional `nodePort`) |
| Have an ingress controller | `ingress.enabled=true`, `ingress.className`, `ingress.host` |

Ingress with cert-manager:

```yaml
ingress:
  enabled: true
  className: nginx
  host: marauder.example.com
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
  tls:
    secretName: marauder-tls
```

---

## Secrets

`MARAUDER_MASTER_KEY` (required, stable), the DB password, and the S3 backup
credentials each support two modes:

- **Chart-created** — pass the value (`secrets.masterKey`, `secrets.dbPassword`,
  `database.cnpg.backup.s3.credentials.*`). Convenient for quickstart.
- **Existing secret** — set `secrets.existingSecret` /
  `database.cnpg.backup.s3.credentials.existingSecret` to a Secret you manage
  out-of-band (SealedSecrets, External-Secrets, Vault, plain `kubectl`).

The chart never bundles a secrets operator — that choice is yours.

---

## Optional clients and the *arr stack

Off by default. Enable what you want; bundled clients share the downloads
volume automatically:

```yaml
clients:
  qbittorrent: { enabled: true }
arr:
  sonarr:   { enabled: true }
  prowlarr: { enabled: true }
  flaresolverr: { enabled: true }
```

**RuTracker needs `arr.flaresolverr`.** It is the one tracker behind a
Cloudflare challenge; without a solver, every check fails with "No Cloudflare
solver is configured". Setting `arr.flaresolverr.enabled=true` both deploys the
container and sets `MARAUDER_FLARESOLVERR_URL` on the backend — enabling only
one of those is [issue #158](https://github.com/artyomsv/marauder/issues/158)
and looks identical to having no solver. To use a solver you already run, leave
it disabled and set the URL yourself:

```yaml
config:
  MARAUDER_FLARESOLVERR_URL: http://flaresolverr.media.svc:8191
```

Either way the solver must egress from the same public IP as the backend —
Cloudflare binds the clearance to the requesting address.

FlareSolverr has no authentication, so keep it in-cluster: the chart's
NetworkPolicy already restricts ingress to first-party Marauder pods. Do not
expose it via an Ingress or a LoadBalancer Service.

You can also run your download clients outside the chart and point Marauder at
them by Service DNS (e.g. `http://qbittorrent.media.svc:8080`) — the same way
the docker-compose stack uses `http://qbittorrent:6611`.

---

## Verified configurations

These configurations were deployed and exercised end-to-end on a homelab k3s
cluster (k3s v1.31–1.33, MetalLB, Longhorn, local-path) before release:

| Config | Tier | Downloads volume | Exposure | Result |
|---|---|---|---|---|
| Minimal/portable | simple | `pvc` (local-path, RWO) | ClusterIP | All pods Ready; backend serves the API; 0 restarts |
| NAS-style shared | simple | `pvc` (Longhorn, **RWX**) | MetalLB LoadBalancer | LB IP assigned; bundled qBittorrent shares the downloads volume (write from backend → read from client) |
| HA + backups | cnpg (2 instances) | `pvc` (local-path) | ClusterIP | Cluster healthy with failover; on-demand backup `completed`; `ContinuousArchiving=True`; base backups + WAL archive land in S3 (MinIO) |
| Raw escape hatch | simple | `raw` (pre-existing PVC) | ClusterIP | Raw volumeSource passed through and mounted; verified with a special-character DB password |

The DB password is passed to the backend via `PGPASSWORD` (not embedded in the
DSN URL), so passwords containing `/`, `+`, `@`, `:` etc. work correctly.

**End-to-end product flow** was also verified on the same cluster: a topic was
registered (a real, well-seeded public `.torrent` via the Generic .torrent URL
tracker) against a bundled qBittorrent client. The scheduler detected the
release, pushed it to qBittorrent, recorded the delivery, and the download ran
to completion on the shared (Longhorn RWX) downloads volume — with Marauder's
`/topics/{id}/status` reflecting live progress (`downloading` → `seeding`) read
back from the client.

## Upgrades & backups

- **Upgrade:** `helm upgrade marauder deploy/helm/marauder -n marauder --reuse-values`
  (re-using values keeps `MARAUDER_MASTER_KEY` stable). Bump `image.tag` to move
  Marauder versions.
- **CNPG backups:** a daily `ScheduledBackup` plus continuous WAL archiving runs
  automatically when `database.cnpg.backup.enabled=true`. Trigger an on-demand
  backup with a `Backup` CR; restore via a CNPG `bootstrap.recovery` Cluster
  (see the CloudNativePG docs).
