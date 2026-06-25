package sse

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ticketTTL bounds how long an issued SSE ticket is valid. Short by design:
// the browser exchanges its JWT for a ticket immediately before opening the
// EventSource.
const ticketTTL = 30 * time.Second

type ticketEntry struct {
	userID  uuid.UUID
	expires time.Time
}

// TicketStore issues and consumes one-time SSE auth tickets. In-memory and
// single-process (matches the single-backend deployment).
type TicketStore struct {
	mu  sync.Mutex
	m   map[string]ticketEntry
	now func() time.Time
}

// NewTicketStore constructs an empty store.
func NewTicketStore() *TicketStore {
	return &TicketStore{m: map[string]ticketEntry{}, now: time.Now}
}

// Issue mints a single-use ticket bound to userID, valid for ticketTTL.
func (t *TicketStore) Issue(userID uuid.UUID) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("sse: ticket rand: %w", err)
	}
	tok := hex.EncodeToString(buf)
	t.mu.Lock()
	t.pruneLocked()
	t.m[tok] = ticketEntry{userID: userID, expires: t.now().Add(ticketTTL)}
	t.mu.Unlock()
	return tok, nil
}

// Consume validates and deletes a ticket, returning its userID. A second
// consume, an unknown token, or an expired token returns ok=false.
func (t *TicketStore) Consume(token string) (uuid.UUID, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.m[token]
	if !ok {
		return uuid.Nil, false
	}
	delete(t.m, token)
	if t.now().After(e.expires) {
		return uuid.Nil, false
	}
	return e.userID, true
}

// pruneLocked drops expired tickets; caller holds the lock.
func (t *TicketStore) pruneLocked() {
	now := t.now()
	for k, e := range t.m {
		if now.After(e.expires) {
			delete(t.m, k)
		}
	}
}
