package flaresolverr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeSolver stands in for a FlareSolverr instance, recording the request it
// was handed so tests can assert on the wire format.
type fakeSolver struct {
	srv      *httptest.Server
	lastBody map[string]any
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
		f.lastBody = body
		cmd, _ := body["cmd"].(string)
		target, _ := body["url"].(string)
		w.Header().Set("Content-Type", "application/json")
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

// An unconfigured transport must be inert rather than panicking, so wiring can
// pass it unconditionally.
func TestNew_EmptyURLIsDisabled(t *testing.T) {
	rt := New("", 30*time.Second)
	req, _ := http.NewRequest(http.MethodGet, "https://rutracker.org/forum/index.php", nil)
	if _, err := rt.RoundTrip(req); !errors.Is(err, ErrDisabled) {
		t.Errorf("error = %v, want ErrDisabled", err)
	}
}
