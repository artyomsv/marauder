package rutracker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/captchalogin"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

func TestClassifyLogin(t *testing.T) {
	tests := []struct {
		name string
		body string
		want captchalogin.Outcome
	}{
		{"success", `<span id="logged-in-username">bob</span>`, captchalogin.OutcomeSuccess},
		{"captcha demanded", `<input name="cap_sid" value="X">`, captchalogin.OutcomeNeedCaptcha},
		{
			"wrong captcha re-renders the form with a fresh sid",
			`<h2>Введите код подтверждения</h2><input name="cap_sid" value="X">`,
			captchalogin.OutcomeNeedCaptcha,
		},
		{"bad credentials", `<h1>Вход</h1>no marker here`, captchalogin.OutcomeFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyLogin([]byte(tt.body)); got != tt.want {
				t.Errorf("classifyLogin() = %v, want %v", got, tt.want)
			}
		})
	}
}

// prodPlugin is a plugin pinned to the real domain, so host allowlisting is
// exercised as it behaves in production rather than against a test server.
func prodPlugin() *plugin { return &plugin{sessions: forumcommon.New(), domain: defaultDomain} }

func TestParseChallenge_ExtractsSidFieldAndImage(t *testing.T) {
	body := []byte(`<input type="hidden" name="cap_sid" value="SID123">` +
		`<img src="https://static.rutracker.cc/captcha/abc.jpg?9" width="120">` +
		`<input class="reg-input" type="text" name="cap_code_deadbeef" value="">`)
	spec, err := prodPlugin().parseChallenge(body)
	if err != nil {
		t.Fatalf("parseChallenge: %v", err)
	}
	if spec.Fields.Get("cap_sid") != "SID123" {
		t.Errorf("cap_sid = %q", spec.Fields.Get("cap_sid"))
	}
	if spec.AnswerField != "cap_code_deadbeef" {
		t.Errorf("AnswerField = %q", spec.AnswerField)
	}
	if spec.ImageURL != "https://static.rutracker.cc/captcha/abc.jpg?9" {
		t.Errorf("ImageURL = %q", spec.ImageURL)
	}
}

func TestParseChallenge_MissingSid_Errors(t *testing.T) {
	if _, err := prodPlugin().parseChallenge([]byte(`<html>no captcha here</html>`)); err == nil {
		t.Fatal("want an error when the page carries no captcha")
	}
}

// TestParseChallenge_RejectsOffsiteCaptchaHost is the SSRF guard. The captcha
// URL is scraped out of tracker-controlled HTML and then fetched by the
// backend, which may sit on a network the tracker cannot reach; the response is
// handed back to the browser as a data: URL. A page that pointed it at cloud
// metadata or a loopback admin port would turn Marauder into a read primitive
// for the internal network, so the host is allowlisted rather than trusted.
func TestParseChallenge_RejectsOffsiteCaptchaHost(t *testing.T) {
	offsite := []string{
		"http://169.254.169.254/latest/meta-data/captcha",
		"http://127.0.0.1:8679/api/v1/captcha",
		"https://evil.example.com/captcha/x.jpg",
		"https://static.rutracker.cc.evil.com/captcha/x.jpg",
		"file:///etc/captcha",
	}
	for _, bad := range offsite {
		t.Run(bad, func(t *testing.T) {
			body := []byte(`<input name="cap_sid" value="SID">` +
				`<img src="` + bad + `">` +
				`<input name="cap_code_deadbeef" value="">`)
			if _, err := prodPlugin().parseChallenge(body); err == nil {
				t.Errorf("parseChallenge accepted off-site captcha host %q", bad)
			}
		})
	}
}

func TestParseChallenge_AcceptsKnownCaptchaHosts(t *testing.T) {
	ok := []string{
		"https://static.rutracker.cc/captcha/abc.jpg?9",
		"https://rutracker.org/forum/captcha/abc.jpg",
	}
	for _, good := range ok {
		t.Run(good, func(t *testing.T) {
			body := []byte(`<input name="cap_sid" value="SID">` +
				`<img src="` + good + `">` +
				`<input name="cap_code_deadbeef" value="">`)
			spec, err := prodPlugin().parseChallenge(body)
			if err != nil {
				t.Fatalf("parseChallenge(%q) = %v, want accepted", good, err)
			}
			if spec.ImageURL != good {
				t.Errorf("ImageURL = %q, want %q", spec.ImageURL, good)
			}
		})
	}
}

