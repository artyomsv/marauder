package handlers

import (
	"context"
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
