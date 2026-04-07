# Contributing to Marauder

Thanks for considering a contribution. Marauder is built around a deliberately
small number of moving parts so that adding a tracker, a torrent client, or
a notification target is a focused exercise rather than a full system tour.

This guide explains how the project is organized, how to run it locally, and
what is expected of a pull request.

---

## Repository layout

```
marauder/
├── backend/                Go 1.23 backend
│   ├── cmd/server/         main.go
│   ├── internal/api/       chi router, middleware, handlers
│   ├── internal/auth/      JWT manager, OIDC provider
│   ├── internal/config/    env-var loader
│   ├── internal/crypto/    AES-256-GCM and Argon2id helpers
│   ├── internal/db/        pgx pool, goose migrations, repo/
│   ├── internal/domain/    plain Go types shared across layers
│   ├── internal/plugins/   tracker, client, notifier plugin packages
│   ├── internal/scheduler/ periodic check loop with worker pool
│   └── internal/...        problem (RFC 7807), version, logging
├── frontend/               React 19 + Vite 8 + Tailwind 4 + shadcn/ui
│   ├── src/components/ui/  shadcn primitives copied in
│   ├── src/components/layout/  AppShell
│   ├── src/lib/            api client, auth store, utils
│   └── src/pages/          Login, Dashboard, Topics, Placeholders
├── deploy/                 docker-compose stack + nginx gateway
└── docs/                   VISION, COMPETITORS, PRD, ROADMAP, guides
```

---

## Local development

You only need Docker. Marauder is designed so that **no Go, Node, or
Postgres toolchain has to be installed on the host**.

### Bring up the dev stack

```bash
cd deploy
cp .env.example .env
# generate secrets:
sed -i "s|MARAUDER_MASTER_KEY=.*|MARAUDER_MASTER_KEY=$(openssl rand -base64 32)|" .env
sed -i "s|MARAUDER_METRICS_TOKEN=.*|MARAUDER_METRICS_TOKEN=$(openssl rand -hex 32)|" .env

# Production-ish stack: db + backend + frontend + gateway
docker compose --env-file .env up -d

# Dev overlay: also publishes ports and runs real qBit + Transmission
docker compose --env-file .env -f docker-compose.yml -f docker-compose.dev.yml up -d
```

Open: <http://localhost:6688>

Default credentials are in `.env` (`MARAUDER_ADMIN_INITIAL_USERNAME` /
`_PASSWORD`). **Change them after first login.**

### Build the backend without installing Go

```bash
docker run --rm -v "$PWD/backend:/src" -w /src golang:1.23-alpine \
  sh -c "go build ./..."
```

### Run the backend tests

```bash
docker run --rm -v "$PWD/backend:/src" -w /src golang:1.23-alpine \
  sh -c "go test ./..."
```

### Build the frontend without installing Node

```bash
docker run --rm -v "$PWD/frontend:/app" -w /app node:22-alpine \
  sh -c "npm install && npm run build"
```

---

## Writing a tracker plugin

Tracker plugins live in `backend/internal/plugins/trackers/<name>/`.
A plugin is a single Go package with one file (or two if you split tests).

### The contract

Your plugin must implement [`registry.Tracker`](backend/internal/plugins/registry/registry.go):

```go
type Tracker interface {
    Name() string
    DisplayName() string
    CanParse(rawURL string) bool
    Parse(ctx context.Context, rawURL string) (*domain.Topic, error)
    Check(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential) (*domain.Check, error)
    Download(ctx context.Context, topic *domain.Topic, check *domain.Check, creds *domain.TrackerCredential) (*domain.Payload, error)
}
```

You may *also* implement these optional capability interfaces:

- **`registry.WithCredentials`** — your tracker requires a login form.
  Add `Login(ctx, creds) error` and `Verify(ctx, creds) (bool, error)`.
- **`registry.WithQuality`** — your tracker exposes quality variants
  (LostFilm-style). Add `Qualities() []string` and `DefaultQuality() string`.
- **`registry.WithCloudflare`** — your tracker may return a Cloudflare
  challenge. Add `UsesCloudflare() bool`. The scheduler will route HTTP
  failures through the Cloudflare solver sidecar.

### The minimum boilerplate

