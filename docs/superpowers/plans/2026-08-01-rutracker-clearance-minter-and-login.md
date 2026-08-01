# RuTracker Authentication Restoration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore authenticated RuTracker operation — tracker search, and `.torrent` downloads carrying a real announce URL — by demoting FlareSolverr from "the fetch path" to "a Cloudflare-clearance minter", and re-enabling a real login with an adaptive-captcha escalation.

**Architecture:** FlareSolverr solves the Cloudflare managed challenge once and hands back `cf_clearance` plus the browser User-Agent that earned it. A process-wide clearance provider caches that per host; the RuTracker plugin injects it into its ordinary Go `http.Client` on every request and sends the browser UA. With the transport unblocked, the plugin performs a normal credential login; when RuTracker adaptively demands a captcha, the plugin escalates through the existing `captchalogin` engine, which is extended to support per-challenge captcha URLs and dynamic form fields.

**Tech Stack:** Go 1.25, `net/http` + `net/http/cookiejar`, FlareSolverr 3.5.0 (`sessions.create` / `request.get`), Prometheus (`promauto`), zerolog.

## Global Constraints

These come from measurements taken against live RuTracker on **2026-08-01**. Every task's requirements implicitly include this section.

- **`cf_clearance` is bound to the User-Agent**, not the TLS fingerprint. Replaying it with `Marauder/0.3` → 403. Replaying it with a *different* browser UA → 403. Every request that carries the cookie MUST also send the exact `solution.userAgent` FlareSolverr reported.
- **`cf_clearance` is bound to the host.** A `rutracker.org` cookie returns 403 on `rutracker.net`. Cache and mint per host.
- **`cf_clearance` is required on every `/forum/*` request.** `bb_session` alone → 403. `/forum/index.php` is the sole un-challenged path.
- **Minimum working cookie set is `cf_clearance` + `bb_session`.** `bb_guid` and `bb_ssl` are not load-bearing.
- **`bb_session` is path-scoped to `/forum/`.** Harvesting it from a cookie jar requires querying the jar with a `/forum/` URL, not the origin root.
- **The login captcha is adaptive.** A plain credential POST normally succeeds; RuTracker imposes a captcha on a flagged client (e.g. after failed attempts) and clears the flag after a success. Captcha handling is an escalation path, not the default.
- **The captcha image is per-challenge and lives on a different host** (`https://static.rutracker.cc/captcha/<hash>.jpg?<n>`), accompanied by a hidden `cap_sid` and a per-challenge answer field named `cap_code_<md5>`.
- **The clearance is IP-bound.** FlareSolverr must egress from the same public IP as the backend. True for the bundled compose stacks; document it.
- Go files use tabs (gofmt). Errors wrapped with `fmt.Errorf("...: %w", err)`. Sentinels at package level. No `init()` beyond the existing plugin registration. Tests run with `-race`.
- Backend verification command (never install Go locally):
  ```bash
  docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
    sh -c "go build ./... && go vet ./... && go test -race ./..."
  ```

## File Structure

| File | Responsibility |
|---|---|
| `backend/internal/plugins/registry/clearance.go` *(new)* | `Clearance` value type + `ClearanceProvider` interface + process-wide `SetClearanceProvider` / `ClearanceFor` / `InvalidateClearance`. Mirrors `challenge.go` and `SetDomainResolver`. |
| `backend/internal/plugins/registry/clearance_test.go` *(new)* | Hook install/replace/nil-provider behaviour. |
| `backend/internal/flaresolverr/minter.go` *(new)* | `(*Transport).Clearance` / `InvalidateClearance`: per-host cached solve returning cookies + UA. Reuses the existing session machinery in `client.go`. |
| `backend/internal/flaresolverr/minter_test.go` *(new)* | Cache hit/miss, invalidate, error propagation, concurrent minting. |
| `backend/internal/metrics/metrics.go` | Add `FlareSolverrClearanceTotal`. |
| `backend/cmd/server/main.go` | Install the minter as the clearance provider instead of the challenge transport. |
| `backend/internal/plugins/trackers/rutracker/rutracker.go` | `fetchBytes` injects clearance + browser UA and re-mints once on a challenge; `Login` performs a real login; `Verify` routed through the same path. |
| `backend/internal/plugins/captchalogin/engine.go` | `ChallengeSpec` + `Config.ChallengeFrom` so a tracker can supply a per-challenge image URL, extra hidden fields, and a dynamic answer-field name. |
| `backend/internal/plugins/trackers/rutracker/interactive.go` *(new)* | RuTracker's `captchalogin.Config`, login-response classifier, challenge parser, and the three `WithInteractiveLogin` methods. |
| `CLAUDE.md`, `flaresolverr/client.go` pkg doc, `rutracker.go` pkg doc | Replace the refuted TLS-fingerprint claim with the measured UA-binding facts. |

---

### Task 1: Clearance provider hook in the registry

**Files:**
- Create: `backend/internal/plugins/registry/clearance.go`
- Test: `backend/internal/plugins/registry/clearance_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Clearance struct { Cookies map[string]string; UserAgent string }`
  - `func (c Clearance) Valid() bool`
  - `type ClearanceProvider interface { Clearance(ctx context.Context, host string) (Clearance, error); InvalidateClearance(host string) }`
  - `func SetClearanceProvider(p ClearanceProvider)`
  - `func ClearanceFor(ctx context.Context, host string) (Clearance, error)` — returns `(Clearance{}, nil)` when no provider is installed
  - `func InvalidateClearance(host string)` — no-op when no provider is installed

- [ ] **Step 1: Write the failing test**

```go
package registry

import (
	"context"
	"errors"
	"testing"
)

type stubProvider struct {
	clearance   Clearance
	err         error
	invalidated []string
}

func (s *stubProvider) Clearance(context.Context, string) (Clearance, error) {
	return s.clearance, s.err
}
func (s *stubProvider) InvalidateClearance(host string) {
	s.invalidated = append(s.invalidated, host)
}

func TestClearanceFor_NoProvider_ReturnsZeroAndNoError(t *testing.T) {
	SetClearanceProvider(nil)
	got, err := ClearanceFor(context.Background(), "rutracker.org")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got.Valid() {
		t.Fatalf("Valid() = true, want false for the zero Clearance")
	}
}

func TestClearanceFor_WithProvider_ReturnsIt(t *testing.T) {
	t.Cleanup(func() { SetClearanceProvider(nil) })
	SetClearanceProvider(&stubProvider{clearance: Clearance{
		Cookies:   map[string]string{"cf_clearance": "abc"},
		UserAgent: "Mozilla/5.0 Chrome/148",
	}})
	got, err := ClearanceFor(context.Background(), "rutracker.org")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !got.Valid() {
		t.Fatal("Valid() = false, want true")
	}
	if got.Cookies["cf_clearance"] != "abc" || got.UserAgent != "Mozilla/5.0 Chrome/148" {
		t.Fatalf("got %+v", got)
	}
}

func TestClearanceFor_ProviderError_Propagates(t *testing.T) {
	t.Cleanup(func() { SetClearanceProvider(nil) })
	sentinel := errors.New("solver down")
	SetClearanceProvider(&stubProvider{err: sentinel})
	if _, err := ClearanceFor(context.Background(), "rutracker.org"); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}

func TestInvalidateClearance_ForwardsHost_AndIsSafeWithoutProvider(t *testing.T) {
	SetClearanceProvider(nil)
	InvalidateClearance("rutracker.org") // must not panic

	sp := &stubProvider{}
	t.Cleanup(func() { SetClearanceProvider(nil) })
	SetClearanceProvider(sp)
	InvalidateClearance("rutracker.org")
	if len(sp.invalidated) != 1 || sp.invalidated[0] != "rutracker.org" {
		t.Fatalf("invalidated = %v", sp.invalidated)
	}
}

func TestClearance_Valid_RequiresBothCookieAndUA(t *testing.T) {
	tests := []struct {
		name string
		c    Clearance
		want bool
	}{
		{"zero", Clearance{}, false},
		{"cookies only", Clearance{Cookies: map[string]string{"cf_clearance": "x"}}, false},
		{"ua only", Clearance{UserAgent: "x"}, false},
		{"both", Clearance{Cookies: map[string]string{"cf_clearance": "x"}, UserAgent: "y"}, true},
		{"empty cookie map", Clearance{Cookies: map[string]string{}, UserAgent: "y"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.Valid(); got != tt.want {
				t.Errorf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  go test ./internal/plugins/registry/ -run TestClearance -v
```

Expected: FAIL — `undefined: Clearance`, `undefined: SetClearanceProvider`.

- [ ] **Step 3: Write minimal implementation**

