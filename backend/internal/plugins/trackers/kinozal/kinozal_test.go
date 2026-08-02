package kinozal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

const fixtureDetailsHTML = `<html><head><title>The Movie / Кино (2026) [BDRip] [1080p] :: Кинозал.ТВ</title></head>
<body>
<a href="/logout.php">Выход</a>
</body></html>`

// fixtureSrvDetailsHTML is the real response body of
// get_srv_details.php?id=...&action=2 captured from a live logged-in session
// (kinozal.tv id=2136770). The infohash lives here, NOT on the details page,
// and the label is "Инфо хеш" (хеш), not "Инфо хэш".
const fixtureSrvDetailsHTML = `<ul><li>Инфо хеш: 6FADE7192D2257460B7793C9096A79FE6D5012A9</li><li>Размер части торрента: 8 МБ</li></ul>`

func newTestPlugin(t *testing.T) *plugin {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/takelogin.php"):
			http.SetCookie(w, &http.Cookie{Name: "uid", Value: "42"})
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`<a href="/logout.php">Выход</a>`))
		case strings.HasPrefix(r.URL.Path, "/get_srv_details.php"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(fixtureSrvDetailsHTML))
		case strings.HasPrefix(r.URL.Path, "/details.php"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(fixtureDetailsHTML))
		case strings.HasPrefix(r.URL.Path, "/download.php"):
			w.Header().Set("Content-Type", "application/x-bittorrent")
			w.WriteHeader(200)
			_, _ = w.Write([]byte("d8:announce..."))
		case r.URL.Path == "/":
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`<a href="/logout.php">Выход</a>`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(srv.URL, "http://")
	return &plugin{
		sessions:  forumcommon.New(),
		domain:    host,
		transport: &schemeRewrite{},
	}
}

type schemeRewrite struct{}

func (s *schemeRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme == "https" {
		req.URL.Scheme = "http"
	}
	// dl.<host> -> <host>
	req.URL.Host = strings.TrimPrefix(req.URL.Host, "dl.")
	return http.DefaultTransport.RoundTrip(req)
}

