package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/api/middleware"
	"github.com/artyomsv/marauder/backend/internal/auth"
	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/captchalogin"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// fakeInteractiveTracker implements registry.Tracker + WithInteractiveLogin.
// `mode` (set once at construction, never mutated) switches BeginLogin
// between the captcha and no-captcha paths. Two fixed instances are
// registered under distinct names so tests never share mutable state.
type fakeInteractiveTracker struct {
	name string
	mode string
}

func (f *fakeInteractiveTracker) Name() string         { return f.name }
func (f *fakeInteractiveTracker) DisplayName() string  { return "Fake Tracker" }
func (f *fakeInteractiveTracker) CanParse(string) bool { return false }
func (f *fakeInteractiveTracker) Parse(context.Context, string) (*domain.Topic, error) {
	return nil, nil
}
func (f *fakeInteractiveTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (f *fakeInteractiveTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}
func (f *fakeInteractiveTracker) BeginLogin(context.Context, *domain.TrackerCredential) (*registry.LoginChallenge, registry.SessionCookies, error) {
	if f.mode == "nocaptcha" {
		return nil, registry.SessionCookies{"lf_session": "S"}, nil
	}
	return &registry.LoginChallenge{ChallengeID: "chal-1", Image: []byte("GIF"), MIMEType: "image/gif"}, nil, nil
}
func (f *fakeInteractiveTracker) CompleteLogin(_ context.Context, _, answer string) (registry.SessionCookies, error) {
	if answer == "good" {
		return registry.SessionCookies{"lf_session": "S"}, nil
	}
	return nil, captchalogin.ErrWrongCaptcha
}
func (f *fakeInteractiveTracker) RefreshChallenge(context.Context, string) (*registry.LoginChallenge, error) {
	return &registry.LoginChallenge{ChallengeID: "chal-1", Image: []byte("GIF2"), MIMEType: "image/gif"}, nil
}

// Two fixed, immutable fake trackers registered under distinct names.
const (
	fakeCaptchaName   = "faketracker"
	fakeNoCaptchaName = "faketracker-nc"
)

func init() {
	registry.RegisterTracker(&fakeInteractiveTracker{name: fakeCaptchaName, mode: "captcha"})
	registry.RegisterTracker(&fakeInteractiveTracker{name: fakeNoCaptchaName, mode: "nocaptcha"})
}

// fakeCredStore is an in-memory credentialStore for handler tests.
type fakeCredStore struct {
	created   *domain.TrackerCredential
	createErr error
}

func (s *fakeCredStore) Create(_ context.Context, c *domain.TrackerCredential) (*domain.TrackerCredential, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	c.ID = uuid.New()
	c.CreatedAt = time.Now()
	c.UpdatedAt = time.Now()
	s.created = c
	return c, nil
}
func (s *fakeCredStore) GetByID(context.Context, uuid.UUID, uuid.UUID) (*domain.TrackerCredential, error) {
	return nil, nil
}
func (s *fakeCredStore) ListForUser(context.Context, uuid.UUID) ([]*domain.TrackerCredential, error) {
	return nil, nil
}
func (s *fakeCredStore) Update(context.Context, uuid.UUID, uuid.UUID, string, []byte, []byte) error {
	return nil
}
func (s *fakeCredStore) Delete(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func newCredsHandler(t *testing.T, store credentialStore) *Credentials {
	t.Helper()
	mk, err := crypto.LoadMasterKey(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatalf("LoadMasterKey: %v", err)
	}
	return NewCredentials(store, mk, nil, "http://test")
}

func authedReq(t *testing.T, uid uuid.UUID, body any) *http.Request {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(b))
	ctx := context.WithValue(req.Context(), middleware.CtxClaims, &auth.Claims{UserID: uid.String()})
	return req.WithContext(ctx)
}

func TestBeginInteractive_Captcha(t *testing.T) {
	store := &fakeCredStore{}
	h := newCredsHandler(t, store)
	w := httptest.NewRecorder()
	h.BeginInteractive(w, authedReq(t, uuid.New(), beginReq{TrackerName: "faketracker", Username: "u", Password: "p"}))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "captcha" {
		t.Errorf("status = %v, want captcha", resp["status"])
	}
	if resp["challenge_id"] != "chal-1" {
		t.Errorf("challenge_id = %v, want chal-1", resp["challenge_id"])
	}
	if img, _ := resp["captcha_image"].(string); !strings.HasPrefix(img, "data:image/gif;base64,") {
		t.Errorf("captcha_image = %q, want data:image/gif;base64, prefix", img)
	}
	if store.created != nil {
		t.Error("captcha begin must not persist a credential")
	}
}

func TestBeginInteractive_NoCaptchaPersists(t *testing.T) {
	store := &fakeCredStore{}
	h := newCredsHandler(t, store)
	w := httptest.NewRecorder()
	h.BeginInteractive(w, authedReq(t, uuid.New(), beginReq{TrackerName: fakeNoCaptchaName, Username: "u", Password: "p"}))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "logged_in" {
		t.Errorf("status = %v, want logged_in", resp["status"])
	}
	if store.created == nil {
		t.Error("no-captcha begin must persist a credential")
	}
}

func TestCompleteInteractive_Success(t *testing.T) {
	store := &fakeCredStore{}
	h := newCredsHandler(t, store)
	uid := uuid.New()
	h.BeginInteractive(httptest.NewRecorder(), authedReq(t, uid, beginReq{TrackerName: "faketracker", Username: "u", Password: "p"}))
	w := httptest.NewRecorder()
	h.CompleteInteractive(w, authedReq(t, uid, answerReq{TrackerName: "faketracker", ChallengeID: "chal-1", Answer: "good"}))
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201; body %s", w.Code, w.Body.String())
	}
	if store.created == nil {
		t.Error("complete success must persist a credential")
	}
}

