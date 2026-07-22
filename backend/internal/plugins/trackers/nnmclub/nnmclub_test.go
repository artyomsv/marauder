package nnmclub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

const fixtureViewtopicHTML = `<html><head>
<title>Через тернии к звёздам (1980) DVDRip [H.264] Оригинальная версия :: NNM-Club</title>
<meta property="og:image" content="https://a.radikal.ru/a11/2008/6f/f91ffdbf65b2.jpg"/>
</head>
<body>
<a href="logout.php">logout</a>
<a rel="nofollow" href="magnet:?xt=urn:btih:094EC3052ED759240E4DFD89F3F7CA5C5B428FF4" title="Примагнититься"><img src="https://nnmstatic.win/forum/images/magnet.png"></a>
<a href="download.php?id=379398" rel="nofollow">Скачать</a>
</body></html>`

// testTopicURL is a real NNM-Club host so it passes the fetch() SSRF
// allowlist; hostRewrite redirects the actual connection to the test server.
const testTopicURL = "https://nnmclub.to/forum/viewtopic.php?t=42"

func newTestPlugin(t *testing.T) *plugin {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/forum/viewtopic.php"):
			w.WriteHeader(200)
			w.Write([]byte(fixtureViewtopicHTML))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)

	return &plugin{
		sessions:  forumcommon.New(),
		transport: &hostRewrite{host: strings.TrimPrefix(srv.URL, "http://")},
	}
}

// hostRewrite reroutes every request to the test server while leaving the
// request's declared URL (a real nnmclub.to host) intact for the SSRF guard.
type hostRewrite struct{ host string }

func (h *hostRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = h.host
	return http.DefaultTransport.RoundTrip(req)
}

func TestCanParse(t *testing.T) {
	p := &plugin{}
	cases := map[string]bool{
		"https://nnmclub.to/forum/viewtopic.php?t=12345":     true,
		"https://www.nnmclub.to/forum/viewtopic.php?t=12345": true,
		"https://nnmclub.me/forum/viewtopic.php?t=12345":     true,
		"https://nnmclub.to/forum/index.php":                 false,
		"https://example.com/":                               false,
	}
	for u, want := range cases {
		if got := p.CanParse(u); got != want {
			t.Errorf("CanParse(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestUsesCloudflare(t *testing.T) {
	p := &plugin{}
	if !p.UsesCloudflare() {
		t.Error("nnm-club should report UsesCloudflare()")
	}
}

func TestParse(t *testing.T) {
	p := &plugin{}
	topic, err := p.Parse(context.Background(), "https://nnmclub.to/forum/viewtopic.php?t=42")
	if err != nil {
		t.Fatal(err)
	}
	if topic.Extra["topic_id"] != 42 {
		t.Errorf("topic_id: %v", topic.Extra["topic_id"])
	}
}

func TestCheck(t *testing.T) {
	p := newTestPlugin(t)
	topic := &domain.Topic{URL: testTopicURL}
	check, err := p.Check(context.Background(), topic, nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if check.Hash != "094ec3052ed759240e4dfd89f3f7ca5c5b428ff4" {
		t.Errorf("hash: %q", check.Hash)
	}
	if !strings.Contains(check.DisplayName, "Через тернии к звёздам") {
		t.Errorf("display name: %q", check.DisplayName)
	}
	if strings.HasSuffix(check.DisplayName, " :: NNM-Club") {
		t.Errorf("site suffix not stripped: %q", check.DisplayName)
	}
}

func TestDownload(t *testing.T) {
	p := newTestPlugin(t)
	topic := &domain.Topic{URL: testTopicURL}
	payload, err := p.Download(context.Background(), topic, nil, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(payload.TorrentFile) != 0 {
		t.Errorf("expected no torrent file in anonymous mode, got %d bytes", len(payload.TorrentFile))
	}
	if !strings.Contains(payload.MagnetURI, "urn:btih:094ec3052ed759240e4dfd89f3f7ca5c5b428ff4") {
		t.Errorf("magnet missing infohash: %q", payload.MagnetURI)
	}
	if !strings.Contains(payload.MagnetURI, "dn=") {
		t.Errorf("magnet missing display name: %q", payload.MagnetURI)
	}
}

func TestFetch_RejectsOffSiteHost(t *testing.T) {
	// The SSRF guard must refuse any topic URL that is not an NNM-Club host,
	// before a request is ever dialed — so no test server is needed here.
	p := &plugin{sessions: forumcommon.New()}
	bad := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://localhost:6379/",
		"https://evil.com/forum/viewtopic.php?t=1",
		"https://nnmclub.to.evil.com/forum/",
		"ftp://nnmclub.to/forum/",
		"://malformed",
	}
	for _, target := range bad {
		if _, err := p.Check(context.Background(), &domain.Topic{URL: target}, nil); err == nil {
			t.Errorf("Check(%q) should be refused by the SSRF guard", target)
		}
		if _, err := p.ResolveMetadata(context.Background(), target, nil); err == nil {
			t.Errorf("ResolveMetadata(%q) should be refused by the SSRF guard", target)
		}
	}
}

func TestResolveMetadata(t *testing.T) {
	p := newTestPlugin(t)
	meta, err := p.ResolveMetadata(context.Background(), testTopicURL, nil)
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	if !strings.Contains(meta.Title, "Через тернии к звёздам") {
		t.Errorf("title: %q", meta.Title)
	}
	if meta.ImageURL != "https://a.radikal.ru/a11/2008/6f/f91ffdbf65b2.jpg" {
		t.Errorf("image url: %q", meta.ImageURL)
	}
}

func TestCanParse_CustomDomain_AllowedViaResolver(t *testing.T) {
	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "nnmclub" {
			return registry.DomainConfig{Custom: []string{"nnmclub.example"}}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{}
	if !p.CanParse("https://nnmclub.example/forum/viewtopic.php?t=123") {
		t.Error("custom domain should parse")
	}
	if p.CanParse("https://evil.example/forum/viewtopic.php?t=123") {
		t.Error("unlisted domain must not parse")
	}
}

func TestDomains_CanonicalFirst(t *testing.T) {
	p := &plugin{}
	want := []string{"nnmclub.to", "nnmclub.me"}
	if !reflect.DeepEqual(p.Domains(), want) {
		t.Errorf("Domains() = %v, want %v", p.Domains(), want)
	}
}

// hostRecordingRewrite records the Host of every outgoing request, then
// redirects it to the test server (target) over http so no real network is
// hit. Mirrors the kinozal test helper of the same purpose.
type hostRecordingRewrite struct {
	target string
	hosts  []string
}

func (h *hostRecordingRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	h.hosts = append(h.hosts, req.URL.Host)
	req.URL.Scheme = "http"
	req.URL.Host = h.target
	return http.DefaultTransport.RoundTrip(req)
}

// TestCheck_RewritesToActiveDomain asserts that when the admin has
// configured an active domain override, Check fetches that host instead of
// the mirror recorded in the stored topic URL — NNM-Club has no domain
// field, so canonicalURL is the only place this override can take effect.
func TestCheck_RewritesToActiveDomain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(fixtureViewtopicHTML))
	}))
	t.Cleanup(srv.Close)

	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "nnmclub" {
			return registry.DomainConfig{Active: "nnmclub.me"}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })

	rec := &hostRecordingRewrite{target: strings.TrimPrefix(srv.URL, "http://")}
	p := &plugin{sessions: forumcommon.New(), transport: rec}

	topic := &domain.Topic{URL: "https://nnmclub.to/forum/viewtopic.php?t=42"}
	if _, err := p.Check(context.Background(), topic, nil); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(rec.hosts) == 0 {
		t.Fatal("no requests recorded")
	}
	if rec.hosts[0] != "nnmclub.me" {
		t.Errorf("fetch host = %q, want active domain nnmclub.me", rec.hosts[0])
	}
}