func TestCanParse_KnownURLs_MatchExpected(t *testing.T) {
	p := &plugin{}
	cases := map[string]bool{
		"https://kinozal.tv/details.php?id=12345":     true,
		"https://www.kinozal.tv/details.php?id=12345": true,
		"https://kinozal.me/details.php?id=12345":     true,
		"https://kinozal.tv/userlist.php":             false,
		"":                                            false,
	}
	for u, want := range cases {
		if got := p.CanParse(u); got != want {
			t.Errorf("CanParse(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestParse_DetailsURL_ExtractsTopicID(t *testing.T) {
	p := &plugin{}
	topic, err := p.Parse(context.Background(), "https://kinozal.tv/details.php?id=99999")
	if err != nil {
		t.Fatal(err)
	}
	if topic.Extra["topic_id"] != 99999 {
		t.Errorf("topic_id: %v", topic.Extra["topic_id"])
	}
}

func TestCheck_ServerDetails_ResolvesHashAndTitle(t *testing.T) {
	p := newTestPlugin(t)
	topic := &domain.Topic{URL: "https://" + p.domain + "/details.php?id=99999"}
	check, err := p.Check(context.Background(), topic, nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	// Hash comes from get_srv_details.php (the details page has no hash).
	if check.Hash != "6fade7192d2257460b7793c9096a79fe6d5012a9" {
		t.Errorf("hash: %q", check.Hash)
	}
	if !strings.Contains(check.DisplayName, "The Movie") {
		t.Errorf("display name: %q", check.DisplayName)
	}
}

// TestCleanTitle_DecodesWindows1251 feeds real cp1251-encoded bytes (the
// encoding Kinozal actually serves) through cleanTitle and asserts they come
// back as correct UTF-8 with the site suffix stripped. Guards the SQLSTATE
// 22021 bug where undecoded cp1251 Cyrillic is rejected by Postgres on write.
func TestCleanTitle_DecodesWindows1251(t *testing.T) {
	cp1251, err := charmap.Windows1251.NewEncoder().String("Извне (4 сезон) / From :: Кинозал.ТВ")
	if err != nil {
		t.Fatalf("encode cp1251: %v", err)
	}
	if got := cleanTitle(cp1251); got != "Извне (4 сезон) / From" {
		t.Errorf("cleanTitle(cp1251) = %q, want %q", got, "Извне (4 сезон) / From")
	}
}

// TestCleanTitle_StripsAnyMirrorBrandSuffix pins that the site-suffix strip is
// not tied to one mirror's branding. Each Kinozal mirror brands its <title>
// tail after its own domain — measured live on 2026-08-03, kinozal.me serves
// "Кинозал.МЕ" and kinozal.guru serves "Кинозал.GURU" where kinozal.tv served
// "Кинозал.ТВ". A hardcoded single-suffix TrimSuffix leaves the other mirrors'
// tails glued to every topic display name, which is exactly what users see
// after a domain rotation.
func TestCleanTitle_StripsAnyMirrorBrandSuffix(t *testing.T) {
	const want = "Извне (4 сезон) / From"
	tests := []struct {
		name string
		raw  string
	}{
		{"tv brand", want + " :: Кинозал.ТВ"},
		{"me brand", want + " :: Кинозал.МЕ"},
		{"guru brand", want + " :: Кинозал.GURU"},
		{"no suffix", want},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp1251, err := charmap.Windows1251.NewEncoder().String(tt.raw)
			if err != nil {
				t.Fatalf("encode cp1251: %v", err)
			}
			if got := cleanTitle(cp1251); got != want {
				t.Errorf("cleanTitle(%q) = %q, want %q", tt.raw, got, want)
			}
		})
	}
}

// TestCleanTitle_KeepsUnrelatedDoubleColon guards the strip against eating a
// legitimate release title that happens to contain " :: " — only a trailing
// Kinozal brand tail may be removed.
func TestCleanTitle_KeepsUnrelatedDoubleColon(t *testing.T) {
	const raw = "Sherlock :: The Game Is On (2026) :: Кинозал.МЕ"
	const want = "Sherlock :: The Game Is On (2026)"
	if got := cleanTitle(raw); got != want {
		t.Errorf("cleanTitle(%q) = %q, want %q", raw, got, want)
	}
}

// TestResolveMetadata_TitleAndAbsoluteImage serves a cp1251 details page with a
// relative og:image (exactly how kinozal.tv emits posters) and asserts the
// title is decoded + de-suffixed and the image URL is made absolute against the
// trusted domain.
func TestResolveMetadata_TitleAndAbsoluteImage(t *testing.T) {
	title, err := charmap.Windows1251.NewEncoder().String("Извне (4 сезон) / From / 2026 :: Кинозал.ТВ")
	if err != nil {
		t.Fatalf("encode cp1251: %v", err)
	}
	body := "<html><head><title>" + title + "</title>" +
		`<meta property="og:image" content="/i/poster/2/7/2136727.jpg"></head><body></body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(srv.URL, "http://")
	p := &plugin{
		sessions:  forumcommon.New(),
		domain:    host,
		transport: &schemeRewrite{},
	}

	meta, err := p.ResolveMetadata(context.Background(), "https://kinozal.tv/details.php?id=2136770", nil)
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	if meta.Title != "Извне (4 сезон) / From / 2026" {
		t.Errorf("title = %q", meta.Title)
	}
	wantImg := "https://" + host + "/i/poster/2/7/2136727.jpg"
	if meta.ImageURL != wantImg {
		t.Errorf("image URL = %q, want %q", meta.ImageURL, wantImg)
	}
}

// TestResolveMetadata_NoImageReturnsEmpty asserts ImageURL is "" (never
// fabricated) when the page exposes no og:image.
func TestResolveMetadata_NoImageReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`<html><head><title>Plain :: Кинозал.ТВ</title></head><body></body></html>`))
	}))
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(srv.URL, "http://")
	p := &plugin{
		sessions:  forumcommon.New(),
		domain:    host,
		transport: &schemeRewrite{},
	}

	meta, err := p.ResolveMetadata(context.Background(), "https://kinozal.tv/details.php?id=1", nil)
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	if meta.Title != "Plain" {
		t.Errorf("title = %q, want %q", meta.Title, "Plain")
	}
	if meta.ImageURL != "" {
		t.Errorf("image URL = %q, want empty", meta.ImageURL)
	}
}

// TestExtractImageURL_AllArms covers every branch of the og:image
// absolutization switch: absolute passthrough, protocol-relative, root-relative,
// bare-relative, and the no-poster empty case (never fabricated).
func TestExtractImageURL_AllArms(t *testing.T) {
	const host = "kinozal.tv"
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			"absolute https passthrough",
			`<meta property="og:image" content="https://cdn.example.com/p.jpg">`,
			"https://cdn.example.com/p.jpg",
		},
		{
			"protocol-relative gets https",
			`<meta property="og:image" content="//cdn.example.com/p.jpg">`,
			"https://cdn.example.com/p.jpg",
		},
		{
			"root-relative against host",
			`<meta property="og:image" content="/i/poster/2/7/2136727.jpg">`,
			"https://kinozal.tv/i/poster/2/7/2136727.jpg",
		},
		{
			"bare-relative gets slash + host",
			`<meta property="og:image" content="i/poster/x.jpg">`,
			"https://kinozal.tv/i/poster/x.jpg",
		},
		{
			"no og:image returns empty",
			`<html><head><title>No poster</title></head></html>`,
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractImageURL([]byte(tc.body), host); got != tc.want {
				t.Errorf("extractImageURL = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFetchInfohash_LabelSpellings pins the regex tolerance: the live "Инфо хеш"
// (хеш), the legacy "Инфо хэш" (хэш), the English fallback, and the
// no-hash-present case which must return the "no infohash found" error #48 fixed.
func TestFetchInfohash_LabelSpellings(t *testing.T) {
	const wantHash = "6fade7192d2257460b7793c9096a79fe6d5012a9"
	cases := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{"cyrillic хеш (live)", `<li>Инфо хеш: 6FADE7192D2257460B7793C9096A79FE6D5012A9</li>`, wantHash, false},
		{"cyrillic хэш (legacy)", `<li>Инфо хэш: 6FADE7192D2257460B7793C9096A79FE6D5012A9</li>`, wantHash, false},
		{"english Info hash fallback", `<div>Info hash: 6FADE7192D2257460B7793C9096A79FE6D5012A9</div>`, wantHash, false},
		{"no hash present", `<ul><li>Размер части торрента: 8 МБ</li></ul>`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.body
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(srv.Close)
			host := strings.TrimPrefix(srv.URL, "http://")
			p := &plugin{sessions: forumcommon.New(), domain: host, transport: &schemeRewrite{}}

			hash, err := p.fetchInfohash(context.Background(), "https://kinozal.tv/details.php?id=99999", nil)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got hash %q", hash)
				}
				if !strings.Contains(err.Error(), "no infohash found") {
					t.Errorf("error = %v, want it to contain 'no infohash found'", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("fetchInfohash: %v", err)
			}
			if hash != tc.want {
				t.Errorf("hash = %q, want %q", hash, tc.want)
			}
		})
	}
}

// TestCheck_NoInfohash_PropagatesError asserts the #48 failure mode surfaces:
// a get_srv_details response without a hash makes Check return the
// "no infohash found" error rather than a hash-less success.
func TestCheck_NoInfohash_PropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/get_srv_details.php") {
			_, _ = w.Write([]byte(`<ul><li>Размер части торрента: 8 МБ</li></ul>`)) // no hash
			return
		}
		_, _ = w.Write([]byte(fixtureDetailsHTML))
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	p := &plugin{sessions: forumcommon.New(), domain: host, transport: &schemeRewrite{}}

	topic := &domain.Topic{URL: "https://" + p.domain + "/details.php?id=99999"}
	_, err := p.Check(context.Background(), topic, nil)
	if err == nil || !strings.Contains(err.Error(), "no infohash found") {
		t.Fatalf("Check error = %v, want it to contain 'no infohash found'", err)
	}
}

func TestDownload_ValidTopic_ReturnsTorrentBytes(t *testing.T) {
	p := newTestPlugin(t)
	topic := &domain.Topic{
		URL:   "https://" + p.domain + "/details.php?id=99999",
		Extra: map[string]any{"topic_id": 99999},
	}
	payload, err := p.Download(context.Background(), topic, nil, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(payload.TorrentFile) == 0 {
		t.Error("expected torrent bytes")
	}
}

func TestCanonicalDetailsURL_RebuildsFromDomain(t *testing.T) {
	p := &plugin{domain: "kinozal.tv"}
	got, err := p.canonicalDetailsURL("https://kinozal.guru/details.php?id=2072973")
	if err != nil {
		t.Fatalf("canonicalDetailsURL: %v", err)
	}
	if want := "https://kinozal.tv/details.php?id=2072973"; got != want {
		t.Errorf("canonicalDetailsURL = %q, want %q", got, want)
	}
}

// hostRecordingRewrite records the Host of every outgoing request, then
// redirects it to the test server (target) over http so no real network is hit.
type hostRecordingRewrite struct {
	target string // test server host:port (p.domain)
	hosts  []string
}

func (h *hostRecordingRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	h.hosts = append(h.hosts, req.URL.Host)
	req.URL.Scheme = "http"
	req.URL.Host = h.target
	return http.DefaultTransport.RoundTrip(req)
}

func TestCheck_CanonicalizesMirrorHost(t *testing.T) {
	p := newTestPlugin(t) // p.domain == test server host
	rec := &hostRecordingRewrite{target: p.domain}
	p.transport = rec // applied per-session inside fetch()

	// Topic URL points at a different mirror than p.domain.
	topic := &domain.Topic{URL: "https://kinozal.guru/details.php?id=99999"}
	check, err := p.Check(context.Background(), topic, nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !strings.Contains(check.DisplayName, "The Movie") {
		t.Errorf("display name = %q, want it to contain The Movie", check.DisplayName)
	}
	if len(rec.hosts) == 0 {
		t.Fatal("no requests recorded")
	}
	// The FIRST request is the title/details fetch — it must target p.domain,
	// not the raw kinozal.guru mirror.
	if rec.hosts[0] != p.domain {
		t.Errorf("title fetch host = %q, want canonical %q", rec.hosts[0], p.domain)
	}
}

func TestCanParse_CustomDomain_AllowedViaResolver(t *testing.T) {
	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "kinozal" {
			return registry.DomainConfig{Custom: []string{"kinozal.example"}}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{sessions: forumcommon.New(), domain: defaultDomain}
	if !p.CanParse("https://kinozal.example/details.php?id=123") {
		t.Error("custom domain should parse")
	}
	if p.CanParse("https://evil.example/details.php?id=123") {
		t.Error("unlisted domain must not parse")
	}
}

func TestEffectiveDomain_ResolverOverride(t *testing.T) {
	registry.SetDomainResolver(func(string) registry.DomainConfig {
		return registry.DomainConfig{Active: "kinozal.me"}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{sessions: forumcommon.New(), domain: defaultDomain}
	if got := p.effectiveDomain(); got != "kinozal.me" {
		t.Errorf("effectiveDomain = %q, want kinozal.me", got)
	}
	// A test-injected p.domain (≠ defaultDomain) must win over the resolver —
	// this is what keeps every httptest-based e2e test working.
	p.domain = "127.0.0.1:9999"
	if got := p.effectiveDomain(); got != "127.0.0.1:9999" {
		t.Errorf("effectiveDomain with test override = %q, want 127.0.0.1:9999", got)
	}
}

func TestDomains_CanonicalFirst(t *testing.T) {
	p := &plugin{}
	// kinozal.me leads: kinozal.tv stopped resolving (SERVFAIL) on 2026-08-03
	// and was demoted from canonical to a trailing legacy entry, kept only so
	// already-stored topic URLs continue to parse.
	want := []string{"kinozal.me", "kinozal.guru", "kinozal.tv"}
	if !reflect.DeepEqual(p.Domains(), want) {
		t.Errorf("Domains() = %v, want %v", p.Domains(), want)
	}
}
