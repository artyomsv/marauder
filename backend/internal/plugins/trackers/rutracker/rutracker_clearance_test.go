package rutracker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// pluginFor wires a plugin against a caller-supplied test server, mirroring
// newTestPlugin but letting each test script its own responses.
func pluginFor(t *testing.T, srv *httptest.Server) *plugin {
	t.Helper()
	host := strings.TrimPrefix(srv.URL, "http://")
	return &plugin{
		sessions:  forumcommon.New(),
		domain:    host,
		transport: &schemeRewrite{target: host},
	}
}

// stubClearance is a registry.ClearanceProvider for tests.
type stubClearance struct {
	mu          sync.Mutex
	c           registry.Clearance
	mints       int
	invalidated int
}

func (s *stubClearance) Clearance(context.Context, string) (registry.Clearance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mints++
	return s.c, nil
}

func (s *stubClearance) InvalidateClearance(string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidated++
}

func (s *stubClearance) counts() (mints, invalidated int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mints, s.invalidated
}

func installClearance(t *testing.T) *stubClearance {
	t.Helper()
	sc := &stubClearance{c: registry.Clearance{
		Cookies:   map[string]string{"cf_clearance": "CLEAR"},
		UserAgent: "Mozilla/5.0 Chrome/148",
	}}
	registry.SetClearanceProvider(sc)
	t.Cleanup(func() { registry.SetClearanceProvider(nil) })
	return sc
}

func TestFetchBytes_SendsClearanceCookieAndBrowserUA(t *testing.T) {
	var gotUA, gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		if c, err := r.Cookie("cf_clearance"); err == nil {
			gotCookie = c.Value
		}
		_, _ = w.Write([]byte(`<html><title>t :: RuTracker.org</title>magnet:?xt=urn:btih:aabb</html>`))
	}))
	defer srv.Close()
	installClearance(t)

	p := pluginFor(t, srv)
	if _, err := p.fetchBytes(context.Background(), nil, nil,
		"https://"+p.domain+"/forum/viewtopic.php?t=1"); err != nil {
		t.Fatalf("fetchBytes: %v", err)
	}
	if gotUA != "Mozilla/5.0 Chrome/148" {
		t.Errorf("UA = %q, want the clearance browser UA (cf_clearance is UA-bound)", gotUA)
	}
	if gotCookie != "CLEAR" {
		t.Errorf("cf_clearance cookie = %q, want CLEAR", gotCookie)
	}
}

func TestFetchBytes_NoProvider_UsesHonestUA(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`<html></html>`))
	}))
	defer srv.Close()
	registry.SetClearanceProvider(nil)

	p := pluginFor(t, srv)
	if _, err := p.fetchBytes(context.Background(), nil, nil,
		"https://"+p.domain+"/forum/viewtopic.php?t=1"); err != nil {
		t.Fatal(err)
	}
	if gotUA != userAgent {
		t.Errorf("UA = %q, want %q when no clearance is configured", gotUA, userAgent)
	}
}

func TestFetchBytes_ChallengeResponse_InvalidatesAndRetriesOnce(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Cf-Mitigated", "challenge")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`<html>ok</html>`))
	}))
	defer srv.Close()
	sc := installClearance(t)

	p := pluginFor(t, srv)
	body, err := p.fetchBytes(context.Background(), nil, nil,
		"https://"+p.domain+"/forum/viewtopic.php?t=1")
	if err != nil {
		t.Fatalf("fetchBytes: %v", err)
	}
	if !strings.Contains(string(body), "ok") {
		t.Fatalf("body = %q, want the retried response", body)
	}
	if _, inv := sc.counts(); inv != 1 {
		t.Errorf("invalidated = %d, want 1", inv)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("calls = %d, want exactly 2 (one retry, not a loop)", n)
	}
}

func TestFetchBytes_PersistentChallenge_FailsWithoutLooping(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Cf-Mitigated", "challenge")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	installClearance(t)

	p := pluginFor(t, srv)
	_, err := p.fetchBytes(context.Background(), nil, nil,
		"https://"+p.domain+"/forum/viewtopic.php?t=1")
	if !errors.Is(err, registry.ErrCloudflareChallenge) {
		t.Fatalf("err = %v, want ErrCloudflareChallenge", err)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("calls = %d, want 2", n)
	}
}

func TestLogin_PostsCredentials_WhenClearanceConfigured(t *testing.T) {
	var posted url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			posted = r.PostForm
			_, _ = w.Write([]byte(`<html><span id="logged-in-username">bob</span></html>`))
			return
		}
		_, _ = w.Write([]byte(`<html></html>`))
	}))
	defer srv.Close()
	installClearance(t)

	p := pluginFor(t, srv)
	creds := &domain.TrackerCredential{
		UserID:    uuid.New(),
		Username:  "bob",
		SecretEnc: []byte("hunter2"),
	}
	if err := p.Login(context.Background(), creds); err != nil {
		t.Fatalf("Login: %v", err)
	}
	// The regression this guards: Login used to return nil WITHOUT posting
	// anything whenever the solver was configured, which left every session
	// anonymous and broke tracker search.
	if posted.Get("login_username") != "bob" {
		t.Fatalf("login_username = %q, want bob (Login must actually post)", posted.Get("login_username"))
	}
	if posted.Get("login_password") != "hunter2" {
		t.Fatalf("login_password not submitted")
	}
}

