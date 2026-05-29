package lostfilm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// loginStubPlugin builds a plugin whose HTTP traffic is redirected to a
// stub server returning the given ajaxik.php login body.
func loginStubPlugin(t *testing.T, loginBody string) *plugin {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/ajaxik.php") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(loginBody))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	testHost := strings.TrimPrefix(srv.URL, "http://")
	return &plugin{
		sessions: forumcommon.New(),
		domain:   "www.lostfilm.tv",
		transport: &e2etest.HostRewriteTransport{
			From:  "www.lostfilm.tv",
			To:    testHost,
			Inner: &allHostsToTest{To: testHost},
		},
		redirectValidator: permissiveRedirectValidator,
	}
}

// TestLogin_NeedCaptcha_ReturnsActionableError guards the real-world case
// where LostFilm gates a login behind a captcha. It responds HTTP 200
// with {"need_captcha":true,"result":"ok"} — no "error" key — so the old
// loose substring check treated it as success and Verify() later failed
// with the misleading "credentials likely wrong". Login() must instead
// return a captcha-specific, actionable error.
func TestLogin_NeedCaptcha_ReturnsActionableError(t *testing.T) {
	p := loginStubPlugin(t, `{"need_captcha":true,"result":"ok"}`)
	creds := &domain.TrackerCredential{
		UserID:    uuid.New(),
		Username:  "alice",
		SecretEnc: []byte("password"),
	}

	err := p.Login(context.Background(), creds)
	if err == nil {
		t.Fatal("Login returned nil, want captcha error")
	}
	if !errors.Is(err, registry.ErrCaptchaRequired) {
		t.Errorf("error = %v, want errors.Is(err, registry.ErrCaptchaRequired)", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "captcha") {
		t.Errorf("error message = %q, want it to mention captcha", err)
	}
}

// TestLogin_ErrorBody_StillRejected keeps the existing {"error":...}
// failure path working alongside the new captcha branch.
func TestLogin_ErrorBody_StillRejected(t *testing.T) {
	p := loginStubPlugin(t, `{"error":1,"message":"wrong password"}`)
	creds := &domain.TrackerCredential{
		UserID:    uuid.New(),
		Username:  "alice",
		SecretEnc: []byte("badpass"),
	}

	err := p.Login(context.Background(), creds)
	if err == nil {
		t.Fatal("Login returned nil, want error for {\"error\":...} body")
	}
	if errors.Is(err, registry.ErrCaptchaRequired) {
		t.Errorf("error = %v, should NOT be classified as captcha", err)
	}
}