```go
package registry

import (
	"context"
	"sync"
)

// Clearance is a Cloudflare challenge solution: the cookies a browser earned
// plus the User-Agent it earned them with.
//
// The User-Agent is NOT incidental metadata. Measured against live RuTracker
// on 2026-08-01: cf_clearance is accepted only when the request carries the
// exact UA that solved the challenge — replaying it with Marauder's own UA, an
// empty UA, or a different browser UA all return 403 Cf-Mitigated. An earlier
// investigation attributed that rejection to Go's TLS fingerprint and
// concluded the cookie could not be replayed outside a browser at all; that is
// wrong, and this pairing is what makes replay work.
type Clearance struct {
	// Cookies is name -> value, e.g. {"cf_clearance": "..."}.
	Cookies map[string]string
	// UserAgent MUST be sent on every request carrying Cookies.
	UserAgent string
}

// Valid reports whether the clearance is usable. Both halves are required:
// either one alone still yields a challenge.
func (c Clearance) Valid() bool { return len(c.Cookies) > 0 && c.UserAgent != "" }

// ClearanceProvider mints Cloudflare clearances. Implemented by the
// FlareSolverr client; installed once at boot, like SetDomainResolver, because
// plugins register from init() and have no access to configuration.
type ClearanceProvider interface {
	// Clearance returns a usable clearance for host, solving a challenge only
	// when no cached one is available.
	Clearance(ctx context.Context, host string) (Clearance, error)
	// InvalidateClearance drops any cached clearance for host, so the next
	// Clearance call solves afresh.
	InvalidateClearance(host string)
}

var (
	clearanceMu sync.RWMutex
	clearanceP  ClearanceProvider
)

// SetClearanceProvider installs the process-wide provider. Passing nil
// disables it, which is the default: an unconfigured deployment keeps dialling
// trackers directly and simply fails against a challenged one.
func SetClearanceProvider(p ClearanceProvider) {
	clearanceMu.Lock()
	defer clearanceMu.Unlock()
	clearanceP = p
}

// ClearanceFor returns a clearance for host, or the zero Clearance when no
// provider is configured.
//
// "No provider" is deliberately not an error: callers must degrade to a direct
// dial rather than fail, so deployments without a solver behave exactly as
// they did before this feature existed.
func ClearanceFor(ctx context.Context, host string) (Clearance, error) {
	clearanceMu.RLock()
	p := clearanceP
	clearanceMu.RUnlock()
	if p == nil {
		return Clearance{}, nil
	}
	return p.Clearance(ctx, host)
}

// InvalidateClearance drops the cached clearance for host. Safe to call when
// no provider is installed.
func InvalidateClearance(host string) {
	clearanceMu.RLock()
	p := clearanceP
	clearanceMu.RUnlock()
	if p == nil {
		return
	}
	p.InvalidateClearance(host)
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  go test -race ./internal/plugins/registry/ -run TestClearance -v
```

Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/plugins/registry/clearance.go backend/internal/plugins/registry/clearance_test.go
git commit -m "feat(registry): add Cloudflare clearance provider hook"
```

---

### Task 2: FlareSolverr clearance minter

**Files:**
- Create: `backend/internal/flaresolverr/minter.go`
- Create: `backend/internal/flaresolverr/minter_test.go`
- Modify: `backend/internal/metrics/metrics.go` (add one counter after `FlareSolverrSessionsTotal`, before the closing `)` at line 169)

**Interfaces:**
- Consumes: `registry.Clearance` (Task 1); existing `(*Transport).ensureSession`, `(*Transport).mu`, `(*Transport).now`, `Transport.URL`, `Transport.HTTP`, `solveRequest`, `solveResponse` from `client.go`.
- Produces:
  - `func (t *Transport) Clearance(ctx context.Context, host string) (registry.Clearance, error)`
  - `func (t *Transport) InvalidateClearance(host string)`
  - `var ErrNoClearance = errors.New("flaresolverr: solve returned no clearance cookie")`
  - `metrics.FlareSolverrClearanceTotal` with label `result` ∈ `{"minted","cached","error"}`

> Note: `solveResponse.Solution` (`type solution`) currently has no `Cookies` field. Add one in this task:
> ```go
> Cookies []struct {
>     Name  string `json:"name"`
>     Value string `json:"value"`
> } `json:"cookies"`
> ```

- [ ] **Step 1: Add the metric**

In `backend/internal/metrics/metrics.go`, immediately after the `FlareSolverrSessionsTotal` block (line 168) and before the closing `)`:

```go
	// FlareSolverrClearanceTotal counts Cloudflare clearance acquisitions by
	// result: "minted" (a challenge was actually solved), "cached" (an
	// in-memory clearance was reused) and "error".
	//
	// A healthy install mints rarely — the observed cf_clearance lifetime is
	// about a year — so a sustained "minted" rate means clearances are being
	// rejected and re-solved, usually because the egress IP is changing or the
	// User-Agent is not being replayed verbatim.
	FlareSolverrClearanceTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "marauder_flaresolverr_clearance_total",
			Help: "Cloudflare clearance acquisitions, partitioned by result (minted/cached/error).",
		},
		[]string{"result"},
	)
```

- [ ] **Step 2: Write the failing test**

```go
package flaresolverr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSolver answers sessions.create and request.get like FlareSolverr 3.5.0:
// HTTP 200 always, with the real outcome in the envelope.
func fakeSolver(t *testing.T, solves *int32, cookie string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req["cmd"] {
		case "sessions.create":
			_, _ = w.Write([]byte(`{"status":"ok","session":"s1"}`))
		case "sessions.destroy":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "request.get":
			atomic.AddInt32(solves, 1)
			_, _ = w.Write([]byte(`{"status":"ok","solution":{"status":200,` +
				`"userAgent":"Mozilla/5.0 Chrome/148",` +
				`"cookies":[{"name":"cf_clearance","value":"` + cookie + `"},` +
				`{"name":"bb_guid","value":"g"}],"response":"<html></html>"}}`))
		default:
			_, _ = w.Write([]byte(`{"status":"error","message":"unknown cmd"}`))
		}
	}))
}

func TestClearance_MintsThenCachesPerHost(t *testing.T) {
	var solves int32
	srv := fakeSolver(t, &solves, "CLEAR")
	defer srv.Close()
	tr := New(srv.URL, 30*time.Second)

	got, err := tr.Clearance(context.Background(), "rutracker.org")
	if err != nil {
		t.Fatalf("first Clearance: %v", err)
	}
	if got.Cookies["cf_clearance"] != "CLEAR" {
		t.Fatalf("cookies = %v", got.Cookies)
	}
	if got.UserAgent != "Mozilla/5.0 Chrome/148" {
		t.Fatalf("ua = %q", got.UserAgent)
	}
	if !got.Valid() {
		t.Fatal("clearance should be Valid")
	}

	if _, err := tr.Clearance(context.Background(), "rutracker.org"); err != nil {
		t.Fatalf("second Clearance: %v", err)
	}
	if n := atomic.LoadInt32(&solves); n != 1 {
		t.Fatalf("solves = %d, want 1 (second call must hit the cache)", n)
	}

	// A different host is a different clearance — cf_clearance is host-bound.
	if _, err := tr.Clearance(context.Background(), "rutracker.net"); err != nil {
		t.Fatalf("other host: %v", err)
	}
	if n := atomic.LoadInt32(&solves); n != 2 {
		t.Fatalf("solves = %d, want 2 (per-host cache)", n)
	}
}

func TestInvalidateClearance_ForcesReMint(t *testing.T) {
	var solves int32
	srv := fakeSolver(t, &solves, "CLEAR")
	defer srv.Close()
	tr := New(srv.URL, 30*time.Second)

	if _, err := tr.Clearance(context.Background(), "rutracker.org"); err != nil {
		t.Fatal(err)
	}
	tr.InvalidateClearance("rutracker.org")
	if _, err := tr.Clearance(context.Background(), "rutracker.org"); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(&solves); n != 2 {
		t.Fatalf("solves = %d, want 2 after invalidate", n)
	}
}

func TestClearance_NoCookieInSolution_ReturnsErrNoClearance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["cmd"] == "sessions.create" {
			_, _ = w.Write([]byte(`{"status":"ok","session":"s1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok","solution":{"status":200,` +
			`"userAgent":"UA","cookies":[{"name":"bb_guid","value":"g"}],"response":"x"}}`))
	}))
	defer srv.Close()
	tr := New(srv.URL, 30*time.Second)
	if _, err := tr.Clearance(context.Background(), "rutracker.org"); err == nil ||
		!strings.Contains(err.Error(), "no clearance") {
		t.Fatalf("err = %v, want ErrNoClearance", err)
	}
}

func TestClearance_Unconfigured_ReturnsErrDisabled(t *testing.T) {
	tr := New("", 30*time.Second)
	if _, err := tr.Clearance(context.Background(), "rutracker.org"); err != ErrDisabled {
		t.Fatalf("err = %v, want ErrDisabled", err)
	}
}

