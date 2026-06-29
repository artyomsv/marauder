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

# render <args...> -> stdout (stderr folded in so template errors are visible)
render() { "$HELM" template "$REL" "$CHART" "$@" 2>&1; }

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

# --- Task 7/8: optional clients + arr ---
assert_absent  "no qbittorrent default" 'name: t-marauder-qbittorrent' --
assert_contains "qbittorrent enabled" 'name: t-marauder-qbittorrent' -- --set clients.qbittorrent.enabled=true
assert_contains "transmission enabled" 'name: t-marauder-transmission' -- --set clients.transmission.enabled=true
assert_absent  "no sonarr default" 'name: t-marauder-sonarr' --
assert_contains "sonarr enabled" 'name: t-marauder-sonarr' -- --set arr.sonarr.enabled=true
assert_contains "prowlarr enabled" 'name: t-marauder-prowlarr' -- --set arr.prowlarr.enabled=true
assert_contains "flaresolverr enabled" 'name: t-marauder-flaresolverr' -- --set arr.flaresolverr.enabled=true

echo "-----------------------------------------"
echo "PASS=$PASS FAIL=$FAIL"
exit $([ "$FAIL" -eq 0 ] && echo 0 || echo 1)
