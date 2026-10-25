package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/artyomsv/marauder/backend/internal/audit"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/problem"
)

// settingsStore is the consumer seam over *repo.Settings.
type settingsStore interface {
	Get(ctx context.Context) (*repo.InstanceSettings, error)
	SetDefaultFollowTrackerTitle(ctx context.Context, v bool) error
}

// Settings is the handler group for the instance-wide defaults at
// /system/settings: the values new topics are created with when their
// creator sends no explicit choice. Every authenticated user may read them
// (the topic form seeds its toggle from the response); only admins write,
// which the router enforces. A stored default is materialised onto the topic
// at create time, so editing it never changes an existing topic.
type Settings struct {
	Store   settingsStore
	BaseURL string
	Audit   *audit.Logger
}

type settingsView struct {
	DefaultFollowTrackerTitle bool `json:"default_follow_tracker_title"`
}

// Get handles GET /system/settings.
func (h *Settings) Get(w http.ResponseWriter, r *http.Request) {
	s, err := h.Store.Get(r.Context())
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("settings: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, settingsView{DefaultFollowTrackerTitle: s.DefaultFollowTrackerTitle})
}

type updateSettingsReq struct {
	// Pointer so an omitted field preserves the stored value, matching the
	// other settings-bearing endpoints.
	DefaultFollowTrackerTitle *bool `json:"default_follow_tracker_title,omitempty"`
}

// Update handles PUT /system/settings (admin only).
func (h *Settings) Update(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	var req updateSettingsReq
	if jerr := json.NewDecoder(r.Body).Decode(&req); jerr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	if req.DefaultFollowTrackerTitle == nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("no settings to update"))
		return
	}
	if err := h.Store.SetDefaultFollowTrackerTitle(r.Context(), *req.DefaultFollowTrackerTitle); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("settings: "+err.Error()))
		return
	}
	if h.Audit != nil {
		h.Audit.Generic(&uid, "settings_update", "settings", "default_follow_tracker_title", "success",
			map[string]any{"default_follow_tracker_title": *req.DefaultFollowTrackerTitle})
	}
	writeJSON(w, http.StatusOK, settingsView{DefaultFollowTrackerTitle: *req.DefaultFollowTrackerTitle})
}
