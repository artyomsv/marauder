package rutracker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

const fixtureTopicHTML = `<html>
<head><title>Some Show / Сериал [s01e01-12] [WEBRip] [1080p] :: RuTracker.org</title></head>
<body>
<div id="logged-in-username">alice</div>
<a class="magnet-link" href="magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&amp;dn=Some.Show.S01">Magnet</a>
<a href="dl.php?t=987654">Скачать .torrent</a>
</body>
</html>`

func newTestPlugin(t *testing.T) (*plugin, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/forum/login.php"):
			http.SetCookie(w, &http.Cookie{Name: "bb_session", Value: "abc123"})
			w.WriteHeader(200)
			w.Write([]byte(`<div id="logged-in-username">alice</div>`))
		case strings.HasPrefix(r.URL.Path, "/forum/viewtopic.php"):
			w.WriteHeader(200)
			w.Write([]byte(fixtureTopicHTML))
		case strings.HasPrefix(r.URL.Path, "/forum/dl.php"):
			w.Header().Set("Content-Type", "application/x-bittorrent")
			w.WriteHeader(200)
			w.Write([]byte("d8:announce..."))
		case r.URL.Path == "/forum/index.php":
			w.WriteHeader(200)
			w.Write([]byte(`<div id="logged-in-username">alice</div>`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)

	hostNoScheme := strings.TrimPrefix(srv.URL, "http://")
	p := &plugin{
		sessions: forumcommon.New(),
		domain:   hostNoScheme,
		// Force the plugin's "https://<domain>" calls to hit the test
		// server by replacing the scheme via a custom RoundTripper.
		transport: &schemeRewrite{target: hostNoScheme},
	}
	return p, srv
}

// schemeRewrite forces https://<host>/path to http://<host>/path so the
// httptest server can serve the requests. The plugin code prepends
// "https://" unconditionally; this transport rewrites it.
type schemeRewrite struct {
	target string
}

func (s *schemeRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme == "https" {
		req.URL.Scheme = "http"
	}
	return http.DefaultTransport.RoundTrip(req)
}

func TestCanParse(t *testing.T) {
	p := &plugin{}
	cases := map[string]bool{
		"https://rutracker.org/forum/viewtopic.php?t=12345":     true,
		"https://www.rutracker.org/forum/viewtopic.php?t=12345": true,
		"https://rutracker.net/forum/viewtopic.php?t=12345":     true,
		"https://rutracker.org/forum/viewforum.php?t=12345":     false,
		"https://kinozal.tv/details.php?id=12345":               false,
		"": false,
	}
	for u, want := range cases {
		if got := p.CanParse(u); got != want {
			t.Errorf("CanParse(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestParseExtractsTopicID(t *testing.T) {
	p := &plugin{}
	topic, err := p.Parse(context.Background(), "https://rutracker.org/forum/viewtopic.php?t=987654")
	if err != nil {
		t.Fatal(err)
	}
	if topic.TrackerName != "rutracker" {
		t.Errorf("tracker name: %s", topic.TrackerName)
	}
	if id := topic.Extra["topic_id"]; id != 987654 {
		t.Errorf("topic_id: %v", id)
	}
}

func TestCheckExtractsHashAndTitle(t *testing.T) {
	p, _ := newTestPlugin(t)
	topic := &domain.Topic{URL: "https://" + p.domain + "/forum/viewtopic.php?t=987654"}

	check, err := p.Check(context.Background(), topic, nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if check.Hash != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("hash: %q", check.Hash)
	}
	if !strings.Contains(check.DisplayName, "Some Show") {
		t.Errorf("display name: %q", check.DisplayName)
	}
}

func TestDownloadReturnsMagnet(t *testing.T) {
	p, _ := newTestPlugin(t)
	topic := &domain.Topic{URL: "https://" + p.domain + "/forum/viewtopic.php?t=987654"}

	payload, err := p.Download(context.Background(), topic, nil, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !strings.HasPrefix(payload.MagnetURI, "magnet:?xt=urn:btih:") {
		t.Errorf("magnet URI: %q", payload.MagnetURI)
	}
}

func TestResolveMetadata_OgImagePreferred(t *testing.T) {
	// Page carries both an og:image meta tag and a postImg <var> — og:image
	// must win as the most robust source.
	html := `<html><head>
<title>Some Show / Сериал [s01e01-12] [WEBRip] [1080p] :: RuTracker.org</title>
<meta property="og:image" content="https://static.rutracker.cc/templates/img/og-poster.jpg">
</head><body>
<div id="logged-in-username">alice</div>
<var class="postImg postImgAligned img-right" title="https://i.fastpic.org/lazy-poster.jpg" src="https://static.rutracker.cc/spacer.gif"></var>
</body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(srv.Close)
	hostNoScheme := strings.TrimPrefix(srv.URL, "http://")
	p := &plugin{
		sessions:  forumcommon.New(),
		domain:    hostNoScheme,
		transport: &schemeRewrite{target: hostNoScheme},
	}

	meta, err := p.ResolveMetadata(context.Background(), "https://"+p.domain+"/forum/viewtopic.php?t=987654", nil)
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	wantTitle := "Some Show / Сериал [s01e01-12] [WEBRip] [1080p]"
	if meta.Title != wantTitle {
		t.Errorf("title = %q, want %q", meta.Title, wantTitle)
	}
	if meta.ImageURL != "https://static.rutracker.cc/templates/img/og-poster.jpg" {
		t.Errorf("image URL = %q, want og:image", meta.ImageURL)
	}
}

func TestResolveMetadata_PostVarTitleFallback(t *testing.T) {
	// No og:image — the real image lives in the lazy-loaded postImg <var>
	// title attribute (src is a placeholder gif).
	html := `<html><head><title>Another Show :: RuTracker.org</title></head><body>
<var class="postImg postImgAligned img-right" title="https://i.fastpic.org/real-poster.jpg" src="https://static.rutracker.cc/spacer.gif"></var>
</body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(srv.Close)
	hostNoScheme := strings.TrimPrefix(srv.URL, "http://")
	p := &plugin{
		sessions:  forumcommon.New(),
		domain:    hostNoScheme,
		transport: &schemeRewrite{target: hostNoScheme},
	}

	meta, err := p.ResolveMetadata(context.Background(), "https://"+p.domain+"/forum/viewtopic.php?t=1", nil)
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	if meta.Title != "Another Show" {
		t.Errorf("title = %q, want %q", meta.Title, "Another Show")
	}
	if meta.ImageURL != "https://i.fastpic.org/real-poster.jpg" {
		t.Errorf("image URL = %q, want the <var title> URL", meta.ImageURL)
	}
}

func TestResolveMetadata_NoImageReturnsEmpty(t *testing.T) {
	// The existing fixture has no poster — ImageURL must be "" (never fabricated).
	p, _ := newTestPlugin(t)
	meta, err := p.ResolveMetadata(context.Background(), "https://"+p.domain+"/forum/viewtopic.php?t=987654", nil)
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	if !strings.Contains(meta.Title, "Some Show") {
		t.Errorf("title = %q, want it to contain 'Some Show'", meta.Title)
	}
	if meta.ImageURL != "" {
		t.Errorf("image URL = %q, want empty", meta.ImageURL)
	}
}

func TestLogin(t *testing.T) {
	p, _ := newTestPlugin(t)
	creds := &domain.TrackerCredential{
		UserID:    uuid.New(),
		Username:  "alice",
		SecretEnc: []byte("password123"),
	}
	if err := p.Login(context.Background(), creds); err != nil {
		t.Fatalf("Login: %v", err)
	}
	ok, err := p.Verify(context.Background(), creds)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Error("expected logged in")
	}
}
