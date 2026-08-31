#!/usr/bin/env bash
#
# Table-driven tests for acceptance_is_registry_failure. No Docker needed —
# the fixtures are real compose output captured from nightly run 33192434421
# (the run that filed the false client-canary issues #166-#168) plus the
# harness's own failure wording.
#   bash deploy/acceptance/lib_test.sh
# Exits non-zero if any assertion fails.

set -uo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$DIR/lib.sh"

pass=0
fail=0

# Assert the classifier's exit status (0 = registry/infra, 1 = real failure).
check() {
  local label="$1" want="$2" log="$3" got
  if acceptance_is_registry_failure "$log"; then got=0; else got=1; fi
  if [[ "$got" == "$want" ]]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    printf 'FAIL: %s\n  want-exit=%s got-exit=%s\n  log=%q\n' \
      "$label" "$want" "$got" "$log"
  fi
}

# ------------------------------------------------ real registry failures (0)

# Verbatim from run 33192434421, the three latest-canary legs.
check "docker hub quota, qbittorrent leg" 0 \
  ' qbittorrent-521 Error toomanyrequests: retry-after: 999.152µs, allowed: 44000/minute'
check "docker hub quota, daemon wording" 0 \
  'Error response from daemon: toomanyrequests: retry-after: 656.247µs, allowed: 44000/minute'
check "docker hub quota, deluge leg" 0 \
  ' deluge-220 Error toomanyrequests: retry-after: 1.248776ms, allowed: 44000/minute'

# The same quota reported in Docker Hub's human wording.
check "docker hub quota, human wording" 0 \
  'toomanyrequests: You have reached your pull rate limit. https://www.docker.com/increase-rate-limit'

check "registry 5xx" 0 \
  'failed to fetch oauth token: unexpected status from GET request: 503 Service Unavailable'
check "registry gateway error" 0 \
  'received unexpected HTTP status: 502 Bad Gateway'
check "registry TLS timeout" 0 \
  'net/http: TLS handshake timeout'
check "registry DNS failure" 0 \
  'dial tcp: lookup registry-1.docker.io on 127.0.0.53:53: server misbehaving'
check "buildkit layer fetch" 0 \
  'failed to copy: httpReadSeeker: failed open: unexpected status code 429'
check "engine config blob fetch" 0 \
  'error pulling image configuration: download failed after attempts=6'

# Multi-line, as the harness actually captures it: the marker can sit anywhere.
check "quota buried in a long log" 0 \
  ' gateway Pulling
 db Pulled
 qbittorrent-521 Error toomanyrequests: retry-after: 999.152µs, allowed: 44000/minute
Error response from daemon: toomanyrequests'

# -------------------------------------------------- real client failures (1)

# Produced by acceptance.sh itself once the stack is up — never infra.
check "plugin handshake rejected" 1 \
  'FAIL: qbittorrent/latest create-client -> HTTP 502'
check "client container never healthy" 1 \
  'container marauder-acceptance-deluge-220-1 is unhealthy'
check "compose wait timed out" 1 \
  'error: timeout waiting for containers to be healthy'
check "marauder login failed" 1 \
  'Marauder login failed'
check "empty output" 1 ''

# Benign: compose warns about the locally-built images on every single run
# (they have a build: section and no registry copy). Classifying that as a
# registry failure would make EVERY failure look like infrastructure.
check "build-service pull warning is not infra" 1 \
  " backend Warning pull access denied for marauder-backend, repository does not exist or may require 'docker login': denied: requested access to the resource is denied"

# Guard: a 4xx that is NOT the quota must stay a real failure, so a bad tag
# (typo, withdrawn release) is reported instead of retried forever.
check "manifest unknown is not infra" 1 \
  'manifest for linuxserver/qbittorrent:latest not found: manifest unknown'

# ---------------------------------------------------------------------- tally
if [[ "$fail" -gt 0 ]]; then
  printf '\n%d passed, %d FAILED\n' "$pass" "$fail"
  exit 1
fi
printf '%d passed, 0 failed\n' "$pass"
