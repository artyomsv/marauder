package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/artyomsv/marauder/backend/internal/audit"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/problem"
	"github.com/artyomsv/marauder/backend/internal/topics"
)

// settingsStore is the consumer seam over *repo.Settings.
type settingsStore interface {
	Get(ctx context.Context) (*repo.InstanceSettings, error)
	Update(ctx context.Context, patch repo.SettingsPatch) (*repo.InstanceSettings, error)
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
	DefaultReplaceOnUpdate    bool `json:"default_replace_on_update"`
	DefaultReplaceDeleteData  bool `json:"default_replace_delete_data"`
	DefaultCheckIntervalSec   int  `json:"default_check_interval_sec"`
}

func toSettingsView(s *repo.InstanceSettings) settingsView {
	return settingsView{
		DefaultFollowTrackerTitle: s.DefaultFollowTrackerTitle,
		DefaultReplaceOnUpdate:    s.DefaultReplaceOnUpdate,
		DefaultReplaceDeleteData:  s.DefaultReplaceDeleteData,
		DefaultCheckIntervalSec:   s.DefaultCheckIntervalSec,
	}
}

// Get handles GET /system/settings.
func (h *Settings) Get(w http.ResponseWriter, r *http.Request) {
	s, err := h.Store.Get(r.Context())
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("settings: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, toSettingsView(s))
}

type updateSettingsReq struct {
	// Pointer so an omitted field preserves the stored value, matching the
	// other settings-bearing endpoints.
	DefaultFollowTrackerTitle *bool `json:"default_follow_tracker_title,omitempty"`
	DefaultReplaceOnUpdate    *bool `json:"default_replace_on_update,omitempty"`
	DefaultReplaceDeleteData  *bool `json:"default_replace_delete_data,omitempty"`
	DefaultCheckIntervalSec   *int  `json:"default_check_interval_sec,omitempty"`
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
	if req.DefaultFollowTrackerTitle == nil && req.DefaultReplaceOnUpdate == nil && req.DefaultReplaceDeleteData == nil &&
		req.DefaultCheckIntervalSec == nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("no settings to update"))
		return
	}
	// Same range a topic's own interval must satisfy. The column's CHECK
	// constraint would refuse it too, but as a 500 instead of a 422.
	if req.DefaultCheckIntervalSec != nil && !topics.ValidCheckInterval(*req.DefaultCheckIntervalSec) {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable(topics.ErrCheckIntervalOutOfRange.Error()))
		return
	}
	s, err := h.Store.Update(r.Context(), repo.SettingsPatch{
		DefaultFollowTrackerTitle: req.DefaultFollowTrackerTitle,
		DefaultReplaceOnUpdate:    req.DefaultReplaceOnUpdate,
		DefaultReplaceDeleteData:  req.DefaultReplaceDeleteData,
		DefaultCheckIntervalSec:   req.DefaultCheckIntervalSec,
	})
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("settings: "+err.Error()))
		return
	}
	if h.Audit != nil {
		meta := map[string]any{}
		for key, value := range map[string]*bool{
			"default_follow_tracker_title": req.DefaultFollowTrackerTitle,
			"default_replace_on_update":    req.DefaultReplaceOnUpdate,
			"default_replace_delete_data":  req.DefaultReplaceDeleteData,
		} {
			if value != nil {
				meta[key] = *value
			}
		}
		if req.DefaultCheckIntervalSec != nil {
			meta["default_check_interval_sec"] = *req.DefaultCheckIntervalSec
		}
		resourceID := "topic_defaults"
		if req.DefaultFollowTrackerTitle != nil && len(meta) == 1 {
			resourceID = "default_follow_tracker_title"
		}
		h.Audit.Generic(&uid, "settings_update", "settings", resourceID, "success", meta)
	}
	writeJSON(w, http.StatusOK, toSettingsView(s))
}
