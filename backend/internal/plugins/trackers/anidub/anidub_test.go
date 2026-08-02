package anidub

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

// TestCanParse_KnownHost_Matches is a regression assert: anidub's old regex
// was anchored to tr.anidub.com with no www-optional. The new host-agnostic
// pattern (gated by registry.DomainAllowed) must keep matching that exact
// URL shape.
func TestCanParse_KnownHost_Matches(t *testing.T) {
	p := &plugin{domain: defaultDomain}
	if !p.CanParse("https://tr.anidub.com/anime/2026/the-series.html") {
		t.Error("must still match the canonical tr.anidub.com URL shape")
	}
}

func TestCanParse_CustomDomain_AllowedViaResolver(t *testing.T) {
	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "anidub" {
			return registry.DomainConfig{Custom: []string{"anidub.example"}}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{domain: defaultDomain}
	if !p.CanParse("https://anidub.example/anime/2026/the-series.html") {
		t.Error("custom domain should parse")
	}
	if p.CanParse("https://evil.example/anime/2026/the-series.html") {
		t.Error("unlisted domain must not parse")
	}
}

func TestParse_ExtractsSlugAfterHostShift(t *testing.T) {
	p := &plugin{domain: defaultDomain}
	topic, err := p.Parse(context.Background(), "https://tr.anidub.com/anime/2026/the-series.html")
	if err != nil {
		t.Fatal(err)
	}
	if topic.Extra["slug"] != "the-series" {
		t.Errorf("slug = %v, want the-series", topic.Extra["slug"])
	}
}

func TestEffectiveDomain_ResolverOverride(t *testing.T) {
	registry.SetDomainResolver(func(string) registry.DomainConfig {
		return registry.DomainConfig{Active: "anidub.mirror"}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{domain: defaultDomain}
	if got := p.effectiveDomain(); got != "anidub.mirror" {
		t.Errorf("effectiveDomain = %q, want anidub.mirror", got)
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
	want := []string{"tr.anidub.com"}
	if !reflect.DeepEqual(p.Domains(), want) {
		t.Errorf("Domains() = %v, want %v", p.Domains(), want)
	}
}

// loginPage is the shape tr.anidub.com returns to an anonymous visitor and
// to a REJECTED login: HTTP 200, the login form still present, no logout
// link. The rejection sentence is the verbatim wording captured from the
// live site on 2026-08-02 by posting a nonexistent account — note it
// contains neither of the two phrases the plugin used to look for
// ("Доступ запрещён" / "не верный"), which is why every failed login was
// reported as success.
const loginRejectedHTML = `<html><body>
<form><input name="login_name"><input name="login_password"></form>
<div class="berrors">Внимание, вход на сайт не был произведен.
Возможно, Вы ввели неверное имя пользователя или пароль.</div>
</body></html>`

// loggedInHTML carries DLE's logout link, the positive marker Verify keys on.
const loggedInHTML = `<html><body>
<a href="https://tr.anidub.com/index.php?action=logout">Выход</a>
</body></html>`

func newTestPlugin(t *testing.T, handler http.HandlerFunc) *plugin {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	rec := &hostRecordingRewrite{target: strings.TrimPrefix(srv.URL, "http://")}
	return &plugin{sessions: forumcommon.New(), domain: defaultDomain, transport: rec}
}

func testCreds() *domain.TrackerCredential {
	return &domain.TrackerCredential{Username: "someone", SecretEnc: []byte("pw")}
}

// TestLogin_RejectedCredentials_ReturnsError is the regression test for the
// false-green login: the tracker answers 200 with the login form re-rendered,
// and the plugin must treat that as a failure.
func TestLogin_RejectedCredentials_ReturnsError(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(loginRejectedHTML))
	})
	if err := p.Login(context.Background(), testCreds()); err == nil {
		t.Fatal("Login must fail when the tracker re-renders the login form")
	}
}

func TestLogin_NonOKStatus_ReturnsError(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("<html><body>nope</body></html>"))
	})
	if err := p.Login(context.Background(), testCreds()); err == nil {
		t.Fatal("Login must fail on a non-200 response")
	}
}

