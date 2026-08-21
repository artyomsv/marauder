#!/usr/bin/env bash
# Render-assertion test harness for the marauder Helm chart.
# Renders the chart with various values and asserts on the output — no cluster
# needed. Set HELM to override the helm binary (default: helm).
set -uo pipefail

# Prevent Git Bash / MSYS from rewriting --set values that look like POSIX
# paths (e.g. /d -> D:/). No-op on Linux CI.
export MSYS_NO_PATHCONV=1
export MSYS2_ARG_CONV_EXCL='*'

HELM="${HELM:-helm}"
HERE="$(cd "$(dirname "$0")" && pwd)"
CHART="$(cd "$HERE/.." && pwd)"
# On Git Bash, hand helm a native Windows path (no-conv leaves args untouched).
case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) CHART="$(cygpath -w "$CHART")";; esac
REL=t                      # release name -> fullname "t-marauder"
FAIL=0
PASS=0

# Most assertions don't care about the master key; supply one by default so the
# required-masterKey guard doesn't trip every render. Override per-assertion.
DEFAULTS="--set secrets.masterKey=ci-test --set-string secrets.dbPassword=ci-test"

# render <args...> -> stdout (stderr folded in so template errors are visible).
# `|| true` so an intentional `fail` (non-zero exit) doesn't trip pipefail and
# mask grep's result — assertions judge by output content, not exit code.
render() { "$HELM" template "$REL" "$CHART" $DEFAULTS "$@" 2>&1 || true; }

# assert_contains "desc" "pattern" -- <helm args>
assert_contains() {
  local desc="$1" pat="$2"; shift 3
  if render "$@" | grep -Eq -- "$pat"; then PASS=$((PASS+1));
  else echo "FAIL: $desc (missing /$pat/)"; FAIL=$((FAIL+1)); fi
}
# assert_absent "desc" "pattern" -- <helm args>
assert_absent() {
  local desc="$1" pat="$2"; shift 3
  if render "$@" | grep -Eq -- "$pat"; then echo "FAIL: $desc (found /$pat/)"; FAIL=$((FAIL+1));
  else PASS=$((PASS+1)); fi
}

CNPG="--set database.mode=cnpg --set database.cnpg.assertCRDs=false"
# A shared downloads volume so enabling a client/arr doesn't trip the RWX guard.
SHARED="--set persistence.downloads.type=existingClaim --set persistence.downloads.existingClaim=media"
# CNPG backup base args.
BK="$CNPG --set database.cnpg.backup.enabled=true --set database.cnpg.backup.s3.destinationPath=s3://b/x --set database.cnpg.backup.s3.endpointURL=http://m:9000"

# --- Task 1: scaffold ---
assert_contains "serviceaccount present"        'kind: ServiceAccount' --
assert_contains "serviceaccount name"           'name: t-marauder$'    --

# --- Task 3: core wiring ---
assert_contains "backend mounts /config"        'mountPath: /config'   --
assert_contains "backend mounts /downloads"     'mountPath: /downloads' --
assert_contains "chart secret created"          'kind: Secret'         --
assert_absent  "no chart secret w/ existing"    'name: t-marauder-secrets' -- --set secrets.existingSecret=mine
assert_contains "backend refs existing secret"  'name: mine'           -- --set secrets.existingSecret=mine
assert_contains "cfsolver deployment"           'name: t-marauder-cfsolver' --
assert_contains "cfsolver port 9244"            'containerPort: 9244'  --
assert_contains "cfsolver url 9244"             'cfsolver:9244'        --
assert_contains "backend wait-for-db init"      'name: wait-for-db'    --
assert_contains "backend uses PGPASSWORD"       'name: PGPASSWORD'     --
assert_absent  "no password in DSN"             'postgres://[^@"]*:[^@"/]*@' --

# --- Task 2: volume model ---
assert_contains "nfs source"      'server: 10.0.0.1' -- --set persistence.downloads.type=nfs --set persistence.downloads.nfs.server=10.0.0.1 --set persistence.downloads.nfs.path=/d
assert_contains "nfs path"        'path: /d'         -- --set persistence.downloads.type=nfs --set persistence.downloads.nfs.server=10.0.0.1 --set persistence.downloads.nfs.path=/d
assert_contains "hostPath source" 'hostPath:'        -- --set persistence.downloads.type=hostPath --set persistence.downloads.hostPath.path=/mnt/d
assert_contains "existingClaim"   'claimName: myclaim' -- --set persistence.downloads.type=existingClaim --set persistence.downloads.existingClaim=myclaim
assert_contains "emptyDir source" 'emptyDir:'        -- --set persistence.downloads.type=emptyDir
assert_contains "pvc object"      'name: t-marauder-downloads' -- --set persistence.downloads.type=pvc --set persistence.downloads.pvc.size=50Gi
assert_contains "pvc size"        'storage: 50Gi'    -- --set persistence.downloads.type=pvc --set persistence.downloads.pvc.size=50Gi
assert_absent  "pvc no SC when empty" 'storageClassName' -- --set persistence.downloads.type=pvc --set persistence.downloads.pvc.size=50Gi --set database.simple.persistence.pvc.storageClass=
assert_contains "pvc SC when set" 'storageClassName: "longhorn"' -- --set persistence.downloads.type=pvc --set persistence.downloads.pvc.size=50Gi --set persistence.downloads.pvc.storageClass=longhorn
assert_contains "raw passthrough" 'driver: foo.csi' -- --set persistence.downloads.type=raw --set-json 'persistence.downloads.raw={"csi":{"driver":"foo.csi"}}'

