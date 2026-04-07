package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/audit"
	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/problem"
)

// Clients handles /clients.
type Clients struct {
	Clients *repo.Clients
	Master  *crypto.MasterKey
	Audit   *audit.Logger
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
