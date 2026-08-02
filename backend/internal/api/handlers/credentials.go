package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
// credentialStore is the consumer-side seam over *repo.TrackerCredentials
// so the handler is unit-testable with a fake store (mirrors the
// scheduler's consumer-interface pattern). *repo.TrackerCredentials
// satisfies it, so production wiring is unchanged.
type credentialStore interface {
	Create(ctx context.Context, c *domain.TrackerCredential) (*domain.TrackerCredential, error)
	GetByID(ctx context.Context, id, userID uuid.UUID) (*domain.TrackerCredential, error)
	GetForTracker(ctx context.Context, userID uuid.UUID, trackerName string) (*domain.TrackerCredential, error)
	ListForUser(ctx context.Context, userID uuid.UUID) ([]*domain.TrackerCredential, error)
	Update(ctx context.Context, id, userID uuid.UUID, username string, secretEnc, secretNonce []byte) error
	SetSession(ctx context.Context, id, userID uuid.UUID, sessionEnc, sessionNonce []byte) error
	Delete(ctx context.Context, id, userID uuid.UUID) error
}

type Credentials struct {
	Creds   credentialStore
	Master  *crypto.MasterKey
	Audit   *audit.Logger
	BaseURL string
	pending *interactivePendingStore
}

// NewCredentials builds the handler with an initialized interactive-login
// pending store.
func NewCredentials(creds credentialStore, master *crypto.MasterKey, auditLog *audit.Logger, baseURL string) *Credentials {
	return &Credentials{Creds: creds, Master: master, Audit: auditLog, BaseURL: baseURL, pending: newInteractivePendingStore()}
}

// credentialView is the safe-to-return shape — never includes the secret.
type credentialView struct {
	ID             string `json:"id"`
	TrackerName    string `json:"tracker_name"`
	DisplayName    string `json:"display_name"`
	Username       string `json:"username"`
	SessionExpired bool   `json:"session_expired"`
	// Verified reports the outcome of the login round-trip this response
	// performed. Absent on list responses, where nothing was checked.
	// False means "credential saved, but the plugin could not confirm the
	// session is authenticated" — deliberately distinct from both success
	// and failure, so the UI never shows a green tick for an unchecked login.
	Verified  *bool  `json:"verified,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// loginAndVerify runs the plugin's Login + Verify sequence and fails if
// *either* step fails. The Verify step is critical: many tracker plugins'
// Login methods do only a negative-indicator check ("does the response
// body contain the string 'error'?") which gives false positives for
// bad credentials — the server returns 200 with a fresh login form and
// no error phrase. Verify hits an authenticated page and looks for a
// positive marker (logout link, logged-in username), providing the
// second independent signal.
//
// The (bool, error) shape of Verify is intentionally strict: (false, nil)
// means "request succeeded but the session is NOT logged in" and MUST
// be treated as failure. Discarding the bool is a bug — the test login
// endpoint used to do exactly that until a user reported entering a
// wrong username and seeing "login succeeded".
//
// The returned verified flag distinguishes the two non-error outcomes.
// A plugin that returns registry.ErrVerifyUnsupported is declaring it has no
// way to check the session; the credential is still usable (Login succeeded),
// so this is not a failure, but callers MUST NOT present it as a verified
// login. Everything else keeps the strict behaviour above.
func loginAndVerify(ctx context.Context, wc registry.WithCredentials, creds *domain.TrackerCredential) (verified bool, err error) {
	if err := wc.Login(ctx, creds); err != nil {
		return false, fmt.Errorf("login: %w", err)
	}
	ok, err := wc.Verify(ctx, creds)
	if errors.Is(err, registry.ErrVerifyUnsupported) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("verify: %w", err)
	}
	if !ok {
		return false, errors.New("verify: session is not logged in (credentials likely wrong)")
	}
	return true, nil
}

// withVerified stamps the per-call verification outcome onto a view. Kept
// separate from toCredView because verification is not persisted state: it is
// the result of the round-trip this one request made.
//
// Takes a pointer so both callers can use it: Update passes nil when no
// password changed and therefore no round-trip ran, which must serialise as an
// absent field rather than false.
func withVerified(v credentialView, verified *bool) credentialView {
	v.Verified = verified
	return v
}

func toCredView(c *domain.TrackerCredential) credentialView {
	display := c.TrackerName
	if t := registry.GetTracker(c.TrackerName); t != nil {
		display = t.DisplayName()
	}
	return credentialView{
		ID:             c.ID.String(),
		TrackerName:    c.TrackerName,
		DisplayName:    display,
		Username:       c.Username,
		SessionExpired: c.SessionExpiredAt != nil,
		CreatedAt:      c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:      c.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
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
	ctx, cancel := slowOperation(w, r, slowOperationBudget)
	defer cancel()
	verified, lerr := loginAndVerify(ctx, wc, transient)
	if lerr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable(explainLoginFailure(lerr)))
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
	writeJSON(w, http.StatusCreated, withVerified(toCredView(created), &verified))
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
	var verified *bool
	if req.Password != "" {
		// Validate the new password by attempting Login + Verify first.
		plugin := registry.GetTracker(existing.TrackerName)
		// A type assertion on a nil interface already yields ok == false, so
		// no separate nil check is needed.
		if wc, ok := plugin.(registry.WithCredentials); ok {
			transient := &domain.TrackerCredential{
				UserID:      uid,
				TrackerName: existing.TrackerName,
				Username:    req.Username,
				SecretEnc:   []byte(req.Password),
			}
			ctx, cancel := slowOperation(w, r, slowOperationBudget)
			ok, err := loginAndVerify(ctx, wc, transient)
			cancel()
			if err != nil {
				problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable(explainLoginFailure(err)))
				return
			}
			// Only set when a login round-trip actually ran: an update that
			// keeps the current password checks nothing, and must not report
			// a verification it did not perform.
			verified = &ok
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
	writeJSON(w, http.StatusOK, withVerified(toCredView(updated), verified))
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
	// Session/captcha trackers (e.g. LostFilm) validate the stored session
	// cookie, not the password — Login reports "no stored session" unless we
	// attach it. Decrypt and pass it through, mirroring the scheduler's
	// per-check credential load so Test reflects the real session state.
	if len(c.SessionEnc) > 0 {
		sess, derr := h.Master.Decrypt(c.SessionEnc, c.SessionNonce)
		if derr != nil {
			problem.Write(w, r, h.BaseURL, problem.ErrInternal("decrypt session: "+derr.Error()))
			return
		}
		transient.SessionEnc = sess
	}
	ctx, cancel := slowOperation(w, r, slowOperationBudget)
	defer cancel()
	verified, lerr := loginAndVerify(ctx, wc, transient)
	if lerr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable(explainLoginFailure(lerr)))
		return
	}
	// ok reports "the sign-in attempt did not fail"; verified reports whether
	// anything actually confirmed the session. They differ for plugins that
	// return registry.ErrVerifyUnsupported, and the UI must not collapse them.
	//
	// Deliberately no human-readable detail here: the frontend has a localised
	// string for this state, and an English sentence from the server would win
	// over it, so a Russian user would see the translated copy on create and
	// English on test — for the same credential.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "verified": verified})
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
