package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/audit"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/metrics"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/problem"
)

// categoriesFetchTimeout bounds the GET /clients/{id}/categories upstream call
// (qBittorrent login + list) so one slow client can't hold a request open for
// the plugin client's full ~30s worst case. Mirrors the topic-status path.
const categoriesFetchTimeout = 10 * time.Second

// clientStore is the persistence seam for the Clients handler, satisfied by
// *repo.Clients. Defined at the consumer so the handler is unit-testable with
// a fake store (no DB).
type clientStore interface {
	ListForUser(ctx context.Context, userID uuid.UUID) ([]*domain.Client, error)
	Create(ctx context.Context, c *domain.Client) (*domain.Client, error)
	GetByID(ctx context.Context, id, userID uuid.UUID) (*domain.Client, error)
	Update(ctx context.Context, id, userID uuid.UUID, displayName string, isDefault bool, configEnc, configNonce []byte) error
	Delete(ctx context.Context, id, userID uuid.UUID) error
}

// cryptor is the encryption seam, satisfied by *crypto.MasterKey.
type cryptor interface {
	Encrypt(plaintext []byte) (ct, nonce []byte, err error)
	Decrypt(ct, nonce []byte) ([]byte, error)
}

// Clients handles /clients.
type Clients struct {
	Clients clientStore
	Master  cryptor
	Audit   *audit.Logger
	Log     zerolog.Logger
	BaseURL string
}