func TestClearance_ConcurrentCallsSolveOnce(t *testing.T) {
	var solves int32
	srv := fakeSolver(t, &solves, "CLEAR")
	defer srv.Close()
	tr := New(srv.URL, 30*time.Second)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := tr.Clearance(context.Background(), "rutracker.org"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := atomic.LoadInt32(&solves); n != 1 {
		t.Fatalf("solves = %d, want 1 (single-flight per host)", n)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  go test ./internal/flaresolverr/ -run 'TestClearance|TestInvalidate' -v
```

Expected: FAIL — `tr.Clearance undefined`.

- [ ] **Step 4: Add the Cookies field to the solution struct**

In `backend/internal/flaresolverr/client.go`, extend `type solution` (line 359):

```go
type solution struct {
	Status    int    `json:"status"`
	Response  string `json:"response"`
	UserAgent string `json:"userAgent"`
	// Cookies is the browser's cookie jar after the solve. cf_clearance
	// lives here; it is what makes the clearance-minter model possible.
	Cookies []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"cookies"`
}
```

- [ ] **Step 5: Write minimal implementation**

```go
// Package-level addition: FlareSolverr as a clearance MINTER rather than a
// fetch path.
//
// The transport in client.go proxies every request through the browser, which
// costs a browser round-trip per fetch, serialises under load, and cannot
// carry a binary body. Minting instead solves the challenge once and hands the
// resulting cf_clearance + User-Agent back to the caller, which then uses an
// ordinary Go http.Client. Measured on 2026-08-01, that replay works for every
// gated RuTracker path including the binary dl.php.
package flaresolverr

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/artyomsv/marauder/backend/internal/metrics"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// ErrNoClearance is returned when a solve succeeded but produced no
// cf_clearance cookie — which means the host was not actually challenged, or
// Cloudflare served something other than the managed challenge. Returning an
// error rather than an empty clearance keeps callers from silently sending
// unauthenticated requests forever.
var ErrNoClearance = errors.New("flaresolverr: solve returned no clearance cookie")

// clearanceCookieName is the only cookie worth caching: it is the challenge
// solution. Session cookies belong to the tracker plugin, not here.
const clearanceCookieName = "cf_clearance"

type clearanceEntry struct {
	mu    sync.Mutex // single-flight per host
	value registry.Clearance
}

// clearanceFor returns the per-host entry, creating it on first use.
func (t *Transport) clearanceFor(host string) *clearanceEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.clearances == nil {
		t.clearances = map[string]*clearanceEntry{}
	}
	e, ok := t.clearances[host]
	if !ok {
		e = &clearanceEntry{}
		t.clearances[host] = e
	}
	return e
}

// Clearance implements registry.ClearanceProvider.
//
// Single-flight is per host rather than global: a burst of topic checks on one
// tracker must solve once, but a second tracker must not queue behind it.
func (t *Transport) Clearance(ctx context.Context, host string) (registry.Clearance, error) {
	if t == nil || t.URL == "" {
		return registry.Clearance{}, ErrDisabled
	}
	e := t.clearanceFor(host)

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.value.Valid() {
		metrics.FlareSolverrClearanceTotal.WithLabelValues("cached").Inc()
		return e.value, nil
	}

	c, err := t.mint(ctx, host)
	if err != nil {
		metrics.FlareSolverrClearanceTotal.WithLabelValues("error").Inc()
		return registry.Clearance{}, err
	}
	e.value = c
	metrics.FlareSolverrClearanceTotal.WithLabelValues("minted").Inc()
	return c, nil
}

// InvalidateClearance implements registry.ClearanceProvider.
func (t *Transport) InvalidateClearance(host string) {
	if t == nil {
		return
	}
	e := t.clearanceFor(host)
	e.mu.Lock()
	e.value = registry.Clearance{}
	e.mu.Unlock()
}

// mint drives one real solve. It targets the host root: any challenged path
// yields the same host-wide cf_clearance, and the root is the cheapest page to
// render.
func (t *Transport) mint(ctx context.Context, host string) (registry.Clearance, error) {
	if _, err := url.Parse("https://" + host + "/"); err != nil || host == "" {
		return registry.Clearance{}, fmt.Errorf("flaresolverr: refusing to mint for host %q", host)
	}
	session, serr := t.ensureSession(ctx)
	if serr != nil && t.OnDegraded != nil {
		// Session-less minting still works, just slower. Report it for the
		// same reason RoundTrip does.
		t.OnDegraded(serr)
	}
	budget, err := t.solveBudget(ctx)
	if err != nil {
		return registry.Clearance{}, err
	}
	var sr solveResponse
	if err := t.solve(ctx, solveRequest{
		Cmd:        "request.get",
		URL:        "https://" + host + "/",
		MaxTimeout: int(budget / time.Millisecond),
		Session:    session,
	}, &sr); err != nil {
		return registry.Clearance{}, err
	}
	out := registry.Clearance{
		Cookies:   map[string]string{},
		UserAgent: sr.Solution.UserAgent,
	}
	for _, c := range sr.Solution.Cookies {
		if c.Name == clearanceCookieName {
			out.Cookies[c.Name] = c.Value
		}
	}
	if !out.Valid() {
		return registry.Clearance{}, fmt.Errorf("%w (host %s)", ErrNoClearance, host)
	}
	return out, nil
}
```

Two supporting edits in `client.go`:

1. Add the cache field to `Transport` (inside the `mu`-guarded block, after `nextCreateAttempt`):

```go
	// clearances caches one Cloudflare clearance per host. cf_clearance is
	// host-bound: a rutracker.org cookie is rejected on rutracker.net.
	clearances map[string]*clearanceEntry
```

2. Extract the solve round-trip so `mint` and `fetch` share it. Replace the body of `fetch` from `payload, err := json.Marshal(...)` through the `sr.Status != "ok"` check with a call to a new helper, and add:

```go
// solve performs one request.* round-trip against FlareSolverr and decodes the
// envelope. FlareSolverr answers HTTP 200 even for a failed command, so the
// envelope status is checked here rather than at each call site.
func (t *Transport) solve(ctx context.Context, body solveRequest, out *solveResponse) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("flaresolverr: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL+"/v1", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("flaresolverr: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("flaresolverr: call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("flaresolverr: status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("flaresolverr: decode: %w", err)
	}
	if out.Status != "ok" {
		return fmt.Errorf("flaresolverr: %s", out.Message)
	}
	return nil
}
```

`fetch` then becomes:

```go
func (t *Transport) fetch(ctx context.Context, req *http.Request, session string) (*http.Response, error) {
	budget, err := t.solveBudget(ctx)
	if err != nil {
		return nil, err
	}
	var sr solveResponse
	if err := t.solve(ctx, solveRequest{
		Cmd:        "request.get",
		URL:        req.URL.String(),
		MaxTimeout: int(budget / time.Millisecond),
		Session:    session,
	}, &sr); err != nil {
		return nil, err
	}
	body := []byte(sr.Solution.Response)
	return &http.Response{
		StatusCode:    sr.Solution.Status,
		Status:        fmt.Sprintf("%d %s", sr.Solution.Status, http.StatusText(sr.Solution.Status)),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}, nil
}
```

- [ ] **Step 6: Run tests to verify they pass**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  go test -race ./internal/flaresolverr/ ./internal/metrics/ -v
```

Expected: PASS, including every pre-existing `client_test.go` test (the `fetch` refactor must not change behaviour).

- [ ] **Step 7: Commit**

```bash
git add backend/internal/flaresolverr/ backend/internal/metrics/metrics.go
git commit -m "feat(flaresolverr): mint Cloudflare clearance for replay from Go"
```

---

### Task 3: Wire the minter at boot

**Files:**
- Modify: `backend/cmd/server/main.go:130-160`

**Interfaces:**
- Consumes: `(*flaresolverr.Transport).Clearance` / `InvalidateClearance` (Task 2), `registry.SetClearanceProvider` (Task 1).
- Produces: a booted process where `registry.ClearanceFor` works.

- [ ] **Step 1: Replace the challenge-transport install**

Replace the comment block and the `registry.SetChallengeTransport(solver)` line with:

```go
	// Cloudflare clearance: RuTracker sits behind a managed challenge on every
	// /forum/ path. FlareSolverr solves it once and we replay the resulting
	// cf_clearance + User-Agent from ordinary Go requests.
	//
	// This deliberately does NOT install the solver as a fetch transport. An
	// earlier revision did, on the belief that cf_clearance was bound to the
	// TLS fingerprint of the client that earned it and so could not leave the
	// browser. Measured against live RuTracker on 2026-08-01, the binding is to
	// the User-Agent, not the fingerprint: replayed with the browser's own UA
	// the cookie is accepted from plain net/http on every gated path. Proxying
	// each fetch through the browser therefore bought nothing and cost a great
	// deal — a browser round-trip per request, serialised solves, no binary
	// bodies (so no .torrent), and no login (so no search).
	if cfg.FlareSolverrURL != "" {
		solver := flaresolverr.New(cfg.FlareSolverrURL, cfg.FlareSolverrTimeout)
		solver.OnDegraded = func(err error) {
			logger.Warn().Err(err).
				Msg("flaresolverr session unavailable; solving without one (slower)")
		}
		registry.SetClearanceProvider(solver)
		defer func() {
			shCtx, shCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer shCancel()
			if err := solver.Close(shCtx); err != nil {
				logger.Warn().Err(err).Msg("flaresolverr session cleanup failed")
			}
		}()
		logger.Info().
			Str("url", cfg.FlareSolverrURL).
			Dur("timeout", cfg.FlareSolverrTimeout).
			Msg("flaresolverr clearance provider enabled")
	}
```

- [ ] **Step 2: Verify the build**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  sh -c "go build ./... && go vet ./..."
```

Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add backend/cmd/server/main.go
git commit -m "feat(server): install flaresolverr as clearance provider"
```

---

### Task 4: RuTracker fetches with clearance and a browser UA

**Files:**
- Modify: `backend/internal/plugins/trackers/rutracker/rutracker.go` — `fetchBytes` (464-506), `Login` (177-245), `Verify` (249-266), `effectiveTransport` (98-103), the `userAgent` const (44)
- Test: `backend/internal/plugins/trackers/rutracker/rutracker_test.go`

**Interfaces:**
- Consumes: `registry.ClearanceFor`, `registry.InvalidateClearance`, `registry.Clearance` (Task 1).
- Produces:
  - `func (p *plugin) requestUA(c registry.Clearance) string`
  - `fetchBytes` gains a single re-mint retry on a Cloudflare challenge.
  - `Login` performs a real credential POST (no more unconditional skip).

- [ ] **Step 1: Write the failing tests**

Append to `rutracker_test.go`:

```go
// stubClearance installs a clearance provider for the duration of a test.
type stubClearance struct {
	mu          sync.Mutex
	c           registry.Clearance
	mints       int
	invalidated int
}

func (s *stubClearance) Clearance(context.Context, string) (registry.Clearance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mints++
	return s.c, nil
}
func (s *stubClearance) InvalidateClearance(string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidated++
}

func TestFetchBytes_SendsClearanceCookieAndBrowserUA(t *testing.T) {
	var gotUA, gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		if c, err := r.Cookie("cf_clearance"); err == nil {
			gotCookie = c.Value
		}
		_, _ = w.Write([]byte(`<html><title>t :: RuTracker.org</title>magnet:?xt=urn:btih:aabb</html>`))
	}))
	defer srv.Close()

	sc := &stubClearance{c: registry.Clearance{
		Cookies:   map[string]string{"cf_clearance": "CLEAR"},
		UserAgent: "Mozilla/5.0 Chrome/148",
	}}
	registry.SetClearanceProvider(sc)
	t.Cleanup(func() { registry.SetClearanceProvider(nil) })

	p := newTestPlugin(t, srv) // existing helper: injects p.domain + p.transport
	if _, err := p.fetchBytes(context.Background(), nil, nil,
		"https://"+p.domain+"/forum/viewtopic.php?t=1"); err != nil {
		t.Fatalf("fetchBytes: %v", err)
	}
	if gotUA != "Mozilla/5.0 Chrome/148" {
		t.Errorf("UA = %q, want the clearance browser UA (cf_clearance is UA-bound)", gotUA)
	}
	if gotCookie != "CLEAR" {
		t.Errorf("cf_clearance cookie = %q, want CLEAR", gotCookie)
	}
}

func TestFetchBytes_NoProvider_UsesHonestUA(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`<html></html>`))
	}))
	defer srv.Close()
	registry.SetClearanceProvider(nil)

	p := newTestPlugin(t, srv)
	if _, err := p.fetchBytes(context.Background(), nil, nil,
		"https://"+p.domain+"/forum/viewtopic.php?t=1"); err != nil {
		t.Fatal(err)
	}
	if gotUA != userAgent {
		t.Errorf("UA = %q, want %q when no clearance is configured", gotUA, userAgent)
	}
}

