package rutor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/infohash"
	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

func TestCanParse_KnownAndRetiredHosts_MatchExpected(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"canonical mirror", "https://rutor.info/torrent/12345/the.movie", true},
		{"canonical with www", "https://www.rutor.info/torrent/12345/the.movie", true},
		{"live mirror with trailing slash", "https://new-rutor.org/torrent/12345/the.movie/", true},
		// rutor.org is a retired fetch target but stayed in parseDomains, so
		// topics stored against it before 2026-09-03 must still parse.
		{"retired host still parses", "https://rutor.org/torrent/12345/the.movie", true},
		{"not a topic path", "https://rutor.info/search/movie", false},
		{"empty", "", false},
	}
	p := &plugin{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.CanParse(tt.url); got != tt.want {
				t.Errorf("CanParse(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestCanParse_CustomDomain_AllowedViaResolver(t *testing.T) {
	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "rutor" {
			return registry.DomainConfig{Custom: []string{"rutor.example"}}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{}
	if !p.CanParse("https://rutor.example/torrent/123") {
		t.Error("custom domain should parse")
	}
	if p.CanParse("https://evil.example/torrent/123") {
		t.Error("unlisted domain must not parse")
	}
}

func TestParse_ExtractsTopicIDAfterHostShift(t *testing.T) {
	p := &plugin{}
	topic, err := p.Parse(context.Background(), "https://rutor.org/torrent/99999/the.movie")
	if err != nil {
		t.Fatal(err)
	}
	if topic.Extra["topic_id"] != "99999" {
		t.Errorf("topic_id: %v", topic.Extra["topic_id"])
	}
}

func TestEffectiveDomain_ResolverOverride(t *testing.T) {
	registry.SetDomainResolver(func(string) registry.DomainConfig {
		return registry.DomainConfig{Active: "new-rutor.org"}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{domain: defaultDomain}
	if got := p.effectiveDomain(); got != "new-rutor.org" {
		t.Errorf("effectiveDomain = %q, want new-rutor.org", got)
	}
	// A test-injected p.domain (non-empty, != defaultDomain) must win over
	// the resolver — this is what keeps every httptest-based test working.
	p.domain = "127.0.0.1:9999"
	if got := p.effectiveDomain(); got != "127.0.0.1:9999" {
		t.Errorf("effectiveDomain with test override = %q, want 127.0.0.1:9999", got)
	}
}

// TestEffectiveDomain_UnsetFallsBackToDefault guards the rutor-specific
// wrinkle: unlike kinozal, a plugin literal built without setting the
// domain field at all (as the pre-existing e2e test does) must resolve to
// the compiled default, not an empty host.
func TestEffectiveDomain_UnsetFallsBackToDefault(t *testing.T) {
	p := &plugin{}
	if got := p.effectiveDomain(); got != defaultDomain {
		t.Errorf("effectiveDomain with unset domain = %q, want %q", got, defaultDomain)
	}
}

func TestDomains_CanonicalFirst(t *testing.T) {
	p := &plugin{}
	want := []string{"rutor.info", "new-rutor.org"}
	if !reflect.DeepEqual(p.Domains(), want) {
		t.Errorf("Domains() = %v, want %v", p.Domains(), want)
	}
}

// TestDomains_CanonicalMatchesDefault ties two facts together: torrentURLs
// falls back to defaultDomain, and Domains() advertises knownDomains[0] as
// canonical. Letting them drift would silently change which mirror a
// download falls back to.
func TestDomains_CanonicalMatchesDefault(t *testing.T) {
	if knownDomains[0] != defaultDomain {
		t.Errorf("knownDomains[0] = %q, want defaultDomain %q", knownDomains[0], defaultDomain)
	}
	if !slices.Contains(parseDomains, defaultDomain) {
		t.Errorf("parseDomains %v must contain defaultDomain %q", parseDomains, defaultDomain)
	}
}

// hostRecordingRewrite records the Host of every outgoing request, stamps it
// on the request as X-Orig-Host so a handler can answer per-mirror, then
// redirects it to the test server (target) over http so no real network is
// hit. Mirrors the kinozal/nnmclub test helper of the same purpose, plus the
// host tag the shared e2etest.HostRewriteTransport cannot provide.
type hostRecordingRewrite struct {
	target string
	hosts  []string
}

func (h *hostRecordingRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	h.hosts = append(h.hosts, req.URL.Host)
	req.Header.Set("X-Orig-Host", req.URL.Host)
	req.URL.Scheme = "http"
	req.URL.Host = h.target
	return http.DefaultTransport.RoundTrip(req)
}

// fixtureRutorHTML mirrors the shape of a real topic page as observed on
// 2026-09-03: the mirror brands the <title> with its own host as a prefix,
// the topic's own magnet sits in <div id="download"> with its query
// HTML-escaped and carrying trackers, the site chrome has images of its own,
// the poster is the first image inside <table id="details">, and a "similar
// releases" list further down carries magnets for OTHER releases.
const fixtureRutorHTML = `<html><head><title>rutor.info :: The Movie [1080p]</title></head>
<body>
<img src="//cdnbunny.org/logo.jpg" alt="rutor.info logo" />
<div id="download">
<a href="magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&amp;dn=The.Movie&amp;tr=udp://opentor.net:6969&amp;tr=http://retracker.local/announce">magnet</a>
<a href="//d.rutor.info/download/12345">torrent</a>
</div>
<table id="details"><tr><td></td><td><br /><img src="https://i128.fastpic.org/big/2026/0902/a4/poster.jpg" /><br />
<b>Nazvanie: </b>The Movie</td></tr></table>
<div id="similar">
<a href="magnet:?xt=urn:btih:ffffffffffffffffffffffffffffffffffffffff&amp;dn=Some.Other.Release">other</a>
</div>
</body></html>`

const fixtureInfohash = "0123456789abcdef0123456789abcdef01234567"

// fixturePlugin wires a plugin to a test server that always answers with
// page, recording (and stamping) the host of every request.
func fixturePlugin(t *testing.T, page string) (*hostRecordingRewrite, *plugin) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(srv.Close)
	rec := &hostRecordingRewrite{target: strings.TrimPrefix(srv.URL, "http://")}
	return rec, &plugin{httpClient: &http.Client{Transport: rec}}
}

// TestCheck_RewritesToActiveDomain asserts that when the admin has
// configured an active domain override, Check fetches that host instead of
// the mirror recorded in the stored topic URL — rutor has no id-based
// rebuild (unlike kinozal), so canonicalURL is the only place this
// override actually takes effect.
func TestCheck_RewritesToActiveDomain(t *testing.T) {
	rec, p := fixturePlugin(t, fixtureRutorHTML)
	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "rutor" {
			return registry.DomainConfig{Active: "new-rutor.org"}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })

	topic := &domain.Topic{URL: "https://rutor.org/torrent/12345/the.movie"}
	check, err := p.Check(context.Background(), topic, nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if check.DisplayName != "The Movie [1080p]" {
		t.Errorf("display name = %q, want the mirror prefix stripped", check.DisplayName)
	}
	if check.Hash != fixtureInfohash {
		t.Errorf("Hash = %q, want the download-block magnet %q", check.Hash, fixtureInfohash)
	}
	if len(rec.hosts) == 0 {
		t.Fatal("no requests recorded")
	}
	if rec.hosts[0] != "new-rutor.org" {
		t.Errorf("fetch host = %q, want active domain new-rutor.org", rec.hosts[0])
	}
}

// TestCheck_LegacyRutorOrgTopic_RepointedToDefault is the migration path for
// every topic added before 2026-09-03: no admin override is configured, so
// the rewrite must come from the compiled default alone. Without it the
// stored rutor.org host reaches fetch, which refuses it (rutor.org is
// deliberately absent from knownDomains) and the topic errors forever.
func TestCheck_LegacyRutorOrgTopic_RepointedToDefault(t *testing.T) {
	registry.SetDomainResolver(nil)
	rec, p := fixturePlugin(t, fixtureRutorHTML)

	topic := &domain.Topic{URL: "https://rutor.org/torrent/12345/the.movie"}
	if _, err := p.Check(context.Background(), topic, nil); err != nil {
		t.Fatalf("Check on a legacy rutor.org topic: %v", err)
	}
	if len(rec.hosts) == 0 || rec.hosts[0] != defaultDomain {
		t.Errorf("fetch host = %v, want %q", rec.hosts, defaultDomain)
	}
}

// TestCanonicalURL_ForcesHTTPS covers the other half of the legacy-topic
// migration: urlPattern accepts http, and the magnet on a plaintext page is
// the value Check trusts and the .torrent is verified against.
func TestCanonicalURL_ForcesHTTPS(t *testing.T) {
	registry.SetDomainResolver(nil)
	p := &plugin{}
	got, err := p.canonicalURL("http://rutor.org/torrent/12345/the.movie")
	if err != nil {
		t.Fatalf("canonicalURL: %v", err)
	}
	if want := "https://rutor.info/torrent/12345/the.movie"; got != want {
		t.Errorf("canonicalURL = %q, want %q", got, want)
	}
}

// TestCheck_PrefersDownloadBlockMagnet pins the anchor: a "similar releases"
// magnet winning would silently monitor a different release.
func TestCheck_PrefersDownloadBlockMagnet(t *testing.T) {
	// The other release's magnet is placed FIRST in the body, so only the
	// download-block anchor can pick the right one.
	page := `<html><head><title>rutor.info :: Right Release</title></head><body>
<a href="magnet:?xt=urn:btih:ffffffffffffffffffffffffffffffffffffffff">other</a>
<div id="download"><a href="magnet:?xt=urn:btih:` + fixtureInfohash + `">mine</a></div>
</body></html>`
	_, p := fixturePlugin(t, page)
	check, err := p.Check(context.Background(), &domain.Topic{URL: "https://rutor.info/torrent/12345/x"}, nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if check.Hash != fixtureInfohash {
		t.Errorf("Hash = %q, want the download-block magnet", check.Hash)
	}
}

func TestCheck_NoMagnet_Errors(t *testing.T) {
	_, p := fixturePlugin(t, `<html><head><title>rutor.info :: Gone</title></head><body>nothing</body></html>`)
	if _, err := p.Check(context.Background(), &domain.Topic{URL: "https://rutor.info/torrent/1/x"}, nil); err == nil {
		t.Fatal("Check on a page with no magnet must error")
	}
}

// TestCheck_MalformedInfohash_ErrorsLoudly guards against the silent
// truncation a length-agnostic hex regex would produce: a bad hash recorded
// as check.Hash looks like a successful check forever.
func TestCheck_MalformedInfohash_ErrorsLoudly(t *testing.T) {
	page := `<html><head><title>rutor.info :: Odd</title></head><body>
<div id="download"><a href="magnet:?xt=urn:btih:ABCDEF2345">short</a></div></body></html>`
	_, p := fixturePlugin(t, page)
	_, err := p.Check(context.Background(), &domain.Topic{URL: "https://rutor.info/torrent/1/x"}, nil)
	if err == nil {
		t.Fatal("a malformed infohash must error, not be truncated into a hash")
	}
	if !strings.Contains(err.Error(), "unreadable magnet") {
		t.Errorf("error = %v, want it to name the unreadable magnet", err)
	}
}

// TestDownload_RewritesToActiveDomain mirrors TestCheck_RewritesToActiveDomain
// for Download: during a primary-domain outage with an admin-configured
// active mirror, Download must fetch the mirror host too.
func TestDownload_RewritesToActiveDomain(t *testing.T) {
	rec, p := fixturePlugin(t, fixtureRutorHTML)
	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "rutor" {
			return registry.DomainConfig{Active: "new-rutor.org"}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })

	topic := &domain.Topic{URL: "https://rutor.org/torrent/12345/the.movie"}
	payload, err := p.Download(context.Background(), topic, nil, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !strings.Contains(strings.ToLower(payload.MagnetURI), "urn:btih:0123456789abcdef0123456789abcdef01234567") {
		t.Errorf("magnet missing infohash: %q", payload.MagnetURI)
	}
	if len(rec.hosts) == 0 {
		t.Fatal("no requests recorded")
	}
	if rec.hosts[0] != "new-rutor.org" {
		t.Errorf("fetch host = %q, want active domain new-rutor.org", rec.hosts[0])
	}
}

// TestDownload_MagnetFallbackKeepsTrackers is the point of matching the
// HTML-escaped magnet form: stopping at the bare `&` of `&amp;` strips every
// &tr= announce URL, leaving a hash-only magnet that can only be resolved
// through DHT — on the exact path that runs when the download host is broken.
func TestDownload_MagnetFallbackKeepsTrackers(t *testing.T) {
	registry.SetDomainResolver(nil)
	_, p := fixturePlugin(t, fixtureRutorHTML)
	payload, err := p.Download(context.Background(), &domain.Topic{URL: "https://rutor.info/torrent/12345/x"}, nil, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(payload.TorrentFile) != 0 {
		t.Fatal("fixture serves HTML on /download/, so this must be the magnet path")
	}
	for _, want := range []string{
		"dn=The.Movie",
		"tr=udp://opentor.net:6969",
		"tr=http://retracker.local/announce",
	} {
		if !strings.Contains(payload.MagnetURI, want) {
			t.Errorf("magnet %q is missing %q", payload.MagnetURI, want)
		}
	}
	if strings.Contains(payload.MagnetURI, "&amp;") {
		t.Errorf("magnet still HTML-escaped: %q", payload.MagnetURI)
	}
}

// TestFetch_RejectsOffSiteHost closes rutor's previously-missing host guard:
// fetch must refuse any URL that is not a known/admin-configured rutor host,
// before a request is ever dialed — so no test server is needed here.
func TestFetch_RejectsOffSiteHost(t *testing.T) {
	p := &plugin{httpClient: &http.Client{}}
	bad := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://localhost:6379/",
		"https://evil.com/torrent/1",
		"https://rutor.info.evil.com/torrent/1",
		"ftp://rutor.info/torrent/1",
		"://malformed",
		// The download-host allowance strips one "d." prefix and re-checks
		// the base, so it must not become a way in for an unrelated host.
		"https://d.evil.com/download/1",
		"https://d.rutor.info.evil.com/download/1",
		"https://d.d.evil.com/download/1",
		// rutor.org is parseable but retired as a fetch target: it is a
		// stale clone that 404s every current id, so a request must never
		// be built against it.
		"https://rutor.org/torrent/1",
		"https://d.rutor.org/download/1",
	}
	for _, target := range bad {
		t.Run(target, func(t *testing.T) {
			if _, err := p.fetch(context.Background(), target); err == nil {
				t.Errorf("fetch(%q) should be refused by the SSRF guard", target)
			}
		})
	}
}

// TestFetch_RefusesOffSiteRedirect covers the hop the initial-URL guard
// cannot see: a mirror operator 302-ing a topic page at an internal address
// would otherwise have its response body parsed and its <title> surfaced.
func TestFetch_RefusesOffSiteRedirect(t *testing.T) {
	registry.SetDomainResolver(nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	c := newHTTPClient()
	// Rewrites only rutor hosts, so the redirect target is left intact and
	// must be stopped by CheckRedirect rather than by the rewrite.
	c.Transport = &e2etest.HostRewriteTransport{
		From: defaultDomain, To: strings.TrimPrefix(srv.URL, "http://"), StripSubdomain: true,
	}
	p := &plugin{httpClient: c}

	_, err := p.fetch(context.Background(), "https://rutor.info/torrent/1/x")
	if err == nil {
		t.Fatal("an off-site redirect must be refused")
	}
	if !strings.Contains(err.Error(), "refusing to fetch off-site host") {
		t.Errorf("error = %v, want the off-site host refusal", err)
	}
}

// TestFetchHostAllowed_DownloadSubdomain locks the one host shape the guard
// must let through beyond the mirrors themselves: the .torrent host.
func TestFetchHostAllowed_DownloadSubdomain(t *testing.T) {
	for _, host := range []string{"rutor.info", "new-rutor.org", "d.rutor.info", "d.new-rutor.org"} {
		t.Run(host, func(t *testing.T) {
			if !fetchHostAllowed(host) {
				t.Errorf("fetchHostAllowed(%q) = false, want true", host)
			}
		})
	}
}

func TestCleanTitle_StripsOnlyKnownMirrorBranding(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"canonical mirror prefix", "rutor.info :: A Real Release", "A Real Release"},
		{"other live mirror", "new-rutor.org :: Master Run 9 (2026) MP3", "Master Run 9 (2026) MP3"},
		{"retired mirror still stripped", "rutor.org :: Old Topic", "Old Topic"},
		{"padded", "  rutor.info  ::  Padded  ", "Padded"},
		{"no prefix at all", "Plain Release Name", "Plain Release Name"},
		{"release name keeps its own separator", "rutor.info :: Show :: Season 2", "Show :: Season 2"},
		{"bare separator is not branding", "Show :: Season 2", "Show :: Season 2"},
		// A dotted release name is hostname-shaped. Stripping on shape alone
		// ate these; only a real mirror name may be removed.
		{"dotted release, no spaces", "Halo.3::ODST (2009) RePack", "Halo.3::ODST (2009) RePack"},
		{"dotted release with spaces", "The.Movie.2026.1080p :: BluRay", "The.Movie.2026.1080p :: BluRay"},
		{"initialism release", "S.W.A.T :: Season 8", "S.W.A.T :: Season 8"},
		{"branded dotted release", "rutor.info :: Halo.3::ODST", "Halo.3::ODST"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanTitle(tt.in); got != tt.want {
				t.Errorf("cleanTitle(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestExtractImageURL(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			"details-table poster beats the site logo",
			fixtureRutorHTML,
			"https://i128.fastpic.org/big/2026/0902/a4/poster.jpg",
		},
		{
			"protocol-relative is absolutised",
			`<table id="details"><tr><td><img src="//img.example/p.jpg" /></td></tr></table>`,
			"https://img.example/p.jpg",
		},
		{
			"site-relative is absolutised",
			`<table id="details"><tr><td><img src="/pic/p.jpg" /></td></tr></table>`,
			"https://rutor.info/pic/p.jpg",
		},
		{
			"bare-relative is absolutised",
			`<table id="details"><tr><td><img src="pic/p.jpg" /></td></tr></table>`,
			"https://rutor.info/pic/p.jpg",
		},
		{
			// The value is uploader-controlled and ends up in an <img src>.
			"javascript scheme is dropped",
			`<table id="details"><tr><td><img src="javascript:alert(1)" /></td></tr></table>`,
			"",
		},
		{
			"data scheme is dropped",
			`<table id="details"><tr><td><img src="data:image/png;base64,AAAA" /></td></tr></table>`,
			"",
		},
		{
			"no poster reports none rather than the site chrome",
			`<img src="//cdnbunny.org/logo.jpg"><table id="details"><tr><td>no image</td></tr></table>`,
			"",
		},
		{
			// A first-</table> match would end at the inner close and miss
			// the poster entirely; TagBlockInner balances the nesting.
			"nested table before the poster",
			`<table id="details"><tr><td><table><tr><td>files</td></tr></table>` +
				`<img src="https://img.example/poster.jpg" /></td></tr></table>`,
			"https://img.example/poster.jpg",
		},
		{
			"no details table at all",
			`<html><body><img src="https://img.example/x.jpg"></body></html>`,
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractImageURL([]byte(tt.body), "rutor.info"); got != tt.want {
				t.Errorf("extractImageURL = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveMetadata_TitleAndPoster(t *testing.T) {
	registry.SetDomainResolver(nil)
	_, p := fixturePlugin(t, fixtureRutorHTML)
	meta, err := p.ResolveMetadata(context.Background(), "https://rutor.info/torrent/12345/the.movie", nil)
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	if meta.Title != "The Movie [1080p]" {
		t.Errorf("Title = %q, want the mirror prefix stripped", meta.Title)
	}
	if meta.ImageURL != "https://i128.fastpic.org/big/2026/0902/a4/poster.jpg" {
		t.Errorf("ImageURL = %q", meta.ImageURL)
	}
}

// realTorrent is a minimal single-file torrent. Its info dict hashes to the
// value torrentInfohash derives at run time, so the constant and the
// expectation cannot drift apart.
const realTorrent = "d8:announce8:udp://x/" +
	"4:infod6:lengthi1e4:name1:a12:piece lengthi16384e6:pieces0:ee"

func torrentInfohash(t *testing.T) string {
	t.Helper()
	h, err := infohash.FromTorrent([]byte(realTorrent))
	if err != nil {
		t.Fatalf("fixture torrent is not parseable: %v", err)
	}
	return h
}

// topicPage renders a topic page whose download-block magnet carries hash.
func topicPage(hash string) string {
	return fmt.Sprintf(
		`<html><head><title>rutor.info :: A Release</title></head><body>`+
			`<div id="download"><a href="magnet:?xt=urn:btih:%s&amp;dn=A.Release">m</a></div>`+
			`</body></html>`, hash)
}

// downloadServer serves a topic page whose magnet carries magnetHash, and a
// /download/<id> endpoint answering with torrentStatus/torrentBody.
func downloadServer(t *testing.T, magnetHash string, torrentStatus int, torrentBody string) (*plugin, *[]string) {
	t.Helper()
	var paths []string
	page := topicPage(magnetHash)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if strings.HasPrefix(r.URL.Path, "/download/") {
			if torrentStatus != 200 {
				w.WriteHeader(torrentStatus)
				return
			}
			w.Header().Set("Content-Type", "application/x-bittorrent")
			_, _ = w.Write([]byte(torrentBody))
			return
		}
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(srv.Close)
	p := &plugin{httpClient: &http.Client{
		Transport: &e2etest.HostRewriteTransport{
			From: defaultDomain, To: strings.TrimPrefix(srv.URL, "http://"), StripSubdomain: true,
		},
	}}
	return p, &paths
}

// TestDownload_PrefersRealTorrentFile is the point of the .torrent upgrade:
// a magnet needs peer discovery to resolve metadata, the file does not.
func TestDownload_PrefersRealTorrentFile(t *testing.T) {
	registry.SetDomainResolver(nil)
	hash := torrentInfohash(t)
	p, paths := downloadServer(t, hash, 200, realTorrent)

	topic := &domain.Topic{URL: "https://rutor.info/torrent/12345/a-release"}
	payload, err := p.Download(context.Background(), topic, nil, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(payload.TorrentFile) == 0 {
		t.Fatal("TorrentFile is empty, want the real .torrent bytes")
	}
	// domain.Payload's contract is exactly one of the two, and every client
	// plugin resolves a magnet first when both are set.
	if payload.MagnetURI != "" {
		t.Errorf("MagnetURI = %q, want empty when a .torrent was fetched", payload.MagnetURI)
	}
	if payload.FileName != "rutor-12345.torrent" {
		t.Errorf("FileName = %q", payload.FileName)
	}
	if !slices.Contains(*paths, "/download/12345") {
		t.Errorf("download endpoint was not requested; paths=%v", *paths)
	}
}

// TestDownload_FallsBackToCanonicalDownloadHost exercises the reason
// torrentURLs returns a list at all: a topic pinned to new-rutor.org, whose
// own download hosts serve nothing usable, must still get a real file from
// the canonical mirror.
func TestDownload_FallsBackToCanonicalDownloadHost(t *testing.T) {
	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "rutor" {
			return registry.DomainConfig{Active: "new-rutor.org"}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })

	hash := torrentInfohash(t)
	page := topicPage(hash)
	var served []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/download/") {
			_, _ = w.Write([]byte(page))
			return
		}
		orig := r.Header.Get("X-Orig-Host")
		served = append(served, orig)
		switch orig {
		case "d.new-rutor.org": // certificate does not cover it in the wild
			w.WriteHeader(http.StatusBadGateway)
		case "new-rutor.org": // its own /download/ answers 200 with HTML
			_, _ = w.Write([]byte("<html><body>not a torrent</body></html>"))
		default: // d.rutor.info — the canonical mirror serves the bytes
			_, _ = w.Write([]byte(realTorrent))
		}
	}))
	t.Cleanup(srv.Close)

	rec := &hostRecordingRewrite{target: strings.TrimPrefix(srv.URL, "http://")}
	p := &plugin{httpClient: &http.Client{Transport: rec}}

	payload, err := p.Download(context.Background(), &domain.Topic{URL: "https://new-rutor.org/torrent/12345/a"}, nil, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(payload.TorrentFile) == 0 {
		t.Fatalf("fell back to a magnet; download hosts tried: %v", served)
	}
	want := []string{"d.new-rutor.org", "new-rutor.org", "d.rutor.info"}
	if !reflect.DeepEqual(served, want) {
		t.Errorf("download hosts tried = %v, want %v", served, want)
	}
}

func TestDownload_FallsBackToMagnetWhenTorrentHostFails(t *testing.T) {
	registry.SetDomainResolver(nil)
	hash := torrentInfohash(t)
	p, _ := downloadServer(t, hash, http.StatusNotFound, "")

	topic := &domain.Topic{URL: "https://rutor.info/torrent/12345/a-release"}
	payload, err := p.Download(context.Background(), topic, nil, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(payload.TorrentFile) != 0 {
		t.Error("TorrentFile should be empty when the download host 404s")
	}
	if !strings.Contains(payload.MagnetURI, hash) {
		t.Errorf("MagnetURI = %q, want the page magnet", payload.MagnetURI)
	}
}

// TestDownload_FallsBackWhenDownloadHostServesHTML covers the observed
// new-rutor.org behaviour: its download link answers 200 with an HTML page,
// which must never be handed to a torrent client as a file.
func TestDownload_FallsBackWhenDownloadHostServesHTML(t *testing.T) {
	registry.SetDomainResolver(nil)
	hash := torrentInfohash(t)
	p, _ := downloadServer(t, hash, 200, "<html><body>not a torrent</body></html>")

	topic := &domain.Topic{URL: "https://rutor.info/torrent/12345/a-release"}
	payload, err := p.Download(context.Background(), topic, nil, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(payload.TorrentFile) != 0 {
		t.Error("an HTML body must not be returned as TorrentFile")
	}
	if !strings.Contains(payload.MagnetURI, hash) {
		t.Errorf("MagnetURI = %q, want the page magnet", payload.MagnetURI)
	}
}

// TestDownload_RejectsMismatchedTorrent guards the delivery bookkeeping:
// Check derives the topic hash from the page magnet, so a file with a
// different infohash would record a delivery that never matches the check.
func TestDownload_RejectsMismatchedTorrent(t *testing.T) {
	registry.SetDomainResolver(nil)
	other := fixtureInfohash
	p, _ := downloadServer(t, other, 200, realTorrent)

	topic := &domain.Topic{URL: "https://rutor.info/torrent/12345/a-release"}
	payload, err := p.Download(context.Background(), topic, nil, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(payload.TorrentFile) != 0 {
		t.Error("a torrent whose infohash differs from the magnet must be rejected")
	}
	if !strings.Contains(payload.MagnetURI, other) {
		t.Errorf("MagnetURI = %q, want the page magnet", payload.MagnetURI)
	}
}

func TestTorrentURLs_ActiveThenCanonical(t *testing.T) {
	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "rutor" {
			return registry.DomainConfig{Active: "new-rutor.org"}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })

	p := &plugin{}
	want := []string{
		"https://d.new-rutor.org/download/42",
		"https://new-rutor.org/download/42",
		"https://d.rutor.info/download/42",
		"https://rutor.info/download/42",
	}
	if got := p.torrentURLs("42"); !reflect.DeepEqual(got, want) {
		t.Errorf("torrentURLs = %v, want %v", got, want)
	}

	// On the canonical domain the seen-set collapses the duplicate pair.
	registry.SetDomainResolver(nil)
	want = []string{"https://d.rutor.info/download/42", "https://rutor.info/download/42"}
	if got := p.torrentURLs("42"); !reflect.DeepEqual(got, want) {
		t.Errorf("torrentURLs on canonical domain = %v, want %v", got, want)
	}
}

func newSearchTestPlugin(t *testing.T, handler http.HandlerFunc) *plugin {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &plugin{httpClient: &http.Client{
		Transport: &e2etest.HostRewriteTransport{
			From: defaultDomain, To: strings.TrimPrefix(srv.URL, "http://"), StripSubdomain: true,
		},
	}}
}

const searchFixtureHTML = `<html><body><table>
<tr><td>Header row without link</td></tr>
<tr class="gai"><td>22&nbsp;&#1048;&#1102;&#1083;&nbsp;26</td><td>
<a class="downgif" href="/download/975045"><img src="/s/i/d.gif" alt="D"></a>
<a href="magnet:?xt=urn:btih:aaaabbbbccccddddeeeeffff0000111122223333"><img src="/s/i/m.gif" alt="M"></a>
<a href="/torrent/975045/test-release-1080p">Test release <b>1080p</b></a></td>
<td align="right">1.4&nbsp;GB</td>
<td align="center"><span class="green">&nbsp;17&nbsp;</span>&nbsp;<span class="red">3</span></td></tr>
<tr class="tum"><td>21&nbsp;&#1048;&#1102;&#1083;&nbsp;26</td><td>
<a class="downgif" href="/download/975001"><img src="/s/i/d.gif" alt="D"></a>
<a href="/torrent/975001/another-release">Another release</a></td>
<td align="right">804.9&nbsp;MB</td>
<td align="center"><span class="green">2</span></td></tr>
</table></body></html>`

func TestSearch_ParsesResultsFromFixture(t *testing.T) {
	registry.SetDomainResolver(nil)
	var gotPath string
	p := newSearchTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(searchFixtureHTML))
	})
	results, err := p.Search(context.Background(), "test query", nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotPath != "/search/0/0/000/0/test%20query" {
		t.Errorf("request path = %q, want /search/0/0/000/0/test%%20query", gotPath)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	first := results[0]
	if first.Title != "Test release 1080p" {
		t.Errorf("Title = %q", first.Title)
	}
	if first.URL != "https://rutor.info/torrent/975045" {
		t.Errorf("URL = %q", first.URL)
	}
	if first.Size != "1.4 GB" {
		t.Errorf("Size = %q", first.Size)
	}
	if first.Seeders != 17 {
		t.Errorf("Seeders = %d", first.Seeders)
	}
	if results[1].Seeders != 2 {
		t.Errorf("second Seeders = %d", results[1].Seeders)
	}
}

func TestSearch_EmptyQuery_NoRequest(t *testing.T) {
	called := false
	p := newSearchTestPlugin(t, func(http.ResponseWriter, *http.Request) { called = true })
	results, err := p.Search(context.Background(), "   ", nil)
	if err != nil || results != nil {
		t.Fatalf("empty query: results=%v err=%v, want nil,nil", results, err)
	}
	if called {
		t.Error("empty query must not hit the tracker")
	}
}

func TestSearch_NoRows_EmptyNotError(t *testing.T) {
	p := newSearchTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body>no matches</body></html>`))
	})
	results, err := p.Search(context.Background(), "nothing", nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("results = %d, want 0", len(results))
	}
}

func TestSearch_FetchError_Propagates(t *testing.T) {
	p := newSearchTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	if _, err := p.Search(context.Background(), "anything", nil); err == nil {
		t.Fatal("Search on a 503 must return an error")
	}
}

func TestSearch_CapsAtFiftyResults(t *testing.T) {
	var rows strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&rows,
			`<tr class="gai"><td>d</td><td><a href="/torrent/%d/slug">Release %d</a></td>`+
				`<td align="right">1 GB</td><td align="center"><span class="green">1</span></td></tr>`, i, i)
	}
	page := "<html><body><table>" + rows.String() + "</table></body></html>"
	p := newSearchTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(page))
	})
	results, err := p.Search(context.Background(), "many", nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 50 {
		t.Errorf("results = %d, want capped at 50", len(results))
	}
}
