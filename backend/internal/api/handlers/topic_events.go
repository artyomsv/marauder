package handlers

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/problem"
)

// topicEventsStore is the consumer seam over *repo.TopicEvents.
type topicEventsStore interface {
	ListForTopic(ctx context.Context, topicID, userID uuid.UUID, limit int, beforeID int64) ([]*domain.TopicEvent, error)
}

// topicOwnerStore verifies topic ownership before returning its history.
type topicOwnerStore interface {
	GetByID(ctx context.Context, id uuid.UUID, userID *uuid.UUID) (*domain.Topic, error)
}

// TopicEvents handles GET /topics/{id}/events.
type TopicEvents struct {
	Events  topicEventsStore
	Topics  topicOwnerStore
	BaseURL string
}

type topicEventView struct {
	ID        int64          `json:"id"`
	EventType string         `json:"event_type"`
	Severity  string         `json:"severity"`
	Message   string         `json:"message"`
	Data      map[string]any `json:"data,omitempty"`
	CreatedAt string         `json:"created_at"`
}

// List handles GET /topics/{id}/events?limit=&before=.
func (h *TopicEvents) List(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	id, ierr := uuid.Parse(chi.URLParam(r, "id"))
	if ierr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid id"))
		return
	}
	if _, err := h.Topics.GetByID(r.Context(), id, &uid); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrNotFound("topic not found"))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	rows, err := h.Events.ListForTopic(r.Context(), id, uid, limit, before)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	out := make([]topicEventView, 0, len(rows))
	for _, e := range rows {
		out = append(out, topicEventView{
			ID:        e.ID,
			EventType: e.EventType,
			Severity:  e.Severity,
			Message:   e.Message,
			Data:      e.Data,
			CreatedAt: e.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}