// TestBeginLogin_NoCaptchaDemanded_ReturnsSessionDirectly is the common case:
// RuTracker's captcha is adaptive, so an untrusted-client challenge is the
// exception. The user must not be shown a picture when the tracker did not
// ask for one.
func TestBeginLogin_NoCaptchaDemanded_ReturnsSessionDirectly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "bb_session", Value: "SESS", Path: "/forum/"})
		_, _ = w.Write([]byte(`<span id="logged-in-username">bob</span>`))
	}))
	defer srv.Close()
	installClearance(t)

	p := pluginFor(t, srv)
	challenge, cookies, err := p.BeginLogin(context.Background(), &domain.TrackerCredential{
		UserID: uuid.New(), Username: "bob", SecretEnc: []byte("pw"),
	})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	if challenge != nil {
		t.Fatal("no captcha should be shown when the tracker did not ask for one")
	}
	if cookies["bb_session"] != "SESS" {
		t.Fatalf("cookies = %v, want bb_session=SESS", cookies)
	}
}

// TestBeginLogin_CaptchaDemanded_ReturnsChallenge covers the escalation, and
// pins that the per-challenge image is fetched from the separate static host
// RuTracker serves captchas from.
func TestBeginLogin_CaptchaDemanded_ReturnsChallenge(t *testing.T) {
	// One server for both the login form and the image: the captcha host is
	// allowlisted, so a second httptest server on a different port is (rightly)
	// rejected as off-site.
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/captcha/") {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte{0xFF, 0xD8, 0xFF})
			return
		}
		_, _ = w.Write([]byte(`<input type="hidden" name="cap_sid" value="SID123">` +
			`<img src="` + srv.URL + `/captcha/abc.jpg">` +
			`<input type="text" name="cap_code_deadbeef" value="">`))
	}))
	defer srv.Close()
	installClearance(t)

	p := pluginFor(t, srv)
	challenge, cookies, err := p.BeginLogin(context.Background(), &domain.TrackerCredential{
		UserID: uuid.New(), Username: "bob", SecretEnc: []byte("pw"),
	})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	if cookies != nil {
		t.Fatal("cookies must be nil while a captcha is outstanding")
	}
	if challenge == nil || len(challenge.Image) == 0 {
		t.Fatal("want a challenge carrying the captcha image")
	}
	if challenge.MIMEType != "image/jpeg" {
		t.Errorf("MIMEType = %q, want image/jpeg", challenge.MIMEType)
	}
}

// TestPlugin_ImplementsWithInteractiveLogin pins the capability: /system/info
// reports supports_interactive_login by type-asserting this interface, and the
// credential UI shows the captcha dialog off that flag. A plugin that silently
// stopped implementing it would leave a captcha-flagged account with no way to
// re-authenticate.
func TestPlugin_ImplementsWithInteractiveLogin(t *testing.T) {
	var p any = &plugin{}
	if _, ok := p.(registry.WithInteractiveLogin); !ok {
		t.Fatal("rutracker must implement registry.WithInteractiveLogin")
	}
}

// TestEng_RebuildsWhenActiveDomainChanges guards a rotation hazard: the engine
// bakes LoginURL from the active domain, so a cached engine built for the old
// host would POST there while the session was cleared for the new one.
func TestEng_RebuildsWhenActiveDomainChanges(t *testing.T) {
	p := &plugin{sessions: forumcommon.New(), domain: defaultDomain}

	first := p.eng()
	if same := p.eng(); same != first {
		t.Fatal("eng() must cache while the domain is unchanged")
	}

	// Simulate a rotation / admin change of the active domain.
	p.domain = "rutracker.net"
	rebuilt := p.eng()
	if rebuilt == first {
		t.Fatal("eng() must rebuild after the active domain changes, or it keeps POSTing to the old host")
	}
}
