#!/usr/bin/env bash
# Generate the plain Kubernetes manifests under deploy/k8s/** from the Helm
# chart (the single source of truth). Run with no args to (re)write them, or
# with --check to verify the committed files are in sync (CI gate).
#
#   deploy/render-manifests.sh            # write deploy/k8s/**
#   deploy/render-manifests.sh --check    # exit nonzero if out of date
#
# Set HELM to override the helm binary (default: helm).
set -euo pipefail

HELM="${HELM:-helm}"
HERE="$(cd "$(dirname "$0")" && pwd)"
CHART="$HERE/helm/marauder"
OUT="$HERE/k8s"
REL=marauder
NS=marauder
case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) export MSYS_NO_PATHCONV=1; CHART="$(cygpath -w "$CHART")";; esac

HEADER="# GENERATED FILE — do not edit by hand.
# Produced by deploy/render-manifests.sh from deploy/helm/marauder (the source
# of truth). Edit the chart and re-run the script; CI verifies these stay in
# sync. Secrets carry placeholders — replace before applying.
---"

render_tier() {
  # $1 = tier name (subdir), $2.. = extra helm args
  local tier="$1"; shift
  { echo "$HEADER"
    "$HELM" template "$REL" "$CHART" -n "$NS" \
      --set secrets.masterKey=REPLACE_ME_BASE64_32_BYTES \
      --set secrets.dbPassword=REPLACE_ME \
      --set initialAdmin.password=REPLACE_ME \
      "$@"
  }
}

write_or_check() {
  local mode="$1"
  local rc=0
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  render_tier simple-db -f "$CHART/values-simple-db.yaml" > "$tmp/simple-db.yaml"
  render_tier cnpg -f "$CHART/values-cnpg.yaml" --set database.cnpg.assertCRDs=false \
    --set database.cnpg.backup.s3.credentials.accessKeyId=REPLACE_ME \
    --set database.cnpg.backup.s3.credentials.secretAccessKey=REPLACE_ME > "$tmp/cnpg.yaml"

  for t in simple-db cnpg; do
    local dest="$OUT/$t/marauder.yaml"
    if [ "$mode" = check ]; then
      if ! diff -u "$dest" "$tmp/$t.yaml" >/dev/null 2>&1; then
        echo "OUT OF DATE: $dest (run deploy/render-manifests.sh)"; rc=1
      fi
    else
      mkdir -p "$OUT/$t"
      cp "$tmp/$t.yaml" "$dest"
      echo "wrote $dest"
    fi
  done
  return $rc
}

if [ "${1:-}" = "--check" ]; then
  write_or_check check && echo "deploy/k8s manifests are in sync."
else
  write_or_check write
fi
