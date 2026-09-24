package tapochek

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Issue #198: the site's nginx answers 503 to requests that arrive together.
// These pin the two plugin-side answers — one request at a time, and one
// retry of a 503 — against a server that counts what reached it.

const throttleTarget = "https://" + defaultDomain + "/index.php"

// statusSequence answers each request with the next status in the list and
// repeats the last one after that. It returns the handler and a counter.
func statusSequence(statuses ...int) (http.HandlerFunc, *atomic.Int32) {
	var n atomic.Int32
	return func(w http.ResponseWriter, _ *http.Request) {
		i := int(n.Add(1)) - 1
		if i >= len(statuses) {
			i = len(statuses) - 1
		}
		w.WriteHeader(statuses[i])
		_, _ = io.WriteString(w, "ok")
	}, &n
}

func TestDo_503ThenOK_RetriesOnce(t *testing.T) {
	h, calls := statusSequence(http.StatusServiceUnavailable, http.StatusOK)
	p := newTestPlugin(t, h)
	p.retryDelay = time.Millisecond

	body, err := p.get(context.Background(), p.newSession(), throttleTarget)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
}

func TestDo_503Twice_GivesUpAfterOneRetry(t *testing.T) {
	h, calls := statusSequence(http.StatusServiceUnavailable)
	p := newTestPlugin(t, h)
	p.retryDelay = time.Millisecond

	_, err := p.get(context.Background(), p.newSession(), throttleTarget)
	// The scheduler's classifyError reads the status from this marker.
	if err == nil || !strings.Contains(err.Error(), "-> 503") {
		t.Fatalf("err = %v, want one naming -> 503", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
}

func TestDo_OtherErrorStatus_NotRetried(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusTooManyRequests, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			h, calls := statusSequence(status, http.StatusOK)
			p := newTestPlugin(t, h)
			p.retryDelay = time.Millisecond

			if _, err := p.get(context.Background(), p.newSession(), throttleTarget); err == nil {
				t.Fatal("get succeeded, want the first status as an error")
			}
			if got := calls.Load(); got != 1 {
				t.Errorf("requests = %d, want 1", got)
			}
		})
	}
}

// The login POST is the request issue #198 was first reported on, so the
// retry must send the form again, not an empty body.
func TestDo_503OnPost_ResendsTheForm(t *testing.T) {
	var (
		mu     sync.Mutex
		bodies []string
	)
	p := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		first := len(bodies) == 1
		mu.Unlock()
		if first {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	p.retryDelay = time.Millisecond

	form := url.Values{"login_username": {"someone"}}
	if _, err := p.post(context.Background(), p.newSession(), "https://"+defaultDomain+"/login.php", form); err != nil {
		t.Fatalf("post: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 || bodies[1] != form.Encode() {
		t.Errorf("bodies = %q, want the form sent twice", bodies)
	}
}

func TestDo_ContextEndsDuringRetryPause_ReturnsThe503(t *testing.T) {
	h, calls := statusSequence(http.StatusServiceUnavailable, http.StatusOK)
	p := newTestPlugin(t, h)
	p.retryDelay = time.Hour

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := p.get(ctx, p.newSession(), throttleTarget)
	if err == nil || !strings.Contains(err.Error(), "-> 503") {
		t.Fatalf("err = %v, want the 503", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

func TestDo_ParallelCallers_OneRequestAtATime(t *testing.T) {
	var inFlight, peak atomic.Int32
	p := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		now := inFlight.Add(1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
		w.WriteHeader(http.StatusOK)
	})

	const callers = 5
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Separate sessions: the limit is the site's, not a session's.
			_, err := p.get(context.Background(), p.newSession(), throttleTarget)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("get: %v", err)
		}
	}
	if got := peak.Load(); got != 1 {
		t.Errorf("peak concurrent requests = %d, want 1", got)
	}
}

func TestDo_ContextEndsWhileQueued_GivesUp(t *testing.T) {
	release := make(chan struct{})
	p := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	})
	// Create the slot here so reading it below does not race its creation.
	p.slotOnce.Do(func() { p.slot = make(chan struct{}, 1) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = p.get(context.Background(), p.newSession(), throttleTarget)
	}()
	// Wait until the first request holds the slot.
	deadline := time.Now().Add(5 * time.Second)
	for len(p.slot) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("first request never took the slot")
		}
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := p.get(ctx, p.newSession(), throttleTarget)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
	close(release)
	<-done
}