func TestFetchBytes_ChallengeResponse_InvalidatesAndRetriesOnce(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Cf-Mitigated", "challenge")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`<html>ok</html>`))
	}))
	defer srv.Close()

	sc := &stubClearance{c: registry.Clearance{
		Cookies: map[string]string{"cf_clearance": "CLEAR"}, UserAgent: "UA",
	}}
	registry.SetClearanceProvider(sc)
	t.Cleanup(func() { registry.SetClearanceProvider(nil) })

	p := newTestPlugin(t, srv)
	body, err := p.fetchBytes(context.Background(), nil, nil,
		"https://"+p.domain+"/forum/viewtopic.php?t=1")
	if err != nil {
		t.Fatalf("fetchBytes: %v", err)
	}
	if !strings.Contains(string(body), "ok") {
		t.Fatalf("body = %q, want the retried response", body)
	}
	if sc.invalidated != 1 {
		t.Errorf("invalidated = %d, want 1", sc.invalidated)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("calls = %d, want exactly 2 (one retry, not a loop)", calls)
	}
}

func TestFetchBytes_PersistentChallenge_FailsWithoutLooping(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Cf-Mitigated", "challenge")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	sc := &stubClearance{c: registry.Clearance{
		Cookies: map[string]string{"cf_clearance": "CLEAR"}, UserAgent: "UA",
	}}
	registry.SetClearanceProvider(sc)
	t.Cleanup(func() { registry.SetClearanceProvider(nil) })

	p := newTestPlugin(t, srv)
	_, err := p.fetchBytes(context.Background(), nil, nil,
		"https://"+p.domain+"/forum/viewtopic.php?t=1")
	if !errors.Is(err, registry.ErrCloudflareChallenge) {
		t.Fatalf("err = %v, want ErrCloudflareChallenge", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestLogin_PostsCredentials_WhenClearanceConfigured(t *testing.T) {
	var posted url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			posted = r.PostForm
			_, _ = w.Write([]byte(`<html><span id="logged-in-username">bob</span></html>`))
			return
		}
		_, _ = w.Write([]byte(`<html></html>`))
	}))
	defer srv.Close()

	sc := &stubClearance{c: registry.Clearance{
		Cookies: map[string]string{"cf_clearance": "CLEAR"}, UserAgent: "UA",
	}}
	registry.SetClearanceProvider(sc)
	t.Cleanup(func() { registry.SetClearanceProvider(nil) })

	p := newTestPlugin(t, srv)
	creds := &domain.TrackerCredential{
		UserID:    uuid.New(),
		Username:  "bob",
		SecretEnc: []byte("hunter2"),
	}
	if err := p.Login(context.Background(), creds); err != nil {
		t.Fatalf("Login: %v", err)
	}
	// The regression this guards: Login used to return nil WITHOUT posting
	// anything whenever the solver was configured, which left every session
	// anonymous and broke tracker search.
	if posted.Get("login_username") != "bob" {
		t.Fatalf("login_username = %q, want bob (Login must actually post)", posted.Get("login_username"))
	}
	if posted.Get("login_password") != "hunter2" {
		t.Fatalf("login_password not submitted")
	}
}
```

Add imports as needed: `sync`, `sync/atomic`, `errors`, `net/url`, `github.com/google/uuid`.

> If `newTestPlugin` does not already exist in `rutracker_test.go`, add it, matching how the existing tests build a plugin against an `httptest` server (host override via `p.domain`, `p.transport` set to an `e2etest.HostRewriteTransport` or the server's client transport).

- [ ] **Step 2: Run tests to verify they fail**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  go test ./internal/plugins/trackers/rutracker/ -run 'TestFetchBytes|TestLogin_Posts' -v
```

Expected: FAIL — UA is `Marauder/0.3`, no cookie sent, `Login` posts nothing.

- [ ] **Step 3: Implement**

Change the `userAgent` const comment (line 44) and add a resolver:

```go
	// userAgent identifies Marauder honestly and is used only when NO
	// Cloudflare clearance is configured. A clearance carries its own
	// User-Agent which MUST be sent verbatim — see requestUA.
	userAgent = "Marauder/0.3 (+https://marauder.cc)"
```

```go
// requestUA picks the User-Agent for an outbound request.
//
// A clearance is only honoured by Cloudflare when the request repeats the
// exact User-Agent the browser used to earn it; sending Marauder's own UA with
// a valid cf_clearance returns 403 (measured 2026-08-01). Without a clearance
// there is nothing to match, so we identify ourselves honestly.
func (p *plugin) requestUA(c registry.Clearance) string {
	if c.Valid() {
		return c.UserAgent
	}
	return userAgent
}
```

Replace `effectiveTransport` so the plugin no longer routes through the solver:

```go
// effectiveTransport returns the plugin-local transport used by tests to reach
// an httptest server. It deliberately no longer consults
// registry.ChallengeTransport: RuTracker is not fetched through a browser any
// more, it replays a minted clearance from ordinary Go requests.
func (p *plugin) effectiveTransport() http.RoundTripper { return p.transport }
```

Rewrite `fetchBytes` to inject the clearance and retry once:

```go
func (p *plugin) fetchBytes(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential, target string) ([]byte, error) {
	body, err := p.fetchOnce(ctx, creds, target)
	if !errors.Is(err, registry.ErrCloudflareChallenge) {
		return body, err
	}
	// A challenge with a clearance attached means the clearance died — the
	// egress IP moved, or Cloudflare rotated it. Drop it and solve once more.
	// Exactly once: if the second attempt is challenged too, something else is
	// wrong and retrying would spin a browser per check.
	registry.InvalidateClearance(p.effectiveDomain())
	return p.fetchOnce(ctx, creds, target)
}

func (p *plugin) fetchOnce(ctx context.Context, creds *domain.TrackerCredential, target string) ([]byte, error) {
	// SSRF guard: every caller builds target from p.effectiveDomain(), but
	// this is the last line of defense before dialing — refuse any host that
	// isn't the resolved effective domain or a known/admin-configured
	// rutracker domain, and any non-HTTP scheme.
	u, err := url.Parse(strings.TrimSpace(target))
	if err != nil {
		return nil, fmt.Errorf("rutracker: invalid URL: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("rutracker: refusing URL scheme %q", u.Scheme)
	}
	if u.Host != p.effectiveDomain() && !registry.DomainAllowed(pluginName, u.Hostname(), knownDomains) {
		return nil, fmt.Errorf("rutracker: refusing to fetch off-site host %q", u.Hostname())
	}

	sess := p.session(creds)
	clearance := p.applyClearance(ctx, sess, u)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", p.requestUA(clearance))
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	resp, err := sess.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if isCloudflareChallenge(resp) {
		return nil, fmt.Errorf("rutracker GET %s: %w", target, registry.ErrCloudflareChallenge)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("rutracker GET %s -> %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

// session returns the per-user (or anonymous) session, with the test transport
// applied. Centralised so Login, Verify and fetch cannot drift apart — Verify
// previously forgot to set the transport and so dialled the live site during
// tests and bypassed the solver in production.
func (p *plugin) session(creds *domain.TrackerCredential) *forumcommon.Session {
	key := pluginName + ":nocreds"
	if creds != nil {
		key = forumcommon.SessionKey(pluginName, creds.UserID.String())
	}
	sess := p.sessions.GetOrCreate(key, userAgent)
	if tr := p.effectiveTransport(); tr != nil {
		sess.Client.Transport = tr
	}
	return sess
}

// applyClearance seeds the session jar with the Cloudflare clearance cookie
// for u's host and returns it so the caller can match the User-Agent. A solver
// failure is not fatal: the request proceeds without a clearance and the
// tracker's own 403 surfaces as ErrCloudflareChallenge, which is a far clearer
// signal than a solver-internal error.
func (p *plugin) applyClearance(ctx context.Context, sess *forumcommon.Session, u *url.URL) registry.Clearance {
	c, err := registry.ClearanceFor(ctx, p.effectiveDomain())
	if err != nil {
		log.Warn().Str("plugin", pluginName).Err(err).
			Msg("cloudflare clearance unavailable; requesting without one")
		return registry.Clearance{}
	}
	if !c.Valid() {
		return registry.Clearance{}
	}
	jarCookies := make([]*http.Cookie, 0, len(c.Cookies))
	for name, val := range c.Cookies {
		jarCookies = append(jarCookies, &http.Cookie{Name: name, Value: val, Path: "/"})
	}
	root := &url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/"}
	sess.Client.Jar.SetCookies(root, jarCookies)
	return c
}
```

