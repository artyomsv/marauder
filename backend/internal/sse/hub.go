// Package sse provides the live Server-Sent-Events fan-out: an in-memory
// per-user Hub (the events.Publisher implementation) and a one-time ticket
// store for authenticating EventSource connections that cannot send an
// Authorization header.
package sse

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/events"
	"github.com/artyomsv/marauder/backend/internal/metrics"
)

// subscriberBuffer is the per-connection frame buffer. A connection that
// can't keep up past this many queued frames drops frames rather than
// blocking the hub.
const subscriberBuffer = 64

// subscriber is one live connection's frame queue. mu guards closed and ch so
// that Publish's send and unsubscribe's close are mutually exclusive — a send
// to a closed channel panics even inside a select/case, so the closed flag is
// the only safe guard.
type subscriber struct {
	mu     sync.Mutex
	ch     chan []byte
	closed bool
}

// Hub fans serialized SSE frames out to a user's live connections. It is the
// events.Publisher implementation wired into the bus in Phase 3.
type Hub struct {
	mu   sync.Mutex
	subs map[uuid.UUID]map[*subscriber]struct{}
	log  zerolog.Logger
}

// NewHub constructs an empty Hub.
func NewHub(log zerolog.Logger) *Hub {
	return &Hub{subs: map[uuid.UUID]map[*subscriber]struct{}{}, log: log.With().Str("component", "sse").Logger()}
}

// Subscribe registers a connection for userID and returns its frames channel
// plus an unsubscribe func (idempotent). The channel is closed by unsubscribe.
func (h *Hub) Subscribe(userID uuid.UUID) (<-chan []byte, func()) {
	s := &subscriber{ch: make(chan []byte, subscriberBuffer)}
	h.mu.Lock()
	set, ok := h.subs[userID]
	if !ok {
		set = map[*subscriber]struct{}{}
		h.subs[userID] = set
	}
	set[s] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			h.mu.Lock()
			if set, ok := h.subs[userID]; ok {
				delete(set, s)
				if len(set) == 0 {
					delete(h.subs, userID)
				}
			}
			h.mu.Unlock()
			// Close under the subscriber mutex so Publish's send loop cannot
			// race between observing the old snapshot and sending to a closed
			// channel. The hub mutex is NOT held here (Publish holds it only
			// during the snapshot copy, never during the send).
			s.mu.Lock()
			s.closed = true
			close(s.ch)
			s.mu.Unlock()
		})
	}
	return s.ch, unsub
}

// Publish serializes ev (with its history id) once and non-blockingly sends
// the frame to every live connection of ev's owner. A full buffer drops the
// frame (metered) rather than blocking. id<=0 means ephemeral (no id: line).
func (h *Hub) Publish(userID uuid.UUID, ev events.Event, id int64) {
	frame, err := serializeFrame(ev, id)
	if err != nil {
		h.log.Warn().Err(err).Str("type", string(ev.Type)).Msg("serialize frame failed")
		return
	}
	h.mu.Lock()
	set := h.subs[userID]
	targets := make([]*subscriber, 0, len(set))
	for s := range set {
		targets = append(targets, s)
	}
	h.mu.Unlock()
	for _, s := range targets {
		s.mu.Lock()
		if !s.closed {
			select {
			case s.ch <- frame:
			default:
				metrics.SSEDroppedFramesTotal.Inc()
			}
		}
		s.mu.Unlock()
	}
}

// wireEvent is the JSON shape sent in each SSE data: line. The frontend
// (Phase 3b) consumes this. topic_id is omitted when nil; id is the history
// id (0 for ephemeral events).
type wireEvent struct {
	ID       int64          `json:"id"`
	Type     string         `json:"type"`
	TopicID  string         `json:"topic_id,omitempty"`
	Severity string         `json:"severity,omitempty"`
	Title    string         `json:"title,omitempty"`
	Body     string         `json:"body,omitempty"`
	Link     string         `json:"link,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

// serializeFrame renders an SSE frame. Persisted events (id>0) get an `id:`
// line so the browser's Last-Event-ID drives replay; ephemeral events don't.
func serializeFrame(ev events.Event, id int64) ([]byte, error) {
	w := wireEvent{
		ID: id, Type: string(ev.Type), Severity: ev.Severity,
		Title: ev.Title, Body: ev.Body, Link: ev.Link, Data: ev.Data,
	}
	if ev.TopicID != nil {
		w.TopicID = ev.TopicID.String()
	}
	payload, err := json.Marshal(w)
	if err != nil {
		return nil, fmt.Errorf("sse: marshal event: %w", err)
	}
	var b []byte
	if id > 0 {
		b = append(b, fmt.Sprintf("id: %d\n", id)...)
	}
	b = append(b, "data: "...)
	b = append(b, payload...)
	b = append(b, "\n\n"...)
	return b, nil
}