# --- Task 4: exposure ---
assert_contains "gateway clusterip default" 'type: ClusterIP' --
assert_contains "gateway loadbalancer" 'type: LoadBalancer' -- --set gateway.service.type=LoadBalancer
assert_contains "gateway nodeport"     'type: NodePort'     -- --set gateway.service.type=NodePort
assert_absent  "no ingress by default" 'kind: Ingress'      --
assert_contains "ingress host"  'host: "m.example.com"'     -- --set ingress.enabled=true --set ingress.host=m.example.com
assert_contains "ingress tls"   'secretName: tls'           -- --set ingress.enabled=true --set ingress.host=m.example.com --set ingress.tls.secretName=tls

# --- Task 5: simple db ---
assert_contains "simple statefulset" 'name: t-marauder-db$' --
assert_contains "simple postgres img" 'image: postgres:17'  --
assert_contains "backend host simple" 'marauder-db:5432'    --

# --- Task 6: cnpg db ---
assert_contains "cnpg cluster" 'kind: Cluster'              -- $CNPG
assert_contains "cnpg apiver"  'postgresql.cnpg.io/v1'      -- $CNPG
assert_contains "backend host cnpg" 'marauder-db-rw:5432'   -- $CNPG
assert_contains "cnpg objectstore" 'kind: ObjectStore'      -- $CNPG --set database.cnpg.backup.enabled=true --set database.cnpg.backup.s3.destinationPath=s3://b/x --set database.cnpg.backup.s3.endpointURL=http://m:9000
assert_contains "cnpg scheduledbackup" 'kind: ScheduledBackup' -- $CNPG --set database.cnpg.backup.enabled=true --set database.cnpg.backup.s3.destinationPath=s3://b/x --set database.cnpg.backup.s3.endpointURL=http://m:9000
assert_contains "cnpg retention" 'retentionPolicy: "30d"'  -- $CNPG --set database.cnpg.backup.enabled=true --set database.cnpg.backup.s3.destinationPath=s3://b/x --set database.cnpg.backup.s3.endpointURL=http://m:9000
assert_absent  "no statefulset in cnpg" 'kind: StatefulSet' -- $CNPG

# --- Task 7/8: optional clients + arr (need a shared downloads volume) ---
assert_absent  "no qbittorrent default" 'name: t-marauder-qbittorrent' --
assert_contains "qbittorrent enabled" 'name: t-marauder-qbittorrent' -- --set clients.qbittorrent.enabled=true $SHARED
assert_contains "qbittorrent tag pinned" 'linuxserver/qbittorrent:5.0.3' -- --set clients.qbittorrent.enabled=true $SHARED
assert_contains "transmission enabled" 'name: t-marauder-transmission' -- --set clients.transmission.enabled=true $SHARED
assert_absent  "no sonarr default" 'name: t-marauder-sonarr' --
assert_contains "sonarr enabled" 'name: t-marauder-sonarr' -- --set arr.sonarr.enabled=true $SHARED
assert_contains "prowlarr enabled" 'name: t-marauder-prowlarr' -- --set arr.prowlarr.enabled=true
assert_contains "flaresolverr enabled" 'name: t-marauder-flaresolverr' -- --set arr.flaresolverr.enabled=true
# The container alone is not the feature: issue #158 was every deployment path
# starting a solver the backend was never pointed at, which is indistinguishable
# at runtime from having no solver. Assert the wiring, both directions.
assert_contains "flaresolverr url wired" 'MARAUDER_FLARESOLVERR_URL: "http://t-marauder-flaresolverr:8191"' -- --set arr.flaresolverr.enabled=true
assert_absent  "no flaresolverr url by default" 'MARAUDER_FLARESOLVERR_URL' --
# An explicit override must win outright, not render a second duplicate key.
assert_contains "flaresolverr url override wins" 'MARAUDER_FLARESOLVERR_URL: "http://mine:8191"' -- --set arr.flaresolverr.enabled=true --set config.MARAUDER_FLARESOLVERR_URL=http://mine:8191
assert_absent  "override leaves no bundled url" 'MARAUDER_FLARESOLVERR_URL: "http://t-marauder-flaresolverr' -- --set arr.flaresolverr.enabled=true --set config.MARAUDER_FLARESOLVERR_URL=http://mine:8191
# multiple clients at once share one downloads volume
assert_contains "multi: qbittorrent" 'name: t-marauder-qbittorrent' -- --set clients.qbittorrent.enabled=true --set clients.transmission.enabled=true $SHARED
assert_contains "multi: transmission" 'name: t-marauder-transmission' -- --set clients.qbittorrent.enabled=true --set clients.transmission.enabled=true $SHARED