func TestLogin_CaptchaDemanded_ReturnsErrCaptchaRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><input name="cap_sid" value="SID"></html>`))
	}))
	defer srv.Close()
	installClearance(t)

	p := pluginFor(t, srv)
	err := p.Login(context.Background(), &domain.TrackerCredential{
		UserID: uuid.New(), Username: "bob", SecretEnc: []byte("pw"),
	})
	if !errors.Is(err, registry.ErrCaptchaRequired) {
		t.Fatalf("err = %v, want ErrCaptchaRequired", err)
	}
}

// TestCheck_CarriesClearanceToTracker proves the wiring end to end, replacing
// the old challenge-transport test: a Check must reach the tracker carrying
// the clearance cookie and its matching User-Agent, because a request without
// both is exactly what Cloudflare blocks.
func TestCheck_CarriesClearanceToTracker(t *testing.T) {
	var gotUA, gotCookie, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotPath = r.URL.RequestURI()
		if c, err := r.Cookie("cf_clearance"); err == nil {
			gotCookie = c.Value
		}
		_, _ = w.Write([]byte(fixtureTopicHTML))
	}))
	defer srv.Close()
	installClearance(t)

	p := pluginFor(t, srv)
	topic := &domain.Topic{
		TrackerName: "rutracker",
		URL:         "https://rutracker.org/forum/viewtopic.php?t=987654",
		Extra:       map[string]any{"topic_id": 987654},
	}
	if _, err := p.Check(context.Background(), topic, nil); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !strings.Contains(gotPath, "viewtopic.php?t=987654") {
		t.Errorf("requested %q, want the topic page", gotPath)
	}
	if gotCookie != "CLEAR" {
		t.Errorf("cf_clearance = %q, want CLEAR", gotCookie)
	}
	if gotUA != "Mozilla/5.0 Chrome/148" {
		t.Errorf("UA = %q, want the clearance UA", gotUA)
	}
}

// TestLogin_TrackerErrorStatus_IsNotReportedAsBadCredentials guards a
// misdiagnosis the UI showed for real: while RuTracker's origin was returning
// HTTP 520, the Test button said "invalid credentials (no logged-in marker in
// response)". The marker is naturally absent from an error page, so a
// positive-indicator check alone cannot tell "wrong password" from "the site is
// broken" — exactly the reasoning behind the Cloudflare guard next to it.
func TestLogin_TrackerErrorStatus_IsNotReportedAsBadCredentials(t *testing.T) {
	for _, status := range []int{500, 502, 520, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte("<html>error</html>"))
			}))
			defer srv.Close()
			installClearance(t)

			p := pluginFor(t, srv)
			err := p.Login(context.Background(), &domain.TrackerCredential{
				UserID: uuid.New(), Username: "bob", SecretEnc: []byte("pw"),
			})
			if err == nil {
				t.Fatal("want an error")
			}
			if strings.Contains(err.Error(), "invalid credentials") {
				t.Errorf("err = %q, must not blame the credentials for a %d from the tracker",
					err, status)
			}
			if !strings.Contains(err.Error(), strconv.Itoa(status)) {
				t.Errorf("err = %q, want the tracker's status reported", err)
			}
		})
	}
}

// TestLogin_ChallengeResponse_InvalidatesAndRetriesOnce guards the asymmetry
// that shipped first: fetchBytes healed a rejected clearance by re-minting, but
// Login did not. A clearance Cloudflare had rotated therefore wedged EVERY
// login permanently — the UI kept saying "blocked by Cloudflare" while the
// fetch path was quietly fine.
func TestLogin_ChallengeResponse_InvalidatesAndRetriesOnce(t *testing.T) {
	var posts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			_, _ = w.Write([]byte(`<html></html>`))
			return
		}
		if atomic.AddInt32(&posts, 1) == 1 {
			w.Header().Set("Cf-Mitigated", "challenge")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`<html><span id="logged-in-username">bob</span></html>`))
	}))
	defer srv.Close()
	sc := installClearance(t)

	p := pluginFor(t, srv)
	if err := p.Login(context.Background(), &domain.TrackerCredential{
		UserID: uuid.New(), Username: "bob", SecretEnc: []byte("pw"),
	}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, inv := sc.counts(); inv != 1 {
		t.Errorf("invalidated = %d, want 1 (a rejected clearance must be dropped)", inv)
	}
	if n := atomic.LoadInt32(&posts); n != 2 {
		t.Errorf("login posts = %d, want exactly 2 (one retry, not a loop)", n)
	}
}

func TestLogin_PersistentChallenge_FailsWithoutLooping(t *testing.T) {
	var posts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			atomic.AddInt32(&posts, 1)
		}
		w.Header().Set("Cf-Mitigated", "challenge")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	installClearance(t)

	p := pluginFor(t, srv)
	err := p.Login(context.Background(), &domain.TrackerCredential{
		UserID: uuid.New(), Username: "bob", SecretEnc: []byte("pw"),
	})
	if !errors.Is(err, registry.ErrCloudflareChallenge) {
		t.Fatalf("err = %v, want ErrCloudflareChallenge", err)
	}
	if n := atomic.LoadInt32(&posts); n != 2 {
		t.Errorf("login posts = %d, want 2", n)
	}
}

func TestVerify_UsesClearanceAndTransport(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`<div id="logged-in-username">alice</div>`))
	}))
	defer srv.Close()
	installClearance(t)

	p := pluginFor(t, srv)
	ok, err := p.Verify(context.Background(), &domain.TrackerCredential{UserID: uuid.New()})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Fatal("Verify = false, want true")
	}
	// Verify used to dial directly, ignoring both the clearance and the test
	// transport, so it reported a live session as dead on a challenged site.
	if gotUA != "Mozilla/5.0 Chrome/148" {
		t.Errorf("UA = %q, want the clearance UA", gotUA)
	}
}