func TestLogin_Accepted_ReturnsNil(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(loggedInHTML))
	})
	if err := p.Login(context.Background(), testCreds()); err != nil {
		t.Fatalf("Login on an accepted sign-in: %v", err)
	}
}

// TestVerify_AnonymousPage_ReturnsFalse pins the half of Verify that is
// measured rather than assumed: the live anonymous page carries login_name
// twice and action=logout zero times (captured 2026-08-02).
func TestVerify_AnonymousPage_ReturnsFalse(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(loginRejectedHTML))
	})
	ok, err := p.Verify(context.Background(), testCreds())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ok {
		t.Error("Verify must report false for a page with no logout link")
	}
}

func TestVerify_LoggedInPage_ReturnsTrue(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(loggedInHTML))
	})
	ok, err := p.Verify(context.Background(), testCreds())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Error("Verify must report true when the logout link is present")
	}
}

// realTopicHTML mirrors the structure of a live tr.anidub.com topic page,
// captured 2026-08-02. Every element the plugin reads is reproduced exactly as
// the site emits it:
//
//   - the title is inside <span id="news-title">, NOT directly in <h1>
//   - there is no data-hash attribute anywhere on the page (the plugin's
//     original regex matched nothing site-wide, on movie and series pages alike)
//   - a series page carries one torrent block per quality variant
//   - the topic's own poster is scoped to <span class="poster">; an unrelated
//     sidebar poster with a DIFFERENT title appears earlier in the document
const realTopicHTML = `<html><head>
<meta property="og:title" content="Покемон (2023) : Горизонты / Pokémon (2023) Horizons: The Series [144 из XX]" />
<meta name="openstat-verification" content="b5c63e3c0ac502c2cd2d07d20e10e0b8df065ca4" />
</head><body>
<div class="sidebar"><img src="https://static2.statics.life/tracker/poster/sidebar00.jpg" alt="Времена Года / TV-Saitama" /></div>
<h1><span id="news-title">Покемон (2023) : Горизонты / Pokémon (2023) Horizons: The Series [144 из XX]</span></h1>
<span class="poster"><img src="https://static2.statics.life/tracker/poster/6fe93608f3.jpg" alt=""></span>
<div id='torrent_41936_info'>
<a href="/engine/download.php?id=41936">Скачать торрент!</a>
<div class="list down">
Раздают: <span class="li_distribute_m">1</span> Качают: <span class="li_swing_m">0</span> Размер: <span class="red">35.34 GB</span>
</div>
<div class="list torrentname">
<span>Имя файла:</span> <span class="red" title="&#091;AniDub&#093;_Pokemon_Horizons_100+.torrent">name</span>
</div></div>
<div id='torrent_44590_info'>
<a href="/engine/download.php?id=44590">Скачать торрент!</a>
<div class="list down">
Раздают: <span class="li_distribute_m">1</span> Качают: <span class="li_swing_m">0</span> Размер: <span class="red">66.16 GB</span>
</div>
<div class="list torrentname">
<span>Имя файла:</span> <span class="red" title="&#091;AniDub&#093;_Pokemon_Horizons.torrent">name</span>
</div></div>
</body></html>`

func checkFixture(t *testing.T, body string) *domain.Check {
	t.Helper()
	p := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(body))
	})
	check, err := p.Check(context.Background(), &domain.Topic{URL: "https://tr.anidub.com/anime_tv/x/1-a.html"}, nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return check
}

// TestCheck_RealPageWithoutDataHash is the regression test for a topic that
// could never be checked: the plugin looked for data-hash, which this site
// does not emit, so every check failed with "no infohash found".
func TestCheck_RealPageWithoutDataHash(t *testing.T) {
	check := checkFixture(t, realTopicHTML)
	if check.Hash == "" {
		t.Fatal("Check must derive a change token from a page that has no data-hash")
	}
}

// TestCheck_TitleFromNestedSpan pins the second half of the same bug: the old
// title regex required text directly inside <h1>, so a nested <span> left
// DisplayName empty and the scheduler's placeholder self-heal never fired.
func TestCheck_TitleFromNestedSpan(t *testing.T) {
	check := checkFixture(t, realTopicHTML)
	want := "Покемон (2023) : Горизонты / Pokémon (2023) Horizons: The Series [144 из XX]"
	if check.DisplayName != want {
		t.Errorf("DisplayName = %q, want %q", check.DisplayName, want)
	}
}

