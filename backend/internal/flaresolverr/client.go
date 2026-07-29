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

// Transport issues requests through a FlareSolverr instance.
type Transport struct {
	// URL is the FlareSolverr root, e.g. "http://flaresolverr:8191".
	URL string
	// Timeout bounds the whole exchange, including FlareSolverr's own solve.
	Timeout time.Duration
	// HTTP talks to FlareSolverr itself (not to the tracker).
	HTTP *http.Client

	// mu guards session. It is deliberately held across the sessions.create
	// round-trip so concurrent first requests produce exactly one session
	// rather than one browser each. That serialisation costs nothing real:
	// FlareSolverr processes requests one at a time regardless.
	mu      sync.Mutex
	session string
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
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.session != "" {
		return t.session, nil
	}
	var sr sessionResponse
	// Let FlareSolverr name the session: several Marauder instances may share
	// one solver, and a fixed name would have them fighting over it.
	if err := t.command(ctx, sessionRequest{Cmd: "sessions.create"}, &sr); err != nil {
		return "", err
	}
	if sr.Status != "ok" || sr.Session == "" {
		return "", fmt.Errorf("flaresolverr: sessions.create: %s", sr.Message)
	}
	t.session = sr.Session
	return t.session, nil
}

// dropSession forgets name, but only if it is still the active one — otherwise
// it would discard a replacement another goroutine has already installed.
func (t *Transport) dropSession(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.session == name {
		t.session = ""
	}
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
	t.mu.Unlock()
	if name == "" {
		return nil
	}
	var sr sessionResponse
	return t.command(ctx, sessionRequest{Cmd: "sessions.destroy", Session: name}, &sr)
}

// isSessionGone reports whether err looks like FlareSolverr rejecting our
// session rather than failing the fetch on its merits. Matching on the word is
// deliberately loose: the exact wording varies across FlareSolverr versions,
// and the cost of a false positive is one extra session creation.
func isSessionGone(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "session")
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
// The configured timeout remains the ceiling; the deadline only ever
// tightens it.
func (t *Transport) maxTimeoutMillis(ctx context.Context) int {
	budget := t.Timeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < budget {
			budget = remaining
		}
	}
	if budget < minSolveBudget {
		budget = minSolveBudget
	}
	return int(budget / time.Millisecond)
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

	ctx := req.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// A session lets FlareSolverr reuse an already-cleared browser context
	// instead of re-solving the challenge every call. If a session cannot be
	// obtained we fetch without one: strictly slower, but still correct, so a
	// FlareSolverr that has sessions disabled or is at capacity degrades
	// rather than breaking every check.
	session, _ := t.ensureSession(ctx)

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
	payload, err := json.Marshal(solveRequest{
		Cmd:        "request.get",
		URL:        req.URL.String(),
		MaxTimeout: t.maxTimeoutMillis(ctx),
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
