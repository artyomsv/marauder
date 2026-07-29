// Package flaresolverr provides an http.RoundTripper that fetches pages
// through a FlareSolverr instance instead of dialling the tracker directly.
//
// # Why a RoundTripper
//
// Cloudflare binds its clearance cookie to the TLS fingerprint of the client
// that earned it. Verified against live RuTracker on 2026-07-28: a valid
// cf_clearance minted by a real browser is still rejected with 403 when
// replayed from Go, even from the same IP with the same User-Agent and a full
// set of browser headers. So the "solve once, hand the cookie to Go" model
// cannot work — the request itself has to be issued by a browser.
//
// Expressing that as a RoundTripper means the browser hop is invisible to
// callers: a plugin keeps using its ordinary *http.Client, and only the
// Transport changes. No call site has to know.
//
// # Scope: GET only, and why
//
// This transport implements GET only. That is a deliberate scope decision,
// NOT a FlareSolverr limitation — verified against 3.5.0 on 2026-07-28,
// `request.post` submits form-urlencoded bodies correctly and
// `sessions.create` keeps cookies alive across calls, so a login POST is
// entirely possible.
//
// It is not implemented because it would buy nothing yet. The only thing a
// RuTracker login unlocks is dl.php, which serves a binary .torrent — and
// binary is where FlareSolverr genuinely stops: it returns the page as a JSON
// string, so Chrome renders the .torrent as HTML and the bytes do not
// survive (tested against a known-good public .torrent: the response came
// back wrapped in <html><body>, with no bencode).
//
// So the sequence login -> dl.php cannot complete regardless of POST support.
// Meanwhile the topic page is public and carries a magnet with an announce
// URL, and Download already prefers that fallback. Anonymous operation is
// therefore complete for RuTracker today.
//
// If binary transport is ever solved (a FlareSolverr /download endpoint, or
// an in-page fetch returning base64), adding POST here is the natural next
// step and the reason above no longer applies.
package flaresolverr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/artyomsv/marauder/backend/internal/metrics"
)

// ErrDisabled is returned when no FlareSolverr URL is configured. Callers can
// therefore install the transport unconditionally and let the error surface
// only if something actually tries to use it.
var ErrDisabled = errors.New("flaresolverr is not configured")

// ErrMethodUnsupported is returned for any method other than GET — a scope
// limit of this transport, not of FlareSolverr (see the package doc). Failing
// loudly matters: silently turning a login POST into a GET would look like a
// successful request that never submitted the form.
var ErrMethodUnsupported = errors.New("flaresolverr transport supports GET only")

// ErrClosed is returned once Close has run: the transport will not create
// another session, so a request arriving during shutdown fails instead of
// leaving an orphaned browser behind.
var ErrClosed = errors.New("flaresolverr transport is closed")

// ErrCreateCoolingDown is returned while a recent sessions.create failure is
// still being negative-cached, so a solver that cannot make sessions is asked
// once per cooldown rather than once per request.
var ErrCreateCoolingDown = errors.New("flaresolverr session creation is cooling down")

// ErrBudgetTooShort is returned when the caller's remaining deadline is below
// what a solve could possibly use. Refusing beats dialling: FlareSolverr would
// keep driving a browser for the full budget we asked for after we had already
// abandoned the request, and because it serialises, that abandoned browser
// blocks the next topic's fetch — the exact queue amplification behind the
// 2026-07-30 outage.
var ErrBudgetTooShort = errors.New("flaresolverr: remaining deadline too short to attempt a solve")

const (
	// createFailureCooldown is how long a failed sessions.create suppresses
	// further attempts.
	createFailureCooldown = 30 * time.Second
	// sessionDestroyTimeout bounds the detached best-effort destroy of an
	// abandoned session.
	sessionDestroyTimeout = 10 * time.Second
)