Rewrite `Login` — delete the whole skip block (lines 181-203) and use the shared session/clearance helpers:

```go
// Login posts the login form. The cookie jar attached to the session captures
// bb_session for subsequent calls.
//
// This runs even when a solver is configured. An earlier revision skipped the
// POST entirely in that case, on the belief that a login could not survive the
// browser hop and unlocked only dl.php anyway. Both halves were wrong:
// tracker.php search is login-gated too, and a clearance replayed with its own
// User-Agent lets an ordinary Go POST reach the login form (measured
// 2026-08-01). Skipping it left every session anonymous, which is what made
// search report "needs a tracker account".
func (p *plugin) Login(ctx context.Context, creds *domain.TrackerCredential) error {
	if creds == nil || creds.Username == "" {
		return errors.New("rutracker credentials are required")
	}
	// A stored session from the interactive captcha flow wins: rehydrating it
	// avoids a login POST entirely, and RuTracker imposes a captcha on clients
	// that log in repeatedly.
	if len(creds.SessionEnc) > 0 {
		if err := p.restoreSession(ctx, creds); err == nil {
			return nil
		}
		// Fall through: a dead stored session should be re-established by a
		// fresh credential login rather than reported as a hard failure.
	}

	endpoint := "https://" + p.effectiveDomain() + "/forum/login.php"
	u, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	sess := p.session(creds)
	clearance := p.applyClearance(ctx, sess, u)

	form := url.Values{
		"login_username": {creds.Username},
		"login_password": {string(creds.SecretEnc)}, // already decrypted by the caller
		"login":          {"вход"},
		"redirect":       {"index.php"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", p.requestUA(clearance))
	req.Header.Set("Origin", "https://"+p.effectiveDomain())
	req.Header.Set("Referer", endpoint)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return fmt.Errorf("rutracker login: %w", err)
	}
	defer resp.Body.Close()
	// Check for the interstitial BEFORE testing for the logged-in marker: a
	// challenge page naturally lacks that marker, and reporting it as bad
	// credentials is the misdiagnosis this guard exists to prevent.
	if isCloudflareChallenge(resp) {
		return fmt.Errorf("rutracker login: %w", registry.ErrCloudflareChallenge)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("rutracker login: read body: %w", err)
	}
	if strings.Contains(string(body), `id="logged-in-username"`) {
		sess.LoggedIn = true
		return nil
	}
	// RuTracker imposes a captcha on a flagged client and clears the flag after
	// a success, so this is a transient state, not a permanent one. Report it
	// as a captcha requirement so the caller can run the interactive flow.
	if bytes.Contains(body, []byte("cap_sid")) {
		return fmt.Errorf("rutracker login: %w", registry.ErrCaptchaRequired)
	}
	return errors.New("rutracker login failed: invalid credentials (no logged-in marker in response)")
}

// restoreSession rehydrates cookies harvested by the interactive login and
// confirms they still work. Mirrors the LostFilm pattern.
func (p *plugin) restoreSession(ctx context.Context, creds *domain.TrackerCredential) error {
	var cookies map[string]string
	if err := json.Unmarshal(creds.SessionEnc, &cookies); err != nil {
		return fmt.Errorf("rutracker: corrupt stored session: %w", registry.ErrSessionExpired)
	}
	sess := p.session(creds)
	// bb_session is scoped to /forum/, so it must be set against that path.
	u, _ := url.Parse("https://" + p.effectiveDomain() + "/forum/")
	jarCookies := make([]*http.Cookie, 0, len(cookies))
	for name, val := range cookies {
		jarCookies = append(jarCookies, &http.Cookie{Name: name, Value: val, Path: "/forum/"})
	}
	sess.Client.Jar.SetCookies(u, jarCookies)
	ok, err := p.Verify(ctx, creds)
	if err != nil {
		return err
	}
	if !ok {
		return registry.ErrSessionExpired
	}
	sess.LoggedIn = true
	return nil
}
```

Rewrite `Verify` to go through the shared fetch path:

```go
// Verify checks whether the cached session is still valid by hitting a page
// that renders the logged-in marker.
//
// It goes through fetchBytes rather than dialling directly so it picks up the
// Cloudflare clearance, the matching User-Agent and the test transport. The
// direct dial it used before ignored all three, which made it report "session
// invalid" for a perfectly good session whenever the site was challenged.
func (p *plugin) Verify(ctx context.Context, creds *domain.TrackerCredential) (bool, error) {
	target := "https://" + p.effectiveDomain() + "/forum/index.php"
	body, err := p.fetchBytes(ctx, nil, creds, target)
	if err != nil {
		return false, err
	}
	return bytes.Contains(body, []byte(`id="logged-in-username"`)), nil
}
```

Add `bytes` and `encoding/json` to the import block.

- [ ] **Step 4: Run tests to verify they pass**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  go test -race ./internal/plugins/trackers/rutracker/ -v
```

Expected: PASS, including all pre-existing RuTracker tests.

- [ ] **Step 5: Full backend verification**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  sh -c "go build ./... && go vet ./... && go test -race ./..."
```

Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/plugins/trackers/rutracker/
git commit -m "fix(rutracker): authenticate using a replayed Cloudflare clearance

Login was skipped whenever the challenge solver was active, leaving every
session anonymous: tracker search reported 'needs a tracker account' and
downloads fell back to a hash-only magnet with no announce URL.

Replay the minted cf_clearance with the User-Agent that earned it and the
tracker accepts ordinary Go requests on every gated path, so the login POST,
the authenticated search and the binary .torrent all work again.

Verify now shares the fetch path instead of dialling directly, so it no
longer misreports a live session as dead on a challenged site."
```

---

### Task 5: Per-challenge captcha support in the shared engine

**Files:**
- Modify: `backend/internal/plugins/captchalogin/engine.go` — `Config` (33-41), `fetchCaptcha` (68-91), `Begin` (92-123), `Refresh` (126-137), `Complete` (140-161)
- Modify: `backend/internal/plugins/captchalogin/pending.go` — store the challenge spec alongside the session
- Test: `backend/internal/plugins/captchalogin/engine_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces:
  - `type ChallengeSpec struct { ImageURL string; Fields url.Values; AnswerField string }`
  - `Config.ChallengeFrom func(body []byte) (ChallengeSpec, error)` — optional; nil keeps the existing static-`CaptchaURL` behaviour (LostFilm).

**Why:** LostFilm's captcha is a fixed endpoint with a fixed field name. RuTracker's image URL is unique per challenge and lives on `static.rutracker.cc`, its `cap_sid` must be echoed back, and the answer field is named `cap_code_<md5>`. `BuildForm` cannot know any of that because it never sees the login response.

- [ ] **Step 1: Write the failing test**

```go
func TestBegin_ChallengeFrom_UsesPerChallengeImageURL(t *testing.T) {
	var captchaHits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<input name="cap_sid" value="SID123">` +
			`<input name="cap_code_deadbeef" value="">` +
			`<img src="IMGURL">`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&captchaHits, 1)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xFF, 0xD8, 0xFF})
	}))
	defer imgSrv.Close()

	cfg := Config{
		LoginURL:    srv.URL + "/login",
		CookieNames: []string{"bb_session"},
		BuildForm: func(c *domain.TrackerCredential, answer string, needCaptcha bool) url.Values {
			return url.Values{"login_username": {c.Username}}
		},
		Classify: func(body []byte) Outcome {
			if strings.Contains(string(body), "cap_sid") {
				return OutcomeNeedCaptcha
			}
			return OutcomeSuccess
		},
		ChallengeFrom: func(body []byte) (ChallengeSpec, error) {
			return ChallengeSpec{
				ImageURL:    imgSrv.URL + "/captcha.jpg",
				Fields:      url.Values{"cap_sid": {"SID123"}},
				AnswerField: "cap_code_deadbeef",
			}, nil
		},
	}
	e := New(cfg, func() *forumcommon.Session { return newSession(t) })

	challenge, cookies, err := e.Begin(context.Background(),
		&domain.TrackerCredential{Username: "bob"})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if cookies != nil {
		t.Fatal("cookies should be nil when a captcha is required")
	}
	if challenge == nil || challenge.ChallengeID == "" {
		t.Fatal("want a challenge with an ID")
	}
	if challenge.MIMEType != "image/jpeg" {
		t.Errorf("MIMEType = %q, want image/jpeg", challenge.MIMEType)
	}
	if atomic.LoadInt32(&captchaHits) != 1 {
		t.Errorf("captcha fetched %d times, want 1 from the per-challenge URL", captchaHits)
	}
}

