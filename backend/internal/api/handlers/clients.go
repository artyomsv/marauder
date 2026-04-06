package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

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
	BaseURL string
}

type clientView struct {
	ID           string          `json:"id"`
	ClientName   string          `json:"client_name"`
	DisplayName  string          `json:"display_name"`
	IsDefault    bool            `json:"is_default"`
	Config       json.RawMessage `json:"config,omitempty"`
	CreatedAt    string          `json:"created_at"`
	UpdatedAt    string          `json:"updated_at"`
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
