# Design: Client Acceptance Tests + Upstream Version Canary

| Field | Value |
|---|---|
| Date | 2026-06-18 |
| Status | Approved — ready for implementation planning |
| Related | Issue #38 (qBittorrent 5.2.x `204` login break); `deploy/docker-compose.test-clients.yml`; `.github/workflows/e2e.yml` |

## 1. Problem

Marauder pushes torrents into four download clients (qBittorrent,
Transmission, Deluge, µTorrent) through version-sensitive HTTP/RPC contracts.
Issue #38 showed how fragile this is: qBittorrent 5.2.0 changed its login
success response from `200 "Ok."` to `204 No Content`, silently breaking
client setup for everyone on the new version. A **user** reported it before we
did.

Two gaps let that happen:

1. **No coverage breadth.** The nightly `e2e.yml` exercises a real client
   end-to-end, but **only qBittorrent**. Transmission, Deluge, and µTorrent
   have unit tests against in-process fakes only — nothing proves the real
   protocol still works.
2. **No baseline/canary separation.** `e2e.yml` runs the dev overlay, which
   pins `qbittorrent:latest`. So it *was* effectively a "test against latest"
   probe — but with nothing pinned as a known-good baseline. When `latest`
   breaks, the single nightly signal goes red and cannot distinguish *"we
   regressed"* from *"upstream changed under us."*

## 2. Goals / Non-goals

**Goals**

- Automated **acceptance test for every supported client** that proves the
  plugin can authenticate/connect against a **real** client container.
- Run that against a **pinned, verified version** (the known-good baseline)
  **and** against the client's **latest** version (an upstream canary).
- When `latest` breaks, surface it as an **early, actionable notification**
  (red CI check + an auto-filed GitHub issue) without blocking releases.
- Keep PR CI fast — no per-PR container spin-up.

**Non-goals (this iteration)**

- Deeper pipeline assertions (push a magnet → scheduler tick → confirm the
  torrent is present in the client). The existing `e2e.yml` already does this
  for qBittorrent; extending it to all clients is future work (§9).
- Testing every historical version — only `pinned` and `latest` per client.
- µTorrent `latest` canary (its `ekho/utorrent` image is unmaintained;
  "latest" ≈ pinned, so there is no meaningful upstream drift to catch).

## 3. Acceptance level (decided)

Each test performs the **connectivity/auth** check, no torrent push:

> Bring up `db + backend + <one client>` → log into the Marauder API →
> `POST /api/v1/clients` → assert `2xx`.

A successful create means the backend ran the plugin's `Test()` —
login/handshake — against the live client. This is the exact path that #38
broke, it is uniform across all four clients, and it runs in seconds (no
75-second scheduler tick).

## 4. Architecture — a 2-axis matrix

- **Axis A — client:** `qbittorrent`, `transmission`, `deluge`, `utorrent`
- **Axis B — channel:** `pinned` | `latest`

µTorrent is `pinned`-only. Result: **4 pinned + 3 latest = 7 isolated cells**.
Each cell brings up its own single client, so a failure attributes cleanly to
one `(client, channel)` pair.

## 5. Components

Three units, each independently understandable and testable.

### 5.1 Client definitions — `deploy/docker-compose.test-clients.yml` (extend)

Already the single source of truth for each client's image, internal port,
healthcheck, and volumes. Extension: make the **image tag of each primary
service env-overridable with the pinned value as default**, so the canary can
swap in `latest` at runtime without committing a `latest` tag:

| Service (primary) | Image tag expression |
|---|---|
| `qbittorrent-521` | `lscr.io/linuxserver/qbittorrent:${MARAUDER_TEST_QBIT_TAG:-5.2.1}` |
| `transmission-412` | `lscr.io/linuxserver/transmission:${MARAUDER_TEST_TRANSMISSION_TAG:-4.1.2}` |
| `deluge-220` | `lscr.io/linuxserver/deluge:${MARAUDER_TEST_DELUGE_TAG:-2.2.0}` |
| `utorrent` | `ekho/utorrent:${MARAUDER_TEST_UTORRENT_TAG:-v2.1.0}` |

`qbittorrent-514` (the legacy 5.1.4 manual-regression service) keeps its hard
pin. Defaults remain pinned, so the file stays rule-compliant (no committed
`latest`); only the canary job exports `MARAUDER_TEST_*_TAG=latest`.

### 5.2 Acceptance runner — `deploy/acceptance/acceptance.sh <client> <channel>`

A self-contained, **locally runnable** script — the "function" under test:

- **Inputs:** `client ∈ {qbittorrent,transmission,deluge,utorrent}`,
  `channel ∈ {pinned,latest}`.