func TestComplete_ChallengeFrom_SubmitsSidAndDynamicAnswerField(t *testing.T) {
	var posted url.Values
	var posts int32
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if atomic.AddInt32(&posts, 1) == 1 {
			_, _ = w.Write([]byte(`<input name="cap_sid" value="SID123">`))
			return
		}
		posted = r.PostForm
		http.SetCookie(w, &http.Cookie{Name: "bb_session", Value: "SESS", Path: "/"})
		_, _ = w.Write([]byte(`<span id="logged-in-username">bob</span>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xFF})
	}))
	defer imgSrv.Close()

	cfg := Config{
		LoginURL:    srv.URL + "/login",
		CookieNames: []string{"bb_session"},
		BuildForm: func(c *domain.TrackerCredential, answer string, needCaptcha bool) url.Values {
			return url.Values{"login_username": {c.Username}}
		},
		Classify: func(body []byte) Outcome {
			if strings.Contains(string(body), "logged-in-username") {
				return OutcomeSuccess
			}
			return OutcomeNeedCaptcha
		},
		ChallengeFrom: func(body []byte) (ChallengeSpec, error) {
			return ChallengeSpec{
				ImageURL:    imgSrv.URL + "/c.jpg",
				Fields:      url.Values{"cap_sid": {"SID123"}},
				AnswerField: "cap_code_deadbeef",
			}, nil
		},
	}
	e := New(cfg, func() *forumcommon.Session { return newSession(t) })
	challenge, _, err := e.Begin(context.Background(), &domain.TrackerCredential{Username: "bob"})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	cookies, err := e.Complete(context.Background(), challenge.ChallengeID, "xxa")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if posted.Get("cap_sid") != "SID123" {
		t.Errorf("cap_sid = %q, want SID123", posted.Get("cap_sid"))
	}
	if posted.Get("cap_code_deadbeef") != "xxa" {
		t.Errorf("answer field = %q, want xxa", posted.Get("cap_code_deadbeef"))
	}
	if cookies["bb_session"] != "SESS" {
		t.Errorf("harvested = %v, want bb_session=SESS", cookies)
	}
}

func TestBegin_NilChallengeFrom_KeepsStaticCaptchaURL(t *testing.T) {
	// Guards the LostFilm path: an unset ChallengeFrom must not change anything.
	var staticHits int32
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&staticHits, 1)
		w.Header().Set("Content-Type", "image/gif")
		_, _ = w.Write([]byte("GIF89a"))
	}))
	defer imgSrv.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"need_captcha":true}`))
	}))
	defer srv.Close()

	e := New(Config{
		LoginURL:    srv.URL,
		CaptchaURL:  imgSrv.URL,
		CookieNames: []string{"lf_session"},
		BuildForm: func(c *domain.TrackerCredential, answer string, needCaptcha bool) url.Values {
			return url.Values{"mail": {c.Username}}
		},
		Classify: func([]byte) Outcome { return OutcomeNeedCaptcha },
	}, func() *forumcommon.Session { return newSession(t) })

	ch, _, err := e.Begin(context.Background(), &domain.TrackerCredential{Username: "bob"})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if ch.MIMEType != "image/gif" {
		t.Errorf("MIMEType = %q", ch.MIMEType)
	}
	if atomic.LoadInt32(&staticHits) != 1 {
		t.Errorf("static captcha hits = %d, want 1", staticHits)
	}
}
```

> `newSession(t)` should reuse whatever helper `engine_test.go` already uses to build a `*forumcommon.Session`; if none exists, add
> `func newSession(t *testing.T) *forumcommon.Session { t.Helper(); return forumcommon.New().GetOrCreate(t.Name()+uuid.NewString(), "test-ua") }`.

- [ ] **Step 2: Run tests to verify they fail**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  go test ./internal/plugins/captchalogin/ -run 'ChallengeFrom' -v
```

Expected: FAIL — `undefined: ChallengeSpec`, unknown field `ChallengeFrom`.

- [ ] **Step 3: Implement**

Add to `engine.go`:

```go
// ChallengeSpec describes one captcha instance extracted from a login
// response. It exists for trackers whose captcha is not a fixed endpoint:
// RuTracker mints a new image URL per attempt on a separate static host, echoes
// a cap_sid back, and names the answer field cap_code_<md5>.
type ChallengeSpec struct {
	// ImageURL is the absolute URL of this challenge's captcha image.
	ImageURL string
	// Fields are hidden inputs that must be replayed with the answer.
	Fields url.Values
	// AnswerField is the form field the user's answer belongs in. Empty means
	// BuildForm already places the answer itself.
	AnswerField string
}
```

Extend `Config`:

```go
	// ChallengeFrom, when set, extracts the per-challenge captcha details from
	// the body of the login response that demanded one. Leave nil for trackers
	// with a fixed CaptchaURL and a fixed answer field name (LostFilm).
	ChallengeFrom func(body []byte) (ChallengeSpec, error)
```

Change `fetchCaptcha` to take an explicit URL:

```go
func (e *Engine) fetchCaptcha(ctx context.Context, sess *forumcommon.Session, imageURL string) (*registry.LoginChallenge, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	// ... body unchanged from here ...
}
```

In `Begin`, replace the `OutcomeNeedCaptcha` arm:

```go
	case OutcomeNeedCaptcha:
		spec := ChallengeSpec{ImageURL: e.cfg.CaptchaURL}
		if e.cfg.ChallengeFrom != nil {
			var perr error
			if spec, perr = e.cfg.ChallengeFrom(body); perr != nil {
				return nil, nil, fmt.Errorf("interactive begin: parse challenge: %w", perr)
			}
		}
		challenge, ferr := e.fetchCaptcha(ctx, sess, spec.ImageURL)
		if ferr != nil {
			return nil, nil, fmt.Errorf("interactive begin: fetch captcha: %w", ferr)
		}
		id, perr := e.store.put(sess, creds, spec)
		if perr != nil {
			return nil, nil, perr
		}
		challenge.ChallengeID = id
		return challenge, nil, nil
```

In `Complete`, merge the spec into the form:

```go
	form := e.cfg.BuildForm(p.creds, answer, true)
	for k, vs := range p.spec.Fields {
		form[k] = vs
	}
	if p.spec.AnswerField != "" {
		form.Set(p.spec.AnswerField, answer)
	}
	body, err := e.post(ctx, p.sess, form)
```

In `Refresh`, re-derive the challenge when the tracker mints them per attempt:

```go
// Refresh obtains a new captcha for a pending challenge.
//
// With ChallengeFrom set the image cannot simply be re-fetched: RuTracker ties
// the image to the cap_sid issued alongside it, so a new picture requires a new
// login attempt. Re-post on the pending jar and re-extract.
func (e *Engine) Refresh(ctx context.Context, challengeID string) (*registry.LoginChallenge, error) {
	p, ok := e.store.get(challengeID)
	if !ok {
		return nil, errors.New("unknown or expired challenge")
	}
	spec := p.spec
	if e.cfg.ChallengeFrom != nil {
		body, err := e.post(ctx, p.sess, e.cfg.BuildForm(p.creds, "", false))
		if err != nil {
			return nil, fmt.Errorf("interactive refresh: %w", err)
		}
		if e.cfg.Classify(body) != OutcomeNeedCaptcha {
			return nil, errors.New("interactive refresh: tracker no longer offers a captcha")
		}
		if spec, err = e.cfg.ChallengeFrom(body); err != nil {
			return nil, fmt.Errorf("interactive refresh: parse challenge: %w", err)
		}
		e.store.updateSpec(challengeID, spec)
	}
	if spec.ImageURL == "" {
		spec.ImageURL = e.cfg.CaptchaURL
	}
	challenge, err := e.fetchCaptcha(ctx, p.sess, spec.ImageURL)
	if err != nil {
		return nil, err
	}
	challenge.ChallengeID = challengeID
	return challenge, nil
}
```

In `pending.go`, add `spec ChallengeSpec` to the pending entry, change `put` to `put(sess *forumcommon.Session, creds *domain.TrackerCredential, spec ChallengeSpec) (string, error)`, and add:

```go
// updateSpec replaces the stored challenge details after a Refresh minted a
// new captcha.
func (s *pendingStore) updateSpec(id string, spec ChallengeSpec) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.entries[id]; ok {
		p.spec = spec
	}
}
```

Update the LostFilm call site only if it referenced `fetchCaptcha` or `put` directly (it should not — both are unexported engine internals).

- [ ] **Step 4: Run tests to verify they pass**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  go test -race ./internal/plugins/captchalogin/ ./internal/plugins/trackers/lostfilm/ -v
```

Expected: PASS, including every pre-existing LostFilm test (the nil-`ChallengeFrom` path must be unchanged).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/plugins/captchalogin/
git commit -m "feat(captchalogin): support per-challenge captcha URL and form fields"
```

---

### Task 6: RuTracker interactive captcha login

**Files:**
- Create: `backend/internal/plugins/trackers/rutracker/interactive.go`
- Create: `backend/internal/plugins/trackers/rutracker/interactive_test.go`
- Modify: `backend/internal/plugins/trackers/rutracker/rutracker.go` — add `engine`/`engineOnce` fields to `plugin`

**Interfaces:**
- Consumes: `captchalogin.New`, `captchalogin.Config`, `captchalogin.ChallengeSpec`, `captchalogin.Outcome*` (Task 5); `p.session`, `p.applyClearance`, `p.requestUA`, `p.effectiveDomain` (Task 4).
- Produces: `BeginLogin` / `CompleteLogin` / `RefreshChallenge` on `*plugin`, satisfying `registry.WithInteractiveLogin`.

**Behaviour:** `BeginLogin` posts a plain credential login. RuTracker normally accepts it, in which case the engine harvests `bb_session` and no captcha ever reaches the user. When RuTracker demands a captcha the image is returned for the user to solve.

