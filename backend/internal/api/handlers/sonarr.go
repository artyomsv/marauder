package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/audit"
	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/problem"
	"github.com/artyomsv/marauder/backend/internal/sonarr"
)

// sonarrInstancesStore is the consumer seam over *repo.SonarrInstances.
type sonarrInstancesStore interface {
	List(ctx context.Context, master *crypto.MasterKey) ([]domain.SonarrInstance, error)
	Get(ctx context.Context, master *crypto.MasterKey, id uuid.UUID) (*domain.SonarrInstance, error)
	Create(ctx context.Context, master *crypto.MasterKey, inst domain.SonarrInstance) (*domain.SonarrInstance, error)
	Update(ctx context.Context, master *crypto.MasterKey, id uuid.UUID, inst domain.SonarrInstance) (*domain.SonarrInstance, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// Sonarr is the admin-only handler group for the Sonarr integration
// (/system/sonarr/instances). The API key is never returned — callers learn
// only whether one is set (api_key_set).
type Sonarr struct {
	Instances sonarrInstancesStore
	Master    *crypto.MasterKey
	Audit     *audit.Logger
	Log       zerolog.Logger
	Timeout   time.Duration
	BaseURL   string
}

type instanceView struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
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
	CreatedAt          string   `json:"created_at"`
	UpdatedAt          string   `json:"updated_at"`
}

type instanceReq struct {
	Name               string   `json:"name"`
	Enabled            bool     `json:"enabled"`
	SonarrURL          string   `json:"sonarr_url"`
	APIKey             string   `json:"api_key"` // "" = keep existing (on update)
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

// List handles GET /system/sonarr/instances (admin).
func (h *Sonarr) List(w http.ResponseWriter, r *http.Request) {
	instances, err := h.Instances.List(r.Context(), h.Master)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	views := make([]instanceView, 0, len(instances))
	for i := range instances {
		views = append(views, toInstanceView(&instances[i]))
	}
	writeJSON(w, http.StatusOK, views)
}

// Get handles GET /system/sonarr/instances/{id} (admin).
func (h *Sonarr) Get(w http.ResponseWriter, r *http.Request) {
	id, ierr := uuid.Parse(chi.URLParam(r, "id"))
	if ierr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid instance id"))
		return
	}
	inst, err := h.Instances.Get(r.Context(), h.Master, id)
	if err != nil {
		problem.Write(w, r, h.BaseURL, sonarrLookupErr(err))
		return
	}
	writeJSON(w, http.StatusOK, toInstanceView(inst))
}

// Create handles POST /system/sonarr/instances (admin). The saving admin
// becomes the owner of topics this instance auto-creates.
func (h *Sonarr) Create(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	inst, perr := h.decodeInstance(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	inst.OwnerUserID = &uid

	created, err := h.Instances.Create(r.Context(), h.Master, inst)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	h.audit(&uid, "sonarr.instance.create", created.ID.String(),
		map[string]any{"name": created.Name, "enabled": created.Enabled, "url": created.URL})
	writeJSON(w, http.StatusCreated, toInstanceView(created))
}

// Update handles PUT /system/sonarr/instances/{id} (admin). An empty api_key
// preserves the stored one; the owner is fixed at create time.
func (h *Sonarr) Update(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	id, ierr := uuid.Parse(chi.URLParam(r, "id"))
	if ierr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid instance id"))
		return
	}
	inst, perr := h.decodeInstance(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}

	updated, err := h.Instances.Update(r.Context(), h.Master, id, inst)
	if err != nil {
		problem.Write(w, r, h.BaseURL, sonarrLookupErr(err))
		return
	}
	h.audit(&uid, "sonarr.instance.update", id.String(),
		map[string]any{"name": updated.Name, "enabled": updated.Enabled, "url": updated.URL})
	writeJSON(w, http.StatusOK, toInstanceView(updated))
}

// Delete handles DELETE /system/sonarr/instances/{id} (admin).
func (h *Sonarr) Delete(w http.ResponseWriter, r *http.Request) {
	uid, _ := currentUserID(r)
	id, ierr := uuid.Parse(chi.URLParam(r, "id"))
	if ierr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid instance id"))
		return
	}
	if err := h.Instances.Delete(r.Context(), id); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	h.audit(nilIfNil(uid), "sonarr.instance.delete", id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

// TestExisting handles POST /system/sonarr/instances/{id}/test (admin),
// verifying connectivity using the instance's stored url + key.
func (h *Sonarr) TestExisting(w http.ResponseWriter, r *http.Request) {
	id, ierr := uuid.Parse(chi.URLParam(r, "id"))
	if ierr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid instance id"))
		return
	}
	inst, err := h.Instances.Get(r.Context(), h.Master, id)
	if err != nil {
		problem.Write(w, r, h.BaseURL, sonarrLookupErr(err))
		return
	}
	h.runTest(w, r, inst.URL, inst.APIKey)
}

// Test handles POST /system/sonarr/test (admin), an ad-hoc connectivity check
// for the add form before an instance exists. url + key must be supplied.
func (h *Sonarr) Test(w http.ResponseWriter, r *http.Request) {
	var req testSonarrReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	h.runTest(w, r, strings.TrimSpace(req.SonarrURL), req.APIKey)
}

// runTest performs the Sonarr connectivity check and writes the result. The raw
// error is logged server-side but NOT echoed to the client — a connection error
// can leak internal network details (resolved host/IP).
func (h *Sonarr) runTest(w http.ResponseWriter, r *http.Request, targetURL, apiKey string) {
	if targetURL == "" || apiKey == "" {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("sonarr_url and api_key are required"))
		return
	}
	status, err := sonarr.NewClient(targetURL, apiKey, h.Timeout).SystemStatus(r.Context())
	if err != nil {
		h.Log.Warn().Err(err).Str("url", targetURL).Msg("sonarr connection test failed")
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable(
			"could not connect to Sonarr — verify the URL is reachable and the API key is correct"))
		return
	}
	uid, _ := currentUserID(r)
	h.audit(nilIfNil(uid), "sonarr.instance.test", "", nil)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"version":  status.Version,
		"app_name": status.AppName,
	})
}

