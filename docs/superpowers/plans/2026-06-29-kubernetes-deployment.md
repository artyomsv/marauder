# Kubernetes Deployment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a robust, cluster-agnostic Kubernetes deployment for Marauder — a single Helm chart (source of truth) plus derived Kustomize and plain-manifest forms — supporting a simple single-pod Postgres tier and a CNPG+Barman→S3 tier, with a uniform, widely-configurable volume model.

**Architecture:** One Helm chart under `deploy/helm/marauder` is authoritative. A reusable `marauder.volume`/`marauder.volumeMount` template gives every persistent volume (DB data, app config, downloads, optional client/arr config) the same six source types (existingClaim/pvc/nfs/hostPath/emptyDir/raw). Kustomize inflates the chart via `helmCharts:`; plain manifests under `deploy/k8s/**` are `helm template` output, CI-verified against the chart. Tested live on a homelab k3s cluster in the `testing` namespace only.

**Tech Stack:** Helm (chart apiVersion v2, must work on Helm 3.x and 4.x), Kustomize (Helm inflation), CloudNativePG + Barman Cloud plugin, kubeconform (schema validation), GitHub Actions CI, k3s + MetalLB + nfs-client/local-path/longhorn StorageClasses for testing.

## Global Constraints

- Chart `apiVersion: v2`; avoid Helm-v4-only template syntax so Helm 3.x users work. Verify with `helm lint` under the installed v4 binary.
- **No load-balancer assumption.** Gateway `Service.type` defaults to `ClusterIP`; `LoadBalancer`/`NodePort` and an optional `Ingress` are opt-in. MetalLB is a test-only convenience, never a chart default.
- **CNPG operator is a prerequisite, not bundled.** Tier B asserts the `clusters.postgresql.cnpg.io` + `objectstores.barmancloud.cnpg.io` CRDs exist and fails with a clear message otherwise.
- **Generic S3** for backups: `endpointURL` + bucket/path + credentials Secret. No DigitalOcean/vendor hardcoding.
- Images: `ghcr.io/artyomsv/marauder-backend|frontend|cfsolver`, tag from `.Values.image.tag` (default the current release, overridable). Gateway uses upstream `nginx`.
- Every persistent volume routes through the `marauder.volume`/`marauder.volumeMount` helper. No inline volume sources in workload templates.
- Source-of-truth rule: edit only the Helm chart; regenerate `deploy/k8s/**` via the render script. CI fails on drift.
- Secrets: each of `MARAUDER_MASTER_KEY`, DB password, S3 creds supports chart-created (from values) **or** `existingSecret`. `MARAUDER_MASTER_KEY` documented as must-stay-stable.
- Testing happens exclusively in the `testing` namespace on the homelab k3s cluster. Never touch other namespaces.
- Commits: imperative mood, ≤72 char subject, no AI/agentic attribution. Commit per task.

---

## File Structure

```
deploy/
  helm/marauder/
    Chart.yaml
    values.yaml                      # all toggles, documented
    values-simple-db.yaml            # preset: simple tier
    values-cnpg.yaml                 # preset: cnpg tier
    .helmignore
    templates/
      _helpers.tpl                   # names, labels, marauder.volume(+Mount), env, image
      NOTES.txt
      serviceaccount.yaml
      configmap.yaml                 # MARAUDER_* env
      secret.yaml                    # chart-created secrets (skipped when existingSecret)
      backend.yaml                   # Deployment + Service
      cfsolver.yaml                  # Deployment + Service
      frontend.yaml                  # Deployment + Service
      gateway.yaml                   # Deployment + Service (+ optional Ingress)
      ingress.yaml
      db-simple.yaml                 # StatefulSet + headless Service (mode=simple)
      db-cnpg.yaml                   # Cluster + ObjectStore + ScheduledBackup (mode=cnpg)
      clients/
        qbittorrent.yaml             # optional
        transmission.yaml            # optional
      arr/
        sonarr.yaml                  # optional
        prowlarr.yaml                # optional
        flaresolverr.yaml            # optional
    tests/                           # helm-unittest-style YAML render tests
      *_test.yaml
  kustomize/
    base/kustomization.yaml          # helmCharts: -> ../../helm/marauder
    overlays/simple-db/kustomization.yaml
    overlays/cnpg/kustomization.yaml
  k8s/                               # GENERATED, CI-verified
    simple-db/marauder.yaml
    cnpg/marauder.yaml
  render-manifests.sh                # helm template -> deploy/k8s/**
docs/kubernetes.md
.github/workflows/helm.yml
```