- **Behavior:**
  1. For `channel=latest`, export the matching `MARAUDER_TEST_*_TAG=latest`.
  2. `docker compose -f docker-compose.yml -f docker-compose.test-clients.yml
     up -d --wait <client-service>` plus the base stack so the Marauder API is
     reachable at the gateway (`http://localhost:34080`, as `e2e.yml` does).
     Relies on the healthchecks already on each client so `--wait` gates on
     real readiness; the `.env` (master key, metrics token) is generated the
     same way as in `e2e.yml`.
  3. Resolve that client's credentials (§6).
  4. Log into the Marauder API, `POST /clients`, assert `2xx`; on non-2xx,
     print the response body + `docker logs` for the client and backend, exit
     non-zero.
  5. Always `down -v` (teardown + volume wipe).
- **Output:** process exit code (0 = acceptance passed).
- **Local use:** `./deploy/acceptance/acceptance.sh deluge pinned`.

Encapsulating per-client credential/URL knowledge here (not in the workflow)
keeps the CI YAML thin and lets a developer reproduce any cell locally.

### 5.3 CI workflow — `.github/workflows/client-acceptance.yml` (new)

A dedicated workflow, kept separate from `e2e.yml` (that file owns the *deep*
qBittorrent pipeline; this owns the *broad shallow* matrix).

- **Triggers:** `schedule` (nightly cron), `push: tags: ['v*']`,
  `workflow_dispatch`. No per-PR trigger — matches the existing e2e philosophy.
- **Job `pinned`** — `strategy.matrix.client: [qbittorrent, transmission,
  deluge, utorrent]`, each calling `acceptance.sh <client> pinned`.
  **Blocking.** A red here means *our* regression. Because it runs on
  `tags: v*`, it gates releases.
- **Job `latest`** — `strategy.matrix.client: [qbittorrent, transmission,
  deluge]`, each calling `acceptance.sh <client> latest`. Runs nightly +
  dispatch **only** (not on tag — releases must not hinge on upstream's
  latest). The acceptance step uses `continue-on-error: true` so the cell
  shows **red but does not fail the run or block anything**, followed by an
  `if: failure()` step that files the canary alert (§7).

## 6. Per-client credential resolution

| Client | Marauder `client_name` | Config sent to `POST /clients` | Credential source |
|---|---|---|---|
| qBittorrent | `qbittorrent` | `{url: http://qbittorrent-521:6611, username: admin, password: <temp>}` | temp password scraped from `docker logs <container>` ("temporary password") |
| Transmission | `transmission` | `{url: http://transmission-412:9091/transmission/rpc, username: "", password: ""}` | none — image runs `rpc-authentication-required: false` |
| Deluge | `deluge` | `{url: http://deluge-220:8112, password: deluge}` | linuxserver default web password `deluge` |
| µTorrent | `utorrent` | `{url: http://utorrent:8080, username: admin, password: ""}` | image default `admin` / empty (verified against the token endpoint) |

The backend reaches each client by **Docker service DNS** because the runner
stacks the base compose and the matrix file on the same network.

## 7. Canary alert (latest-channel failure)

On a `latest` cell failure, an `if: failure()` step opens-or-updates a deduped
GitHub issue using the `gh` CLI (`GITHUB_TOKEN`, `issues: write`):

- Title marker: `[client-canary] <client> latest acceptance failing`.
- Label: `client-canary`.
- Dedup: `gh issue list --label client-canary --state open` filtered by the
  title marker → if found, add a comment with the run link; else
  `gh issue create`.

This reproduces how #38 reached us (a tracked issue) but days earlier and
self-reported, while the red-but-non-blocking check keeps it visible in the
Actions tab.

## 8. Data flow & error handling

```
matrix cell (client, channel)
  → acceptance.sh
    → [latest only] export MARAUDER_TEST_<X>_TAG=latest
    → docker compose up -d --wait <client-service> + base stack (API @ :34080)
    → resolve creds → POST /api/v1/clients → assert 2xx
    → down -v (always)
  → exit code
    → pinned fail  : job red → blocks tag/release
    → latest fail  : continue-on-error red annotation + deduped gh issue
```

- **Teardown** is `if: always()` (`down -v`) so no leaked containers/volumes.
- **Diagnostics on failure:** dump the client + backend `docker logs` before
  teardown.
- **Flake guard:** `--wait` plus the existing per-client healthchecks ensure
  the client is actually serving before the API call; the backend's own
  healthcheck gates readiness.

## 9. Out of scope / future

- Promote the canary from connectivity to full pipeline (magnet → scheduler
  tick → confirm in client) per client — reuses the `e2e.yml` pattern.
- Auto-bump the pinned baseline to a new version once its canary has been
  green for N nights (turn a passing canary into a pin PR).
- Live `Status` (`WithStatus`) assertions for qBittorrent/Transmission.

## 10. Validation

The `pinned` half of this matrix was already executed manually during the #38
work: `create-client → Test()` succeeded against real `qbittorrent` 5.2.1 +
5.1.4, `transmission` 4.1.2, `deluge` 2.2.0, and `utorrent` v2.1.0. The first
automated `pinned` run is therefore expected green; this design automates and
schedules that, and layers the `latest` canary on top.