// Transport issues requests through a FlareSolverr instance.
type Transport struct {
	// URL is the FlareSolverr root, e.g. "http://flaresolverr:8191".
	URL string
	// Timeout bounds the whole exchange, including FlareSolverr's own solve.
	Timeout time.Duration
	// HTTP talks to FlareSolverr itself (not to the tracker).
	HTTP *http.Client

	// OnDegraded, when set, is called whenever a fetch has to fall back to
	// session-less mode. That fallback silently reinstates the per-request
	// challenge re-solve which caused the 2026-07-30 outage, so it must be
	// observable rather than invisible; the package has no logger of its own.
	OnDegraded func(error)

	// mu guards the fields below. It is NEVER held across a network call:
	// sync.Mutex.Lock cannot be cancelled, so a waiter could not honour its
	// own deadline, and the scheduler runs ONE bounded worker pool shared by
	// every tracker — a sick solver would park workers belonging to trackers
	// that never touch it. Single-flight is provided by sem instead, which
	// can be selected against ctx.Done().
	mu      sync.Mutex
	session string
	closed  bool
	// nextCreateAttempt negative-caches a failed sessions.create so a solver
	// that cannot make sessions is asked once per cooldown instead of once
	// per request.
	nextCreateAttempt time.Time

	// sem is a one-slot semaphore serialising session creation.
	sem chan struct{}
	// now is a clock seam for the negative-cache cooldown.
	now func() time.Time

	// The session is shared by every user and topic. That is safe ONLY
	// because Login is skipped whenever this transport is active, so no
	// per-user credential or auth cookie ever enters the shared browser. If
	// request.post/login is ever added (see the package doc), the session
	// must become per-credential — otherwise one user's tracker cookies
	// would ride along on every other user's fetches.
	_ struct{}
}

var _ http.RoundTripper = (*Transport)(nil)

// New constructs a Transport. An empty url yields a transport whose
// RoundTrip always fails with ErrDisabled.
func New(url string, timeout time.Duration) *Transport {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Transport{
		URL:     strings.TrimRight(url, "/"),
		Timeout: timeout,
		// The outer client's deadline is deliberately looser than the solve
		// budget so FlareSolverr gets the chance to answer "not solved"
		// rather than being cut off mid-challenge.
		HTTP: &http.Client{Timeout: timeout + 15*time.Second},
		sem:  make(chan struct{}, 1),
		now:  time.Now,
	}
}

type solveRequest struct {
	Cmd        string `json:"cmd"`
	URL        string `json:"url"`
	MaxTimeout int    `json:"maxTimeout"`
	Session    string `json:"session,omitempty"`
}

// sessionRequest drives the sessions.* commands, which take no URL.
type sessionRequest struct {
	Cmd     string `json:"cmd"`
	Session string `json:"session,omitempty"`
}

// ensureSession returns the shared session name, creating one on first use.
//
// This is the fix for the root cause of the 2026-07-30 RuTracker outage.
// Without a session FlareSolverr spins a fresh browser and re-solves the
// Cloudflare challenge for every request — measured at 10-20s against
// RuTracker — and because it serialises requests, several topics checking at
// once queue past the scheduler's budget and all fail. Solving once and
// reusing the cleared context removes both the per-request cost and the queue.
func (t *Transport) ensureSession(ctx context.Context) (string, error) {
	if s, err, ready := t.cachedSession(); ready {
		return s, err
	}

	// Acquire the creation slot in a way the caller can abandon. A plain
	// mutex here would block a waiter past its own deadline (measured at
	// ~1.2s over a 200ms budget) and, because the scheduler's worker pool is
	// shared by every tracker, would delay checks for trackers that do not
	// use the solver at all.
	select {
	case t.sem <- struct{}{}:
		defer func() { <-t.sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}

	// Re-check: the goroutine that held the slot may have just succeeded, or
	// recorded a failure we should now honour rather than repeat.
	if s, err, ready := t.cachedSession(); ready {
		return s, err
	}

	var sr sessionResponse
	// FlareSolverr names the session: several Marauder instances may share one
	// solver, and a fixed name would have them fighting over it. The
	// trade-off is that a SIGKILLed Marauder leaves a session nobody can
	// identify at boot; a "marauder-<uuid>" scheme would keep both properties
	// and allow a future sessions.list sweep.
	err := t.command(ctx, sessionRequest{Cmd: "sessions.create"}, &sr)
	if err == nil && sr.Session == "" {
		err = fmt.Errorf("flaresolverr: sessions.create returned no session: %s", sr.Message)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if err != nil {
		t.nextCreateAttempt = t.now().Add(createFailureCooldown)
		metrics.FlareSolverrSessionsTotal.WithLabelValues("error").Inc()
		return "", err
	}
	t.session = sr.Session
	t.nextCreateAttempt = time.Time{}
	metrics.FlareSolverrSessionsTotal.WithLabelValues("created").Inc()
	return t.session, nil
}

// cachedSession reports the session state without any network call. ready is
// false only when the caller should attempt a creation itself.
func (t *Transport) cachedSession() (session string, err error, ready bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.session != "" {
		return t.session, nil, true
	}
	// After Close, never mint another session. Shutdown does not join the
	// scheduler's workers, so a worker can still be mid-check when Close
	// runs; its request fails with a session error and the retry path would
	// otherwise create a replacement that nothing is left to destroy — the
	// very orphan Close exists to prevent. Failing an in-flight check during
	// shutdown is acceptable; leaking a browser is not.
	if t.closed {
		return "", ErrClosed, true
	}
	if t.now().Before(t.nextCreateAttempt) {
		return "", ErrCreateCoolingDown, true
	}
	return "", nil, false
}

// dropSession forgets name, but only if it is still the active one — otherwise
// it would discard a replacement another goroutine has already installed.
//
// It also asks the solver to destroy the abandoned session. FlareSolverr holds
// one Chrome per session and does not expire them unless SESSION_TTL_MINUTES
// is configured, so merely forgetting a name strands ~300 MB until the
// container restarts. The destroy is best-effort and detached: it legitimately
// fails when the session really is gone, and it must not inherit a request
// context that is already cancelled.
func (t *Transport) dropSession(name string) {
	t.mu.Lock()
	stale := t.session == name
	if stale {
		t.session = ""
	}
	t.mu.Unlock()
	if !stale {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), sessionDestroyTimeout)
		defer cancel()
		var sr sessionResponse
		_ = t.command(ctx, sessionRequest{Cmd: "sessions.destroy", Session: name}, &sr)
		metrics.FlareSolverrSessionsTotal.WithLabelValues("replaced").Inc()
	}()
}