**Testing mechanism for chart work:** render assertions via `helm template` piped through `grep`/`yq` in a portable bash harness (`deploy/helm/marauder/tests/render.sh`), runnable in Git Bash and CI without extra plugins. Schema validation via `kubeconform` in Docker. Live install verification on the `testing` namespace for the acceptance task. (The TDD "test" is the render assertion; "implementation" is the template.)

---

### Task 1: Chart scaffold, helpers, lint baseline

**Files:**
- Create: `deploy/helm/marauder/Chart.yaml`, `values.yaml`, `.helmignore`
- Create: `deploy/helm/marauder/templates/_helpers.tpl`, `NOTES.txt`, `serviceaccount.yaml`
- Test: `deploy/helm/marauder/tests/render.sh`

**Interfaces:**
- Produces: `marauder.name`, `marauder.fullname`, `marauder.labels`, `marauder.selectorLabels`, `marauder.componentLabels` (template helpers consumed by every workload), `marauder.image` (renders `repo:tag`).

- [ ] **Step 1: Write render harness + first failing assertion.** Create `tests/render.sh` that runs `helm template t . [-f valuesfile] [--set ...]` and greps. First assertion: default render contains `ServiceAccount` named `t-marauder`. Run it → fails (no chart yet).
- [ ] **Step 2:** Write `Chart.yaml` (apiVersion v2, name `marauder`, type application, version 0.1.0, appVersion matching current release), `.helmignore`, minimal `values.yaml` (`nameOverride`, `fullnameOverride`, `serviceAccount.create: true`).
- [ ] **Step 3:** Write `_helpers.tpl` with the name/label/image helpers (standard Helm idiom).
- [ ] **Step 4:** Write `serviceaccount.yaml` (gated on `.Values.serviceAccount.create`) and a `NOTES.txt` summarizing access URL by Service type.
- [ ] **Step 5:** Run `helm lint .` (expect 0 failures) and `tests/render.sh` (expect PASS).
- [ ] **Step 6: Commit** — `feat(deploy): scaffold marauder helm chart`

---

### Task 2: The `volumeSpec` helper (centerpiece)

**Files:**
- Modify: `deploy/helm/marauder/templates/_helpers.tpl`
- Modify: `values.yaml` (add `persistence.config`, `persistence.downloads` blocks with `type` + per-type sub-blocks)
- Test: `deploy/helm/marauder/tests/render.sh` (add volume cases)

**Interfaces:**
- Produces:
  - `marauder.volume` — input `dict "name" <volName> "spec" <persistence.X>`; emits a single pod `volumes:` entry whose source matches `spec.type` (`existingClaim`→`persistentVolumeClaim.claimName`, `pvc`→`persistentVolumeClaim.claimName: <fullname>-<volName>`, `nfs`→`nfs{server,path}`, `hostPath`→`hostPath{path,type}`, `emptyDir`→`emptyDir`, `raw`→`toYaml spec.raw`).
  - `marauder.volumeMount` — input `dict "name" <volName> "mountPath" <path>`; emits the `volumeMounts:` entry.
  - `marauder.pvc` — for `type: pvc`, emits a `PersistentVolumeClaim` object (`<fullname>-<volName>`) with `storageClassName` (omitted when empty → cluster default), `accessModes`, `resources.requests.storage`.