- [ ] **Step 1: Write the failing test**

```go
package rutracker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/captchalogin"
)

func TestClassifyLogin(t *testing.T) {
	tests := []struct {
		name string
		body string
		want captchalogin.Outcome
	}{
		{"success", `<span id="logged-in-username">bob</span>`, captchalogin.OutcomeSuccess},
		{"captcha demanded", `<input name="cap_sid" value="X">`, captchalogin.OutcomeNeedCaptcha},
		{
			"wrong captcha",
			`<h2>Введите код подтверждения</h2><input name="cap_sid" value="X">`,
			captchalogin.OutcomeNeedCaptcha,
		},
		{"bad credentials", `<h1>Вход</h1>no marker here`, captchalogin.OutcomeFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyLogin([]byte(tt.body)); got != tt.want {
				t.Errorf("classifyLogin() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseChallenge_ExtractsSidFieldAndImage(t *testing.T) {
	body := []byte(`<input type="hidden" name="cap_sid" value="SID123">` +
		`<img src="https://static.rutracker.cc/captcha/abc.jpg?9">` +
		`<input class="reg-input" type="text" name="cap_code_deadbeef" value="">`)
	spec, err := parseChallenge(body)
	if err != nil {
		t.Fatalf("parseChallenge: %v", err)
	}
	if spec.Fields.Get("cap_sid") != "SID123" {
		t.Errorf("cap_sid = %q", spec.Fields.Get("cap_sid"))
	}
	if spec.AnswerField != "cap_code_deadbeef" {
		t.Errorf("AnswerField = %q", spec.AnswerField)
	}
	if spec.ImageURL != "https://static.rutracker.cc/captcha/abc.jpg?9" {
		t.Errorf("ImageURL = %q", spec.ImageURL)
	}
}

func TestParseChallenge_MissingSid_Errors(t *testing.T) {
	if _, err := parseChallenge([]byte(`<html>no captcha here</html>`)); err == nil {
		t.Fatal("want an error when the page carries no captcha")
	}
}

func TestBeginLogin_NoCaptchaDemanded_ReturnsSessionDirectly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "bb_session", Value: "SESS", Path: "/forum/"})
		_, _ = w.Write([]byte(`<span id="logged-in-username">bob</span>`))
	}))
	defer srv.Close()

	p := newTestPlugin(t, srv)
	challenge, cookies, err := p.BeginLogin(context.Background(), &domain.TrackerCredential{
		UserID: uuid.New(), Username: "bob", SecretEnc: []byte("pw"),
	})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	if challenge != nil {
		t.Fatal("no captcha should be shown when the tracker did not ask for one")
	}
	if cookies["bb_session"] != "SESS" {
		t.Fatalf("cookies = %v, want bb_session=SESS", cookies)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  go test ./internal/plugins/trackers/rutracker/ -run 'TestClassifyLogin|TestParseChallenge|TestBeginLogin' -v
```

Expected: FAIL — `undefined: classifyLogin`, `parseChallenge`, `BeginLogin`.

- [ ] **Step 3: Implement**

```go
package rutracker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sync"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/captchalogin"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

var _ registry.WithInteractiveLogin = (*plugin)(nil)

var (
	capSidRe   = regexp.MustCompile(`name="cap_sid"[^>]*value="([^"]+)"`)
	capFieldRe = regexp.MustCompile(`name="(cap_code_[a-f0-9]+)"`)
	capImgRe   = regexp.MustCompile(`<img[^>]+src="(https://[^"]*captcha[^"]*)"`)
)

// classifyLogin maps a RuTracker login response to an Outcome.
//
// The captcha is adaptive: RuTracker imposes it on a client it distrusts (a run
// of failed attempts is enough) and drops it again after a success. So
// NeedCaptcha is a normal, transient state rather than a property of the site,
// and a plain credential login is the expected path.
//
// A wrong answer is reported as NeedCaptcha rather than WrongCaptcha because
// RuTracker re-renders the same form with a FRESH cap_sid — the previous
// pending challenge is dead either way, so the caller must be handed the new
// picture rather than retrying against the old one.
func classifyLogin(body []byte) captchalogin.Outcome {
	if bytes.Contains(body, []byte(`id="logged-in-username"`)) {
		return captchalogin.OutcomeSuccess
	}
	if bytes.Contains(body, []byte("cap_sid")) {
		return captchalogin.OutcomeNeedCaptcha
	}
	return captchalogin.OutcomeFailed
}

// parseChallenge extracts one captcha instance from a login response.
//
// All three parts are per-attempt: the image lives on static.rutracker.cc under
// a content hash, cap_sid pairs the answer to the image, and the answer field
// is named cap_code_<md5>. None can be hard-coded in the plugin config.
func parseChallenge(body []byte) (captchalogin.ChallengeSpec, error) {
	sid := capSidRe.FindSubmatch(body)
	field := capFieldRe.FindSubmatch(body)
	img := capImgRe.FindSubmatch(body)
	if sid == nil || field == nil || img == nil {
		return captchalogin.ChallengeSpec{},
			errors.New("rutracker: login page has no parseable captcha challenge")
	}
	return captchalogin.ChallengeSpec{
		ImageURL:    string(img[1]),
		Fields:      url.Values{"cap_sid": {string(sid[1])}},
		AnswerField: string(field[1]),
	}, nil
}

// captchaConfig is the RuTracker-specific interactive-login configuration.
// CaptchaURL is intentionally empty: ChallengeFrom supplies a per-attempt URL.
func (p *plugin) captchaConfig() captchalogin.Config {
	return captchalogin.Config{
		LoginURL:      "https://" + p.effectiveDomain() + "/forum/login.php",
		CookieNames:   []string{"bb_session"},
		ChallengeFrom: parseChallenge,
		Classify:      classifyLogin,
		BuildForm: func(c *domain.TrackerCredential, _ string, _ bool) url.Values {
			// The answer is placed by the engine under the per-challenge
			// AnswerField name, so it is deliberately absent here.
			return url.Values{
				"login_username": {c.Username},
				"login_password": {string(c.SecretEnc)},
				"login":          {"вход"},
				"redirect":       {"index.php"},
			}
		},
	}
}

// newInteractiveSession returns a FRESH, independent session on every call —
// the invariant captchalogin.Engine relies on so concurrent logins cannot
// cross-contaminate captcha cookies. Each one is seeded with the Cloudflare
// clearance, without which login.php answers 403 rather than a form.
func (p *plugin) newInteractiveSession() *forumcommon.Session {
	sess := forumcommon.New().GetOrCreate(
		forumcommon.SessionKey(pluginName, "interactive"), userAgent)
	if tr := p.effectiveTransport(); tr != nil {
		sess.Client.Transport = tr
	}
	u, err := url.Parse("https://" + p.effectiveDomain() + "/forum/")
	if err != nil {
		return sess
	}
	if c := p.applyClearance(context.Background(), sess, u); c.Valid() {
		sess.UserAgent = c.UserAgent
	}
	return sess
}

func (p *plugin) eng() *captchalogin.Engine {
	p.engineOnce.Do(func() {
		p.engine = captchalogin.New(p.captchaConfig(), p.newInteractiveSession)
	})
	return p.engine
}

// BeginLogin implements registry.WithInteractiveLogin. It returns a captcha
// only when RuTracker actually demands one; the common case returns the
// harvested session and the user never sees a picture.
func (p *plugin) BeginLogin(ctx context.Context, creds *domain.TrackerCredential) (*registry.LoginChallenge, registry.SessionCookies, error) {
	if creds == nil || creds.Username == "" {
		return nil, nil, fmt.Errorf("rutracker: credentials are required")
	}
	return p.eng().Begin(ctx, creds)
}

func (p *plugin) CompleteLogin(ctx context.Context, challengeID, answer string) (registry.SessionCookies, error) {
	return p.eng().Complete(ctx, challengeID, answer)
}

func (p *plugin) RefreshChallenge(ctx context.Context, challengeID string) (*registry.LoginChallenge, error) {
	return p.eng().Refresh(ctx, challengeID)
}
```

Add the two fields to `type plugin struct` in `rutracker.go`:

```go
	engineOnce sync.Once
	engine     *captchalogin.Engine
```

and import `sync`.

- [ ] **Step 4: Run tests to verify they pass**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  go test -race ./internal/plugins/trackers/rutracker/ -v
```

Expected: PASS.

- [ ] **Step 5: Verify the capability is surfaced to the frontend**

`GET /api/v1/system/info` reports `supports_interactive_login` per tracker by type-asserting `registry.WithInteractiveLogin`, so the RuTracker credential form picks up the captcha dialog automatically. Confirm:

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  go test -race ./internal/api/... -v
```

Expected: PASS. If a handler test pins the exact set of interactive trackers, update it to include `rutracker`.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/plugins/trackers/rutracker/
git commit -m "feat(rutracker): interactive captcha login for flagged clients

RuTracker imposes a login captcha on a client it distrusts and drops it
again after a success. Escalate to the shared captcha engine when the
tracker asks, so a flagged account can still re-authenticate in-app
instead of being stuck anonymous."
```

---

### Task 7: Correct the documentation the old premise left behind

**Files:**
- Modify: `backend/internal/flaresolverr/client.go:1-40` (package doc)
- Modify: `backend/internal/plugins/trackers/rutracker/rutracker.go:84-91` (`UsesCloudflare` doc)
- Modify: `CLAUDE.md` — the `flaresolverr` and `cfsolver` rows of the backend package table, and the `WithSearch` paragraph
- Modify: `docs/getting-started.md` — FlareSolverr setup note (add the same-egress-IP requirement)

