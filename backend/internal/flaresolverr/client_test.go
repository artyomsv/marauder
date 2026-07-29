package flaresolverr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSolver stands in for a FlareSolverr instance, recording the request it
// was handed so tests can assert on the wire format.
type fakeSolver struct {
	srv      *httptest.Server
	mu       sync.Mutex
	lastBody map[string]any
	bodies   []map[string]any
}

// cmdCount returns how many requests carried the given cmd.
func (f *fakeSolver) cmdCount(cmd string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cmdCountLocked(cmd)
}

// cmdCountLocked is cmdCount for callers already holding f.mu.
func (f *fakeSolver) cmdCountLocked(cmd string) int {
	n := 0
	for _, b := range f.bodies {
		if c, _ := b["cmd"].(string); c == cmd {
			n++
		}
	}
	return n
}

// sessionsUsed returns the distinct non-empty session values seen on
// request.get calls.
func (f *fakeSolver) sessionsUsed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for _, b := range f.bodies {
		if c, _ := b["cmd"].(string); c != "request.get" {
			continue
		}
		s, _ := b["session"].(string)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func newFakeSolver(t *testing.T, respond func(w http.ResponseWriter, cmd, url string)) *fakeSolver {
	t.Helper()
	f := &fakeSolver{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		cmd, _ := body["cmd"].(string)
		target, _ := body["url"].(string)
		f.mu.Lock()
		f.lastBody = body
		f.bodies = append(f.bodies, body)
		nth := f.cmdCountLocked("sessions.create")
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		// Sessions are handled here so individual tests only describe the
		// request.get behaviour they care about.
		if cmd == "sessions.create" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":  "ok",
				"message": "Session created successfully.",
				"session": fmt.Sprintf("sess-%d", nth),
			})
			return
		}
		if cmd == "sessions.destroy" {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "message": "The session has been removed."})
			return
		}
		respond(w, cmd, target)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// okSolution writes the success envelope FlareSolverr returns once it has
// cleared the challenge.
func okSolution(w http.ResponseWriter, status int, html string) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"message": "Challenge solved!",
		"solution": map[string]any{
			"status":    status,
			"response":  html,
			"userAgent": "Mozilla/5.0 (X11; Linux x86_64) Chrome/148.0.0.0",
		},
	})
}

func TestRoundTrip_ReturnsSolvedPage(t *testing.T) {
	f := newFakeSolver(t, func(w http.ResponseWriter, _, _ string) {
		okSolution(w, 200, `<html><title>RuTracker.org</title></html>`)
	})
	rt := New(f.srv.URL, 30*time.Second)

	req, _ := http.NewRequest(http.MethodGet, "https://rutracker.org/forum/viewtopic.php?t=1", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "RuTracker.org") {
		t.Errorf("body = %q, want the solved page HTML", string(body))
	}
	// The caller must be able to inspect the response as if it had made the
	// request itself.
	if resp.Request == nil || resp.Request.URL.String() != req.URL.String() {
		t.Errorf("Response.Request should point back at the original request")
	}
}