- Consumes: `marauder.fullname` (Task 1).

- [ ] **Step 1: Write failing render assertions** in `render.sh` for each type, e.g.:
  - `--set persistence.downloads.type=nfs,persistence.downloads.nfs.server=10.0.0.1,persistence.downloads.nfs.path=/d` → output contains `nfs:` and `server: 10.0.0.1`.
  - `type=hostPath` → `hostPath:` + `path:`.
  - `type=existingClaim,existingClaim=myclaim` → `claimName: myclaim`.
  - `type=pvc` → a `kind: PersistentVolumeClaim` doc named `t-marauder-downloads` AND no `storageClassName` line when `pvc.storageClass=""`; with `pvc.storageClass=longhorn` → `storageClassName: longhorn`.
  - `type=emptyDir` → `emptyDir:`.
  - `type=raw` with `--set-json persistence.downloads.raw='{"csi":{"driver":"x"}}'` → `csi:` + `driver: x`.
  Run → fail.
- [ ] **Step 2:** Implement `marauder.volume`, `marauder.volumeMount`, `marauder.pvc` in `_helpers.tpl` (a `with`/`if eq .spec.type` ladder; `pvc` and `existingClaim` both render `persistentVolumeClaim`). Add a standalone `pvc.yaml` template that loops persistence entries with `type: pvc` and emits PVCs.
- [ ] **Step 3:** Add the `persistence.config` and `persistence.downloads` defaults to `values.yaml` (default `config.type=emptyDir`, `downloads.type=emptyDir` with documented accessModes default `[ReadWriteOnce]`).
- [ ] **Step 4:** Run `tests/render.sh` → all volume cases PASS. `helm lint .` clean.
- [ ] **Step 5: Commit** — `feat(deploy): add reusable volumeSpec helm helper`

---

### Task 3: Core backend + cfsolver + config + secrets

**Files:**
- Create: `templates/configmap.yaml`, `secret.yaml`, `backend.yaml`, `cfsolver.yaml`
- Modify: `values.yaml` (`image`, `config`, `secrets`, `extraEnv`, `resources`, per-component blocks)
- Test: `tests/render.sh`

**Interfaces:**
- Consumes: `marauder.image`, `marauder.labels`, `marauder.volume`/`marauder.volumeMount` (downloads + config), name helpers.
- Produces: ConfigMap `<fullname>-config` (MARAUDER_* env from `.Values.config`), Secret `<fullname>-secrets` (keys `MARAUDER_MASTER_KEY`, `MARAUDER_DB_PASSWORD`) created only when `secrets.create` and no `existingSecret`. Backend env: `envFrom` configmap + secretRef; `MARAUDER_DB_URL` assembled from db host/port/name/user + password secret key.

- [ ] **Step 1: Failing assertions:** default render has Deployment `t-marauder-backend` with `envFrom` referencing `t-marauder-config` + a secret; `--set secrets.existingSecret=mine` → no `kind: Secret` from chart AND backend secretRef name `mine`; backend mounts downloads at `/downloads` and config at `/config`; cfsolver Deployment + Service `t-marauder-cfsolver` present, no volumes. Run → fail.
- [ ] **Step 2:** Write `configmap.yaml` (range over `.Values.config` map → data; include `MARAUDER_CFSOLVER_URL` default `http://<fullname>-cfsolver:8191`). Write `secret.yaml` (gated). Write `backend.yaml` (Deployment + Service:8679 internal, probes on backend health path, `envFrom`, explicit `MARAUDER_DB_*`, volumes via helper). Write `cfsolver.yaml`.
- [ ] **Step 3:** Add values blocks. DB connection values under `database.host/port/name/user` with sensible defaults that Task 5/6 set.
- [ ] **Step 4:** `tests/render.sh` PASS; `helm lint` clean; `helm template . | kubeconform -strict -ignore-missing-schemas` (via Docker) clean.
- [ ] **Step 5: Commit** — `feat(deploy): add backend, cfsolver, config and secrets`

