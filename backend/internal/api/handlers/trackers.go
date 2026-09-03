package handlers

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/problem"
	"github.com/artyomsv/marauder/backend/internal/topics"
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

	searchInFlight  sync.Map // userID -> struct{}; per-user single-flight search gate
	previewInFlight sync.Map // userID -> struct{}; per-user single-flight preview gate
	searchLast      sync.Map // userID -> time.Time; sequential-search cooldown gate
}

const (
	// previewTimeout bounds one metadata fetch for GET /trackers/preview.
	previewTimeout = 15 * time.Second
	// previewWarmTimeout bounds the credential warm that may precede it.
	previewWarmTimeout = 5 * time.Second
)

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
	// Pass the user's stored credential (warmed Verify-first/Login-on-miss)
	// rather than nil. Most trackers expose a title and poster publicly and
	// ignore it, but a fully login-gated one — Toloka serves a guest a stub
	// with an empty <title> — would otherwise resolve nothing at all, and
	// the preview would stay blank for exactly the users who do have an
	// account. Anonymous stays the fallback: no stored credential, or a
	// login that fails, simply yields nil here.
	uid, uerr := currentUserID(r)
	var creds *domain.TrackerCredential
	if uerr == nil {
		// Per-user single-flight, same shape as /trackers/search. The form
		// fires a preview on every settled URL edit, so a user pasting,
		// correcting and re-pasting queues several — each of which may warm a
		// session by logging in. Serialising them keeps one user from
		// hammering a tracker's login form from one browser tab.
		if _, busy := h.previewInFlight.LoadOrStore(uid, struct{}{}); busy {
			problem.Write(w, r, h.BaseURL, problem.ErrTooManyRequests("a preview is already running; wait for it to finish"))
			return
		}
		defer h.previewInFlight.Delete(uid)

		// The warm gets its own budget rather than a slice of the fetch's:
		// warming is a login round-trip against the same tracker the fetch is
		// about to hit, so one shared deadline lets a slow login consume it
		// all and leave ResolveMetadata to fail instantly on a dead context.
		wctx, wcancel := context.WithTimeout(r.Context(), previewWarmTimeout)
		// Dropping the two diagnostic returns: "no account stored" and "an
		// account that would not warm" both mean "preview anonymously" here.
		// The preview is cosmetic and never an error, so neither is reportable.
		creds, _, _ = warmCredentials(wctx, h.Creds, h.Master, uid, t)
		wcancel()
	}
	ctx, cancel := context.WithTimeout(r.Context(), previewTimeout)
	defer cancel()
	meta, err := wm.ResolveMetadata(ctx, rawURL, creds)
	if err != nil || meta == nil {
		// Fail-open: the preview is cosmetic, never a hard error for the user.
		writeJSON(w, http.StatusOK, registry.Metadata{})
		return
	}
	// The poster is scraped off a tracker page and the browser renders it
	// straight into an <img src>, so apply the same scheme allowlist the
	// create path applies before it stores one.
	meta.ImageURL = topics.SafeImageURL(meta.ImageURL)
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