// Close destroys the shared session so a restart does not leave an orphaned
// browser behind on the solver. Safe to call on an unconfigured transport.
func (t *Transport) Close(ctx context.Context) error {
	if t == nil || t.URL == "" {
		return nil
	}
	t.mu.Lock()
	name := t.session
	t.session = ""
	t.closed = true
	t.mu.Unlock()
	if name == "" {
		return nil
	}
	var sr sessionResponse
	return t.command(ctx, sessionRequest{Cmd: "sessions.destroy", Session: name}, &sr)
}

// isSessionGone reports whether err says our session is ABSENT — the one state
// a replacement actually fixes.
//
// It deliberately requires absence wording rather than the bare word
// "session". A capacity message ("browser session limit reached") or "Session
// already exists" names a session that is alive; discarding it there destroys
// a working browser, and if the follow-up create fails for the same reason the
// transport is left session-less, reinstating the per-request re-solve storm
// this whole change exists to remove.
func isSessionGone(err error) bool {
	if err == nil {
		return false
	}
	m := strings.ToLower(err.Error())
	if !strings.Contains(m, "session") {
		return false
	}
	for _, absent := range []string{"not exist", "does not exist", "not found", "unknown", "invalid", "expired"} {
		if strings.Contains(m, absent) {
			return true
		}
	}
	return false
}

// command performs a sessions.* round-trip and decodes the envelope.
func (t *Transport) command(ctx context.Context, body sessionRequest, out *sessionResponse) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("flaresolverr: encode %s: %w", body.Cmd, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL+"/v1", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("flaresolverr: build %s: %w", body.Cmd, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("flaresolverr: %s: %w", body.Cmd, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("flaresolverr: %s status %d", body.Cmd, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("flaresolverr: decode %s: %w", body.Cmd, err)
	}
	// FlareSolverr answers HTTP 200 even for a command that failed, reporting
	// the real outcome in the envelope. Checking it here rather than at each
	// call site means no caller can forget: Close previously reported
	// successful cleanup on an envelope error, silently leaving the browser
	// session alive with no warning to explain the leak.
	if out.Status != "ok" {
		return fmt.Errorf("flaresolverr: %s: %s", body.Cmd, out.Message)
	}
	return nil
}

type sessionResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Session string `json:"session"`
}

type solution struct {
	Status    int    `json:"status"`
	Response  string `json:"response"`
	UserAgent string `json:"userAgent"`
}

type solveResponse struct {
	Status   string   `json:"status"`
	Message  string   `json:"message"`
	Solution solution `json:"solution"`
}

// minSolveBudget is the floor for maxTimeout. Below roughly this, a solve
// cannot finish anyway, and sending a near-zero budget would make
// FlareSolverr fail instantly with a confusing message rather than letting
// the caller's own deadline surface.
const minSolveBudget = 5 * time.Second

// maxTimeoutMillis is the solve budget handed to FlareSolverr, in
// milliseconds.
//
// It tracks the caller's remaining deadline rather than the configured
// timeout, because callers routinely allow far less: the scheduler's checkCtx
// is TrackerHTTPTimeout+5s (35s by default) and each download iteration gets
// only TrackerHTTPTimeout (30s). Sending the configured value regardless
// meant the caller always cancelled first — leaving FlareSolverr driving a
// browser for a request nobody was waiting on, and surfacing a context
// cancellation instead of a real "challenge not solved" answer.
//
// The configured timeout is the ceiling and the deadline only tightens it. If
// what remains is below minSolveBudget the caller gets ErrBudgetTooShort
// instead of a floored budget: telling the solver "you have 5s" when we will
// abandon in 200ms leaves a browser running for a request nobody awaits, and
// FlareSolverr serialises, so that browser blocks the next topic.
func (t *Transport) solveBudget(ctx context.Context) (time.Duration, error) {
	budget := t.Timeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < budget {
			budget = remaining
		}
	}
	// A configured timeout below the floor is the operator's explicit choice,
	// so honour it rather than silently exceeding the documented ceiling.
	floor := minSolveBudget
	if t.Timeout < floor {
		floor = t.Timeout
	}
	if budget < floor {
		return 0, fmt.Errorf("%w (%s left, need %s)", ErrBudgetTooShort, budget.Round(time.Millisecond), floor)
	}
	return budget, nil
}

