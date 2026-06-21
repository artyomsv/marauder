package rutracker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/text/encoding/charmap"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/infohash"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// validBencodedTorrent is a minimal but structurally valid single-file
// .torrent the infohash package can parse. The mock dl.php serves it so
// tests can prove Download submits a real torrent (with announce URLs)
// rather than RuTracker's hash-only page magnet (#52).
const validBencodedTorrent = "d8:announce19:http://tracker/annc4:infod6:lengthi100e4:name4:test12:piece lengthi16384e6:pieces20:aaaaaaaaaaaaaaaaaaaaee"

// TestCleanTitle_DecodesWindows1251 feeds real cp1251-encoded bytes (the
// encoding RuTracker actually serves) through cleanTitle and asserts they come
// back as correct UTF-8. Guards the SQLSTATE 22021 bug where undecoded cp1251
// Cyrillic was rejected by Postgres on write.
func TestCleanTitle_DecodesWindows1251(t *testing.T) {
	cp1251, err := charmap.Windows1251.NewEncoder().String("Голод / Hunger :: RuTracker.org")
	if err != nil {
		t.Fatalf("encode cp1251: %v", err)
	}
	if got := cleanTitle(cp1251); got != "Голод / Hunger" {
		t.Errorf("cleanTitle(cp1251) = %q, want %q", got, "Голод / Hunger")
	}
}

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
			w.Write([]byte(validBencodedTorrent))
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

// TestDownloadReturnsMagnetWithoutCredentials covers the documented
// fallback: dl.php needs the session cookie, so with no creds Download
// must hand back the page magnet rather than fetch an unauthenticated
// (HTML login) dl.php response.
func TestDownloadReturnsMagnetWithoutCredentials(t *testing.T) {
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

// TestDownloadPrefersTorrentFileWhenAuthenticated is the #52 regression: a
// RuTracker page magnet is hash-only (no announce URLs), so an
// authenticated Download must fetch the .torrent via dl.php instead of
// handing qBittorrent a trackerless magnet it can never resolve.
func TestDownloadPrefersTorrentFileWhenAuthenticated(t *testing.T) {
	p, _ := newTestPlugin(t)
	creds := &domain.TrackerCredential{
		UserID:    uuid.New(),
		Username:  "alice",
		SecretEnc: []byte("password123"),
	}
	topic := &domain.Topic{URL: "https://" + p.domain + "/forum/viewtopic.php?t=987654"}

	payload, err := p.Download(context.Background(), topic, nil, creds)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if payload.MagnetURI != "" {
		t.Errorf("expected no magnet, got hash-only magnet %q", payload.MagnetURI)
	}
	if len(payload.TorrentFile) == 0 {
		t.Fatal("expected a .torrent payload, got none")
	}
	if _, verr := infohash.FromTorrent(payload.TorrentFile); verr != nil {
		t.Errorf("payload is not a valid bencoded torrent: %v", verr)
	}
}

// TestDownloadFallsBackToMagnetWhenTorrentInvalid guards the fail-open
// path: if dl.php returns something that is not a bencoded torrent (an
// HTML error/login page), Download must not submit that garbage — it
// falls back to the page magnet.
func TestDownloadFallsBackToMagnetWhenTorrentInvalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/forum/viewtopic.php"):
			w.WriteHeader(200)
			w.Write([]byte(fixtureTopicHTML))
		case strings.HasPrefix(r.URL.Path, "/forum/dl.php"):
			w.WriteHeader(200)
			w.Write([]byte("<html><body>Please log in</body></html>"))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	hostNoScheme := strings.TrimPrefix(srv.URL, "http://")
	p := &plugin{
		sessions:  forumcommon.New(),
		domain:    hostNoScheme,
		transport: &schemeRewrite{target: hostNoScheme},
	}
	creds := &domain.TrackerCredential{UserID: uuid.New(), Username: "alice", SecretEnc: []byte("pw")}
	topic := &domain.Topic{URL: "https://" + p.domain + "/forum/viewtopic.php?t=987654"}

	payload, err := p.Download(context.Background(), topic, nil, creds)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !strings.HasPrefix(payload.MagnetURI, "magnet:?xt=urn:btih:") {
		t.Errorf("expected magnet fallback, got %q / %d torrent bytes", payload.MagnetURI, len(payload.TorrentFile))
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

	meta, err := p.ResolveMetadata(context.Background(), "https://rutracker.org/forum/viewtopic.php?t=987654", nil)
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

	meta, err := p.ResolveMetadata(context.Background(), "https://rutracker.org/forum/viewtopic.php?t=1", nil)
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
	meta, err := p.ResolveMetadata(context.Background(), "https://rutracker.org/forum/viewtopic.php?t=987654", nil)
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
