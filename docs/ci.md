# CI / CD pipeline

Marauder's GitHub Actions setup is split across five workflows + a
Dependabot config. This document explains what each one does, when it
runs, and what you should do if it fails.

## At a glance

| Workflow | Trigger | What it does | Time budget |
|---|---|---|---|
| [`ci.yml`](../.github/workflows/ci.yml) | every PR + push to main | Backend test/vet/lint/govulncheck + frontend typecheck/build + cfsolver build | < 3 min |
| [`docker.yml`](../.github/workflows/docker.yml) | push to main + tag | Build all 3 Docker images, Trivy scan (HIGH/CRITICAL) | ~3 min |
| [`e2e.yml`](../.github/workflows/e2e.yml) | nightly + workflow_dispatch + tag | Full compose-stack walkthrough: magnet → qBittorrent end-to-end | ~5 min |
| [`release.yml`](../.github/workflows/release.yml) | tag push (`v*`) | Multi-arch build, cosign sign, SBOM, GHCR push, GitHub Release | ~10 min |
| [`codeql.yml`](../.github/workflows/codeql.yml) | every PR + push to main + weekly | GitHub CodeQL SAST (Go + TypeScript) | ~5 min |

Plus [`.github/dependabot.yml`](../.github/dependabot.yml) — not a
workflow but a config file. Updates Go modules, npm packages,
GitHub Actions versions, and Docker base images weekly with grouped
minor/patch PRs.

---

## ci.yml

The fast-feedback pipeline. Every PR runs through this; merging is
blocked unless every job is green.

### Jobs

- **`backend`** — `go vet`, `go test -race -coverprofile`,
  `govulncheck`. Matrix-driven over Go versions (currently just 1.23
  but trivial to extend).
- **`backend-lint`** — `golangci-lint` with the rule set in
  [`backend/.golangci.yml`](../backend/.golangci.yml). 11 linters
  total: errcheck, govet, ineffassign, staticcheck, unused,
  bodyclose, rowserrcheck, sqlclosecheck, errorlint, exhaustive,
  gosec.
- **`cfsolver`** — separate job because the cfsolver service has its
  own go.mod. Runs `go build` and `go vet`.
- **`frontend`** — `npm ci`, `npm run typecheck`, `npm run build`.
  Prints the bundle size at the end so reviewers can see if a change
  blew the budget.

### When it fails

| Symptom | Fix |
|---|---|
| `go vet` red | `go vet ./...` locally — usually a printf-format mismatch or a struct tag typo |
| Race detector red | A real data race. Run `go test -race -run TheTest -count=10` to repro |
| `golangci-lint` red | `golangci-lint run` locally; see the rule that fired |
| `govulncheck` red | A direct or transitive dependency has a CVE. Bump the dep, regenerate `go.sum` |
| `npm run build` red | Run `npm run build` locally; usually a TS error in a recently-touched file |
| `npm run typecheck` red | The build sometimes hides type errors that strict-mode tsc catches; run `npm run typecheck` |

---

## docker.yml

Build verification + Trivy vulnerability scanning. Does NOT push
images — that's `release.yml`.

### Jobs

- **`build-scan`** — matrix over the three images (`backend`,
  `frontend`, `cfsolver`). Each one builds with buildx, loads the
  resulting image into the local Docker daemon, then runs Trivy with
  `severity: HIGH,CRITICAL` and `exit-code: 1`. SARIF output is
  uploaded to GitHub's code-scanning view.

### When it fails

| Symptom | Fix |
|---|---|
| Image build red | Reproduce with `docker build -f <Dockerfile> .` locally. Common cause: a `COPY` source path drift after a refactor |
| Trivy red on HIGH/CRITICAL | Update the affected base image (alpine, debian-slim) or pin past the vulnerable dep version. Open a fresh PR; the scan will re-run |
| Trivy red on something `unfixed` | The `ignore-unfixed: true` flag should already filter these. If you see one, file a Trivy bug |

---

## e2e.yml

The heavyweight end-to-end test. Brings up the full Marauder docker
compose stack PLUS a real qBittorrent container, then runs the
walkthrough from [`docs/test-e2e-magnet.md`](test-e2e-magnet.md).

### Why it's not on every PR

It's slow (~5 minutes including stack startup) and noisy. Putting it
in the PR path would burn shared minutes and frustrate contributors.
Instead it runs:

- **Nightly** at 04:00 UTC against `main`
- **On every tag push** (so a release is never tagged without a green E2E)
- **On `workflow_dispatch`** when a maintainer wants to verify a
  specific commit

### What it actually verifies