// RoundTrip fetches req.URL through FlareSolverr and rebuilds the result as a
// normal *http.Response.
//
// A tracker-level status (404, 403, …) is returned as a response, because it
// is a real HTTP outcome the caller branches on. Only a FlareSolverr failure —
// an unsolved challenge, a crashed browser — is a transport error.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t == nil || t.URL == "" {
		return nil, ErrDisabled
	}
	if req.Method != http.MethodGet {
		return nil, fmt.Errorf("%w (got %s %s)", ErrMethodUnsupported, req.Method, req.URL)
	}

	// The URL is handed to a real browser inside the solver container, from a
	// network position Marauder may not itself hold. Today's only consumer
	// validates hosts, but this transport is installed process-wide and is now
	// stateful, so guard here too: a future plugin that forgets its own check
	// must not be able to make the browser read local files or cloud metadata.
	if req.URL == nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") || req.URL.Host == "" {
		return nil, fmt.Errorf("flaresolverr: refusing to fetch %q: only absolute http(s) URLs are allowed", req.URL)
	}

	ctx := req.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// A session lets FlareSolverr reuse an already-cleared browser context
	// instead of re-solving the challenge every call. If one cannot be
	// obtained we fetch without it: strictly slower, but still correct, so a
	// solver with sessions disabled or at capacity degrades rather than
	// breaking every check.
	//
	// That degradation reinstates the per-request re-solve behind the
	// 2026-07-30 outage, so it is reported rather than swallowed — the package
	// has no logger, hence the hook.
	session, serr := t.ensureSession(ctx)
	if serr != nil {
		metrics.FlareSolverrSessionsTotal.WithLabelValues("degraded").Inc()
		if t.OnDegraded != nil {
			t.OnDegraded(serr)
		}
	}

	out, err := t.fetch(ctx, req, session)
	// A session FlareSolverr no longer recognises — it restarted, or the
	// session aged out — is recoverable exactly once: drop it, make a new one
	// and repeat. Without this, one FlareSolverr restart would wedge every
	// challenge-gated tracker until Marauder itself restarted.
	if err != nil && session != "" && isSessionGone(err) {
		t.dropSession(session)
		if fresh, serr := t.ensureSession(ctx); serr == nil {
			return t.fetch(ctx, req, fresh)
		}
	}
	return out, err
}

// fetch performs one request.get against FlareSolverr. An empty session omits
// the field, which makes FlareSolverr use a throwaway browser.
func (t *Transport) fetch(ctx context.Context, req *http.Request, session string) (*http.Response, error) {
	budget, err := t.solveBudget(ctx)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(solveRequest{
		Cmd:        "request.get",
		URL:        req.URL.String(),
		MaxTimeout: int(budget / time.Millisecond),
		Session:    session,
	})
	if err != nil {
		return nil, fmt.Errorf("flaresolverr: encode request: %w", err)
	}
	solveReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL+"/v1", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("flaresolverr: build request: %w", err)
	}
	solveReq.Header.Set("Content-Type", "application/json")

	resp, err := t.HTTP.Do(solveReq)
	if err != nil {
		return nil, fmt.Errorf("flaresolverr: call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("flaresolverr: status %d", resp.StatusCode)
	}

	var sr solveResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("flaresolverr: decode: %w", err)
	}
	if sr.Status != "ok" {
		return nil, fmt.Errorf("flaresolverr: %s", sr.Message)
	}

	body := []byte(sr.Solution.Response)
	out := &http.Response{
		StatusCode:    sr.Solution.Status,
		Status:        fmt.Sprintf("%d %s", sr.Solution.Status, http.StatusText(sr.Solution.Status)),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
	return out, nil
}