---

### Task 4: frontend + gateway + ingress + service exposure

**Files:**
- Create: `templates/frontend.yaml`, `gateway.yaml`, `ingress.yaml`
- Modify: `values.yaml` (`gateway.service.type`, `ingress.*`)
- Test: `tests/render.sh`

**Interfaces:**
- Consumes: name/label helpers, `marauder.image`.
- Produces: gateway Service `<fullname>-gateway` (type from `.Values.gateway.service.type`, default ClusterIP) port 80→6688; frontend Deployment+Service; optional Ingress routing host→gateway. Gateway config (nginx) supplied via a ConfigMap mirroring `deploy/nginx/gateway.conf` (proxies `/`→frontend, `/api`→backend).

- [ ] **Step 1: Failing assertions:** default gateway Service `type: ClusterIP`; `--set gateway.service.type=LoadBalancer` → `type: LoadBalancer`; `--set gateway.service.type=NodePort` → `type: NodePort`; `ingress.enabled=false` default → no `kind: Ingress`; `--set ingress.enabled=true,ingress.host=m.example.com` → Ingress with that host + backend gateway; `--set ingress.tls.secretName=tls` → `tls:` block. Run → fail.
- [ ] **Step 2:** Write `frontend.yaml`, gateway nginx ConfigMap + Deployment + Service in `gateway.yaml`, `ingress.yaml` (gated, `ingressClassName` from values, optional cert-manager annotation passthrough via `ingress.annotations`).
- [ ] **Step 3:** `tests/render.sh` PASS; lint + kubeconform clean.
- [ ] **Step 4: Commit** — `feat(deploy): add frontend, gateway and optional ingress`

---

### Task 5: DB Tier A — simple single-pod Postgres

**Files:**
- Create: `templates/db-simple.yaml`
- Modify: `values.yaml` (`database.mode`, `database.simple.*`)
- Test: `tests/render.sh`

**Interfaces:**
- Consumes: `marauder.volume` helpers (data volume), secret for password.
- Produces: when `database.mode=simple`: StatefulSet `<fullname>-db` (postgres:17, `replicas:1`), headless Service `<fullname>-db` (the host backend uses), `volumeClaimTemplate` driven by `database.simple.persistence` volumeSpec (default `type: pvc`, empty storageClass).

- [ ] **Step 1: Failing assertions:** default (`mode=simple`) → `kind: StatefulSet` `t-marauder-db`, postgres image, headless Service (`clusterIP: None`); `--set database.mode=cnpg` → no StatefulSet from this template; data PVC honors `database.simple.persistence` (e.g. storageClass override appears). Backend `MARAUDER_DB_*` host resolves to `t-marauder-db`. Run → fail.
- [ ] **Step 2:** Write `db-simple.yaml` (gated `eq .Values.database.mode "simple"`). Password via `secretKeyRef`. Document "no HA, no backups" in NOTES when this mode is active.
- [ ] **Step 3:** `tests/render.sh` PASS; lint + kubeconform clean.
- [ ] **Step 4: Commit** — `feat(deploy): add simple single-pod postgres tier`

---

### Task 6: DB Tier B — CNPG Cluster + Barman→S3

**Files:**
- Create: `templates/db-cnpg.yaml`
- Modify: `values.yaml` (`database.cnpg.*`: `instances`, `storage`, `backup.enabled`, `backup.s3.{endpointURL,destinationPath,bucketCredentialsSecret}`, `backup.retentionPolicy`, compression/`archive_timeout` defaults, `assertCRDs`)
- Test: `tests/render.sh`

**Interfaces:**
- Consumes: name/label helpers.
- Produces: when `database.mode=cnpg`: `postgresql.cnpg.io/v1` Cluster `<fullname>-db` (instances≥2, storage via `storageClass`+size, bootstrap.initdb with db/owner/secret); when `backup.enabled`: `barmancloud.cnpg.io/v1` ObjectStore `<fullname>-backup-store` (generic S3 endpoint, creds Secret) + `ScheduledBackup`. Backend host uses CNPG-created `<fullname>-db-rw` Service.

