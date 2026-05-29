package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/audit"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/captchalogin"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/problem"
)

// interactiveChallengeTTL bounds how long the handler keeps challenge
// metadata. Keep it equal to captchalogin's engine-side pendingTTL — if
// they drift the failure is still graceful (a 404 here, or the engine's
// "expired challenge" 422), but equal TTLs avoid confusing mismatches.
const interactiveChallengeTTL = 5 * time.Minute

// handlerPending holds correlation metadata for an in-flight interactive
// login. The password is carried so that a captcha-only re-auth can
// encrypt and store it when the session is persisted in CompleteInteractive.
type handlerPending struct {
	userID       uuid.UUID
	trackerName  string
	username     string
	password     string
	credentialID uuid.UUID
	expires      time.Time
}

type interactivePendingStore struct {
	mu sync.Mutex
	m  map[string]*handlerPending
}

func newInteractivePendingStore() *interactivePendingStore {
	return &interactivePendingStore{m: map[string]*handlerPending{}}
}

func (s *interactivePendingStore) put(challengeID string, p *handlerPending) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.m {
		if now.After(v.expires) {
			delete(s.m, k)
		}
	}
	s.m[challengeID] = p
}

func (s *interactivePendingStore) get(challengeID string, userID uuid.UUID) (*handlerPending, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.m[challengeID]
	if !ok || time.Now().After(p.expires) || p.userID != userID {
		return nil, false
	}
	return p, true
}

func (s *interactivePendingStore) del(challengeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, challengeID)
}

type beginReq struct {
	TrackerName string `json:"tracker_name"`
	Username    string `json:"username"`
	Password    string `json:"password"`
}
type answerReq struct {
	TrackerName string `json:"tracker_name"`
	ChallengeID string `json:"challenge_id"`
	Answer      string `json:"answer"`
}
type refreshChallengeReq struct {
	TrackerName string `json:"tracker_name"`
	ChallengeID string `json:"challenge_id"`
}

func dataURL(mime string, img []byte) string {
	if mime == "" {
		mime = "image/gif"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(img)
}

func (h *Credentials) interactivePlugin(trackerName string) (registry.WithInteractiveLogin, *problem.Error) {
	t := registry.GetTracker(trackerName)
	if t == nil {
		return nil, problem.ErrUnprocessable("unknown tracker plugin: " + trackerName)
	}
	wi, ok := t.(registry.WithInteractiveLogin)
	if !ok {
		return nil, problem.ErrUnprocessable("tracker '" + trackerName + "' does not support interactive login")
	}
	return wi, nil
}

func (h *Credentials) persistSession(ctx context.Context, uid uuid.UUID, trackerName, username, password string, cookies registry.SessionCookies) (*domain.TrackerCredential, error) {
	cookieJSON, err := json.Marshal(cookies)
	if err != nil {
		return nil, err
	}
	sEnc, sNonce, err := h.Master.Encrypt(cookieJSON)
	if err != nil {
		return nil, err
	}
	// Upsert: re-authenticating an existing account (the ErrSessionExpired
	// recovery path) refreshes the stored session in place rather than
	// violating UNIQUE(user_id, tracker_name) with a second Create.
	// The existing password (SecretEnc) is left untouched on the upsert path.
	if existing, gerr := h.Creds.GetForTracker(ctx, uid, trackerName); gerr == nil && existing != nil {
		if serr := h.Creds.SetSession(ctx, existing.ID, uid, sEnc, sNonce); serr != nil {
			return nil, serr
		}
		existing.SessionEnc, existing.SessionNonce = sEnc, sNonce
		return existing, nil
	}
	pEnc, pNonce, err := h.Master.Encrypt([]byte(password))
	if err != nil {
		return nil, err
	}
	return h.Creds.Create(ctx, &domain.TrackerCredential{
		UserID: uid, TrackerName: trackerName, Username: username,
		SecretEnc: pEnc, SecretNonce: pNonce, SessionEnc: sEnc, SessionNonce: sNonce,
	})
}

// BeginInteractive handles POST /credentials/interactive/begin. It starts
// an interactive (captcha) login: if the tracker returns cookies outright
// (trusted IP, no captcha) the credential is persisted immediately;
// otherwise a captcha challenge is returned for the user to solve.
func (h *Credentials) BeginInteractive(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	var req beginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	if req.TrackerName == "" || req.Username == "" || req.Password == "" {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("tracker_name, username, and password are required"))
		return
	}
	wi, perr := h.interactivePlugin(req.TrackerName)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	transient := &domain.TrackerCredential{UserID: uid, TrackerName: req.TrackerName, Username: req.Username, SecretEnc: []byte(req.Password)}
	challenge, cookies, err := wi.BeginLogin(r.Context(), transient)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable(err.Error()))
		return
	}
	if cookies != nil { // trusted IP, no captcha required
		created, cerr := h.persistSession(r.Context(), uid, req.TrackerName, req.Username, req.Password, cookies)
		if cerr != nil {
			problem.Write(w, r, h.BaseURL, problem.ErrInternal("persist session: "+cerr.Error()))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "logged_in", "credential": toCredView(created)})
		return
	}
	h.pending.put(challenge.ChallengeID, &handlerPending{
		userID: uid, trackerName: req.TrackerName, username: req.Username, password: req.Password,
		expires: time.Now().Add(interactiveChallengeTTL),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "captcha", "challenge_id": challenge.ChallengeID,
		"captcha_image": dataURL(challenge.MIMEType, challenge.Image),
	})
}