func TestRoundTrip_SendsRequestGetForTheTargetURL(t *testing.T) {
	f := newFakeSolver(t, func(w http.ResponseWriter, _, _ string) {
		okSolution(w, 200, "<html></html>")
	})
	rt := New(f.srv.URL, 30*time.Second)

	const target = "https://rutracker.org/forum/index.php"
	req, _ := http.NewRequest(http.MethodGet, target, nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()

	if got := f.lastBody["cmd"]; got != "request.get" {
		t.Errorf("cmd = %v, want request.get", got)
	}
	if got := f.lastBody["url"]; got != target {
		t.Errorf("url = %v, want %q", got, target)
	}
	if _, ok := f.lastBody["maxTimeout"]; !ok {
		t.Error("maxTimeout must be sent so FlareSolverr bounds its own solve")
	}
}

// The scheduler gives a check far less time than this transport's own
// timeout: checkCtx is TrackerHTTPTimeout+5s (35s by default) and a download
// iteration only gets TrackerHTTPTimeout (30s). Sending the configured 60s
// regardless means the caller cancels first, so FlareSolverr keeps driving a
// browser nobody is waiting for and the operator sees a context cancellation
// instead of a real answer. maxTimeout must therefore track whatever deadline
// the caller actually granted.
func TestRoundTrip_MaxTimeoutRespectsCallerDeadline(t *testing.T) {
	f := newFakeSolver(t, func(w http.ResponseWriter, _, _ string) {
		okSolution(w, 200, "<html></html>")
	})
	rt := New(f.srv.URL, 60*time.Second) // configured far higher than the caller allows

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://rutracker.org/forum/index.php", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()

	got, _ := f.lastBody["maxTimeout"].(float64)
	if got <= 0 || got > 10_000 {
		t.Errorf("maxTimeout = %v ms, want it bounded by the caller's 10s deadline", got)
	}
}

// With no deadline on the request the configured timeout is the bound.
func TestRoundTrip_MaxTimeoutFallsBackToConfigured(t *testing.T) {
	f := newFakeSolver(t, func(w http.ResponseWriter, _, _ string) {
		okSolution(w, 200, "<html></html>")
	})
	rt := New(f.srv.URL, 45*time.Second)

	req, _ := http.NewRequest(http.MethodGet, "https://rutracker.org/forum/index.php", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()

	if got, _ := f.lastBody["maxTimeout"].(float64); got != 45_000 {
		t.Errorf("maxTimeout = %v ms, want the configured 45000", got)
	}
}

// A tracker-level failure (404, 403) is a legitimate HTTP outcome and must be
// handed back as a response, not swallowed into a transport error — callers
// like the rutracker plugin branch on the status code.
func TestRoundTrip_PropagatesNon200TrackerStatus(t *testing.T) {
	f := newFakeSolver(t, func(w http.ResponseWriter, _, _ string) {
		okSolution(w, 404, "<html>not found</html>")
	})
	rt := New(f.srv.URL, 30*time.Second)

	req, _ := http.NewRequest(http.MethodGet, "https://rutracker.org/forum/viewtopic.php?t=1", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("StatusCode = %d, want the tracker's 404 to survive", resp.StatusCode)
	}
}

// When FlareSolverr itself fails (challenge unsolved, browser crash) that is a
// transport failure, not a page.
func TestRoundTrip_SolverErrorIsATransportError(t *testing.T) {
	f := newFakeSolver(t, func(w http.ResponseWriter, _, _ string) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "error",
			"message": "Challenge not solved!",
		})
	})
	rt := New(f.srv.URL, 30*time.Second)

	req, _ := http.NewRequest(http.MethodGet, "https://rutracker.org/forum/index.php", nil)
	_, err := rt.RoundTrip(req)
	if err == nil {
		t.Fatal("expected an error when FlareSolverr reports failure")
	}
	if !strings.Contains(err.Error(), "Challenge not solved") {
		t.Errorf("error should carry the solver's message, got %v", err)
	}
}

// FlareSolverr's request.get cannot carry a request body, so a POST (the
// login form) must fail loudly rather than being silently downgraded to a GET
// that appears to succeed while never submitting anything.
func TestRoundTrip_RejectsNonGET(t *testing.T) {
	f := newFakeSolver(t, func(w http.ResponseWriter, _, _ string) {
		okSolution(w, 200, "<html></html>")
	})
	rt := New(f.srv.URL, 30*time.Second)

	req, _ := http.NewRequest(http.MethodPost, "https://rutracker.org/forum/login.php",
		strings.NewReader("login_username=x"))
	_, err := rt.RoundTrip(req)
	if !errors.Is(err, ErrMethodUnsupported) {
		t.Errorf("POST error = %v, want ErrMethodUnsupported", err)
	}
}

