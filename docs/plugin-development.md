# Plugin Development Guide

This guide walks through writing a new plugin for Marauder. Marauder
has three plugin kinds and they all follow the same pattern: a Go
package in `backend/internal/plugins/<kind>/<name>/`, a single struct
implementing the plugin interface, and an `init()` function that
self-registers with the global registry.

There are no proprietary plugin loaders, no YAML schemas to fight,
no separate plugin manifests. **A plugin is one Go file plus its test.**

---

## Anatomy of a plugin

The shape is the same for trackers, clients, and notifiers:

```go
package mytracker

import (
    "context"
    "github.com/artyomsv/marauder/backend/internal/domain"
    "github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

func init() {
    registry.RegisterTracker(&plugin{})
}

type plugin struct{}

func (p *plugin) Name() string        { return "mytracker" }
func (p *plugin) DisplayName() string { return "My Tracker" }

// ... implement the rest of registry.Tracker
```

The `init()` function runs at process start when `cmd/server/main.go`
blank-imports the package. There is no other registration step.

---

## Writing a tracker plugin

### The interface

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

- **`CanParse`** must return `true` if-and-only-if your plugin can
  meaningfully `Parse` the URL. The scheduler picks the first plugin
  whose `CanParse` returns true (in stable alphabetical order), so be
  precise. Use a `regexp` against the canonical URL form.
- **`Parse`** is called once when the user adds the topic. Extract the
  topic ID, the canonical URL, and any per-topic options into
  `topic.Extra`. Don't make HTTP requests here unless you have to —
  it's also called from validation paths.
- **`Check`** is called by the scheduler on every tick. Return a
  `*domain.Check` with a stable `Hash` field. The scheduler treats a
  changed hash as "topic was updated" and triggers `Download`.
- **`Download`** is called only when `Check` reports an update. Return
  either a `MagnetURI` or a `TorrentFile` byte slice in the
  `*domain.Payload`. Don't worry about clients — the scheduler decrypts
  the user's client config and routes the payload itself.

### Optional capability interfaces

Implement any of these in addition to `Tracker`:

```go
// The tracker requires user credentials.
type WithCredentials interface {
    Tracker
    Login(ctx context.Context, creds *domain.TrackerCredential) error
    Verify(ctx context.Context, creds *domain.TrackerCredential) (bool, error)
}

// The tracker exposes per-topic quality variants (LostFilm, Anidub).
type WithQuality interface {
    Tracker
    Qualities() []string
    DefaultQuality() string
}

// The tracker may return Cloudflare challenge pages — opt into the
// cfsolver sidecar's bypass.
type WithCloudflare interface {
    Tracker
    UsesCloudflare() bool
}
```

The registry detects these via type assertion at runtime — no separate
registration needed.

### Sharing HTTP sessions

Forum-style trackers should use `internal/plugins/trackers/forumcommon`
which provides a `SessionStore` keyed by `(plugin_name, user_id)`.
Cookies persist for the lifetime of the process, so concurrent topic
checks for the same user reuse the same logged-in client.

### Testing a tracker plugin

Always use **recorded HTML fixtures** rather than live sites:

```go
const fixtureTopicHTML = `<html>
<head><title>Some Show :: My Tracker</title></head>
<body>
<a href="magnet:?xt=urn:btih:0123456789ABCDEF...">Magnet</a>
</body>
</html>`

func newTestPlugin(t *testing.T) (*plugin, *httptest.Server) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if strings.HasPrefix(r.URL.Path, "/topic/") {
            w.Write([]byte(fixtureTopicHTML))
        }
    }))
    t.Cleanup(srv.Close)

    host := strings.TrimPrefix(srv.URL, "http://")
    return &plugin{
        domain:    host,
        transport: &schemeRewrite{},
    }, srv
}
```

The trick: production code prepends `https://`, so tests inject a
custom `http.RoundTripper` that rewrites scheme to `http` before the
request leaves the test process.