// TestCheck_HashIgnoresSeederChurn is the important negative: seeder and
// leecher counts move constantly. If they fed the change token, every check
// would look like a new release and re-download the same torrent forever.
func TestCheck_HashIgnoresSeederChurn(t *testing.T) {
	churned := strings.ReplaceAll(realTopicHTML,
		`Раздают: <span class="li_distribute_m">1</span> Качают: <span class="li_swing_m">0</span>`,
		`Раздают: <span class="li_distribute_m">417</span> Качают: <span class="li_swing_m">92</span>`)
	if churned == realTopicHTML {
		t.Fatal("fixture guard: seeder substitution did not apply")
	}
	if got, want := checkFixture(t, churned).Hash, checkFixture(t, realTopicHTML).Hash; got != want {
		t.Errorf("hash changed on seeder churn alone: %q vs %q", got, want)
	}
}

func TestCheck_HashChangesWhenTorrentReuploaded(t *testing.T) {
	reuploaded := strings.ReplaceAll(realTopicHTML, "66.16 GB", "70.02 GB")
	if reuploaded == realTopicHTML {
		t.Fatal("fixture guard: size substitution did not apply")
	}
	if checkFixture(t, reuploaded).Hash == checkFixture(t, realTopicHTML).Hash {
		t.Error("hash must change when a torrent block is re-uploaded")
	}
}

// TestResolveMetadata_TitleAndOwnPoster covers the reported symptom: the
// AddTopic preview came back empty because the plugin had no WithMetadata.
func TestResolveMetadata_TitleAndOwnPoster(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(realTopicHTML))
	})
	meta, err := p.ResolveMetadata(context.Background(), "https://tr.anidub.com/anime_tv/x/1-a.html", nil)
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	title, image := meta.Title, meta.ImageURL
	if !strings.Contains(title, "Покемон") {
		t.Errorf("title = %q, want the real topic title", title)
	}
	// The sidebar poster belongs to a different show and appears FIRST in the
	// document — picking it would show the wrong art for the topic.
	if strings.Contains(image, "sidebar00") {
		t.Errorf("image = %q, want the topic's own poster, not the sidebar's", image)
	}
	if !strings.Contains(image, "6fe93608f3.jpg") {
		t.Errorf("image = %q, want the poster scoped to span.poster", image)
	}
}

// hostRecordingRewrite records the Host of every outgoing request, then
// redirects it to the test server (target) over http so no real network is
// hit. Mirrors the nnmclub/rutor test helper of the same purpose.
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

// fixtureAnidubHTML used to carry a `data-hash` attribute that the live site
// has never emitted — invented markup, which is why the plugin's broken
// selectors passed their tests for so long. It now reuses the captured page
// shape so a fixture can only pass if the real page would.
const fixtureAnidubHTML = realTopicHTML

// TestCheck_RewritesToActiveDomain asserts that when the admin has
// configured an active domain override, Check fetches that host instead of
// the mirror recorded in the stored topic URL — anidub has no id-based
// rebuild, so canonicalURL is the only place this override actually takes
// effect.
func TestCheck_RewritesToActiveDomain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(fixtureAnidubHTML))
	}))
	t.Cleanup(srv.Close)

	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "anidub" {
			return registry.DomainConfig{Active: "anidub.mirror"}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })

	rec := &hostRecordingRewrite{target: strings.TrimPrefix(srv.URL, "http://")}
	p := &plugin{sessions: forumcommon.New(), domain: defaultDomain, transport: rec}

	topic := &domain.Topic{URL: "https://tr.anidub.com/anime/2026/the-series.html"}
	if _, err := p.Check(context.Background(), topic, nil); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(rec.hosts) == 0 {
		t.Fatal("no requests recorded")
	}
	if rec.hosts[0] != "anidub.mirror" {
		t.Errorf("fetch host = %q, want active domain anidub.mirror", rec.hosts[0])
	}
}
