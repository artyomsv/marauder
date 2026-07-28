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

	payload, err := json.Marshal(solveRequest{
		Cmd:        "request.get",
		URL:        req.URL.String(),
		MaxTimeout: int(t.Timeout / time.Millisecond),
	})
	if err != nil {
		return nil, fmt.Errorf("flaresolverr: encode request: %w", err)
	}

	ctx := req.Context()
	if ctx == nil {
		ctx = context.Background()
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