- [ ] **Step 1: Failing assertions:** `-f values-cnpg.yaml` → `kind: Cluster` apiVersion `postgresql.cnpg.io/v1`, `instances: 2`; backend `MARAUDER_DB_*` host = `t-marauder-db-rw`; with `backup.enabled=true` → `kind: ObjectStore` with `endpointURL` from values and `kind: ScheduledBackup`; backup carries `retentionPolicy` + `wal.compression`. CRD-assert helper renders a `required`/lookup guard. Run → fail.
- [ ] **Step 2:** Write `db-cnpg.yaml` (gated `eq .Values.database.mode "cnpg"`). Use `.Capabilities.APIVersions.Has "postgresql.cnpg.io/v1"` guarded by `database.cnpg.assertCRDs` → `fail` with install hint. Wire backup defaults from the monorepo lessons (retention, lz4 WAL, archive_timeout, data/WAL compression).
- [ ] **Step 3:** `tests/render.sh` PASS; lint with `-f values-cnpg.yaml`; kubeconform with `-ignore-missing-schemas` (CRDs).
- [ ] **Step 4: Commit** — `feat(deploy): add cnpg postgres tier with barman s3 backup`

---

### Task 7: Optional download clients

**Files:**
- Create: `templates/clients/qbittorrent.yaml`, `templates/clients/transmission.yaml`
- Modify: `values.yaml` (`clients.qbittorrent.enabled`, `clients.transmission.enabled`, each with `image`, `config` volumeSpec, shared downloads mount path)
- Test: `tests/render.sh`

**Interfaces:**
- Consumes: `marauder.volume` (per-client config + shared downloads), name/label helpers.
- Produces: when enabled, a Deployment + Service per client; each mounts its own config volume and the shared `persistence.downloads` volume at the client's download path.

- [ ] **Step 1: Failing assertions:** default → no qbittorrent/transmission objects; `--set clients.qbittorrent.enabled=true` → Deployment + Service `t-marauder-qbittorrent`, mounts downloads volume + its config volume; same for transmission. Run → fail.
- [ ] **Step 2:** Write the two templates (linuxserver images, sensible default ports, downloads mount shared with backend).
- [ ] **Step 3:** `tests/render.sh` PASS; lint + kubeconform clean.
- [ ] **Step 4: Commit** — `feat(deploy): add optional bundled download clients`

---

### Task 8: Optional *arr stack

**Files:**
- Create: `templates/arr/sonarr.yaml`, `prowlarr.yaml`, `flaresolverr.yaml`
- Modify: `values.yaml` (`arr.sonarr.enabled`, `arr.prowlarr.enabled`, `arr.flaresolverr.enabled` + per-app image/config volumeSpec)
- Test: `tests/render.sh`

**Interfaces:**
- Consumes: `marauder.volume`, name/label helpers.
- Produces: when enabled, Deployment + Service per app; sonarr mounts downloads + config; prowlarr/flaresolverr config only.

- [ ] **Step 1: Failing assertions:** default → none; enabling each yields its Deployment+Service; sonarr mounts downloads volume. Run → fail.
- [ ] **Step 2:** Write the three templates (mirror `deploy/docker-compose.arr.yml` images/ports; flaresolverr internal-only Service).
- [ ] **Step 3:** `tests/render.sh` PASS; lint + kubeconform clean.
- [ ] **Step 4: Commit** — `feat(deploy): add optional sonarr/prowlarr/flaresolverr stack`

---

### Task 9: Value presets + chart docs (values.yaml comments)

**Files:**
- Create: `values-simple-db.yaml`, `values-cnpg.yaml`
- Modify: `values.yaml` (ensure every key is commented)
- Test: `helm lint` with each preset

