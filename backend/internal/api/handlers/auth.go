// Package handlers holds the HTTP handler functions for each API resource.
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/api/middleware"
	"github.com/artyomsv/marauder/backend/internal/auth"
	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/problem"
)

// Auth is the handler group for /auth/*.
type Auth struct {
	Users   *repo.Users
	Manager *auth.Manager
	BaseURL string
}

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Login handles POST /auth/login.
func (h *Auth) Login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	if req.Username == "" || req.Password == "" {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("username and password required"))
		return
	}

	u, err := h.Users.GetByUsername(r.Context(), req.Username)
	if err != nil {
		// Do not distinguish "user not found" from "wrong password" to
		// avoid user enumeration.
		problem.Write(w, r, h.BaseURL, problem.ErrUnauthorized("invalid credentials"))
		return
	}
	if u.IsDisabled {
		problem.Write(w, r, h.BaseURL, problem.ErrForbidden("account disabled"))
		return
	}
	if u.PasswordHash == "" {
		// User exists but is OIDC-only — can't log in with password.
		problem.Write(w, r, h.BaseURL, problem.ErrUnauthorized("invalid credentials"))
		return
	}
	ok, err := crypto.VerifyPassword(req.Password, u.PasswordHash)
	if err != nil || !ok {
		problem.Write(w, r, h.BaseURL, problem.ErrUnauthorized("invalid credentials"))
		return
	}

	pair, err := h.Manager.Issue(r.Context(), u, r.UserAgent(), clientIP(r))
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("issue token: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, pair)
}

type refreshReq struct {
	RefreshToken string `json:"refresh_token"`
}

// Refresh handles POST /auth/refresh.
func (h *Auth) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RefreshToken == "" {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("refresh_token required"))
		return
	}
	// Look up the token's user via the hash so we can Refresh with the
	// right user object.
	hash := crypto.HashToken(req.RefreshToken)
	tokRepo := h.Manager
	_ = tokRepo
	// Fetch the refresh token record directly via the manager's refresh
	// method by first loading the token, then the user, then calling
	// Refresh.
	tok, err := h.loadToken(hash)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnauthorized("invalid refresh token"))
		return
	}
	user, err := h.Users.GetByID(r.Context(), tok.UserID)
	if err != nil || user.IsDisabled {
		problem.Write(w, r, h.BaseURL, problem.ErrUnauthorized("invalid refresh token"))
		return
	}
	pair, err := h.Manager.Refresh(r.Context(), req.RefreshToken, user, r.UserAgent(), clientIP(r))
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnauthorized(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, pair)
}

// loadToken is a tiny helper that goes through the manager's refresh repo.
// To keep the handler simple we expose a bridge via the manager. Since the
// manager doesn't currently expose the repo, we duplicate a minimal helper.
// In practice this is fine because Auth is the only caller.
func (h *Auth) loadToken(hash string) (*tokStub, error) {
	// We use the RefreshTokens repo via a method injected on Manager.
	// To avoid plumbing, we reconstruct via reflection-free accessor:
	// see Manager.PeekRefresh below.
	tok, err := h.Manager.PeekRefresh(hash)
	if err != nil {
		return nil, err
	}
	return &tokStub{UserID: tok.UserID}, nil
}

type tokStub struct {
	UserID uuid.UUID
}

// Logout handles POST /auth/logout.
func (h *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	var req refreshReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RefreshToken == "" {
		// idempotent — treat as already logged out
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := h.Manager.Revoke(r.Context(), req.RefreshToken); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Me handles GET /auth/me.
func (h *Auth) Me(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnauthorized("no claims"))
		return
	}
	id, err := uuid.Parse(claims.UserID)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnauthorized("bad claims"))
		return
	}
	u, err := h.Users.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("user not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       u.ID,
		"username": u.Username,
		"email":    u.Email,
		"role":     u.Role,
	})
}

// --- helpers ------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func clientIP(r *http.Request) string {
	// Trust X-Forwarded-For only if a reverse proxy is known; for v0.1 we
	// take the first entry and fall back to RemoteAddr.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if i := strings.LastIndex(r.RemoteAddr, ":"); i >= 0 {
		return r.RemoteAddr[:i]
	}
	return r.RemoteAddr
}