See `internal/plugins/trackers/rutracker/rutracker_test.go` for the
canonical example.

---

## Writing a torrent client plugin

```go
type Client interface {
    Name() string
    DisplayName() string
    ConfigSchema() map[string]any
    Test(ctx context.Context, rawConfig []byte) error
    Add(ctx context.Context, rawConfig []byte, payload *domain.Payload, opts domain.AddOptions) error
}
```

- **`ConfigSchema`** returns a JSON Schema document that the frontend
  uses to render the configuration form. For v0.4 the frontend uses
  hard-coded field hints in `frontend/src/pages/Clients.tsx`, but the
  schema is the source of truth and v0.5 will switch to schema-driven
  rendering.
- **`Test`** is called when the user clicks "Test connection" or
  before the config is persisted. Return nil on success.
- **`Add`** receives the **decrypted** raw config bytes from the
  scheduler — you don't see ciphertext.

### Testing a client plugin

Stand up a tiny `net/http/httptest.Server` that mimics the real
client's API. See `qbittorrent_test.go` for the qBittorrent WebUI v2
fake or `transmission_test.go` for the transmission RPC 409-dance.

For real-world validation, the dev compose overlay
(`deploy/docker-compose.dev.yml`) starts real qBittorrent and
Transmission containers — see `docs/test-e2e-magnet.md` for the
walkthrough.

---

## Writing a notifier plugin

```go
type Notifier interface {
    Name() string
    DisplayName() string
    ConfigSchema() map[string]any
    Test(ctx context.Context, rawConfig []byte) error
    Send(ctx context.Context, rawConfig []byte, msg domain.Message) error
}
```

`Test` typically calls `Send` with a hard-coded "this is a test"
message. Mock the upstream with `httptest.Server` for the unit tests,
or substitute a function field (the `email` plugin's `sender` field is
the cleanest example).

---

## Wiring a plugin into the binary

Add a single blank import to `backend/cmd/server/main.go`:

```go
import (
    // ...
    _ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/mytracker"
)
```

That's the entire wiring. The plugin is now visible in
`GET /api/v1/system/info`, can be configured through the UI, and is
called by the scheduler whenever a topic with `tracker_name="mytracker"`
is due.

---

## Validation status conventions

Tracker plugins shipped without live-account validation should be
marked **alpha** in their package doc comment. The convention is:

```go
// Package mytracker implements a tracker plugin for example.com.
//
// **Validation status:** structurally complete with fixture-based unit
// tests. Live validation requires a real account, which was not
// available in the original implementation session — see CONTRIBUTING.md
// for the validation procedure.
```

A first-time contributor with a real account can:

1. Set the plugin's credentials via the UI.
2. Add a known-good topic URL.
3. Wait one scheduler tick.
4. Verify in the System status page that the topic was checked
   without errors and that the hash matches what they see in their
   browser.
5. File an issue with `validated: true` in the title and the plugin
   moves out of alpha in the next release.

---

## Performance budgets

A plugin's `Check` is called on every scheduler tick. To stay polite
to upstream servers and to keep Marauder's footprint small:

- **Aim for < 1 second per Check** in the steady state. The default
  HTTP timeout is 30s, but real CIS forum trackers respond in under
  500ms once the session is hot.
- **Don't make extra HTTP calls in `Parse`** unless you absolutely
  have to. Parse should be a regex.
- **Cache the result of `Check`** within `Download` if you can — the
  scheduler always calls them as a pair, and a re-fetch is wasted
  bandwidth.
- **Honour the context.** Every `Check` and `Download` receives a
  context with a deadline. Plumb it through to your `http.NewRequestWithContext`
  calls.

---

## Where to ask for help

- Open a GitHub Issue with the `plugin` label.
- Look at existing plugins under `backend/internal/plugins/`. They
  are all small, all follow the same pattern, and most are under 200
  lines.
- The shared `forumcommon` and `cfsolver` packages exist precisely to
  handle the cross-cutting concerns that would otherwise be repeated
  in every plugin.
