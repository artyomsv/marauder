# Nightly multi-arch build + verify — design

**Date:** 2026-06-22
**Status:** approved
**Related:** issue #74 (arm64 image shipped an amd64 binary), PR #79 (the fix +
release-time arch check)

## Problem

`backend/Dockerfile` and `cfsolver/Dockerfile` once hardcoded `GOARCH=amd64`, so
the published `linux/arm64` images contained an amd64 binary and died with
`exec format error` on real `aarch64` hosts (issue #74). PR #79 fixes the
Dockerfiles and adds a release-time arch check. That check guards the
*published* manifest, but only at tag time. Nothing continuously verifies that
`main` still builds **and runs** on arm64 between releases.

## Goal

A nightly workflow that builds all three images from `main` HEAD **natively on
each architecture** and confirms they actually run there, filing a deduped
canary issue on failure. Catches arch/build regressions on `main` before they
are ever tagged.

## Why native runners (not QEMU)

On an **amd64** runner an amd64 binary inside an arm64 image runs *natively* —
the kernel never raises `exec format error` (QEMU only emulates foreign-arch
ELFs). So execution-based testing on amd64 cannot catch a wrong-arch arm64
binary. On a **native `ubuntu-24.04-arm` runner** the same broken binary fails
for real. This repo is public, so native arm64 runners are free. Native
execution keeps the verify trivial ("run it, fail on `exec format error`") while
being faithful.

This is complementary to PR #79: #79 inspects the *published* manifest's binary
arch at release time; this runs *HEAD* on real hardware every night.

## Design

New file: `.github/workflows/nightly-build.yml`.

**Triggers**
- `schedule: cron '0 4 * * *'` — 04:00 UTC, matching the e2e nightly.
- `workflow_dispatch`.
- `concurrency` group with `cancel-in-progress: true`.

**Permissions:** `contents: read`, `issues: write` (canary).

**Job `build-verify`** — `runs-on: ${{ matrix.runner }}`, `fail-fast: false`,
`timeout-minutes: 20`.

Matrix = image × runner (6 legs):

| image | kind | binpath | runners |
|---|---|---|---|
| backend | go | `/usr/local/bin/marauder` | `ubuntu-24.04`, `ubuntu-24.04-arm` |
| cfsolver | go-server | `/usr/local/bin/cfsolver` | `ubuntu-24.04`, `ubuntu-24.04-arm` |
| frontend | nginx | — | `ubuntu-24.04`, `ubuntu-24.04-arm` |

**Steps:** checkout → setup-buildx → build natively (`push: false`,
`load: true`, **no** `--platform` so the runner's own arch is the target, no
QEMU) → verify (branches on `kind`).

**Verify — native, so `exec format error` is genuine:**
- **backend (`go`):** `docker run --rm` the image; assert output does **not**
  contain `exec format error` **and** does contain its own config-load error
  (`fatal: load config:`), proving the Go runtime initialised on this arch.
  (It cannot reach `/health` without a DB; reaching config-load is the honest
  liveness signal and needs no services.)
- **cfsolver (`go-server`):** run detached with a published port, poll `/health`
  until healthy or a short timeout — it has no DB dependency, so it genuinely
  serves. Fail on timeout.
- **frontend (`nginx`):** run detached with a published port, `curl` the served
  root, assert a successful response.

**On failure:** `continue-on-error: true` on the verify step, then a deduped
canary issue **per failing leg** (label `build-canary`, title
`[build-canary] nightly build failing: <image> on <runner>`): create if absent,
else comment the run URL. Per-leg titles (rather than one shared title) avoid
`gh issue create` races across the 6 parallel matrix legs — the same per-key
dedup `client-acceptance.yml` uses per-client. End each failing leg with a red
**non-blocking** check (`::warning::` + `exit 1`).

## Out of scope (YAGNI)

- Full-stack `docker compose` bring-up with Postgres — heavier than the agreed
  liveness check.
- Pushing the nightly images anywhere — this is a build+run gate, not a publish.
- Additional architectures beyond amd64/arm64 (only those two are published).

## Placement

Committed to the existing `74-...` branch / PR #79 (related theme: multi-arch
image correctness). The PR description is extended to cover the workflow.
`CLAUDE.md`'s workflow list gains a one-line mention.