- [ ] **Step 1:** Write `values-simple-db.yaml` (mode simple, downloads example as comments) and `values-cnpg.yaml` (mode cnpg, backup.enabled true, S3 placeholders, instances 2).
- [ ] **Step 2:** `helm lint . -f values-simple-db.yaml` and `-f values-cnpg.yaml` both clean; `tests/render.sh` runs both presets.
- [ ] **Step 3: Commit** — `docs(deploy): add simple-db and cnpg value presets`

---

### Task 10: Kustomize base + overlays

**Files:**
- Create: `deploy/kustomize/base/kustomization.yaml`, `overlays/simple-db/kustomization.yaml`, `overlays/cnpg/kustomization.yaml`
- Test: `kubectl kustomize --enable-helm` build

- [ ] **Step 1: Failing check:** `kubectl kustomize deploy/kustomize/overlays/simple-db --enable-helm` errors (no files). 
- [ ] **Step 2:** Write base `kustomization.yaml` with `helmCharts:` entry (name marauder, path `../../helm/marauder`, releaseName `marauder`, valuesInline namespace). Overlays set `namespace:` + `valuesFile`/`additionalValuesFiles` to the tier preset.
- [ ] **Step 3:** `kubectl kustomize deploy/kustomize/overlays/simple-db --enable-helm` and `.../cnpg --enable-helm` both produce manifests (StatefulSet vs Cluster respectively).
- [ ] **Step 4: Commit** — `feat(deploy): add kustomize base and tier overlays`

---

### Task 11: Generated plain manifests + render script

**Files:**
- Create: `deploy/render-manifests.sh`, `deploy/k8s/simple-db/marauder.yaml`, `deploy/k8s/cnpg/marauder.yaml`
- Test: the render script's `--check` mode

**Interfaces:**
- Produces: `render-manifests.sh` runs `helm template marauder deploy/helm/marauder -n marauder -f <preset>` per tier → writes `deploy/k8s/<tier>/marauder.yaml`. `--check` re-renders to a temp dir and `diff`s; nonzero exit on drift.

- [ ] **Step 1:** Write `render-manifests.sh` (write mode + `--check` mode).
- [ ] **Step 2:** Run it (write mode) → creates both manifest files.
- [ ] **Step 3:** Run `render-manifests.sh --check` → exit 0 (in sync). Edit a template trivially, re-check → exit nonzero (proves the gate), revert.
- [ ] **Step 4: Commit** — `feat(deploy): generate plain k8s manifests with drift check`

---

### Task 12: CI workflow

**Files:**
- Create: `.github/workflows/helm.yml`
- Test: `act`-free reasoning + local equivalents already run in prior tasks

- [ ] **Step 1:** Write `helm.yml` (triggers on PR/push touching `deploy/helm/**`, `deploy/kustomize/**`, `deploy/k8s/**`): jobs — `lint` (helm lint both presets), `render-check` (`deploy/render-manifests.sh --check`), `validate` (helm template | kubeconform-strict), `kustomize-build` (`kubectl kustomize --enable-helm` both overlays), `install-smoke` (kind + `helm install` simple tier, wait for rollout).
- [ ] **Step 2:** Validate YAML syntax locally (`helm template`-equivalent steps already pass). 
- [ ] **Step 3: Commit** — `ci(deploy): add helm lint, render-check and kind smoke`

---

### Task 13: Docs + CLAUDE.md

**Files:**
- Create: `docs/kubernetes.md`
- Modify: `CLAUDE.md` (Top-level layout: add `deploy/helm`, `deploy/kustomize`, `deploy/k8s`)
- Modify: `README.md` (link to the k8s guide) — optional

- [ ] **Step 1:** Write `docs/kubernetes.md`: prerequisites, both tiers, the volume menu with copy-paste examples per backend (existingClaim/NFS/hostPath/dynamic/raw), RWX guidance for downloads, CNPG operator install prereq, secrets handling, exposure options (ClusterIP/LB/NodePort/Ingress; note MetalLB vs cloud LB vs none), optional clients & arr, install via Helm/Kustomize/plain manifests.
- [ ] **Step 2:** Update `CLAUDE.md` layout block.
- [ ] **Step 3: Commit** — `docs(deploy): add kubernetes deployment guide`

