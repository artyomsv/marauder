package lostfilm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/captchalogin"
	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// interactivePlugin builds a plugin whose production hostname IS the
// test server's host. This matters for the interactive captcha flow:
// captchalogin.harvest parses Config.LoginURL to look up the harvested
// cookies in the jar, and HostRewriteTransport mutates req.URL.Host in
// place — so the cookie jar stores the Set-Cookie under the rewritten
// (test) host, not "www.lostfilm.tv". Pointing p.domain at the test
// host and using a scheme-only rewrite keeps the harvest host and the
// jar's storage host identical.
func interactivePlugin(t *testing.T, h http.HandlerFunc) *plugin {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	return &plugin{
		sessions:          forumcommon.New(),
		domain:            host,
		transport:         &e2etest.SchemeRewrite{},
		redirectValidator: permissiveRedirectValidator,
	}
}

func TestClassifyLostfilmLogin(t *testing.T) {
	cases := []struct {
		name string
		body string
		want captchalogin.Outcome
	}{
		{"need_captcha", `{"need_captcha":true,"result":"ok"}`, captchalogin.OutcomeNeedCaptcha},
		{"wrong_captcha error 4", `{"error":4,"result":"ok"}`, captchalogin.OutcomeWrongCaptcha},
		{"success object", `{"success":true,"name":"x"}`, captchalogin.OutcomeSuccess},
		{"failed error 1", `{"error":1}`, captchalogin.OutcomeFailed},
		// Regression: a multi-digit error code must NOT collide with the
		// captcha-specific error 4 (the old substring check matched "error":4
		// inside "error":40 and looped the user on a captcha).
		{"failed error 40 not wrong-captcha", `{"error":40,"result":"x"}`, captchalogin.OutcomeFailed},
		{"failed on malformed json", `not json`, captchalogin.OutcomeFailed},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyLostfilmLogin([]byte(tt.body)); got != tt.want {
				t.Errorf("classify(%s) = %d, want %d", tt.body, got, tt.want)
			}
		})
	}
}

func TestInteractiveLogin_CaptchaFlowHarvestsSession(t *testing.T) {
	p := interactivePlugin(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ajaxik.users.php":
			_ = r.ParseForm()
			if r.Form.Get("need_captcha") == "1" {
				http.SetCookie(w, &http.Cookie{Name: "lf_session", Value: "SESS"})
				_, _ = w.Write([]byte(`{"success":true,"name":"alice"}`))
				return
			}
			_, _ = w.Write([]byte(`{"need_captcha":true,"result":"ok"}`))
		case "/simple_captcha.php":
			w.Header().Set("Content-Type", "image/gif")
			_, _ = w.Write([]byte("GIF87a"))
		}
	})
	creds := &domain.TrackerCredential{UserID: uuid.New(), Username: "alice@example.com", SecretEnc: []byte("pw")}
	ch, cookies, err := p.BeginLogin(context.Background(), creds)
	if err != nil || ch == nil || cookies != nil {
		t.Fatalf("want challenge, got ch=%v cookies=%v err=%v", ch, cookies, err)
	}
	got, err := p.CompleteLogin(context.Background(), ch.ChallengeID, "1234")
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	if got["lf_session"] != "SESS" {
		t.Errorf("lf_session = %q, want SESS", got["lf_session"])
	}
}

func TestLogin_NoStoredSession_ReturnsErrSessionExpired(t *testing.T) {
	p := interactivePlugin(t, func(w http.ResponseWriter, r *http.Request) {})
	err := p.Login(context.Background(), &domain.TrackerCredential{UserID: uuid.New()})
	if !errors.Is(err, registry.ErrSessionExpired) {
		t.Errorf("err = %v, want ErrSessionExpired", err)
	}
}

func TestLogin_RehydratesAndValidates(t *testing.T) {
	p := interactivePlugin(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/my" {
			_, _ = w.Write([]byte(`<a href="/logout">logout</a>`))
		}
	})
	sessionJSON, _ := json.Marshal(map[string]string{"lf_session": "SESS"})
	creds := &domain.TrackerCredential{UserID: uuid.New(), SessionEnc: sessionJSON}
	if err := p.Login(context.Background(), creds); err != nil {
		t.Fatalf("Login: %v", err)
	}
}

func TestLogin_DeadSession_ReturnsErrSessionExpired(t *testing.T) {
	p := interactivePlugin(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/my" {
			_, _ = w.Write([]byte(`<html>please log in</html>`)) // no "logout" marker
		}
	})
	sessionJSON, _ := json.Marshal(map[string]string{"lf_session": "DEAD"})
	creds := &domain.TrackerCredential{UserID: uuid.New(), SessionEnc: sessionJSON}
	if err := p.Login(context.Background(), creds); !errors.Is(err, registry.ErrSessionExpired) {
		t.Errorf("err = %v, want ErrSessionExpired", err)
	}
}
