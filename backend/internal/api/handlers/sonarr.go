package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/audit"
	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/problem"
	"github.com/artyomsv/marauder/backend/internal/sonarr"
)

// sonarrSettingsStore is the consumer seam over *repo.Settings.
type sonarrSettingsStore interface {
	GetSonarr(ctx context.Context, master *crypto.MasterKey) (*domain.SonarrConfig, error)
	UpsertSonarr(ctx context.Context, master *crypto.MasterKey, c *domain.SonarrConfig) error
}

// Sonarr is the admin-only handler group for the Sonarr integration config
// (/system/sonarr). The API key is never returned — callers learn only
// whether one is set (api_key_set).
type Sonarr struct {
	Settings sonarrSettingsStore
	Master   *crypto.MasterKey
	Audit    *audit.Logger
	Log      zerolog.Logger
	Timeout  time.Duration
	BaseURL  string
}

type sonarrConfigView struct {
	Enabled            bool     `json:"enabled"`
	SonarrURL          string   `json:"sonarr_url"`
	APIKeySet          bool     `json:"api_key_set"`
	PollIntervalSec    int      `json:"poll_interval_sec"`
	AllowedTrackers    []string `json:"allowed_trackers"`
	DefaultClientID    *string  `json:"default_client_id"`
	DefaultCategory    string   `json:"default_category"`
	DefaultDownloadDir string   `json:"default_download_dir"`
	UpdateExisting     bool     `json:"update_existing"`
	LastSeenAt         *string  `json:"last_seen_at"`
}

type updateSonarrReq struct {
	Enabled            bool     `json:"enabled"`
	SonarrURL          string   `json:"sonarr_url"`
	APIKey             string   `json:"api_key"` // "" = keep existing
	PollIntervalSec    int      `json:"poll_interval_sec"`
	AllowedTrackers    []string `json:"allowed_trackers"`
	DefaultClientID    *string  `json:"default_client_id"` // null/"" = none
	DefaultCategory    string   `json:"default_category"`
	DefaultDownloadDir string   `json:"default_download_dir"`
	UpdateExisting     bool     `json:"update_existing"`
}

type testSonarrReq struct {
	SonarrURL string `json:"sonarr_url"`
	APIKey    string `json:"api_key"`
}

// Get handles GET /system/sonarr (admin).
func (h *Sonarr) Get(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.Settings.GetSonarr(r.Context(), h.Master)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, toSonarrView(cfg))
}

// Update handles PUT /system/sonarr (admin). The saving admin becomes the
// owner of auto-created topics. An empty api_key preserves the stored one.
func (h *Sonarr) Update(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}

	var req updateSonarrReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	req.SonarrURL = strings.TrimSpace(req.SonarrURL)
	if req.Enabled && req.SonarrURL == "" {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("sonarr_url is required when enabled"))
		return
	}
	if req.SonarrURL != "" && !validHTTPURL(req.SonarrURL) {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("sonarr_url must be a valid http(s) URL"))
		return
	}
	if req.PollIntervalSec < 0 {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("poll_interval_sec must be >= 0"))
		return
	}
	clientID, cerr := parseOptionalUUID(req.DefaultClientID)
	if cerr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("default_client_id must be a UUID"))
		return
	}

	cfg := &domain.SonarrConfig{
		Enabled:            req.Enabled,
		URL:                req.SonarrURL,
		APIKey:             req.APIKey,
		PollIntervalSec:    req.PollIntervalSec,
		AllowedTrackers:    req.AllowedTrackers,
		DefaultClientID:    clientID,
		DefaultCategory:    strings.TrimSpace(req.DefaultCategory),
		DefaultDownloadDir: strings.TrimSpace(req.DefaultDownloadDir),
		UpdateExisting:     req.UpdateExisting,
		OwnerUserID:        &uid,
	}
	if err := h.Settings.UpsertSonarr(r.Context(), h.Master, cfg); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	if h.Audit != nil {
		h.Audit.Generic(&uid, "sonarr.config.update", "settings", "1", "success",
			map[string]any{"enabled": req.Enabled, "url": req.SonarrURL}) // never the key
	}

	updated, err := h.Settings.GetSonarr(r.Context(), h.Master)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, toSonarrView(updated))
}

// Test handles POST /system/sonarr/test (admin). Verifies connectivity using
// the supplied url/key, falling back to the stored values for either.
func (h *Sonarr) Test(w http.ResponseWriter, r *http.Request) {
	uid, _ := currentUserID(r)

	var req testSonarrReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	targetURL := strings.TrimSpace(req.SonarrURL)
	apiKey := req.APIKey
	if targetURL == "" || apiKey == "" {
		stored, err := h.Settings.GetSonarr(r.Context(), h.Master)
		if err != nil {
			problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
			return
		}
		if targetURL == "" {
			targetURL = stored.URL
		}
		if apiKey == "" {
			apiKey = stored.APIKey
		}
	}
	if targetURL == "" || apiKey == "" {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("sonarr_url and api_key are required"))
		return
	}

	// The client already enforces h.Timeout via http.Client.Timeout, so no
	// extra context deadline is needed. The raw error is logged server-side
	// but NOT echoed to the client — a connection error can leak internal
	// network details (resolved host/IP), and a generic, actionable message
	// is enough for the admin who supplied the URL.
	status, err := sonarr.NewClient(targetURL, apiKey, h.Timeout).SystemStatus(r.Context())
	if err != nil {
		h.Log.Warn().Err(err).Str("url", targetURL).Msg("sonarr connection test failed")
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable(
			"could not connect to Sonarr — verify the URL is reachable and the API key is correct"))
		return
	}
	if h.Audit != nil {
		uidPtr := &uid
		if uid == uuid.Nil {
			uidPtr = nil
		}
		h.Audit.Generic(uidPtr, "sonarr.config.test", "settings", "1", "success", nil)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"version":  status.Version,
		"app_name": status.AppName,
	})
}

func toSonarrView(c *domain.SonarrConfig) sonarrConfigView {
	v := sonarrConfigView{
		Enabled:            c.Enabled,
		SonarrURL:          c.URL,
		APIKeySet:          c.APIKey != "",
		PollIntervalSec:    c.PollIntervalSec,
		AllowedTrackers:    c.AllowedTrackers,
		DefaultCategory:    c.DefaultCategory,
		DefaultDownloadDir: c.DefaultDownloadDir,
		UpdateExisting:     c.UpdateExisting,
	}
	if v.AllowedTrackers == nil {
		v.AllowedTrackers = []string{}
	}
	if c.DefaultClientID != nil {
		s := c.DefaultClientID.String()
		v.DefaultClientID = &s
	}
	if c.LastSeenAt != nil {
		s := c.LastSeenAt.UTC().Format(time.RFC3339)
		v.LastSeenAt = &s
	}
	return v
}

func parseOptionalUUID(s *string) (*uuid.UUID, error) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, nil
	}
	id, err := uuid.Parse(strings.TrimSpace(*s))
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func validHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
