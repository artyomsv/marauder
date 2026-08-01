package flaresolverr

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
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
// solution. Tracker session cookies belong to the plugin that earned them, not
// here — a shared minter hoarding them would leak one user's session into
// every other user's requests.
const clearanceCookieName = "cf_clearance"

// clearanceEntry is the cached clearance for one host plus the gate that keeps
// concurrent callers from all solving at once.
//
// sem is a one-slot semaphore rather than a sync.Mutex because a mutex cannot
// be cancelled: a waiter would block past its own deadline. That is not
// hypothetical — a boot-time warm-up solve took 58s while the scheduler's check
// sat on the lock and failed 0.4ms after the solve finished, its context long
// expired. The transport's ensureSession already avoids a mutex for exactly
// this reason; this is the same hazard one layer up.
//
// value is guarded by valueMu, which is only ever held for a field read or
// write — never across a network call.
type clearanceEntry struct {
	sem     chan struct{}
	valueMu sync.Mutex
	value   registry.Clearance
}

func (e *clearanceEntry) cached() registry.Clearance {
	e.valueMu.Lock()
	defer e.valueMu.Unlock()
	return e.value
}

func (e *clearanceEntry) store(c registry.Clearance) {
	e.valueMu.Lock()
	e.value = c
	e.valueMu.Unlock()
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
		e = &clearanceEntry{sem: make(chan struct{}, 1)}
		t.clearances[host] = e
	}
	return e
}

// cacheKey derives the per-host cache key from a probe URL.
func cacheKey(probeURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(probeURL))
	if err != nil {
		return "", fmt.Errorf("flaresolverr: invalid probe URL %q: %w", probeURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return "", fmt.Errorf("flaresolverr: probe URL must be absolute http(s), got %q", probeURL)
	}
	return u.Host, nil
}

// Clearance implements registry.ClearanceProvider.
//
// Single-flight is per host rather than global: a burst of topic checks on one
// tracker must solve once, but a second tracker must not queue behind it.
func (t *Transport) Clearance(ctx context.Context, probeURL string) (registry.Clearance, error) {
	if t == nil || t.URL == "" {
		return registry.Clearance{}, ErrDisabled
	}
	host, err := cacheKey(probeURL)
	if err != nil {
		return registry.Clearance{}, err
	}
	e := t.clearanceFor(host)

	if c := e.cached(); c.Valid() {
		metrics.FlareSolverrClearanceTotal.WithLabelValues("cached").Inc()
		return c, nil
	}

	// Take the solve slot in a way the caller can abandon. Waiting here must
	// remain interruptible: a solve can take a minute, and every other caller
	// (a scheduler check, a tracker search) has a far shorter budget of its own.
	select {
	case e.sem <- struct{}{}:
		defer func() { <-e.sem }()
	case <-ctx.Done():
		return registry.Clearance{}, ctx.Err()
	}

	// Re-check: whoever held the slot may have just minted one for us.
	if c := e.cached(); c.Valid() {
		metrics.FlareSolverrClearanceTotal.WithLabelValues("cached").Inc()
		return c, nil
	}

	c, err := t.mint(ctx, probeURL)
	if err != nil {
		metrics.FlareSolverrClearanceTotal.WithLabelValues("error").Inc()
		return registry.Clearance{}, err
	}
	e.store(c)
	metrics.FlareSolverrClearanceTotal.WithLabelValues("minted").Inc()
	return c, nil
}

// InvalidateClearance implements registry.ClearanceProvider.
func (t *Transport) InvalidateClearance(probeURL string) {
	if t == nil || t.URL == "" {
		return
	}
	host, err := cacheKey(probeURL)
	if err != nil {
		return
	}
	t.clearanceFor(host).store(registry.Clearance{})
}

// mint drives one real solve against probeURL.
//
// It solves the caller's URL verbatim rather than deriving a host root,
// because a clearance is scoped to the Cloudflare rule that issued it. Solving
// RuTracker's root returns a cf_clearance — the root 301s to the unchallenged
// /forum/index.php — but that cookie is still rejected with 403 on
// /forum/login.php (measured 2026-08-01). Only a genuinely challenged URL
// yields a clearance that works.
func (t *Transport) mint(ctx context.Context, probeURL string) (registry.Clearance, error) {
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
		URL:        probeURL,
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
		return registry.Clearance{}, fmt.Errorf("%w (%s)", ErrNoClearance, probeURL)
	}
	return out, nil
}
