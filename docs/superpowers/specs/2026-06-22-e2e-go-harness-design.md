# Design: Go full-stack E2E harness

Date: 2026-06-22
Status: Approved (design phase)
Topic: Replace the inline-bash full-stack e2e in `.github/workflows/e2e.yml`
with a maintainable, table-driven Go harness.

## Problem

The full-stack end-to-end test (real docker-compose stack + real qBittorrent +
real scheduler/DB) is currently an inline bash script inside `e2e.yml`
(steps `[1]`–`[6]`: login → create client → create topic → wait → assert
delivery → assert qBit category). It works, but it does not scale:

1. **Not runnable locally** — to test the test you push to CI and wait.
2. **YAML quoting is fragile** — nested quotes / `docker run … sh -c '…'` / `-e`
   passing are easy to break silently.
3. **Brittle assertions** — `grep -q '"category":"sonarr-e2e"'` substring-matches
   JSON; can false-positive, cannot assert absence or structure, gives a poor
   failure diff.
4. **Shared mutable state** — one client, one topic, one infohash, linear
   script. New scenarios either couple to that state or grow the script.
5. **No tooling** — no `shellcheck`, no type checking, no test runner.
6. **Sequential by construction** — every assertion adds wall-clock to one
   monolithic job.

Adding more e2e coverage over time makes all six worse. This design moves the
*test logic* into Go (where it is typed, table-driven, and locally runnable)
while leaving *orchestration* (stack up/down) in the workflow.

## Non-goals

- **Not** replacing `e2etest.RunFullPipeline` — that is the in-process,
  fake-based plugin runner (`backend/internal/plugins/e2etest`) that runs in
  normal `go test ./...`. It is a different layer and stays as-is.
- **Not** touching `client-acceptance.yml` — the shallow create-client/`Test()`
  matrix across client versions has a different purpose and stays as-is.
- **Not** adding a "deliver now" test-only trigger endpoint. The harness keeps
  using the real scheduler tick. (Noted as a possible future optimization.)
- **Not** adding new e2e scenarios beyond the one being ported. This is a 1:1
  migration; the table is *built to* grow but ships with one row.
- **Not** introducing any new test dependency (no hurl/venom/testcontainers).
  Stdlib `net/http` + `testing` only, matching the repo's "manual fakes, no
  frameworks" stance.

## Architecture & execution model

A new black-box **system-test** package that drives the *already-running* full
stack over HTTP.

- **Build tag** `//go:build e2e` on every file. `go test ./...` (normal CI)
  never compiles or runs it. It runs only via `go test -tags=e2e ./e2e/...`.
- **Execution in CI**: `e2e.yml` brings up the stack (unchanged), then runs the
  harness in a `golang:1.25` container joined to the `deploy_default` network.
  The test reaches services by Docker DNS — `gateway:6688` (Marauder API) and
  `qbittorrent:6611` (qBittorrent API). Using in-network service names avoids
  qBittorrent 5.x's host-port `401` (it rejects requests whose Host-header port
  differs from its WebUI port, which the published `34611 → 6611` mapping
  triggers) and needs no production code change.
- **Config via env** (no flags):
  - `MARAUDER_BASE_URL` (e.g. `http://gateway:6688`)
  - `MARAUDER_ADMIN_USER` / `MARAUDER_ADMIN_PASS`
  - `QBIT_URL` (e.g. `http://qbittorrent:6611`)
  - `QBIT_PASSWORD`
  - If `MARAUDER_BASE_URL` is unset, the test `t.Skip`s, so a stray local
    `go test -tags=e2e` is inert rather than a failure.

### Why query qBittorrent directly for the category

Delivery is verified through Marauder's own topic-status API (which exercises
the real backend → qBittorrent → `topic_deliveries` path). The qBittorrent
*category*, however, is not exposed by Marauder's status response today, so the
category assertion reads it back from qBittorrent directly. Running inside the
compose network makes that read trivial and avoids the host-port `401`.

## Directory layout & components

```
backend/e2e/                         (new; package e2e; all files //go:build e2e)
├── doc.go            // package doc + build-tag rationale
├── env_test.go       // readEnv(t) → config struct; TestMain handles the skip check
├── marauder.go       // Marauder API client: Login, CreateQbitClient, CreateTopic, TopicStatus
├── qbit.go           // qBittorrent API client: Login, TorrentInfo(infohash) → {Category, SavePath, State}
├── wait.go           // pollUntil(ctx, interval, timeout, fn) — condition-based wait helper
└── delivery_test.go  // the table-driven scenario(s)
```

- Helpers return typed structs and real `error`s; assertions live in the test
  using stdlib `t.Fatalf` / `t.Errorf`.
- `marauder.go` and `qbit.go` are deliberately tiny and purpose-built (one
  method per call the test needs), not a general SDK.
