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

// Credentials handles /credentials — per-user, per-tracker login
// credentials. Required for forum trackers (LostFilm, RuTracker,
// Kinozal, …) that gate content behind a session cookie.
//
// Threat model: passwords are stored AES-256-GCM-encrypted in
// `tracker_credentials.secret_enc`. The handler decrypts only when
// it needs to call the plugin's Login (on POST/test/etc.) and never
// returns the plaintext to the client. The list endpoint returns
// usernames and IDs but not secrets.
type Credentials struct {
	Creds   *repo.TrackerCredentials
	Master  *crypto.MasterKey
	Audit   *audit.Logger
	BaseURL string
}

// credentialView is the safe-to-return shape — never includes the secret.
type credentialView struct {
	ID          string `json:"id"`
	TrackerName string `json:"tracker_name"`
	DisplayName string `json:"display_name"`
	Username    string `json:"username"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func toCredView(c *domain.TrackerCredential) credentialView {
	display := c.TrackerName
	if t := registry.GetTracker(c.TrackerName); t != nil {
		display = t.DisplayName()
	}
	return credentialView{
		ID:          c.ID.String(),
		TrackerName: c.TrackerName,
		DisplayName: display,
		Username:    c.Username,
		CreatedAt:   c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:   c.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

// List handles GET /credentials.
func (h *Credentials) List(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	items, err := h.Creds.ListForUser(r.Context(), uid)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	out := make([]credentialView, 0, len(items))
	for _, c := range items {
		out = append(out, toCredView(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": out})
}

type createCredentialReq struct {
	TrackerName string `json:"tracker_name"`
	Username    string `json:"username"`
	Password    string `json:"password"`
}

// Create handles POST /credentials. Validates the credential by
// calling the plugin's Login method before persisting — bad
// credentials never reach the database.
func (h *Credentials) Create(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	var req createCredentialReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	if req.TrackerName == "" || req.Username == "" || req.Password == "" {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("tracker_name, username, and password are required"))
		return
	}

	plugin := registry.GetTracker(req.TrackerName)
	if plugin == nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("unknown tracker plugin: "+req.TrackerName))
		return
	}
	wc, ok := plugin.(registry.WithCredentials)
	if !ok {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("tracker '"+req.TrackerName+"' does not require credentials"))
		return
	}

	// Plugins read the plaintext password from creds.SecretEnc in
	// memory. The persisted blob is the encrypted ciphertext.
	transient := &domain.TrackerCredential{
		UserID:      uid,
		TrackerName: req.TrackerName,
		Username:    req.Username,
		SecretEnc:   []byte(req.Password),
	}
	if err := wc.Login(r.Context(), transient); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("login failed: "+err.Error()))
		return
	}

	enc, nonce, err := h.Master.Encrypt([]byte(req.Password))
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("encrypt password: "+err.Error()))
		return
	}
	created, cerr := h.Creds.Create(r.Context(), &domain.TrackerCredential{
		UserID:      uid,
		TrackerName: req.TrackerName,
		Username:    req.Username,
		SecretEnc:   enc,
		SecretNonce: nonce,
	})
	if cerr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("create credential: "+cerr.Error()))
		return
	}
	if h.Audit != nil {
		ip, ua := audit.FromRequest(r)
		h.Audit.Generic(&uid, "credential_create", "tracker_credential", created.ID.String(), "success",
			map[string]any{"tracker_name": req.TrackerName, "ip": ip, "ua": ua})
	}
	writeJSON(w, http.StatusCreated, toCredView(created))
}

type updateCredentialReq struct {
	Username string `json:"username"`
	Password string `json:"password"` // optional — empty means "keep current"
}

// Update handles PUT /credentials/{id}. Allows username and password
// rotation. If `password` is empty, the existing secret is kept.
func (h *Credentials) Update(w http.ResponseWriter, r *http.Request) {
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
	var req updateCredentialReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	if req.Username == "" {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("username is required"))
		return
	}

	existing, err := h.Creds.GetByID(r.Context(), id, uid)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("credential not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}

	encBlob, encNonce := existing.SecretEnc, existing.SecretNonce
	if req.Password != "" {
		// Validate the new password by attempting Login first.
		plugin := registry.GetTracker(existing.TrackerName)
		if wc, ok := plugin.(registry.WithCredentials); ok && plugin != nil {
			transient := &domain.TrackerCredential{
				UserID:      uid,
				TrackerName: existing.TrackerName,
				Username:    req.Username,
				SecretEnc:   []byte(req.Password),
			}
			if err := wc.Login(r.Context(), transient); err != nil {
				problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("login failed: "+err.Error()))
				return
			}
		}
		newEnc, newNonce, err := h.Master.Encrypt([]byte(req.Password))
		if err != nil {
			problem.Write(w, r, h.BaseURL, problem.ErrInternal("encrypt password: "+err.Error()))
			return
		}
		encBlob, encNonce = newEnc, newNonce
	}

	if err := h.Creds.Update(r.Context(), id, uid, req.Username, encBlob, encNonce); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("credential not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("update credential: "+err.Error()))
		return
	}
	if h.Audit != nil {
		ip, ua := audit.FromRequest(r)
		h.Audit.Generic(&uid, "credential_update", "tracker_credential", id.String(), "success",
			map[string]any{"tracker_name": existing.TrackerName, "ip": ip, "ua": ua})
	}
	updated, err := h.Creds.GetByID(r.Context(), id, uid)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, toCredView(updated))
}

// Test handles POST /credentials/{id}/test — re-runs Login + Verify
// against the stored credential. Useful when the user suspects their
// password has been rotated externally.
func (h *Credentials) Test(w http.ResponseWriter, r *http.Request) {
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
	c, err := h.Creds.GetByID(r.Context(), id, uid)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrNotFound("credential not found"))
		return
	}
	plugin := registry.GetTracker(c.TrackerName)
	if plugin == nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("tracker plugin not installed"))
		return
	}
	wc, ok := plugin.(registry.WithCredentials)
	if !ok {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("tracker does not require credentials"))
		return
	}
	plain, err := h.Master.Decrypt(c.SecretEnc, c.SecretNonce)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("decrypt secret: "+err.Error()))
		return
	}
	transient := &domain.TrackerCredential{
		ID:          c.ID,
		UserID:      uid,
		TrackerName: c.TrackerName,
		Username:    c.Username,
		SecretEnc:   plain,
	}
	if err := wc.Login(r.Context(), transient); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("login failed: "+err.Error()))
		return
	}
	if _, err := wc.Verify(r.Context(), transient); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("verify failed: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Delete handles DELETE /credentials/{id}.
func (h *Credentials) Delete(w http.ResponseWriter, r *http.Request) {
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
	if err := h.Creds.Delete(r.Context(), id, uid); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("credential not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	if h.Audit != nil {
		ip, ua := audit.FromRequest(r)
		h.Audit.Generic(&uid, "credential_delete", "tracker_credential", id.String(), "success",
			map[string]any{"ip": ip, "ua": ua})
	}
	w.WriteHeader(http.StatusNoContent)
}