```go
// Package mytracker monitors http(s) topics on My Tracker.
package mytracker

import (
    "context"
    "errors"

    "github.com/artyomsv/marauder/backend/internal/domain"
    "github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

func init() {
    registry.RegisterTracker(&plugin{})
}

type plugin struct{}

func (p *plugin) Name() string        { return "mytracker" }
func (p *plugin) DisplayName() string { return "My Tracker" }
func (p *plugin) CanParse(u string) bool { /* ... */ return false }
func (p *plugin) Parse(ctx context.Context, u string) (*domain.Topic, error)  { return nil, errors.New("todo") }
func (p *plugin) Check(ctx context.Context, t *domain.Topic, c *domain.TrackerCredential) (*domain.Check, error)  { return nil, errors.New("todo") }
func (p *plugin) Download(ctx context.Context, t *domain.Topic, c *domain.Check, cr *domain.TrackerCredential) (*domain.Payload, error) { return nil, errors.New("todo") }
```

Then add a single blank import in `backend/cmd/server/main.go`:

```go
_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/mytracker"
```

That's it. The `init()` self-registers the plugin with the global registry,
and the API/scheduler discover it automatically.

### Tests

For trackers that depend on a real HTTP target, prefer **recorded fixtures**:
save the relevant HTML to `testdata/<topic>.html` and serve it from a
`net/http/httptest.Server` in the test. This makes the test reproducible
and lets the next contributor update the fixture when the site changes.

Example: see [`internal/plugins/registry/registry_test.go`](backend/internal/plugins/registry/registry_test.go)
for the testing pattern.

---

## Writing a torrent client plugin

Same shape, different interface. Implement `registry.Client`:

```go
type Client interface {
    Name() string
    DisplayName() string
    ConfigSchema() map[string]any
    Test(ctx context.Context, rawConfig []byte) error
    Add(ctx context.Context, rawConfig []byte, payload *domain.Payload, opts domain.AddOptions) error
}
```

The `ConfigSchema` is a JSON Schema document that the frontend uses to
auto-render the configuration form. Keep it simple: most clients just need
URL, username, password.

The `rawConfig` parameter is **plaintext JSON**: the scheduler decrypts the
stored ciphertext via the master key before calling you.

Example: [`backend/internal/plugins/clients/qbittorrent/qbittorrent.go`](backend/internal/plugins/clients/qbittorrent/qbittorrent.go).

---

## Writing a notifier plugin

Implement `registry.Notifier`. See
[`backend/internal/plugins/notifiers/telegram/telegram.go`](backend/internal/plugins/notifiers/telegram/telegram.go)
for the pattern.

---

## Pull request checklist

- [ ] `go build ./...` and `go vet ./...` are clean.
- [ ] `go test ./...` passes.
- [ ] If you added a new public field, type, or function, it has a doc
      comment that explains *why* it exists, not just *what* it is.
- [ ] If you changed user-visible behaviour, you updated `CHANGELOG.md`
      under the `[Unreleased]` section.
- [ ] If you completed an item from the roadmap, you ticked the box in
      `docs/ROADMAP.md`.
- [ ] If you touched a tracker or client plugin, you noted in the PR
      description **whether you have validated the change against a real
      live instance** or only against fixtures.
- [ ] Commit messages follow Conventional Commits
      (`feat(scope): ...`, `fix(scope): ...`, `docs: ...`).

---

## What we will and won't merge

We will merge:

- Bug fixes with tests.
- New tracker/client/notifier plugins.
- Performance improvements with before/after numbers.
- Documentation improvements.
- Translation contributions for the UI (`frontend/src/i18n/`).

We will not merge:

- Hard-coded lists of tracker URLs that point at copyrighted content.
- Code that tries to defeat tracker site protections in ways that go
  beyond what monitorrent already did (this is a personal-use automation
  tool, not a circumvention library).
- Removal of the MIT license header from any file.

---

## Code style

- **Go**: standard `gofmt`, `goimports`, and `golangci-lint` (when CI
  ships in v0.2). Repository layer returns domain types only — no `*sql.Rows`
  leakage.
- **TypeScript**: 2-space indent, `interface` over `type` for object
  shapes (per the global preferences), TanStack Query for server state,
  zustand for global UI state.
- **CSS**: Tailwind utilities first, custom CSS only in `index.css`.

---

## Asking questions

Open a GitHub Discussion or Issue rather than emailing maintainers
directly. The history is then searchable for the next person who runs
into the same thing.