- `wait.go` replaces the current fixed `sleep 75` with condition-based polling
  (poll the status endpoint until the infohash appears, bounded by a timeout —
  same ~135s ceiling as today).

## Test structure

```go
//go:build e2e

func TestDelivery(t *testing.T) {
    env := readEnv(t)                  // t.Skip if MARAUDER_BASE_URL unset
    m := marauder.New(env)
    m.Login(t)
    // Create the client with a known base download_dir so the resulting save
    // path is deterministic and assertable (the current bash sets none and
    // therefore cannot assert the path). "/downloads" exists in the
    // linuxserver/qbittorrent image.
    clientID := m.CreateQbitClient(t, "/downloads")  // url=qbittorrent:6611

    cases := []struct {
        name, category, wantSavePath, wantQbitCategory string
    }{
        {"category sets qbit label", "sonarr-e2e",
            "/downloads/sonarr-e2e", "sonarr-e2e"},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            infohash := uniqueInfohash(t)  // obviously-synthetic, per-case
            topicID := m.CreateTopic(t, magnet(infohash), clientID, tc.category)

            // delivery: poll Marauder status until the infohash appears
            pollUntil(ctx, 10*time.Second, 135*time.Second, func() bool {
                return m.TopicStatus(t, topicID).Contains(infohash)
            })

            // category: read back from qBittorrent directly
            info := qb.TorrentInfo(t, infohash)
            if info.Category != tc.wantQbitCategory { t.Errorf(...) }
            if info.SavePath != tc.wantSavePath { t.Errorf(...) }
        })
    }
}
```

- **Delivery** verified via Marauder's status API; **category** verified via
  qBittorrent directly — the same two assertions as today, now typed/structured.
  The **save-path** assertion is a small addition enabled by setting a known
  `download_dir` on the client (the bash asserted neither path nor save_path);
  it strengthens the test at zero extra cost and is deterministic.
- Adding a future scenario = one struct row. First cut ships the single row.
- **No-synthetic-data compliance:** the infohash is an obviously-synthetic
  constant (sequential / `dead…` hex), the category is `sonarr-e2e` — both
  unmistakable test markers; nothing is rendered as real user-facing data.

### Per-case infohash note

The current bash uses one fixed infohash. The table introduces `uniqueInfohash`
so multiple rows do not collide in qBittorrent. For the single shipped row this
is equivalent to a constant; it exists so added rows are isolated. It remains an
obviously-synthetic value (e.g. a fixed prefix plus the case index).

## CI wiring (`e2e.yml`)

The stack bring-up, health-wait, qBit-password grab (with `::add-mask::`), the
"print stack logs on failure", and the "tear down" steps all stay. Only the
inline walkthrough bash (steps `[1]`–`[6]`) is replaced by:

```yaml
- name: Run Go e2e harness
  env:
    QBIT_PASSWORD: ${{ steps.qbit.outputs.password }}
  run: |
    docker run --rm --network deploy_default \
      -v "$PWD/backend:/backend" -w /backend \
      -e MARAUDER_BASE_URL=http://gateway:6688 \
      -e MARAUDER_ADMIN_USER=admin -e MARAUDER_ADMIN_PASS=pleasechangeme \
      -e QBIT_URL=http://qbittorrent:6611 -e QBIT_PASSWORD \
      golang:1.25 go test -tags=e2e -count=1 ./e2e/...
```

Notes:
- `-e QBIT_PASSWORD` forwards the masked step output into the container env;
  it is never string-interpolated into a shell command.
- `-count=1` disables Go's test cache so the e2e always actually runs.
- The Docker network name `deploy_default` matches the `e2e.yml` compose
  project (the workflow runs compose from `deploy/` with no `-p`, so the
  default project name is `deploy`).

## Documentation

`docs/test-e2e-magnet.md` gets a short pointer that the *automated* full-stack
path now lives in `backend/e2e/` (run with `go test -tags=e2e`), while the
manual curl walkthrough remains for humans.

## Testing strategy for this change

- The harness itself is the test; its "passing" is observable in the nightly
  `e2e.yml` run (and on `workflow_dispatch` / tag push).
- Local dry-run: bring up the dev stack, then run the same `docker run … go test
  -tags=e2e` against `deploy_default` to confirm green before merge.
- `go vet -tags=e2e ./e2e/...` and `gofmt` must pass; the package must compile
  under the tag and be invisible without it (verified via `go build ./...`).

## Risks / open considerations

- **Runtime unchanged** — still gated by a real scheduler tick (~up to 135s).
  This design improves maintainability, not speed. A "deliver now" trigger is
  the future lever if runtime becomes painful.
- **Module download in CI** — running `go test` in a fresh `golang:1.25`
  container downloads modules each run. Acceptable for a nightly job; can be
  cached later if needed.
- **Network-name coupling** — `deploy_default` is derived from the compose
  project name; if `e2e.yml` ever adds `-p`, the `--network` value must change
  in lockstep. Called out in the workflow comment.