# --- Hardening (security/code-review round 1) ---
assert_contains "automount token disabled" 'automountServiceAccountToken: false' --
assert_contains "pod seccomp RuntimeDefault" 'type: RuntimeDefault' --
assert_contains "container no-priv-escalation" 'allowPrivilegeEscalation: false' --
assert_contains "backend has resource limit" 'memory: 512Mi' --
assert_contains "backend config checksum" 'checksum/config:' --
assert_absent  "no networkpolicy by default" 'kind: NetworkPolicy' --
assert_contains "networkpolicy when enabled" 'kind: NetworkPolicy' -- --set networkPolicy.enabled=true
# masterKey guard fires when empty (raw call — no DEFAULTS key supplied)
if { "$HELM" template "$REL" "$CHART" 2>&1 || true; } | grep -Eq 'masterKey is required'; then PASS=$((PASS+1)); else echo "FAIL: masterKey required-guard"; FAIL=$((FAIL+1)); fi
# dbPassword guard fires when empty (masterKey supplied, dbPassword left empty)
if { "$HELM" template "$REL" "$CHART" --set secrets.masterKey=x 2>&1 || true; } | grep -Eq 'dbPassword is required'; then PASS=$((PASS+1)); else echo "FAIL: dbPassword required-guard"; FAIL=$((FAIL+1)); fi
# S3 backup creds guard fires when create=true but keys empty
assert_contains "s3 creds guard fires" 'accessKeyId and secretAccessKey are required' -- $BK --set database.cnpg.backup.s3.credentials.create=true
# S3 creds secret rendered when create=true + keys supplied
assert_contains "s3 creds secret created" 'name: t-marauder-backup-s3' -- $BK --set database.cnpg.backup.s3.credentials.create=true --set database.cnpg.backup.s3.credentials.accessKeyId=ak --set database.cnpg.backup.s3.credentials.secretAccessKey=sk
# enabling a client adds a matching NetworkPolicy allow-rule (6 base -> 7)
npq=$(render --set networkPolicy.enabled=true --set clients.qbittorrent.enabled=true $SHARED | grep -c 'kind: NetworkPolicy')
if [ "$npq" -ge 7 ]; then PASS=$((PASS+1)); else echo "FAIL: NP client allow-rule (got $npq policies)"; FAIL=$((FAIL+1)); fi

# --- Validations (fail-fast guards) ---
assert_contains "reserved config key fails" 'managed by the chart' -- --set config.MARAUDER_HTTP_ADDR=:9999
assert_contains "RWO shared downloads fails" 'must include ReadWriteMany' -- --set clients.qbittorrent.enabled=true --set persistence.downloads.type=pvc --set persistence.downloads.pvc.size=1Gi --set 'persistence.downloads.pvc.accessModes={ReadWriteOnce}'
assert_contains "emptyDir shared downloads fails" 'NOT shared' -- --set clients.qbittorrent.enabled=true

# --- Coverage gaps (qa round 1) ---
assert_contains "config volume pvc emitted" 'name: t-marauder-config' -- --set persistence.config.type=pvc --set persistence.config.pvc.size=2Gi
assert_contains "simple non-pvc inline volume" 'claimName: mydb' -- --set database.simple.persistence.type=existingClaim --set database.simple.persistence.existingClaim=mydb
assert_absent  "simple no VCT when non-pvc" 'volumeClaimTemplates' -- --set database.simple.persistence.type=existingClaim --set database.simple.persistence.existingClaim=mydb
assert_contains "nodeport value propagates" 'nodePort: 30080' -- --set gateway.service.type=NodePort --set gateway.service.nodePort=30080
assert_contains "loadBalancerIP propagates" 'loadBalancerIP: 1.2.3.4' -- --set gateway.service.type=LoadBalancer --set gateway.service.loadBalancerIP=1.2.3.4
assert_contains "ingress className" 'ingressClassName: nginx' -- --set ingress.enabled=true --set ingress.host=m.example.com --set ingress.className=nginx
assert_absent  "cnpg no s3 secret w/ existingSecret" 'name: t-marauder-backup-s3' -- $BK --set database.cnpg.backup.s3.credentials.existingSecret=my-s3
assert_contains "cnpg s3 existingSecret referenced" 'name: my-s3' -- $BK --set database.cnpg.backup.s3.credentials.existingSecret=my-s3

# --- H1: cnpg + existingSecret app-password handling ---
assert_contains "cnpg+existingSecret guard fails" 'existingAppSecret' -- $CNPG --set secrets.existingSecret=mine
assert_absent  "cnpg no db-app when existingAppSecret" 'name: t-marauder-db-app' -- $CNPG --set database.cnpg.existingAppSecret=my-app
assert_contains "cnpg uses existingAppSecret" 'name: my-app' -- $CNPG --set database.cnpg.existingAppSecret=my-app

echo "-----------------------------------------"
echo "PASS=$PASS FAIL=$FAIL"
exit $([ "$FAIL" -eq 0 ] && echo 0 || echo 1)
