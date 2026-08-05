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
	srv           *httptest.Server
	mu            sync.Mutex
	lastBody      map[string]any
	bodies        []map[string]any
	destroyShould bool // when true, sessions.destroy answers 200 + status:error
	createFails   bool // when true, sessions.create answers 200 + status:error
	createGate    chan struct{}
}

// failCreate makes sessions.create return an envelope-level failure.
func (f *fakeSolver) failCreate(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createFails = v
}

// gateCreate makes sessions.create block until the returned func is called,
// so a test can hold a create in flight and observe what waiters do.
func (f *fakeSolver) gateCreate() (release func()) {
	f.mu.Lock()
	f.createGate = make(chan struct{})
	g := f.createGate
	f.mu.Unlock()
	return func() { close(g) }
}

// failDestroy makes sessions.destroy return an envelope-level failure while
// still answering HTTP 200 — the shape FlareSolverr actually uses.
func (f *fakeSolver) failDestroy(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.destroyShould = v
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
			f.mu.Lock()
			gate, fail := f.createGate, f.createFails
			f.mu.Unlock()
			if gate != nil {
				<-gate // hold the create in flight until the test releases it
			}
			if fail {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status":  "error",
					"message": "Error: browser limit reached.",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":  "ok",
				"message": "Session created successfully.",
				"session": fmt.Sprintf("sess-%d", nth),
			})
			return
		}
		if cmd == "sessions.destroy" {
			f.mu.Lock()
			fail := f.destroyShould
			f.mu.Unlock()
			if fail {
				// HTTP 200 with an envelope-level failure: FlareSolverr's
				// actual shape for a command that did not succeed.
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status":  "error",
					"message": "Error: This session does not exist.",
				})
				return
			}
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

	// 30s is a real caller budget (a download iteration gets TrackerHTTPTimeout)
	// and clears minSolveBudget, so this exercises the tightening rather than
	// the refusal — TestSolveBudget_TableTest covers budgets below the floor.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://rutracker.org/forum/index.php", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()

	got, _ := f.lastBody["maxTimeout"].(float64)
	if got <= 0 || got > 30_000 {
		t.Errorf("maxTimeout = %v ms, want it bounded by the caller's 30s deadline", got)
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
	var f *fakeSolver
	f = newFakeSolver(t, func(w http.ResponseWriter, _, _ string) {
		// The first request.get reports a gone session; the retry succeeds.
		// Counting via the fake's mutex-guarded request log avoids a second,
		// unsynchronised counter written from handler goroutines.
		if f.cmdCount("request.get") == 1 {
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

// --- availability under a slow or failing solver -------------------------

// TestRoundTrip_WaiterHonoursItsOwnDeadline is an availability fix, not a
// correctness one. Session acquisition originally held a plain sync.Mutex
// across the sessions.create round-trip; sync.Mutex.Lock is not
// context-aware, so a waiter cannot abandon on its own deadline. The
// scheduler runs ONE bounded worker pool shared by every tracker (default 8),
// each check bounded at TrackerHTTPTimeout+5s, so a slow solver could park
// all workers and delay checks for trackers that don't use the solver at all.
func TestRoundTrip_WaiterHonoursItsOwnDeadline(t *testing.T) {
	f := newFakeSolver(t, func(w http.ResponseWriter, _, _ string) {
		okSolution(w, 200, "<html></html>")
	})
	release := f.gateCreate()
	defer release()
	rt := New(f.srv.URL, 30*time.Second)

	// Hold a create in flight.
	started := make(chan struct{})
	go func() {
		close(started)
		req, _ := http.NewRequest(http.MethodGet, "https://rutracker.org/forum/index.php", nil)
		if resp, err := rt.RoundTrip(req); err == nil {
			resp.Body.Close()
		}
	}()
	<-started

	// A second caller with a short deadline must give up on time rather than
	// blocking until the creator finishes.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://rutracker.org/forum/index.php", nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if resp, err := rt.RoundTrip(req); err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("waiter blocked well past its own deadline; acquisition is not context-aware")
	}
}

// A solver that cannot create sessions (browser limit, solver down) must be
// asked once per cooldown, not once per request. Without this, every check
// pays a failed round-trip and the failure is never cached.
func TestRoundTrip_NegativeCachesCreateFailure(t *testing.T) {
	f := newFakeSolver(t, func(w http.ResponseWriter, _, _ string) {
		okSolution(w, 200, "<html></html>")
	})
	f.failCreate(true)
	rt := New(f.srv.URL, 30*time.Second)

	for i := 0; i < 4; i++ {
		req, _ := http.NewRequest(http.MethodGet, "https://rutracker.org/forum/index.php", nil)
		if resp, err := rt.RoundTrip(req); err == nil {
			resp.Body.Close()
		}
	}

	if got := f.cmdCount("sessions.create"); got != 1 {
		t.Errorf("sessions.create attempts = %d, want 1 within the cooldown", got)
	}
}

// --- abandoned sessions --------------------------------------------------

// A replaced session must be destroyed, not merely forgotten. FlareSolverr
// holds one Chrome per session and does not expire them unless
// SESSION_TTL_MINUTES is set (it is not, in deploy/), so a forgotten session
// strands roughly 300 MB until the container restarts.
func TestRoundTrip_DestroysTheSessionItAbandons(t *testing.T) {
	var f *fakeSolver
	f = newFakeSolver(t, func(w http.ResponseWriter, _, _ string) {
		// The first request.get reports a gone session; the retry succeeds.
		// Counting via the fake's mutex-guarded request log avoids a second,
		// unsynchronised counter written from handler goroutines.
		if f.cmdCount("request.get") == 1 {
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
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()

	if got := f.cmdCount("sessions.destroy"); got != 1 {
		t.Errorf("sessions.destroy calls = %d, want 1 — the abandoned session leaks a browser otherwise", got)
	}
}

// isSessionGone must require wording that means the session is ABSENT. A
// message that merely mentions a session (capacity limits, "session already
// exists", a crash naming the session) would otherwise discard a healthy
// session — and if the follow-up create fails for the same reason, the
// transport is left permanently sessionless, reinstating the very re-solve
// storm this change removes.
func TestIsSessionGone_RequiresAbsenceWording(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want bool
	}{
		{"session does not exist", "flaresolverr: request.get: Error: This session does not exist.", true},
		{"session not found", "flaresolverr: request.get: session not found", true},
		{"unknown session", "flaresolverr: request.get: unknown session id", true},
		{"capacity mentions session", "flaresolverr: sessions.create: browser session limit reached", false},
		{"session already exists", "flaresolverr: sessions.create: Session already exists", false},
		{"unsolved challenge", "flaresolverr: request.get: Challenge not solved!", false},
		{"no error", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.msg != "" {
				err = errors.New(tt.msg)
			}
			if got := isSessionGone(err); got != tt.want {
				t.Errorf("isSessionGone(%q) = %v, want %v", tt.msg, got, tt.want)
			}
		})
	}
}

// --- defence in depth ----------------------------------------------------

// The transport hands its URL to a real browser running inside the solver
// container, from a network position Marauder may not itself have. Today the
// only consumer validates hosts, but the transport is installed process-wide
// and now stateful, so a future plugin that forgets the guard must not be able
// to make the browser read local files or cloud metadata.
func TestRoundTrip_RejectsNonHTTPSchemes(t *testing.T) {
	f := newFakeSolver(t, func(w http.ResponseWriter, _, _ string) {
		okSolution(w, 200, "<html></html>")
	})
	rt := New(f.srv.URL, 30*time.Second)

	for _, raw := range []string{"file:///etc/passwd", "ftp://example.com/x", "gopher://example.com"} {
		req, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			continue
		}
		if _, err := rt.RoundTrip(req); err == nil {
			t.Errorf("RoundTrip(%q) succeeded; non-http(s) schemes must be refused", raw)
		}
	}
	if got := f.cmdCount("request.get"); got != 0 {
		t.Errorf("request.get calls = %d, want 0 — nothing should reach the browser", got)
	}
}

// --- shutdown ------------------------------------------------------------

// TestClose_PreventsResurrectingASession closes the orphan hole that Close
// itself would otherwise open. Shutdown does not join the scheduler's
// workers, so a worker can still be mid-check when Close runs. Its in-flight
// request then fails with a session error, the retry path creates a
// REPLACEMENT session, and that one is never destroyed — exactly the orphan
// Close exists to prevent. After Close the transport must therefore refuse to
// mint new sessions; a check failing during shutdown is fine, a leaked
// browser is not.
func TestClose_PreventsResurrectingASession(t *testing.T) {
	f := newFakeSolver(t, func(w http.ResponseWriter, _, _ string) {
		okSolution(w, 200, "<html></html>")
	})
	rt := New(f.srv.URL, 30*time.Second)

	// Establish a session, then shut down.
	req, _ := http.NewRequest(http.MethodGet, "https://rutracker.org/forum/index.php", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("first RoundTrip: %v", err)
	}
	resp.Body.Close()
	if err := rt.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	created := f.cmdCount("sessions.create")

	// A late in-flight request must not resurrect a session.
	req2, _ := http.NewRequest(http.MethodGet, "https://rutracker.org/forum/index.php", nil)
	if resp2, err := rt.RoundTrip(req2); err == nil {
		resp2.Body.Close()
	}

	if got := f.cmdCount("sessions.create"); got != created {
		t.Errorf("sessions.create calls = %d, want %d — Close must not be followed by a new session", got, created)
	}
}

// FlareSolverr answers HTTP 200 even when the command failed, signalling the
// real outcome in the envelope's status field. Ignoring it made Close report
// successful cleanup while the browser session stayed alive, which is a
// silent leak — the operator never sees the warning that would explain it.
func TestClose_SurfacesAnEnvelopeError(t *testing.T) {
	f := newFakeSolver(t, func(w http.ResponseWriter, _, _ string) {
		okSolution(w, 200, "<html></html>")
	})
	rt := New(f.srv.URL, 30*time.Second)
	req, _ := http.NewRequest(http.MethodGet, "https://rutracker.org/forum/index.php", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()

	// Swap the handler so sessions.destroy fails at the envelope level.
	f.failDestroy(true)

	if err := rt.Close(context.Background()); err == nil {
		t.Error("Close must report an envelope-level failure, not silently accept it")
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

// TestSolveBudget_TableTest pins the floor against what a solve actually
// costs. Measured against live RuTracker on 2026-08-05, a managed-challenge
// solve took 10.9s, 11.3s, 11.4s, 11.6s, 11.9s, 12.3s, 12.9s and 13.4s across
// eight runs. The old 5s floor let 15s and 8s budgets through; both timed out,
// and because FlareSolverr serialises, each abandoned browser blocked the next
// caller. Refusing instantly is strictly better than paying the full budget to
// learn the same thing.
func TestSolveBudget_TableTest(t *testing.T) {
	tests := []struct {
		name       string
		configured time.Duration
		remaining  time.Duration // 0 means "no deadline on the context"
		want       time.Duration
		wantErr    bool
	}{
		{name: "no deadline uses the configured ceiling", configured: 60 * time.Second, want: 60 * time.Second},
		{name: "a comfortable deadline tightens the budget", configured: 60 * time.Second, remaining: 35 * time.Second, want: 35 * time.Second},
		{name: "just above the floor is allowed", configured: 60 * time.Second, remaining: minSolveBudget + time.Second, want: minSolveBudget + time.Second},
		// The two live failures from the boot race.
		{name: "15s cannot finish a ~12s solve", configured: 60 * time.Second, remaining: 15 * time.Second, wantErr: true},
		{name: "8s cannot finish a ~12s solve", configured: 60 * time.Second, remaining: 8 * time.Second, wantErr: true},
		// An operator who configures a short ceiling has said what they want;
		// honour it rather than silently exceeding the documented maximum.
		{name: "a configured ceiling below the floor lowers the floor", configured: 3 * time.Second, want: 3 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := New("http://solver:8191", tt.configured)
			ctx := context.Background()
			if tt.remaining > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tt.remaining)
				defer cancel()
			}
			got, err := tr.solveBudget(ctx)
			if tt.wantErr {
				if !errors.Is(err, ErrBudgetTooShort) {
					t.Fatalf("solveBudget = (%s, %v), want ErrBudgetTooShort", got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("solveBudget: %v", err)
			}
			// time.Until burns a few microseconds between setup and the call.
			if diff := tt.want - got; diff < 0 || diff > 100*time.Millisecond {
				t.Errorf("solveBudget = %s, want ~%s", got, tt.want)
			}
		})
	}
}

// minSolveBudget must stay above the slowest solve observed in production, or
// the floor is decoration: a budget that passes the guard and then times out
// leaves a browser running for a caller that has already left.
func TestMinSolveBudget_ExceedsObservedSolveTime(t *testing.T) {
	const slowestObservedSolve = 13400 * time.Millisecond // live RuTracker, 2026-08-05
	if minSolveBudget <= slowestObservedSolve {
		t.Errorf("minSolveBudget = %s, want > %s (the slowest measured challenge solve)",
			minSolveBudget, slowestObservedSolve)
	}
}