// CompleteInteractive handles POST /credentials/interactive/complete. It
// submits the user's captcha answer and persists the harvested session.
func (h *Credentials) CompleteInteractive(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	var req answerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	p, ok := h.pending.get(req.ChallengeID, uid)
	if !ok {
		problem.Write(w, r, h.BaseURL, problem.ErrNotFound("challenge not found or expired"))
		return
	}
	wi, perr := h.interactivePlugin(p.trackerName)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	cookies, err := wi.CompleteLogin(r.Context(), req.ChallengeID, req.Answer)
	if err != nil {
		if errors.Is(err, captchalogin.ErrWrongCaptcha) {
			// Keep the pending challenge so the client can refresh + retry.
			problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("captcha_incorrect"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable(err.Error()))
		return
	}
	created, cerr := h.persistSession(r.Context(), uid, p.trackerName, p.username, p.password, cookies)
	if cerr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("persist session: "+cerr.Error()))
		return
	}
	h.pending.del(req.ChallengeID)
	if h.Audit != nil {
		ip, ua := audit.FromRequest(r)
		h.Audit.Generic(&uid, "credential_create", "tracker_credential", created.ID.String(), "success",
			map[string]any{"tracker_name": p.trackerName, "interactive": true, "ip": ip, "ua": ua})
	}
	writeJSON(w, http.StatusCreated, map[string]any{"credential": toCredView(created)})
}

// RefreshInteractive handles POST /credentials/interactive/refresh. It
// re-fetches a fresh captcha image on the existing pending session.
func (h *Credentials) RefreshInteractive(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	var req refreshChallengeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	p, ok := h.pending.get(req.ChallengeID, uid)
	if !ok {
		problem.Write(w, r, h.BaseURL, problem.ErrNotFound("challenge not found or expired"))
		return
	}
	wi, perr := h.interactivePlugin(p.trackerName)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	challenge, err := wi.RefreshChallenge(r.Context(), req.ChallengeID)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"challenge_id":  challenge.ChallengeID,
		"captcha_image": dataURL(challenge.MIMEType, challenge.Image),
	})
}

// ReauthBegin handles POST /credentials/{id}/reauth/begin — re-authenticate
// an existing credential using its STORED password (no credential re-entry).
func (h *Credentials) ReauthBegin(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid credential id"))
		return
	}
	cred, gerr := h.Creds.GetByID(r.Context(), id, uid)
	if gerr != nil || cred == nil {
		problem.Write(w, r, h.BaseURL, problem.ErrNotFound("credential not found"))
		return
	}
	if len(cred.SecretEnc) == 0 {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("this account has no stored password; delete and re-add it to enable captcha-only re-authentication"))
		return
	}
	wi, perr := h.interactivePlugin(cred.TrackerName)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	pw, derr := h.Master.Decrypt(cred.SecretEnc, cred.SecretNonce)
	if derr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("decrypt password: "+derr.Error()))
		return
	}
	transient := &domain.TrackerCredential{UserID: uid, TrackerName: cred.TrackerName, Username: cred.Username, SecretEnc: pw}
	challenge, cookies, berr := wi.BeginLogin(r.Context(), transient)
	if berr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable(berr.Error()))
		return
	}
	if cookies != nil { // trusted IP, no captcha
		if perr := h.storeReauthSession(r.Context(), id, uid, cookies); perr != nil {
			problem.Write(w, r, h.BaseURL, perr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "logged_in"})
		return
	}
	h.pending.put(challenge.ChallengeID, &handlerPending{
		userID: uid, trackerName: cred.TrackerName, username: cred.Username,
		credentialID: id, expires: time.Now().Add(interactiveChallengeTTL),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "captcha", "challenge_id": challenge.ChallengeID,
		"captcha_image": dataURL(challenge.MIMEType, challenge.Image),
	})
}

// ReauthComplete handles POST /credentials/{id}/reauth/complete.
func (h *Credentials) ReauthComplete(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid credential id"))
		return
	}
	var body struct {
		ChallengeID string `json:"challenge_id"`
		Answer      string `json:"answer"`
	}
	if derr := json.NewDecoder(r.Body).Decode(&body); derr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	p, ok := h.pending.get(body.ChallengeID, uid)
	if !ok || p.credentialID != id {
		problem.Write(w, r, h.BaseURL, problem.ErrNotFound("challenge not found or expired"))
		return
	}
	wi, perr := h.interactivePlugin(p.trackerName)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	cookies, cerr := wi.CompleteLogin(r.Context(), body.ChallengeID, body.Answer)
	if cerr != nil {
		if errors.Is(cerr, captchalogin.ErrWrongCaptcha) {
			problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("captcha_incorrect"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable(cerr.Error()))
		return
	}
	if perr := h.storeReauthSession(r.Context(), id, uid, cookies); perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	h.pending.del(body.ChallengeID)
	cred, _ := h.Creds.GetByID(r.Context(), id, uid)
	writeJSON(w, http.StatusOK, map[string]any{"credential": toCredView(cred)})
}

// storeReauthSession encrypts the harvested cookies and updates the
// existing credential's session in place (SetSession also clears the
// session_expired_at marker).
func (h *Credentials) storeReauthSession(ctx context.Context, id, uid uuid.UUID, cookies registry.SessionCookies) *problem.Error {
	cookieJSON, err := json.Marshal(cookies)
	if err != nil {
		return problem.ErrInternal("marshal cookies: " + err.Error())
	}
	enc, nonce, err := h.Master.Encrypt(cookieJSON)
	if err != nil {
		return problem.ErrInternal("encrypt session: " + err.Error())
	}
	if serr := h.Creds.SetSession(ctx, id, uid, enc, nonce); serr != nil {
		return problem.ErrInternal("persist session: " + serr.Error())
	}
	return nil
}