// TestDownload_RewritesToActiveDomain mirrors TestCheck_RewritesToActiveDomain
// for Download: during a primary-domain outage with an admin-configured
// active mirror, Download must fetch the mirror host too — otherwise Check
// detects releases via the mirror while Download keeps dialing the dead
// host, an endless release.found/download-fail loop.
func TestDownload_RewritesToActiveDomain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(fixtureViewtopicHTML))
	}))
	t.Cleanup(srv.Close)

	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "nnmclub" {
			return registry.DomainConfig{Active: "nnmclub.me"}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })

	rec := &hostRecordingRewrite{target: strings.TrimPrefix(srv.URL, "http://")}
	p := &plugin{sessions: forumcommon.New(), transport: rec}

	topic := &domain.Topic{URL: "https://nnmclub.to/forum/viewtopic.php?t=42"}
	if _, err := p.Download(context.Background(), topic, nil, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(rec.hosts) == 0 {
		t.Fatal("no requests recorded")
	}
	if rec.hosts[0] != "nnmclub.me" {
		t.Errorf("fetch host = %q, want active domain nnmclub.me", rec.hosts[0])
	}
}

// TestResolveMetadata_RewritesToActiveDomain mirrors
// TestCheck_RewritesToActiveDomain for ResolveMetadata.
func TestResolveMetadata_RewritesToActiveDomain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(fixtureViewtopicHTML))
	}))
	t.Cleanup(srv.Close)

	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "nnmclub" {
			return registry.DomainConfig{Active: "nnmclub.me"}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })

	rec := &hostRecordingRewrite{target: strings.TrimPrefix(srv.URL, "http://")}
	p := &plugin{sessions: forumcommon.New(), transport: rec}

	if _, err := p.ResolveMetadata(context.Background(), "https://nnmclub.to/forum/viewtopic.php?t=42", nil); err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	if len(rec.hosts) == 0 {
		t.Fatal("no requests recorded")
	}
	if rec.hosts[0] != "nnmclub.me" {
		t.Errorf("fetch host = %q, want active domain nnmclub.me", rec.hosts[0])
	}
}
