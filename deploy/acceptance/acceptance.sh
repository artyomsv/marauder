#!/usr/bin/env bash
# Acceptance test for one Marauder download-client plugin against a REAL client
# container. Brings up the base stack + a single client (isolated Compose
# project), creates the client through the Marauder API — which runs the
# plugin's Test() login/handshake — and asserts the call returns 2xx.
#
# Usage: acceptance.sh <client> <channel>
#   client  : qbittorrent | transmission | deluge | utorrent
#   channel : pinned | latest
set -euo pipefail

CLIENT="${1:?usage: acceptance.sh <client> <pinned|latest>}"
CHANNEL="${2:?usage: acceptance.sh <client> <pinned|latest>}"

# Run from the deploy/ directory (this script lives in deploy/acceptance/).
DEPLOY_DIR="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=acceptance/lib.sh
source "$DEPLOY_DIR/acceptance/lib.sh"
cd "$DEPLOY_DIR"

# Isolated project + non-default API port so a running dev stack is untouched.
export MARAUDER_HOST_PORT="${MARAUDER_HOST_PORT:-34085}"
API="http://localhost:${MARAUDER_HOST_PORT}"
COMPOSE=(docker compose -p marauder-acceptance
  -f docker-compose.yml -f docker-compose.test-clients.yml)

# Map client -> service, plugin name, internal URL, image-tag env var.
case "$CLIENT" in
  qbittorrent)
    SERVICE=qbittorrent-521; PLUGIN=qbittorrent
    CLIENT_URL="http://qbittorrent-521:6611"; TAG_VAR=MARAUDER_TEST_QBIT_TAG ;;
  transmission)
    SERVICE=transmission-412; PLUGIN=transmission
    CLIENT_URL="http://transmission-412:9091/transmission/rpc"
    TAG_VAR=MARAUDER_TEST_TRANSMISSION_TAG ;;
  deluge)
    SERVICE=deluge-220; PLUGIN=deluge
    CLIENT_URL="http://deluge-220:8112"; TAG_VAR=MARAUDER_TEST_DELUGE_TAG ;;
  utorrent)
    SERVICE=utorrent; PLUGIN=utorrent
    CLIENT_URL="http://utorrent:8080"; TAG_VAR=MARAUDER_TEST_UTORRENT_TAG
    COMPOSE+=(--profile utorrent) ;;
  *) echo "unknown client: $CLIENT" >&2; exit 2 ;;
esac

[ "$CHANNEL" = latest ] && export "${TAG_VAR}=latest"
[ "$CHANNEL" = pinned ] || [ "$CHANNEL" = latest ] || {
  echo "unknown channel: $CHANNEL" >&2; exit 2; }

# Fresh, throwaway env (never touch the developer's deploy/.env).
ENV_FILE="$(mktemp)"
UP_LOG="$(mktemp)"
cp .env.example "$ENV_FILE"
MASTER=$(openssl rand -base64 32)
METRICS=$(openssl rand -hex 32)
sed -i "s|MARAUDER_MASTER_KEY=.*|MARAUDER_MASTER_KEY=${MASTER}|" "$ENV_FILE"
sed -i "s|MARAUDER_METRICS_TOKEN=.*|MARAUDER_METRICS_TOKEN=${METRICS}|" "$ENV_FILE"
COMPOSE+=(--env-file "$ENV_FILE")

cleanup() {
  "${COMPOSE[@]}" down -v >/dev/null 2>&1 || true
  rm -f "$ENV_FILE" "$UP_LOG"
}
trap cleanup EXIT

