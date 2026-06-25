package handlers

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

type fakeHub struct{ ch chan []byte }

func (f *fakeHub) Subscribe(_ uuid.UUID) (<-chan []byte, func()) { return f.ch, func() {} }

type fakeTickets struct {
	issued string
	uid    uuid.UUID
	valid  bool
}

func (f *fakeTickets) Issue(uid uuid.UUID) (string, error) { f.uid = uid; return f.issued, nil }
func (f *fakeTickets) Consume(tok string) (uuid.UUID, bool) {
	if f.valid && tok == f.issued {
		return f.uid, true
	}
	return uuid.Nil, false
}

type fakeEventLister struct{}

func (fakeEventLister) ListForUserSince(_ context.Context, _ uuid.UUID, _ int64) ([]*domain.TopicEvent, error) {
	return nil, nil
}

func TestSSE_Stream_RejectsMissingTicket(t *testing.T) {
	h := &SSE{Hub: &fakeHub{ch: make(chan []byte, 1)}, Tickets: &fakeTickets{}, Events: fakeEventLister{}, HeartbeatInterval: time.Hour}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	rec := httptest.NewRecorder()
	h.Stream(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing ticket: status = %d, want 401", rec.Code)
	}
}

func TestSSE_Stream_ValidTicket_StreamsFrames(t *testing.T) {
	hub := &fakeHub{ch: make(chan []byte, 1)}
	tickets := &fakeTickets{issued: "tok123", uid: uuid.New(), valid: true}
	h := &SSE{Hub: hub, Tickets: tickets, Events: fakeEventLister{}, HeartbeatInterval: time.Hour}

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events?ticket=tok123", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() { h.Stream(rec, req); close(done) }()

	hub.ch <- []byte("data: {\"type\":\"check.started\"}\n\n")
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, "check.started") {
		t.Fatalf("stream body missing pushed frame: %q", body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
}

// TestSSE_Stream_SurvivesServerWriteTimeout proves that the SSE stream is not
// hard-closed by the shared http.Server WriteTimeout. It runs against a REAL
// httptest server (not httptest.ResponseRecorder) so that WriteTimeout is
// actually enforced. Without the rc.SetWriteDeadline(time.Time{}) fix the
// second read reliably fails because the connection is closed at the deadline.
func TestSSE_Stream_SurvivesServerWriteTimeout(t *testing.T) {
	const writeTimeout = 250 * time.Millisecond

	hub := &fakeHub{ch: make(chan []byte, 4)}
	tickets := &fakeTickets{issued: "tok", uid: uuid.New(), valid: true}
	h := &SSE{Hub: hub, Tickets: tickets, Events: fakeEventLister{}, HeartbeatInterval: time.Hour}

	ts := httptest.NewUnstartedServer(http.HandlerFunc(h.Stream))
	ts.Config.WriteTimeout = writeTimeout
	ts.Start()
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"?ticket=tok", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)

	// Push and read the first frame (before the WriteTimeout window expires).
	hub.ch <- []byte(fmt.Sprintf("data: {\"n\":1}\n\n"))
	frame1, err := readSSEFrame(reader)
	if err != nil {
		t.Fatalf("read first frame: %v", err)
	}
	if !strings.Contains(frame1, `"n":1`) {
		t.Fatalf("first frame unexpected: %q", frame1)
	}

	// Wait longer than the server's WriteTimeout so it would fire if still active.
	time.Sleep(writeTimeout + 150*time.Millisecond)

	// Push and read a second frame. With the bug this read returns an error
	// because the server has already closed the connection. With the fix it
	// succeeds because the write deadline was cleared.
	hub.ch <- []byte(fmt.Sprintf("data: {\"n\":2}\n\n"))
	frame2, err := readSSEFrame(reader)
	if err != nil {
		t.Fatalf("read second frame after WriteTimeout window: %v (stream was killed by WriteTimeout — fix is missing)", err)
	}
	if !strings.Contains(frame2, `"n":2`) {
		t.Fatalf("second frame unexpected: %q", frame2)
	}

	// Clean up: cancel the request context so the handler goroutine exits.
	cancel()
}

// readSSEFrame reads lines from r until it hits the blank line that terminates
// an SSE frame, and returns all lines joined.
func readSSEFrame(r *bufio.Reader) (string, error) {
	var sb strings.Builder
	for {
		line, err := r.ReadString('\n')
		sb.WriteString(line)
		if err != nil {
			return sb.String(), err
		}
		if line == "\n" {
			return sb.String(), nil
		}
	}
}