1. `docker compose up -d` brings the full stack to a healthy state
2. The backend health endpoint responds 200
3. The qBittorrent sidecar publishes a temporary password
4. Login → create client → add magnet topic → wait for scheduler tick →
   the torrent appears in qBittorrent's `/api/v2/torrents/info`

If this passes, the v0.1 Definition of Done is still being met.

---

## release.yml

Triggered by a `v*` tag push. Builds, signs, and ships everything.

### Jobs

- **`build`** — matrix over the three images. For each:
  1. Multi-arch build (`linux/amd64`, `linux/arm64`) via QEMU + buildx
  2. Push to `ghcr.io/artyomsv/marauder-<image>:<version>` plus
     `:latest`, `:major.minor`, etc. (via `docker/metadata-action`)
  3. Sign with cosign (keyless, via OIDC). No private keys to manage
  4. Generate a CycloneDX SBOM with `anchore/sbom-action`, upload as
     a workflow artifact

- **`release`** — runs after `build`. Extracts the matching
  `[<version>]` section from `CHANGELOG.md` (with a tiny awk script),
  attaches the per-image SBOMs, and creates a GitHub Release.
  `-rc`, `-alpha`, `-beta` tags are auto-marked as prereleases.

### To cut a release

```bash
# 1. Bump CHANGELOG.md: move [Unreleased] entries into a new
#    [1.1.0] section dated today.
# 2. Commit, push to main, wait for CI green.
git tag -a v1.1.0 -m "Marauder v1.1.0"
git push origin v1.1.0
# 3. Watch release.yml run. It produces the GHCR images and a
#    GitHub Release within ~10 minutes.
```

### When it fails

| Symptom | Fix |
|---|---|
| Multi-arch build slow / red | Most often QEMU-related. Re-run the workflow; if it's flaky, drop arm64 from the matrix temporarily |
| cosign sign red | Check the workflow's `id-token: write` permission is set (it is in our config) |
| CHANGELOG section not found | Make sure you added a `## [1.1.0] — YYYY-MM-DD` heading. The awk extractor matches on `^## [1.1.0]` |

---

## codeql.yml

GitHub's CodeQL SAST. Free, broad, and fast.

### Coverage

- **Go** with `build-mode: autobuild` (CodeQL spins up Go and runs
  `go build ./...` automatically)
- **JavaScript/TypeScript** with `build-mode: none` (no build needed)
- Both use the `security-extended` query pack for broader rules
  (XSS, SQL injection, path traversal, hard-coded credentials,
  unsafe deserialization, weak crypto, etc.)

### When it finds something

CodeQL findings appear under **Security → Code scanning** in the
GitHub UI. They are NOT blocking by default — false positives are
common in SAST. Triage them:

- **Real bug:** open a PR to fix
- **False positive:** suppress with a `// codeql[js/...] not a real issue, see https://...` comment, OR mark as "won't fix" in the UI with a justification

---

## Dependabot

Configured in [`.github/dependabot.yml`](../.github/dependabot.yml).

### What it watches

| Ecosystem | Where | Cadence |
|---|---|---|
| Go modules | `/backend`, `/cfsolver` | Weekly Mon 06:00 UTC |
| npm | `/frontend` | Weekly Mon 06:00 UTC |
| GitHub Actions | `/` | Weekly Mon 06:00 UTC |
| Docker | `/backend`, `/frontend`, `/cfsolver` | Weekly Mon |

### Grouping

Minor and patch updates are grouped per ecosystem so a typical Monday
produces 3-5 PRs instead of 30+. Major updates always come as
individual PRs because they need careful reading.

### Pinned majors

The frontend `package.json` has React 19, Vite 8, and Tailwind 4
locked to their major version per the v1.0 tech-stack lock. Dependabot
will still propose patch bumps within those majors but won't try to
upgrade you to React 20.

---

## Local validation

Want to run the same checks locally without pushing?

```bash
# Backend
docker run --rm -v "$PWD/backend:/src" -w /src golang:1.23-alpine \
  sh -c "go vet ./... && go test -race ./..."

# golangci-lint
docker run --rm -v "$PWD/backend:/src" -w /src \
  golangci/golangci-lint:latest golangci-lint run --timeout=5m

# Frontend
docker run --rm -v "$PWD/frontend:/app" -w /app node:22-alpine \
  sh -c "npm ci && npm run typecheck && npm run build"

# Validate the workflow YAML before pushing
docker run --rm -v "$PWD:/repo" -w /repo rhysd/actionlint:latest -color
```

---

## Adding a new workflow

If you need a new workflow:

1. Drop the YAML in `.github/workflows/`
2. Validate with `actionlint` (see above)
3. Add a row to the **At a glance** table at the top of this file
4. If it touches secrets, document why in the workflow file's header comment
5. If it should run on PRs from forks (almost never the right answer),
   document why explicitly