func TestRoundTrip_HonoursContextCancellation(t *testing.T) {
	f := newFakeSolver(t, func(w http.ResponseWriter, _, _ string) {
		okSolution(w, 200, "<html></html>")
	})
	rt := New(f.srv.URL, 30*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://rutracker.org/forum/index.php", nil)

	if _, err := rt.RoundTrip(req); err == nil {
		t.Error("expected an error for an already-cancelled context")
	}
}

// --- session reuse -------------------------------------------------------

// TestRoundTrip_ReusesOneSessionAcrossRequests is the fix for the root cause
// of the 2026-07-30 outage. Without a session, FlareSolverr spins a fresh
// browser and re-solves the Cloudflare challenge on every single request
// (measured at 10-20s against RuTracker), and because it serialises requests,
// several topics checking at once queue past the scheduler's 35s budget and
// all fail. Solving once and reusing the cleared context removes both the
// per-request cost and the queue.
func TestRoundTrip_ReusesOneSessionAcrossRequests(t *testing.T) {
	f := newFakeSolver(t, func(w http.ResponseWriter, _, _ string) {
		okSolution(w, 200, "<html></html>")
	})
	rt := New(f.srv.URL, 30*time.Second)

	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest(http.MethodGet, "https://rutracker.org/forum/index.php", nil)
		resp, err := rt.RoundTrip(req)
		if err != nil {
			t.Fatalf("RoundTrip %d: %v", i, err)
		}
		resp.Body.Close()
	}

	if got := f.cmdCount("sessions.create"); got != 1 {
		t.Errorf("sessions.create calls = %d, want exactly 1 across three fetches", got)
	}
	if used := f.sessionsUsed(); len(used) != 1 {
		t.Errorf("distinct sessions used = %v, want exactly one reused session", used)
	}
}

// A session that FlareSolverr no longer knows about — it restarted, or the
// session aged out — must be transparently replaced rather than failing the
// check. Without this, one FlareSolverr restart would wedge every
// challenge-gated tracker until Marauder itself was restarted.
func TestRoundTrip_RecreatesSessionWhenItIsGone(t *testing.T) {
	var calls int
	f := newFakeSolver(t, func(w http.ResponseWriter, _, _ string) {
		calls++
		if calls == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":  "error",
				"message": "Error: This session does not exist.",
			})
			return
		}
		okSolution(w, 200, "<html>recovered</html>")
	})
	rt := New(f.srv.URL, 30*time.Second)

	req, _ := http.NewRequest(http.MethodGet, "https://rutracker.org/forum/index.php", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip should have recovered by recreating the session: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "recovered") {
		t.Errorf("body = %q, want the retried fetch's page", string(body))
	}
	if got := f.cmdCount("sessions.create"); got != 2 {
		t.Errorf("sessions.create calls = %d, want 2 (initial + replacement)", got)
	}
}

// A non-session error must NOT trigger a retry: re-driving a browser through
// an unsolvable challenge doubles the cost and delays the real answer.
func TestRoundTrip_DoesNotRetryOnOrdinarySolverError(t *testing.T) {
	f := newFakeSolver(t, func(w http.ResponseWriter, _, _ string) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "error",
			"message": "Challenge not solved!",
		})
	})
	rt := New(f.srv.URL, 30*time.Second)

	req, _ := http.NewRequest(http.MethodGet, "https://rutracker.org/forum/index.php", nil)
	if _, err := rt.RoundTrip(req); err == nil {
		t.Fatal("expected the solver error to surface")
	}
	if got := f.cmdCount("request.get"); got != 1 {
		t.Errorf("request.get calls = %d, want 1 (no retry on a non-session error)", got)
	}
}

// Concurrent first requests must not each create their own session — that
// would spawn several browsers at once on a service that serialises anyway.
func TestRoundTrip_ConcurrentFirstRequestsCreateOneSession(t *testing.T) {
	f := newFakeSolver(t, func(w http.ResponseWriter, _, _ string) {
		okSolution(w, 200, "<html></html>")
	})
	rt := New(f.srv.URL, 30*time.Second)

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, "https://rutracker.org/forum/index.php", nil)
			resp, err := rt.RoundTrip(req)
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()

	if got := f.cmdCount("sessions.create"); got != 1 {
		t.Errorf("sessions.create calls = %d, want 1 under concurrency", got)
	}
}

// An unconfigured transport must be inert rather than panicking, so wiring can
// pass it unconditionally.
func TestNew_EmptyURLIsDisabled(t *testing.T) {
	rt := New("", 30*time.Second)
	req, _ := http.NewRequest(http.MethodGet, "https://rutracker.org/forum/index.php", nil)
	if _, err := rt.RoundTrip(req); !errors.Is(err, ErrDisabled) {
		t.Errorf("error = %v, want ErrDisabled", err)
	}
}
