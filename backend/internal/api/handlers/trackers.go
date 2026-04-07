package handlers

import (
	"net/http"
	"strings"

	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/problem"
)

// Trackers handles /trackers/* — capability discovery for the AddTopic
// form. The frontend pastes a URL, debounces, then calls /match to
// learn what optional fields the tracker supports (quality picker,
// episode filter, credentials requirement, etc.).
type Trackers struct {
	BaseURL string
}

// trackerMatch is the response shape for GET /api/v1/trackers/match.
type trackerMatch struct {
	TrackerName           string   `json:"tracker_name"`
	DisplayName           string   `json:"display_name"`
	Qualities             []string `json:"qualities,omitempty"`
	DefaultQuality        string   `json:"default_quality,omitempty"`
	SupportsEpisodeFilter bool     `json:"supports_episode_filter"`
	RequiresCredentials   bool     `json:"requires_credentials"`
	UsesCloudflare        bool     `json:"uses_cloudflare"`
}

// Match handles GET /api/v1/trackers/match?url=<encoded>.
//
// Returns the tracker plugin that claims the URL plus a snapshot of
// every optional capability the plugin implements. Returns 404 with a
// problem document if no plugin matches.
func (h *Trackers) Match(w http.ResponseWriter, r *http.Request) {
	rawURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if rawURL == "" {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("url query parameter is required"))
		return
	}

	t := registry.FindTrackerForURL(rawURL)
	if t == nil {
		problem.Write(w, r, h.BaseURL, problem.ErrNotFound("no tracker plugin matches this URL"))
		return
	}

	out := trackerMatch{
		TrackerName: t.Name(),
		DisplayName: t.DisplayName(),
	}
	if q, ok := t.(registry.WithQuality); ok {
		out.Qualities = q.Qualities()
		out.DefaultQuality = q.DefaultQuality()
	}
	if ef, ok := t.(registry.WithEpisodeFilter); ok {
		out.SupportsEpisodeFilter = ef.SupportsEpisodeFilter()
	}
	if _, ok := t.(registry.WithCredentials); ok {
		out.RequiresCredentials = true
	}
	if cf, ok := t.(registry.WithCloudflare); ok {
		out.UsesCloudflare = cf.UsesCloudflare()
	}

	writeJSON(w, http.StatusOK, out)
}
