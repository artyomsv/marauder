package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSlowOperation_BoundsTheContext(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/credentials/x/test", nil)

	ctx, cancel := slowOperation(rec, req, 50*time.Millisecond)
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("slowOperation must impose a deadline: a handler that outruns the server's WriteTimeout produces no valid HTTP response at all")
	}
	if d := time.Until(deadline); d > 60*time.Millisecond {
		t.Fatalf("deadline is %s away, want ~50ms", d)
	}
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("ctx.Err() = %v", ctx.Err())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("context never expired")
	}
}

func TestSlowOperation_InheritsRequestCancellation(t *testing.T) {
	rec := httptest.NewRecorder()
	base, cancelReq := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/x", nil).WithContext(base)

	ctx, cancel := slowOperation(rec, req, time.Hour)
	defer cancel()

	// A client that hangs up must still abort the work — the budget is a
	// ceiling, not a floor.
	cancelReq()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("context did not follow the request's cancellation")
	}
}

// TestSlowOperation_SurvivesUnsupportedResponseWriter guards the degenerate
// case: httptest.ResponseRecorder does not implement SetWriteDeadline, and a
// handler must not fall over just because the deadline could not be extended.
func TestSlowOperation_SurvivesUnsupportedResponseWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	ctx, cancel := slowOperation(rec, req, time.Second)
	defer cancel()
	if ctx == nil {
		t.Fatal("want a usable context even when the write deadline cannot be set")
	}
}