---

### Task 14: Live acceptance testing on homelab (testing namespace)

**Files:** none (verification task; fixes loop back into prior tasks/commits)

**Goal:** prove robustness across representative user setups. All installs in `-n testing`, distinct release names, cleaned up after.

- [ ] **Config A — minimal/portable:** `helm install a deploy/helm/marauder -n testing -f values-simple-db.yaml --set image.tag=<rel> --set persistence.downloads.type=pvc,persistence.downloads.pvc.storageClass=local-path,gateway.service.type=ClusterIP`. Wait for backend+db+frontend+gateway+cfsolver Ready. `kubectl -n testing port-forward svc/a-marauder-gateway 18080:80` and curl `/` + `/api/v1/system/info`. Record result. `helm uninstall a -n testing`; delete leftover PVCs.
- [ ] **Config B — NAS/RWX + MetalLB LB + bundled client:** simple tier, `persistence.downloads.type=nfs` (server/path from the `nfs-client` backing NAS or a `type=pvc,storageClass=nfs-client` RWX claim), `gateway.service.type=LoadBalancer`, `clients.qbittorrent.enabled=true`. Verify the LB gets a `192.168.7.x` IP, qbittorrent shares the downloads volume (exec into both pods, write a file from backend, read from qbittorrent). Uninstall + clean.
- [ ] **Config C — CNPG + real S3 backup:** deploy an ephemeral MinIO in `testing` (Deployment+Service+PVC, `CANARY`/obviously-test creds), create a bucket; `helm install c -f values-cnpg.yaml --set database.cnpg.instances=2,database.cnpg.backup.enabled=true,database.cnpg.backup.s3.endpointURL=http://c-minio:9000,...,destinationPath=s3://marauder-backups/c/`. Wait for the CNPG Cluster healthy (`kubectl -n testing get cluster`), trigger an on-demand `Backup`, assert objects land in MinIO and `status.conditions ContinuousArchiving=True`. Uninstall, delete Cluster/PVCs/MinIO.
- [ ] **Config D — raw volume escape hatch:** simple tier with `--set-json persistence.downloads.raw='{"persistentVolumeClaim":{"claimName":"a-preexisting"}}'` (pre-create that PVC) → confirm backend mounts it. Quick render+apply check. Clean up.
- [ ] **Step: Record** all results (pass/fail, fixes applied) in a short section appended to `docs/kubernetes.md` ("Verified configurations") — only real observed outcomes, no fabricated values.
- [ ] **Commit** any fixes discovered during testing with focused messages.

---

## Self-Review

**Spec coverage:** Helm source-of-truth (T1–9), volume model incl. raw (T2), backend/cfsolver/frontend/gateway (T3–4), simple tier (T5), cnpg+barman+S3 (T6), optional clients (T7), optional arr (T8), presets (T9), kustomize (T10), generated manifests + drift gate (T11), CI incl. kind smoke (T12), docs + CLAUDE.md (T13), live multi-config testing incl. RWX + real S3 + LB-agnostic + raw (T14). Networking LB-agnostic constraint exercised in T4 (render) + T14 Config A/B. Secrets chart-created vs existingSecret in T3. GHCR chart publish intentionally deferred (stretch in ticket, not in this plan).

**Placeholder scan:** No "TBD"/"handle edge cases" left; each task lists exact files, gated conditions, and concrete render assertions/commands.

**Type consistency:** `marauder.volume`/`marauder.volumeMount`/`marauder.pvc` signatures defined in T2 and consumed identically in T3/5/7/8; DB host name contract (`<fullname>-db` simple, `<fullname>-db-rw` cnpg) consistent between T5/T6 and the backend env in T3.