# Bring the stack up, retrying registry failures. Every image here is pulled on
# the anonymous Docker Hub quota shared by all GitHub-hosted runners, so a
# `toomanyrequests` reports the busy hour, not the client under test. Retrying
# clears most of them; the ones that survive exit EX_INFRA so the caller can
# tell "could not fetch the images" from "the client rejected us" instead of
# filing an upstream-regression issue about a registry (issues #119-#121, #166-#168).
UP_ATTEMPTS=3
echo "==> $CLIENT/$CHANNEL: bringing up base stack + $SERVICE"
for attempt in $(seq 1 "$UP_ATTEMPTS"); do
  if "${COMPOSE[@]}" up -d --wait --wait-timeout 180 \
       db backend frontend gateway "$SERVICE" >"$UP_LOG" 2>&1; then
    cat "$UP_LOG"
    break
  fi
  cat "$UP_LOG" >&2
  if ! acceptance_is_registry_failure "$(cat "$UP_LOG")"; then
    echo "FAIL: $CLIENT/$CHANNEL: stack did not come up" >&2
    exit 1
  fi
  if [ "$attempt" -eq "$UP_ATTEMPTS" ]; then
    echo "INFRA: $CLIENT/$CHANNEL: registry unavailable after $UP_ATTEMPTS attempts" >&2
    exit "$ACCEPTANCE_EX_INFRA"
  fi
  # Docker Hub's quota windows are minute-scale, so back off in tens of seconds.
  BACKOFF=$((attempt * 30))
  echo "==> registry failure (attempt $attempt/$UP_ATTEMPTS); retrying in ${BACKOFF}s" >&2
  "${COMPOSE[@]}" down -v >/dev/null 2>&1 || true
  sleep "$BACKOFF"
done

# Resolve per-client credentials.
USERNAME=admin; PASSWORD=""
case "$CLIENT" in
  qbittorrent)
    CID="$(${COMPOSE[@]} ps -q "$SERVICE")"
    for _ in $(seq 1 30); do
      PASSWORD=$(docker logs "$CID" 2>&1 \
        | grep "temporary password" | awk '{print $NF}' | tail -1 || true)
      [ -n "$PASSWORD" ] && break; sleep 2
    done
    [ -n "$PASSWORD" ] || { echo "no qBittorrent temp password" >&2; exit 1; } ;;
  transmission) USERNAME=""; PASSWORD="" ;;  # rpc-authentication-required: false
  deluge)       PASSWORD="deluge" ;;          # linuxserver default web password
  utorrent)     USERNAME=admin; PASSWORD="" ;;  # image default admin / empty
esac

# Build the plugin config (Deluge takes no username).
if [ "$PLUGIN" = deluge ]; then
  CONFIG=$(printf '{"url":"%s","password":"%s"}' "$CLIENT_URL" "$PASSWORD")
else
  CONFIG=$(printf '{"url":"%s","username":"%s","password":"%s"}' \
    "$CLIENT_URL" "$USERNAME" "$PASSWORD")
fi

echo "==> logging in to Marauder at $API"
TOK=$(curl -fsS -X POST "$API/api/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"pleasechangeme"}' \
  | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p')
[ -n "$TOK" ] || { echo "Marauder login failed" >&2; exit 1; }

echo "==> creating $PLUGIN client (runs plugin Test())"
REQ=$(printf '{"client_name":"%s","display_name":"acceptance-%s","is_default":false,"config":%s}' \
  "$PLUGIN" "$CLIENT" "$CONFIG")
CODE=$(curl -s -o /tmp/acceptance_resp.json -w '%{http_code}' \
  -X POST "$API/api/v1/clients" \
  -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "$REQ")

if [ "$CODE" -ge 200 ] && [ "$CODE" -lt 300 ]; then
  echo "PASS: $CLIENT/$CHANNEL create-client -> HTTP $CODE"
  exit 0
fi

echo "FAIL: $CLIENT/$CHANNEL create-client -> HTTP $CODE" >&2
cat /tmp/acceptance_resp.json >&2; echo >&2
echo "--- client logs ---" >&2; docker logs "$(${COMPOSE[@]} ps -q "$SERVICE")" 2>&1 | tail -40 >&2
echo "--- backend logs ---" >&2; "${COMPOSE[@]}" logs backend 2>&1 | tail -40 >&2
exit 1
