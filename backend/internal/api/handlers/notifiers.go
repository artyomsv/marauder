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

// Notifiers handles /notifiers.
type Notifiers struct {
	Notifiers *repo.Notifiers
	Master    *crypto.MasterKey
	BaseURL   string
}

type notifierView struct {
	ID           string   `json:"id"`
	NotifierName string   `json:"notifier_name"`
	DisplayName  string   `json:"display_name"`
	Events       []string `json:"events"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

func notifierToView(n *domain.Notifier) notifierView {
	return notifierView{
		ID:           n.ID.String(),
		NotifierName: n.NotifierName,
		DisplayName:  n.DisplayName,
		Events:       n.Events,
		CreatedAt:    n.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:    n.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

// List handles GET /notifiers.
func (h *Notifiers) List(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	items, err := h.Notifiers.ListForUser(r.Context(), uid)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	out := make([]notifierView, 0, len(items))
	for _, n := range items {
		out = append(out, notifierToView(n))
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifiers": out})
}

type createNotifierReq struct {
	NotifierName string          `json:"notifier_name"`
	DisplayName  string          `json:"display_name"`
	Events       []string        `json:"events"`
	Config       json.RawMessage `json:"config"`
}

// Create handles POST /notifiers.
func (h *Notifiers) Create(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	var req createNotifierReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	if req.NotifierName == "" || req.DisplayName == "" || len(req.Config) == 0 {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("notifier_name, display_name, and config are required"))
		return
	}
	plugin := registry.GetNotifier(req.NotifierName)
	if plugin == nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("unknown notifier plugin: "+req.NotifierName))
		return
	}
	if err := plugin.Test(r.Context(), req.Config); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("notifier test failed: "+err.Error()))
		return
	}
	enc, nonce, err := h.Master.Encrypt(req.Config)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("encrypt: "+err.Error()))
		return
	}
	events := req.Events
	if len(events) == 0 {
		events = []string{"updated", "error"}
	}
	created, cerr := h.Notifiers.Create(r.Context(), &domain.Notifier{
		UserID:       uid,
		NotifierName: req.NotifierName,
		DisplayName:  req.DisplayName,
		ConfigEnc:    enc,
		ConfigNonce:  nonce,
		Events:       events,
	})
	if cerr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("create notifier: "+cerr.Error()))
		return
	}
	writeJSON(w, http.StatusCreated, notifierToView(created))
}

// Delete handles DELETE /notifiers/{id}.
func (h *Notifiers) Delete(w http.ResponseWriter, r *http.Request) {
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
	if err := h.Notifiers.Delete(r.Context(), id, uid); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("notifier not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Test handles POST /notifiers/{id}/test.
func (h *Notifiers) Test(w http.ResponseWriter, r *http.Request) {
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
	n, err := h.Notifiers.GetByID(r.Context(), id, uid)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrNotFound("notifier not found"))
		return
	}
	plugin := registry.GetNotifier(n.NotifierName)
	if plugin == nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("notifier plugin not installed"))
		return
	}
	raw, err := h.Master.Decrypt(n.ConfigEnc, n.ConfigNonce)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("decrypt: "+err.Error()))
		return
	}
	if err := plugin.Test(r.Context(), raw); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("test failed: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