**Why:** three files assert that `cf_clearance` is TLS-fingerprint-bound and cannot be replayed from Go. That claim is refuted; leaving it in place invites someone to reinstate the browser-proxy design.

- [ ] **Step 1: Rewrite the flaresolverr package doc**

Replace the `# Why a RoundTripper` and `# Scope: GET only, and why` sections with:

```go
// Package flaresolverr talks to a FlareSolverr instance on behalf of trackers
// that sit behind a Cloudflare managed challenge.
//
// # Clearance minting is the primary mode
//
// Cloudflare answers a challenged request with 403 and a Cf-Mitigated header;
// solving it yields a cf_clearance cookie. That cookie CAN be replayed from
// ordinary Go requests — measured against live RuTracker on 2026-08-01, a
// minted clearance returned 200 on every gated path (viewtopic, login.php,
// tracker.php and the binary dl.php) from a plain net/http client.
//
// The binding is to the User-Agent, not to the TLS fingerprint. Replaying the
// cookie with Marauder's own UA, an empty UA, or a different browser UA all
// return 403; replaying it with the UA FlareSolverr reports returns 200. An
// earlier revision of this package concluded the opposite and built the
// browser-proxy transport below as a result. That conclusion came from a test
// that replayed the cookie while sending the plugin's hardcoded
// "Marauder/0.3" UA, which cannot work.
//
// So Clearance/InvalidateClearance (minter.go) are what plugins should use: one
// solve per host, then ordinary Go requests. It is faster, it does not
// serialise, it carries binary bodies, and it permits a login.
//
// The clearance is also bound to the requesting IP, so the solver must egress
// from the same public address as Marauder — true for the bundled compose
// stacks, worth checking for a split deployment.
//
// # The RoundTripper is a legacy fallback
//
// RoundTrip fetches a page THROUGH the browser instead of dialling. It is GET
// only and returns the page as a JSON string, so binary bodies do not survive
// it. Nothing uses it today; it is retained for a tracker that turns out to
// need in-browser rendering rather than mere clearance.
```

- [ ] **Step 2: Rewrite the RuTracker `UsesCloudflare` doc**

```go
// UsesCloudflare implements registry.WithCloudflare. Verified against the live
// site on 2026-08-01: every /forum/ path answers 403 with Cf-Mitigated
// (/forum/index.php is the sole exception). Declaring this marks the plugin as
// needing a Cloudflare clearance, which fetchBytes mints via the configured
// provider and replays together with the browser User-Agent that earned it.
func (p *plugin) UsesCloudflare() bool { return true }
```

- [ ] **Step 3: Update CLAUDE.md**

Replace the `flaresolverr` row of the backend package table with:

```
| **`flaresolverr`** | client for a FlareSolverr instance, used as a **Cloudflare clearance minter**: `Clearance(ctx, host)` solves the managed challenge once and returns `cf_clearance` + the browser User-Agent, cached per host and installed process-wide via `registry.SetClearanceProvider`. Plugins replay both on ordinary Go requests — the cookie is bound to the **User-Agent** (and the egress IP), NOT to the TLS fingerprint, so replay from `net/http` works on every gated path including the binary `dl.php` (measured 2026-08-01). A stale clearance surfaces as a 403 `Cf-Mitigated`, which `rutracker.fetchBytes` answers by invalidating and re-minting exactly once. Holds one long-lived solver session (`sessions.create`, released by `Close`) because a session-less solve re-runs the challenge every call (10-20s) and FlareSolverr serialises. `Transport.RoundTrip` (fetch *through* the browser) is retained but unused: it is GET-only and string-typed, so it cannot carry a `.torrent` |
```

Amend the `WithSearch` paragraph — RuTracker search no longer depends on anonymous mode:

```
RuTracker's `nm=` query must be cp1251-percent-encoded —
`forumcommon.EncodeWindows1251Query` — and `tracker.php` is login-gated, so the
handler's Verify-first/Login-on-miss session warm-up is what makes it work; an
anonymous session gets the login shell and returns
`registry.ErrSearchRequiresCredentials`.
```

Add to the RuTracker notes near the `WithCredentials` discussion:

```
RuTracker's login captcha is **adaptive** — imposed on a client it distrusts
(a run of failed attempts is enough) and dropped again after a success. So
`Login` posts credentials normally and only returns `registry.ErrCaptchaRequired`
when the response carries `cap_sid`; the interactive flow
(`WithInteractiveLogin`, backed by `captchalogin` with a per-challenge
`ChallengeFrom`) exists for that case. `bb_session` is path-scoped to `/forum/`,
so harvesting and rehydrating it must use that path, not the origin root.
```

- [ ] **Step 4: Verify docs build/lint and nothing else broke**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  sh -c "gofmt -l . | tee /tmp/f && test -z \"\$(cat /tmp/f)\" && go build ./... && go vet ./... && go test -race ./..."
```

Expected: no gofmt output, all tests pass.

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md docs/getting-started.md backend/internal/flaresolverr/client.go \
  backend/internal/plugins/trackers/rutracker/rutracker.go
git commit -m "docs: correct the cf_clearance binding claim

cf_clearance is bound to the User-Agent and the egress IP, not to the TLS
fingerprint. The previous claim ruled out replaying it from Go and drove
the browser-proxy design that left RuTracker anonymous."
```

---

### Task 8: Live end-to-end verification against the dev stack

**Files:** none (verification only)

- [ ] **Step 1: Rebuild and restart the backend**

```bash
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml \
  up -d --no-deps --build backend
docker restart deploy-gateway-1
```

(`--no-deps` so postgres is not recreated; the gateway restart refreshes nginx's cached upstream IP.)

- [ ] **Step 2: Confirm the clearance provider came up**

```bash
docker logs deploy-backend-1 2>&1 | grep -i clearance
```

Expected: `flaresolverr clearance provider enabled`.

- [ ] **Step 3: Re-add the RuTracker account through the UI**

Open http://localhost:34080 → Accounts → RuTracker. The existing credential predates the fix; saving it again exercises `loginAndVerify`. Expected: saves without a "requires login" error. If a captcha appears, solve it — that is the interactive path working.

- [ ] **Step 4: Run a tracker search**

Topics → Add topic → Search mode → query `матрица`, tracker RuTracker.

Expected: results render. Specifically NOT `RuTracker.org: needs a tracker account — add one under Accounts`.

- [ ] **Step 5: Confirm an authenticated download**

Force a check on the existing RuTracker topic and inspect the delivery:

```bash
docker logs deploy-backend-1 2>&1 | grep -iE "rutracker|dl\.php" | tail -20
```

Expected: no `dl.php fetch failed; falling back to hash-only page magnet` warning. The qBittorrent WebUI (http://localhost:34611) should show the torrent with a real tracker announce rather than sitting on "Downloading metadata".

- [ ] **Step 6: Confirm minting is rare**

```bash
curl -s http://localhost:34081/metrics | grep marauder_flaresolverr_clearance_total
```

Expected: `result="minted"` in the low single digits with `result="cached"` climbing. A rising `minted` rate means the clearance is being rejected — check that the browser UA is being replayed and that the solver shares the backend's egress IP.

- [ ] **Step 7: Commit any fixes found, then update the changelog**

Add to `CHANGELOG.md` under `[Unreleased]`:

```markdown
### Fixed
- RuTracker: restored authenticated operation. Tracker search works again, and
  downloads deliver a real `.torrent` with an announce URL instead of a
  hash-only magnet. FlareSolverr is now used to mint a Cloudflare clearance that
  Marauder replays directly, rather than proxying every fetch through a browser.

### Added
- RuTracker: interactive captcha login, used when the tracker demands a captcha
  after distrusting a client.
```

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): record RuTracker authentication restoration"
```

---

## Self-Review

**Spec coverage**

| Finding from the investigation | Task |
|---|---|
| `cf_clearance` replayable from Go | 1, 2 |
| UA binding is strict | 4 (`requestUA`) |
| Host binding | 2 (per-host cache), 4 (invalidate by `effectiveDomain`) |
| Clearance required on every request | 4 (`applyClearance` in the shared fetch path) |
| `bb_session` path-scoped to `/forum/` | 4 (`restoreSession`), 6 (`CookieNames` harvest) |
| Login skip is the direct cause of the search failure | 4 |
| `Verify` never set the transport | 4 |
| Captcha is adaptive | 4 (`ErrCaptchaRequired`), 6 (`classifyLogin`) |
| Per-challenge captcha URL / `cap_sid` / `cap_code_<md5>` | 5, 6 |
| Binary `.torrent` works once authenticated | 8 step 5 |
| Egress-IP constraint | 7 (docs), 8 step 6 (metric) |
| Existing `search.go` regexes parse live markup | no change needed — verified 50/50 rows on 2026-08-01 |

**Placeholder scan:** none — every step carries runnable code or an exact command.

**Type consistency:** `Clearance{Cookies, UserAgent}` and `Valid()` are used identically in Tasks 1, 2, 4, 6. `ChallengeSpec{ImageURL, Fields, AnswerField}` is defined in Task 5 and consumed in Task 6 with matching field names. `parseChallenge` matches the `Config.ChallengeFrom func(body []byte) (ChallengeSpec, error)` signature. `p.session` / `p.applyClearance` / `p.requestUA` are introduced in Task 4 and used in Task 6.

**Known risk carried forward:** the plan removes RuTracker's use of `registry.ChallengeTransport` but leaves `challenge.go` and `Transport.RoundTrip` in the tree with no caller. That is deliberate (documented in Task 7) rather than an oversight; deleting them is a separate, larger change.
