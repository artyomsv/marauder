package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/problem"
	"github.com/artyomsv/marauder/backend/internal/sse"
)

type sseHub interface {
	Subscribe(userID uuid.UUID) (<-chan []byte, func())
}
type sseTickets interface {
	Issue(userID uuid.UUID) (string, error)
	Consume(token string) (uuid.UUID, bool)
}
type sseEventLister interface {
	ListForUserSince(ctx context.Context, userID uuid.UUID, sinceID int64) ([]*domain.TopicEvent, error)
}

// SSE serves the live event stream and its ticket exchange.
type SSE struct {
	Hub               sseHub
	Tickets           sseTickets
	Events            sseEventLister
	HeartbeatInterval time.Duration
	BaseURL           string
}

// Ticket handles POST /events/ticket. JWT-authed (registered under
// RequireAuth); returns a single-use token the browser puts on the
// EventSource URL.
func (h *SSE) Ticket(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	tok, err := h.Tickets.Issue(uid)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("ticket: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": tok})
}

// Stream handles GET /events?ticket=…. Ticket-gated (registered OUTSIDE
// RequireAuth). Streams text/event-stream until the client disconnects.
func (h *SSE) Stream(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.Tickets.Consume(r.URL.Query().Get("ticket"))
	if !ok {
		http.Error(w, "invalid or expired ticket", http.StatusUnauthorized)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // belt-and-suspenders vs proxy buffering
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Replay missed persisted events on reconnect.
	if lastID, err := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64); err == nil && lastID > 0 {
		if rows, lerr := h.Events.ListForUserSince(r.Context(), uid, lastID); lerr == nil {
			for _, e := range rows {
				if frame := sse.FrameFromTopicEvent(e); frame != nil {
					_, _ = w.Write(frame)
				}
			}
			flusher.Flush()
		}
	}

	frames, unsub := h.Hub.Subscribe(uid)
	defer unsub()

	hb := time.NewTicker(h.HeartbeatInterval)
	defer hb.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case frame, ok := <-frames:
			if !ok {
				return
			}
			if _, err := w.Write(frame); err != nil {
				return
			}
			flusher.Flush()
		case <-hb.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
