package flaresolverr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// clearanceResponder answers request.get with a solved-challenge solution
// carrying the named clearance cookie, mirroring FlareSolverr 3.5.0.
func clearanceResponder(cookie string) func(w http.ResponseWriter, cmd, url string) {
	return func(w http.ResponseWriter, cmd, _ string) {
		if cmd != "request.get" {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"solution": map[string]any{
				"status":    200,
				"userAgent": "Mozilla/5.0 Chrome/148",
				"response":  "<html></html>",
				"cookies": []map[string]any{
					{"name": "cf_clearance", "value": cookie},
					{"name": "bb_guid", "value": "g"},
				},
			},
		})
	}
}

func TestClearance_MintsThenCachesPerHost(t *testing.T) {
	f := newFakeSolver(t, clearanceResponder("CLEAR"))
	defer f.srv.Close()
	tr := New(f.srv.URL, 30*time.Second)

	got, err := tr.Clearance(context.Background(), "https://rutracker.org/forum/login.php")
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
	// bb_guid is a tracker cookie, not a challenge solution — the minter must
	// not hoard it, or a stale guid would ride along on every later request.
	if _, ok := got.Cookies["bb_guid"]; ok {
		t.Errorf("cookies = %v, want only cf_clearance", got.Cookies)
	}

	if _, err := tr.Clearance(context.Background(), "https://rutracker.org/forum/login.php"); err != nil {
		t.Fatalf("second Clearance: %v", err)
	}
	if n := f.cmdCount("request.get"); n != 1 {
		t.Fatalf("solves = %d, want 1 (second call must hit the cache)", n)
	}

	// A different host is a different clearance — cf_clearance is host-bound.
	if _, err := tr.Clearance(context.Background(), "https://rutracker.net/forum/login.php"); err != nil {
		t.Fatalf("other host: %v", err)
	}
	if n := f.cmdCount("request.get"); n != 2 {
		t.Fatalf("solves = %d, want 2 (per-host cache)", n)
	}
}

// TestClearance_SolvesTheGivenURL_NotAHostRoot is the regression guard for the
// bug that shipped past the first round of unit tests: mint derived
// "https://<host>/" instead of solving the caller's URL. RuTracker's root
// redirects to an unchallenged page, so the resulting cf_clearance was real but
// satisfied nothing — every gated request still got 403.
func TestClearance_SolvesTheGivenURL_NotAHostRoot(t *testing.T) {
	var solved []string
	f := newFakeSolver(t, func(w http.ResponseWriter, cmd, target string) {
		if cmd != "request.get" {
			return
		}
		solved = append(solved, target)
		clearanceResponder("CLEAR")(w, cmd, target)
	})
	defer f.srv.Close()
	tr := New(f.srv.URL, 30*time.Second)

	probe := "https://rutracker.org/forum/login.php"
	if _, err := tr.Clearance(context.Background(), probe); err != nil {
		t.Fatalf("Clearance: %v", err)
	}
	if len(solved) != 1 || solved[0] != probe {
		t.Fatalf("solved %v, want exactly [%s]", solved, probe)
	}
}

// TestClearance_WaiterHonoursItsOwnDeadline is the regression guard for a real
// incident: the per-host gate was a sync.Mutex, which cannot be cancelled. A
// 58s boot warm-up solve held it while a scheduler check waited, and that check
// failed 0.4ms AFTER the solve completed — its context had long expired. A
// waiter must give up on its own deadline, not the holder's.
func TestClearance_WaiterHonoursItsOwnDeadline(t *testing.T) {
	release := make(chan struct{})
	f := newFakeSolver(t, func(w http.ResponseWriter, cmd, target string) {
		if cmd != "request.get" {
			return
		}
		<-release // hold the solve in flight
		clearanceResponder("CLEAR")(w, cmd, target)
	})
	defer f.srv.Close()
	defer close(release)
	tr := New(f.srv.URL, 60*time.Second)

	probe := "https://rutracker.org/forum/login.php"
	started := make(chan struct{})
	go func() {
		close(started)
		_, _ = tr.Clearance(context.Background(), probe) // holds the slot
	}()
	<-started

	// A second caller with a short budget must return promptly, not block for
	// the whole solve.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := tr.Clearance(ctx, probe)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("waiter blocked past its own deadline — the gate is not cancellable")
	}
}

func TestClearance_RejectsNonAbsoluteProbeURL(t *testing.T) {
	f := newFakeSolver(t, clearanceResponder("CLEAR"))
	defer f.srv.Close()
	tr := New(f.srv.URL, 30*time.Second)
	for _, bad := range []string{"", "rutracker.org", "/forum/login.php", "ftp://rutracker.org/x"} {
		if _, err := tr.Clearance(context.Background(), bad); err == nil {
			t.Errorf("Clearance(%q) = nil error, want a rejection", bad)
		}
	}
}

func TestInvalidateClearance_ForcesReMint(t *testing.T) {
	f := newFakeSolver(t, clearanceResponder("CLEAR"))
	defer f.srv.Close()
	tr := New(f.srv.URL, 30*time.Second)

	if _, err := tr.Clearance(context.Background(), "https://rutracker.org/forum/login.php"); err != nil {
		t.Fatal(err)
	}
	tr.InvalidateClearance("https://rutracker.org/forum/login.php")
	if _, err := tr.Clearance(context.Background(), "https://rutracker.org/forum/login.php"); err != nil {
		t.Fatal(err)
	}
	if n := f.cmdCount("request.get"); n != 2 {
		t.Fatalf("solves = %d, want 2 after invalidate", n)
	}
}

func TestClearance_NoCookieInSolution_ReturnsErrNoClearance(t *testing.T) {
	f := newFakeSolver(t, func(w http.ResponseWriter, cmd, _ string) {
		if cmd != "request.get" {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"solution": map[string]any{
				"status": 200, "userAgent": "UA", "response": "x",
				"cookies": []map[string]any{{"name": "bb_guid", "value": "g"}},
			},
		})
	})
	defer f.srv.Close()
	tr := New(f.srv.URL, 30*time.Second)
	if _, err := tr.Clearance(context.Background(), "https://rutracker.org/forum/login.php"); err == nil ||
		!strings.Contains(err.Error(), "no clearance") {
		t.Fatalf("err = %v, want ErrNoClearance", err)
	}
}

func TestClearance_Unconfigured_ReturnsErrDisabled(t *testing.T) {
	tr := New("", 30*time.Second)
	if _, err := tr.Clearance(context.Background(), "https://rutracker.org/forum/login.php"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("err = %v, want ErrDisabled", err)
	}
}

func TestClearance_ConcurrentCallsSolveOnce(t *testing.T) {
	f := newFakeSolver(t, clearanceResponder("CLEAR"))
	defer f.srv.Close()
	tr := New(f.srv.URL, 30*time.Second)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := tr.Clearance(context.Background(), "https://rutracker.org/forum/login.php"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := f.cmdCount("request.get"); n != 1 {
		t.Fatalf("solves = %d, want 1 (single-flight per host)", n)
	}
}
