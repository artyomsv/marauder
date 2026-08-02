# Client Acceptance + Upstream Canary Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Automate a nightly acceptance matrix that proves every supported download client (qBittorrent, Transmission, Deluge, µTorrent) still authenticates against a real container — on both a pinned baseline and the client's latest version — and auto-files an issue when an upstream release breaks a client.

**Architecture:** Reuse `deploy/docker-compose.test-clients.yml` (parameterised image tags) as the client source of truth. A single `deploy/acceptance/acceptance.sh <client> <channel>` script brings up the base stack plus one client under an isolated Compose project, creates the client through the Marauder API (which runs the plugin's `Test()`), and asserts `2xx`. A new `.github/workflows/client-acceptance.yml` runs it as a matrix: a blocking `pinned` job and a non-blocking `latest` canary job that files a deduped GitHub issue on failure.

**Tech Stack:** Docker Compose v2 (`--wait`), Bash, GitHub Actions, `gh` CLI. No application-code changes.

## Global Constraints

- **No AI references anywhere** — commit messages, auto-filed issue text, code comments, and docs must NOT mention Claude, Anthropic, Opus, Copilot, or any AI assistant/tooling. (User constraint.)
- **Host-exposed ports must be in 30000–49999** (`~/.claude/rules/local-port-ranges.md`).
- **No committed `latest` image tags** — image tags default to pinned versions; the canary swaps to `latest` only at runtime via an env var.
- **Conventional Commits**, imperative mood, subject ≤72 chars.
- **Docker Compose ≥ v2.17** required for `--wait` / `--wait-timeout` (GitHub `ubuntu-latest` satisfies this).
- **Go is run only via Docker** (`golang:1.25`) — not applicable here (no Go changes) but holds if any verification touches Go.
- **Isolated Compose project name `marauder-acceptance`** so local runs never touch a developer's `deploy`-project dev stack or its volumes.

---

## File Structure

| File | Responsibility |
|---|---|
| `deploy/docker-compose.test-clients.yml` (modify) | Parameterise the four primary client image tags with pinned defaults. |
| `deploy/acceptance/acceptance.sh` (create) | The runner: bring up base + one client, create-client via API, assert `Test()` passed. Locally runnable. |
| `.github/workflows/client-acceptance.yml` (create) | Matrix orchestration: blocking `pinned` job + non-blocking `latest` canary with auto-issue. |
| `CHANGELOG.md` (modify) | Record the new acceptance harness under `[Unreleased]`. |
| `CLAUDE.md` (modify) | Document the workflow + runner in the deploy/CI notes. |

---

## Task 1: Parameterise client image tags in the matrix file

**Files:**
- Modify: `deploy/docker-compose.test-clients.yml`

**Interfaces:**
- Produces: four env vars the runner/CI set — `MARAUDER_TEST_QBIT_TAG` (default `5.2.1`), `MARAUDER_TEST_TRANSMISSION_TAG` (default `4.1.2`), `MARAUDER_TEST_DELUGE_TAG` (default `2.2.0`), `MARAUDER_TEST_UTORRENT_TAG` (default `v2.1.0`).

- [ ] **Step 1: Edit the four primary service image lines**

In `deploy/docker-compose.test-clients.yml`, change the image tags of the four primary services (leave `qbittorrent-514` hard-pinned at `5.1.4`):

```yaml
  qbittorrent-521:
    <<: *qbit-common
    image: lscr.io/linuxserver/qbittorrent:${MARAUDER_TEST_QBIT_TAG:-5.2.1}
```
```yaml
  transmission-412:
    image: lscr.io/linuxserver/transmission:${MARAUDER_TEST_TRANSMISSION_TAG:-4.1.2}
```
```yaml
  deluge-220:
    image: lscr.io/linuxserver/deluge:${MARAUDER_TEST_DELUGE_TAG:-2.2.0}
```
```yaml
  utorrent:
    image: ekho/utorrent:${MARAUDER_TEST_UTORRENT_TAG:-v2.1.0}
```

- [ ] **Step 2: Verify the pinned default resolves**

Run:
```bash
docker compose -f deploy/docker-compose.test-clients.yml config | grep -E "image: .*qbittorrent"
```
Expected: `image: lscr.io/linuxserver/qbittorrent:5.2.1`

- [ ] **Step 3: Verify the override resolves to latest**

Run:
```bash
MARAUDER_TEST_QBIT_TAG=latest docker compose -f deploy/docker-compose.test-clients.yml config | grep -E "image: .*qbittorrent:"
```
Expected: includes `image: lscr.io/linuxserver/qbittorrent:latest` (the `qbittorrent-521` line) and `:5.1.4` (the untouched `qbittorrent-514` line).

- [ ] **Step 4: Commit**

```bash
git add deploy/docker-compose.test-clients.yml
git commit -m "test(deploy): parameterise client image tags for canary override"
```

---

## Task 2: Acceptance runner script

**Files:**
- Create: `deploy/acceptance/acceptance.sh`

**Interfaces:**
- Consumes: env vars from Task 1; `deploy/docker-compose.yml` + `deploy/docker-compose.test-clients.yml`; `deploy/.env.example`.
- Produces: CLI `acceptance.sh <client> <channel>` where `client ∈ {qbittorrent,transmission,deluge,utorrent}`, `channel ∈ {pinned,latest}`. Exit 0 = create-client returned 2xx (plugin `Test()` passed).

- [ ] **Step 1: Write the runner script**

Create `deploy/acceptance/acceptance.sh` with exactly this content:

```bash
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
cp .env.example "$ENV_FILE"
MASTER=$(openssl rand -base64 32)
METRICS=$(openssl rand -hex 32)
sed -i "s|MARAUDER_MASTER_KEY=.*|MARAUDER_MASTER_KEY=${MASTER}|" "$ENV_FILE"
sed -i "s|MARAUDER_METRICS_TOKEN=.*|MARAUDER_METRICS_TOKEN=${METRICS}|" "$ENV_FILE"
COMPOSE+=(--env-file "$ENV_FILE")

cleanup() {
  "${COMPOSE[@]}" down -v >/dev/null 2>&1 || true
  rm -f "$ENV_FILE"
}
trap cleanup EXIT

echo "==> $CLIENT/$CHANNEL: bringing up base stack + $SERVICE"
"${COMPOSE[@]}" up -d --wait --wait-timeout 180 \
  db backend frontend gateway "$SERVICE"

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
```

- [ ] **Step 2: Make it executable**

Run:
```bash
chmod +x deploy/acceptance/acceptance.sh
git update-index --chmod=+x deploy/acceptance/acceptance.sh 2>/dev/null || true
```

- [ ] **Step 3: Run qBittorrent pinned — verify PASS**

Run:
```bash
bash deploy/acceptance/acceptance.sh qbittorrent pinned
```
Expected: ends with `PASS: qbittorrent/pinned create-client -> HTTP 201` and exit 0. (Containers are torn down automatically by the trap.)

- [ ] **Step 4: Run the other three pinned clients — verify PASS**

Run each:
```bash
bash deploy/acceptance/acceptance.sh transmission pinned
bash deploy/acceptance/acceptance.sh deluge pinned
bash deploy/acceptance/acceptance.sh utorrent pinned
```
Expected: each prints `PASS: <client>/pinned create-client -> HTTP 201` and exits 0.

- [ ] **Step 5: Run qBittorrent latest — verify PASS**

Run:
```bash
bash deploy/acceptance/acceptance.sh qbittorrent latest
```
Expected: `PASS: qbittorrent/latest create-client -> HTTP 201` (the #38 fix makes latest, currently 5.2.x/204, pass). Confirms the latest channel wiring works.

- [ ] **Step 6: Verify a dev stack is untouched (isolation)**

If a dev stack is running, confirm it survives:
```bash
docker ps --format '{{.Names}}' | grep '^deploy-' | sort
```
Expected: the `deploy-*` dev containers are still present (the runner used project `marauder-acceptance` and a temp env, so nothing in the `deploy` project or `deploy/.env` was changed).

- [ ] **Step 7: Commit**

```bash
git add deploy/acceptance/acceptance.sh
git commit -m "test(deploy): add per-client acceptance runner against real clients"
```

---

## Task 3: CI workflow — blocking pinned job

**Files:**
- Create: `.github/workflows/client-acceptance.yml`

**Interfaces:**
- Consumes: `deploy/acceptance/acceptance.sh` (Task 2).
- Produces: a workflow with a `pinned` job (later extended in Task 4 with the `latest` job).

- [ ] **Step 1: Create the workflow with the pinned job**

Create `.github/workflows/client-acceptance.yml`:

```yaml
name: Client acceptance (pinned + latest canary)

# Broad, shallow matrix: every supported client is created through the
# Marauder API against a REAL container, on a pinned baseline and (nightly)
# on the client's latest image. Complements the deep qBittorrent pipeline in
# e2e.yml. Not run per-PR — too slow; PRs rely on unit tests.

on:
  schedule:
    - cron: '0 5 * * *'   # 05:00 UTC nightly (after e2e at 04:00)
  push:
    tags: ['v*']
  workflow_dispatch:

concurrency:
  group: client-acceptance-${{ github.ref }}
  cancel-in-progress: true

permissions:
  contents: read

jobs:
  pinned:
    name: pinned / ${{ matrix.client }}
    runs-on: ubuntu-latest
    timeout-minutes: 15
    strategy:
      fail-fast: false
      matrix:
        client: [qbittorrent, transmission, deluge, utorrent]
    steps:
      - name: Checkout
        uses: actions/checkout@v6
      - name: Acceptance (pinned baseline)
        run: bash deploy/acceptance/acceptance.sh ${{ matrix.client }} pinned
```

- [ ] **Step 2: Lint the workflow**

Run:
```bash
docker run --rm -v "E:/Projects/Stukans/Marauder:/repo" -w //repo rhysd/actionlint:latest -color .github/workflows/client-acceptance.yml
```
Expected: no errors (exit 0).

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/client-acceptance.yml
git commit -m "ci: add pinned client-acceptance matrix"
```

---

## Task 4: CI workflow — latest canary job + auto-issue

**Files:**
- Modify: `.github/workflows/client-acceptance.yml`

**Interfaces:**
- Consumes: the `pinned` job and `acceptance.sh`.
- Produces: a non-blocking `latest` job that goes red and files a deduped `client-canary` issue on failure.

- [ ] **Step 1: Append the latest canary job**

Add this `latest` job under `jobs:` in `.github/workflows/client-acceptance.yml` (sibling of `pinned`):

```yaml
  latest:
    name: latest-canary / ${{ matrix.client }}
    # Canary only — never gate releases, so skip on tag pushes.
    if: github.event_name != 'push'
    runs-on: ubuntu-latest
    timeout-minutes: 15
    permissions:
      contents: read
      issues: write
    strategy:
      fail-fast: false
      matrix:
        client: [qbittorrent, transmission, deluge]
    steps:
      - name: Checkout
        uses: actions/checkout@v6
      - name: Acceptance (latest image)
        id: acc
        continue-on-error: true
        run: bash deploy/acceptance/acceptance.sh ${{ matrix.client }} latest
      - name: File deduped canary issue on failure
        if: steps.acc.outcome == 'failure'
        env:
          GH_TOKEN: ${{ github.token }}
          CLIENT: ${{ matrix.client }}
          RUN_URL: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}
        run: |
          set -euo pipefail
          gh label create client-canary --color FBCA04 \
            --description "An upstream client latest release broke acceptance" \
            2>/dev/null || true
          TITLE="[client-canary] ${CLIENT} latest acceptance failing"
          NUM=$(gh issue list --label client-canary --state open \
            --json number,title \
            --jq ".[] | select(.title == \"${TITLE}\") | .number" | head -1)
          if [ -n "${NUM}" ]; then
            gh issue comment "${NUM}" \
              --body "Still failing on the latest image. Run: ${RUN_URL}"
          else
            gh issue create --title "${TITLE}" --label client-canary --body \
          "The acceptance test for \`${CLIENT}\` against its **latest** image failed while the pinned baseline is expected green. A new upstream release likely changed the client's API contract. Investigate and, if confirmed, adapt the plugin and bump the pinned baseline. Run: ${RUN_URL}"
          fi
      - name: Surface canary failure as a red (non-blocking) check
        if: steps.acc.outcome == 'failure'
        run: |
          echo "::warning::${{ matrix.client }} latest canary failed — issue filed"
          exit 1
```

- [ ] **Step 2: Lint the workflow**

Run:
```bash
docker run --rm -v "E:/Projects/Stukans/Marauder:/repo" -w //repo rhysd/actionlint:latest -color .github/workflows/client-acceptance.yml
```
Expected: no errors (exit 0).

- [ ] **Step 3: Verify the canary never runs on tags (release-gating safety)**

Confirm the guard text is present:
```bash
grep -n "github.event_name != 'push'" .github/workflows/client-acceptance.yml
```
Expected: one match on the `latest` job's `if:`.

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/client-acceptance.yml
git commit -m "ci: add latest-version canary with deduped issue alert"
```

---

## Task 5: Documentation

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: all prior tasks.

- [ ] **Step 1: Add a CHANGELOG entry**

In `CHANGELOG.md`, under the existing `## [Unreleased]` → `### Added` list, append:

```markdown
- Client acceptance CI (`.github/workflows/client-acceptance.yml` +
  `deploy/acceptance/acceptance.sh`): nightly matrix that creates every
  supported client through the Marauder API against a real container, on a
  pinned baseline (blocking, also runs on tags to gate releases) and on each
  client's latest image (non-blocking canary that auto-files a deduped issue
  when an upstream release breaks a client — the early warning that issue #38
  lacked).
```

- [ ] **Step 2: Add a CLAUDE.md note**

In `CLAUDE.md`, in the paragraph block that already describes
`docker-compose.test-clients.yml` (under "Common dev commands" / deploy
notes), append:

```markdown
`.github/workflows/client-acceptance.yml` drives `deploy/acceptance/acceptance.sh
<client> <pinned|latest>` as a nightly matrix (also on tag push for the pinned
baseline). The runner brings up the base stack plus one client under the
isolated `marauder-acceptance` Compose project (so it never touches a running
`deploy` dev stack) and asserts `POST /api/v1/clients` succeeds — i.e. the
plugin `Test()` passed. The `latest` channel overrides the client image tag via
`MARAUDER_TEST_*_TAG=latest`; on failure the workflow files a deduped
`client-canary` GitHub issue.
```

- [ ] **Step 3: Verify the changelog mentions no AI tooling**

Run:
```bash
grep -iE "claude|anthropic|opus|copilot" CHANGELOG.md CLAUDE.md && echo "REVIEW" || echo "clean of AI refs in new prose"
```
Expected: any matches are only the pre-existing `CLAUDE.md` *filename* string, not new prose you added. (Confirm your additions introduce none.)

- [ ] **Step 4: Commit**

```bash
git add CHANGELOG.md CLAUDE.md
git commit -m "docs: document client-acceptance canary workflow"
```

---

## Self-Review

**Spec coverage:**
- §3 connectivity acceptance → Task 2 (create-client → assert 2xx). ✓
- §4 matrix (4 pinned + 3 latest, µTorrent pinned-only) → Task 3 (`pinned` ×4) + Task 4 (`latest` ×3). ✓
- §5.1 parameterised image tags → Task 1. ✓
- §5.2 runner with per-client creds + local runnability → Task 2. ✓
- §5.3 dedicated workflow, triggers (nightly/tag/dispatch), pinned blocking, latest non-blocking-not-on-tag → Tasks 3–4. ✓
- §6 credential table → Task 2 `case` blocks. ✓
- §7 deduped auto-issue (`gh`, label, title marker) → Task 4. ✓
- §8 teardown always + diagnostics on failure → Task 2 `trap cleanup` + failure log dump. ✓
- §10 validation (pinned expected green) → Task 2 Steps 3–5. ✓

**Placeholder scan:** No TBD/TODO; all script and YAML content is complete and literal. ✓

**Type/name consistency:** Env var names (`MARAUDER_TEST_QBIT_TAG`, etc.) match between Task 1 defaults and Task 2 `TAG_VAR` mapping; service names (`qbittorrent-521`, `transmission-412`, `deluge-220`, `utorrent`) match the matrix file; plugin `client_name` values (`qbittorrent`/`transmission`/`deluge`/`utorrent`) match `registry` plugin names; project name `marauder-acceptance` used consistently. ✓

**Isolation note:** The runner uses an isolated Compose project and a throwaway env file specifically so a developer's `deploy` dev stack and its `deploy/.env` are never modified or torn down (verified in Task 2 Step 6).
