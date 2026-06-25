package sse

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTicket_IssueThenConsume_Once(t *testing.T) {
	ts := NewTicketStore()
	uid := uuid.New()
	tok, err := ts.Issue(uid)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	got, ok := ts.Consume(tok)
	if !ok || got != uid {
		t.Fatalf("Consume = (%v,%v), want (%v,true)", got, ok, uid)
	}
	if _, ok := ts.Consume(tok); ok {
		t.Error("ticket must be single-use")
	}
}

func TestTicket_Unknown_Rejected(t *testing.T) {
	ts := NewTicketStore()
	if _, ok := ts.Consume("nope"); ok {
		t.Error("unknown token must be rejected")
	}
}

func TestTicket_Expired_Rejected(t *testing.T) {
	ts := NewTicketStore()
	base := time.Unix(1000, 0)
	ts.now = func() time.Time { return base }
	uid := uuid.New()
	tok, _ := ts.Issue(uid)
	ts.now = func() time.Time { return base.Add(ticketTTL + time.Second) }
	if _, ok := ts.Consume(tok); ok {
		t.Error("expired ticket must be rejected")
	}
}