type clientView struct {
	ID          string          `json:"id"`
	ClientName  string          `json:"client_name"`
	DisplayName string          `json:"display_name"`
	IsDefault   bool            `json:"is_default"`
	Config      json.RawMessage `json:"config,omitempty"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

func toView(c *domain.Client, cfg json.RawMessage) clientView {
	return clientView{
		ID:          c.ID.String(),
		ClientName:  c.ClientName,
		DisplayName: c.DisplayName,
		IsDefault:   c.IsDefault,
		Config:      cfg,
		CreatedAt:   c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:   c.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

// List handles GET /clients.
func (h *Clients) List(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	items, err := h.Clients.ListForUser(r.Context(), uid)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	out := make([]clientView, 0, len(items))
	for _, c := range items {
		// List view never includes config (it holds secrets)
		out = append(out, toView(c, nil))
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": out})
}

type createClientReq struct {
	ClientName  string          `json:"client_name"`
	DisplayName string          `json:"display_name"`
	IsDefault   bool            `json:"is_default"`
	Config      json.RawMessage `json:"config"`
}

// Create handles POST /clients.
func (h *Clients) Create(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}

	var req createClientReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	if req.ClientName == "" || req.DisplayName == "" || len(req.Config) == 0 {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("client_name, display_name, and config are required"))
		return
	}

	plugin := registry.GetClient(req.ClientName)
	if plugin == nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("unknown client plugin: "+req.ClientName))
		return
	}

	// Validate the config by calling the plugin's Test method.
	if err := plugin.Test(r.Context(), req.Config); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("client test failed: "+err.Error()))
		return
	}

	// Encrypt the config JSON before storing.
	enc, nonce, err := h.Master.Encrypt(req.Config)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("encrypt config: "+err.Error()))
		return
	}

	created, cerr := h.Clients.Create(r.Context(), &domain.Client{
		UserID:      uid,
		ClientName:  req.ClientName,
		DisplayName: req.DisplayName,
		ConfigEnc:   enc,
		ConfigNonce: nonce,
		IsDefault:   req.IsDefault,
	})
	if cerr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("create client: "+cerr.Error()))
		return
	}
	writeJSON(w, http.StatusCreated, toView(created, nil))
}

// Get handles GET /clients/{id}. Returns the client row including the
// decrypted config blob, scoped to the calling user. Used by the
// frontend Edit Client form.
//
// Threat model note: the `config_enc` column at rest is encrypted to
// protect against database-file compromise. Returning the decrypted
// config to the legitimate authenticated owner over an HTTPS-secured
// session is consistent with that model. Every read is audit-logged.
func (h *Clients) Get(w http.ResponseWriter, r *http.Request) {
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
	c, err := h.Clients.GetByID(r.Context(), id, uid)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("client not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	raw, err := h.Master.Decrypt(c.ConfigEnc, c.ConfigNonce)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("decrypt config: "+err.Error()))
		return
	}
	if h.Audit != nil {
		ip, ua := audit.FromRequest(r)
		h.Audit.Generic(&uid, "client_config_read", "client", c.ID.String(), "success",
			map[string]any{"client_name": c.ClientName, "ip": ip, "ua": ua})
	}
	writeJSON(w, http.StatusOK, toView(c, raw))
}

// Update handles PUT /clients/{id}. Body shape is identical to Create.
// The plugin's Test method is called before persistence so a bad
// config never overwrites a good one.
func (h *Clients) Update(w http.ResponseWriter, r *http.Request) {
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

	var req createClientReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	if req.DisplayName == "" || len(req.Config) == 0 {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("display_name and config are required"))
		return
	}

	// Make sure the client exists and belongs to the user, and capture
	// its plugin name (we don't allow swapping plugin types via PUT —
	// the user would delete and re-add for that).
	existing, err := h.Clients.GetByID(r.Context(), id, uid)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("client not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	if req.ClientName != "" && req.ClientName != existing.ClientName {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("client_name cannot change; delete and re-add to switch plugin"))
		return
	}

	plugin := registry.GetClient(existing.ClientName)
	if plugin == nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("client plugin not installed"))
		return
	}
	if err := plugin.Test(r.Context(), req.Config); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("client test failed: "+err.Error()))
		return
	}
	enc, nonce, err := h.Master.Encrypt(req.Config)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("encrypt config: "+err.Error()))
		return
	}
	if err := h.Clients.Update(r.Context(), id, uid, req.DisplayName, req.IsDefault, enc, nonce); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("client not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("update client: "+err.Error()))
		return
	}

	if h.Audit != nil {
		ip, ua := audit.FromRequest(r)
		h.Audit.Generic(&uid, "client_update", "client", id.String(), "success",
			map[string]any{"client_name": existing.ClientName, "ip": ip, "ua": ua})
	}

	updated, err := h.Clients.GetByID(r.Context(), id, uid)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, toView(updated, nil))
}

// Delete handles DELETE /clients/{id}.
func (h *Clients) Delete(w http.ResponseWriter, r *http.Request) {
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
	if err := h.Clients.Delete(r.Context(), id, uid); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("client not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Categories handles GET /clients/{id}/categories. It returns the categories
// the client already knows about (qBittorrent), so the AddTopic form can offer
// them as suggestions while still accepting free-text. The response shape is
// {"supported": bool, "categories": [string]}.
//
// Fail-open: a client whose plugin can't list categories yields
// supported:false, and a transient fetch error (client unreachable, bad creds)
// yields supported:true with an empty list — in both cases the category field
// simply degrades to plain free-text entry, never a hard error. Category is a
// path segment in Marauder (see registry.EffectiveDownloadDir); this list only
// helps pick a value, it does not constrain it.
func (h *Clients) Categories(w http.ResponseWriter, r *http.Request) {
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
	c, err := h.Clients.GetByID(r.Context(), id, uid)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("client not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}

	plugin := registry.GetClient(c.ClientName)

	// Only decrypt the config when the plugin can actually list categories —
	// an unsupported client never needs the secret blob.
	if _, ok := plugin.(registry.WithCategories); !ok {
		writeJSON(w, http.StatusOK, categoriesView(false, nil))
		return
	}
	raw, err := h.Master.Decrypt(c.ConfigEnc, c.ConfigNonce)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("decrypt config: "+err.Error()))
		return
	}

	// Bound the upstream fetch so a slow client can't hold the request open.
	ctx, cancel := context.WithTimeout(r.Context(), categoriesFetchTimeout)
	defer cancel()
	logger := h.Log.With().Str("client_id", id.String()).Str("user_id", uid.String()).Logger()
	supported, names := resolveCategories(ctx, plugin, raw, logger)
	writeJSON(w, http.StatusOK, categoriesView(supported, names))
}

// resolveCategories asks a client plugin for its category list. It is the
// testable core of the Categories handler, isolated from auth/DB/decryption.
//
// Returns (supported, names). A plugin that does not implement
// registry.WithCategories is unsupported (free-text fallback). A plugin that
// supports listing but errors fails open: supported=true with a nil list, so
// the field degrades to free-text rather than the request failing — a warning
// is logged and a metric incremented so the silent degradation is observable.
func resolveCategories(ctx context.Context, plugin registry.Client, raw []byte, logger zerolog.Logger) (bool, []string) {
	lister, ok := plugin.(registry.WithCategories)
	if plugin == nil || !ok {
		return false, nil
	}
	names, err := lister.Categories(ctx, raw)
	if err != nil {
		// The dropdown is a convenience; degrade to free-text rather than
		// failing the request.
		metrics.ClientCategoriesFailOpenTotal.WithLabelValues(plugin.Name()).Inc()
		logger.Warn().Err(err).Str("client_name", plugin.Name()).
			Msg("list client categories failed; degrading to free-text")
		return true, nil
	}
	return true, names
}

// categoriesView builds the GET /clients/{id}/categories response body,
// ensuring categories is always a non-nil JSON array.
func categoriesView(supported bool, names []string) map[string]any {
	if names == nil {
		names = []string{}
	}
	return map[string]any{"supported": supported, "categories": names}
}

// Test handles POST /clients/{id}/test — tests the stored config without
// exposing it.
func (h *Clients) Test(w http.ResponseWriter, r *http.Request) {
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
	c, err := h.Clients.GetByID(r.Context(), id, uid)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrNotFound("client not found"))
		return
	}
	plugin := registry.GetClient(c.ClientName)
	if plugin == nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("client plugin not installed"))
		return
	}
	raw, err := h.Master.Decrypt(c.ConfigEnc, c.ConfigNonce)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("decrypt config: "+err.Error()))
		return
	}
	if err := plugin.Test(r.Context(), raw); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("test failed: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
