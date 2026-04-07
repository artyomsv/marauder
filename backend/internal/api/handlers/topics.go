package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/api/middleware"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/problem"
)

// Topics is the handler group for /topics.
type Topics struct {
	Topics  *repo.Topics
	BaseURL string
}

type createTopicReq struct {
	URL              string     `json:"url"`
	DisplayName      string     `json:"display_name"`
	ClientID         *uuid.UUID `json:"client_id"`
	DownloadDir      string     `json:"download_dir"`
	CheckIntervalSec int        `json:"check_interval_sec"`
}

// List handles GET /topics.
func (h *Topics) List(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	topics, err := h.Topics.ListForUser(r.Context(), uid)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"topics": topics})
}

// Create handles POST /topics.
func (h *Topics) Create(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}

	var req createTopicReq
	if jerr := json.NewDecoder(r.Body).Decode(&req); jerr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	if req.URL == "" {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("url is required"))
		return
	}

	tracker := registry.FindTrackerForURL(req.URL)
	if tracker == nil {
		problem.Write(w, r, h.BaseURL,
			problem.New(http.StatusUnprocessableEntity,
				"topic-url-not-recognized",
				"No tracker plugin matches this URL",
				"The URL '"+req.URL+"' is not parseable by any installed tracker plugin.",
			))
		return
	}

	parsed, parseErr := tracker.Parse(r.Context(), req.URL)
	if parseErr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("parse: "+parseErr.Error()))
		return
	}

	interval := req.CheckIntervalSec
	if interval <= 0 {
		interval = 900 // 15 min default
	}
	displayName := req.DisplayName
	if displayName == "" {
		displayName = parsed.DisplayName
	}

	t := &domain.Topic{
		UserID:           uid,
		TrackerName:      tracker.Name(),
		URL:              req.URL,
		DisplayName:      displayName,
		ClientID:         req.ClientID,
		DownloadDir:      req.DownloadDir,
		Extra:            parsed.Extra,
		CheckIntervalSec: interval,
		NextCheckAt:      time.Now().UTC(),
		Status:           domain.TopicStatusActive,
	}
	created, cerr := h.Topics.Create(r.Context(), t)
	if cerr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("create topic: "+cerr.Error()))
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// Get handles GET /topics/{id}.
func (h *Topics) Get(w http.ResponseWriter, r *http.Request) {
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
	t, gerr := h.Topics.GetByID(r.Context(), id, &uid)
	if gerr != nil {
		if errors.Is(gerr, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("topic not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(gerr.Error()))
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// Delete handles DELETE /topics/{id}.
func (h *Topics) Delete(w http.ResponseWriter, r *http.Request) {
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
	if derr := h.Topics.Delete(r.Context(), id, uid); derr != nil {
		if errors.Is(derr, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("topic not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(derr.Error()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Pause handles POST /topics/{id}/pause.
func (h *Topics) Pause(w http.ResponseWriter, r *http.Request) {
	h.setStatus(w, r, domain.TopicStatusPaused)
}

// Resume handles POST /topics/{id}/resume.
func (h *Topics) Resume(w http.ResponseWriter, r *http.Request) {
	h.setStatus(w, r, domain.TopicStatusActive)
}

func (h *Topics) setStatus(w http.ResponseWriter, r *http.Request, status domain.TopicStatus) {
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
	if uerr := h.Topics.UpdateStatus(r.Context(), id, uid, status); uerr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(uerr.Error()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ------------------------------------------------------------

func currentUserID(r *http.Request) (uuid.UUID, *problem.Error) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		return uuid.Nil, problem.ErrUnauthorized("no claims")
	}
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		return uuid.Nil, problem.ErrUnauthorized("bad claims")
	}
	return uid, nil
}