// decodeInstance decodes and validates an instance request body into a domain
// instance (owner not set here).
func (h *Sonarr) decodeInstance(r *http.Request) (domain.SonarrInstance, *problem.Error) {
	var req instanceReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return domain.SonarrInstance{}, problem.ErrBadRequest("invalid JSON")
	}
	req.Name = strings.TrimSpace(req.Name)
	req.SonarrURL = strings.TrimSpace(req.SonarrURL)
	if req.Name == "" {
		return domain.SonarrInstance{}, problem.ErrUnprocessable("name is required")
	}
	if req.Enabled && req.SonarrURL == "" {
		return domain.SonarrInstance{}, problem.ErrUnprocessable("sonarr_url is required when enabled")
	}
	if req.SonarrURL != "" && !validHTTPURL(req.SonarrURL) {
		return domain.SonarrInstance{}, problem.ErrUnprocessable("sonarr_url must be a valid http(s) URL")
	}
	if req.PollIntervalSec < 0 {
		return domain.SonarrInstance{}, problem.ErrUnprocessable("poll_interval_sec must be >= 0")
	}
	clientID, cerr := parseOptionalUUID(req.DefaultClientID)
	if cerr != nil {
		return domain.SonarrInstance{}, problem.ErrUnprocessable("default_client_id must be a UUID")
	}
	return domain.SonarrInstance{
		Name:               req.Name,
		Enabled:            req.Enabled,
		URL:                req.SonarrURL,
		APIKey:             req.APIKey,
		PollIntervalSec:    req.PollIntervalSec,
		AllowedTrackers:    req.AllowedTrackers,
		DefaultClientID:    clientID,
		DefaultCategory:    strings.TrimSpace(req.DefaultCategory),
		DefaultDownloadDir: strings.TrimSpace(req.DefaultDownloadDir),
		UpdateExisting:     req.UpdateExisting,
	}, nil
}

func (h *Sonarr) audit(uid *uuid.UUID, action, resourceID string, meta map[string]any) {
	if h.Audit == nil {
		return
	}
	h.Audit.Generic(uid, action, "sonarr_instance", resourceID, "success", meta)
}

func toInstanceView(c *domain.SonarrInstance) instanceView {
	v := instanceView{
		ID:                 c.ID.String(),
		Name:               c.Name,
		Enabled:            c.Enabled,
		SonarrURL:          c.URL,
		APIKeySet:          c.APIKey != "",
		PollIntervalSec:    c.PollIntervalSec,
		AllowedTrackers:    c.AllowedTrackers,
		DefaultCategory:    c.DefaultCategory,
		DefaultDownloadDir: c.DefaultDownloadDir,
		UpdateExisting:     c.UpdateExisting,
		CreatedAt:          c.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:          c.UpdatedAt.UTC().Format(time.RFC3339),
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

// sonarrLookupErr maps a repo lookup failure to a 404 (not found) or 500.
func sonarrLookupErr(err error) *problem.Error {
	if errors.Is(err, repo.ErrNotFound) {
		return problem.ErrNotFound("sonarr instance not found")
	}
	return problem.ErrInternal(err.Error())
}

func nilIfNil(uid uuid.UUID) *uuid.UUID {
	if uid == uuid.Nil {
		return nil
	}
	return &uid
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