func TestCompleteInteractive_WrongCaptchaKeepsPending(t *testing.T) {
	h := newCredsHandler(t, &fakeCredStore{})
	uid := uuid.New()
	h.BeginInteractive(httptest.NewRecorder(), authedReq(t, uid, beginReq{TrackerName: "faketracker", Username: "u", Password: "p"}))
	w := httptest.NewRecorder()
	h.CompleteInteractive(w, authedReq(t, uid, answerReq{TrackerName: "faketracker", ChallengeID: "chal-1", Answer: "bad"}))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422; body %s", w.Code, w.Body.String())
	}
	var d map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &d)
	if d["detail"] != "captcha_incorrect" {
		t.Errorf("detail = %v, want captcha_incorrect", d["detail"])
	}
	// Pending must survive a wrong answer so the client can refresh + retry.
	rw := httptest.NewRecorder()
	h.RefreshInteractive(rw, authedReq(t, uid, refreshChallengeReq{TrackerName: "faketracker", ChallengeID: "chal-1"}))
	if rw.Code != http.StatusOK {
		t.Errorf("refresh after wrong captcha: status %d, want 200; body %s", rw.Code, rw.Body.String())
	}
}

func TestCompleteInteractive_UnknownChallenge(t *testing.T) {
	h := newCredsHandler(t, &fakeCredStore{})
	w := httptest.NewRecorder()
	h.CompleteInteractive(w, authedReq(t, uuid.New(), answerReq{TrackerName: "faketracker", ChallengeID: "nope", Answer: "good"}))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body %s", w.Code, w.Body.String())
	}
}

func TestCompleteInteractive_ForeignUserCannotComplete(t *testing.T) {
	h := newCredsHandler(t, &fakeCredStore{})
	owner, attacker := uuid.New(), uuid.New()
	h.BeginInteractive(httptest.NewRecorder(), authedReq(t, owner, beginReq{TrackerName: "faketracker", Username: "u", Password: "p"}))
	w := httptest.NewRecorder()
	h.CompleteInteractive(w, authedReq(t, attacker, answerReq{TrackerName: "faketracker", ChallengeID: "chal-1", Answer: "good"}))
	if w.Code != http.StatusNotFound {
		t.Fatalf("foreign user got status %d, want 404; body %s", w.Code, w.Body.String())
	}
}

func TestBeginInteractive_UnknownTracker(t *testing.T) {
	h := newCredsHandler(t, &fakeCredStore{})
	w := httptest.NewRecorder()
	h.BeginInteractive(w, authedReq(t, uuid.New(), beginReq{TrackerName: "does-not-exist", Username: "u", Password: "p"}))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422; body %s", w.Code, w.Body.String())
	}
}

func TestBeginInteractive_MissingFields(t *testing.T) {
	h := newCredsHandler(t, &fakeCredStore{})
	w := httptest.NewRecorder()
	h.BeginInteractive(w, authedReq(t, uuid.New(), beginReq{TrackerName: "faketracker", Username: "u"})) // no password
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body %s", w.Code, w.Body.String())
	}
}
