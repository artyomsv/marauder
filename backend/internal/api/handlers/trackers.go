package handlers

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/problem"
)

// Trackers handles /trackers/* — capability discovery for the AddTopic
// form (the frontend pastes a URL, debounces, then calls /match to learn
// what optional fields the tracker supports) plus the cross-tracker
// release search (issue #129).
type Trackers struct {
	BaseURL string
	Creds   credentialStore   // nil-safe: login-gated search degrades to anonymous when absent
	Master  *crypto.MasterKey // decrypts stored credentials for login-gated search
	// SearchBudget overrides the per-tracker search timeout (tests); zero
	// means the 15s default.
	SearchBudget time.Duration

	searchInFlight sync.Map // userID -> struct{}; per-user single-flight search gate
	searchLast     sync.Map // userID -> time.Time; sequential-search cooldown gate
}

// trackerMatch is the response shape for GET /api/v1/trackers/match.
type trackerMatch struct {
	TrackerName              string   `json:"tracker_name"`
	DisplayName              string   `json:"display_name"`
	Qualities                []string `json:"qualities,omitempty"`
	DefaultQuality           string   `json:"default_quality,omitempty"`
	SupportsEpisodeFilter    bool     `json:"supports_episode_filter"`
	RequiresCredentials      bool     `json:"requires_credentials"`
	CredentialsOptional      bool     `json:"credentials_optional"`
	SupportsInteractiveLogin bool     `json:"supports_interactive_login"`
	UsesCloudflare           bool     `json:"uses_cloudflare"`
	SupportsSeasonCatalog    bool     `json:"supports_season_catalog"`
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
		// WithCredentials alone means credentials are required. A tracker that
		// also supports anonymous download flips this to optional.
		out.RequiresCredentials = true
		if ad, ok := t.(registry.WithAnonymousDownload); ok && ad.SupportsAnonymousDownload() {
			out.RequiresCredentials = false
			out.CredentialsOptional = true
		}
	}
	if _, ok := t.(registry.WithInteractiveLogin); ok {
		out.SupportsInteractiveLogin = true
	}
	if cf, ok := t.(registry.WithCloudflare); ok {
		out.UsesCloudflare = cf.UsesCloudflare()
	}
	if _, ok := t.(registry.WithSeasonCatalog); ok {
		out.SupportsSeasonCatalog = true
	}

	writeJSON(w, http.StatusOK, out)
}

// Preview handles GET /api/v1/trackers/preview?url=<encoded> — a real title
// and poster image resolved from the page, for the AddTopic form preview.
// Returns 200 with empty fields (not an error) when the tracker can't resolve
// metadata or doesn't implement the capability, so the form degrades quietly.
func (h *Trackers) Preview(w http.ResponseWriter, r *http.Request) {
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
	wm, ok := t.(registry.WithMetadata)
	if !ok {
		writeJSON(w, http.StatusOK, registry.Metadata{})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	meta, err := wm.ResolveMetadata(ctx, rawURL, nil)
	if err != nil || meta == nil {
		// Fail-open: the preview is cosmetic, never a hard error for the user.
		writeJSON(w, http.StatusOK, registry.Metadata{})
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

// Seasons handles GET /api/v1/trackers/seasons?url=<encoded> — the
// released season/episode catalog for the matched tracker.
func (h *Trackers) Seasons(w http.ResponseWriter, r *http.Request) {
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
	sc, ok := t.(registry.WithSeasonCatalog)
	if !ok {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("tracker '"+t.Name()+"' has no season catalog"))
		return
	}
	// Bound the upstream catalog fetch (the session client also caps at
	// ~30s; this makes it cancellable on client disconnect).
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	seasons, err := sc.SeasonCatalog(ctx, rawURL)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadGateway("season catalog unavailable: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"seasons": seasons})
}
