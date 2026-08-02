package anidub

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
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

// TestLogin_DoesNotReuseAnEstablishedSession is the regression test for a
// false-green that survives the rest of this file's fixes.
//
// The session store keys jars by tracker:userID and hands the SAME jar back for
// two hours, and the scheduler calls Login on every check — so a user with
// working credentials always has a warm, authenticated jar. A credential
// re-test or a password rotation would then POST the new (possibly wrong)
// password onto that already-authenticated jar: the tracker renders the
// signed-in page, no rejection marker matches, and Verify confirms a session
// that the credential under test never established.
//
// Login must therefore validate against a fresh jar, so the cookies it sends
// are only ones this attempt earned.
func TestLogin_DoesNotReuseAnEstablishedSession(t *testing.T) {
	var loginCookies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			loginCookies = append(loginCookies, r.Header.Get("Cookie"))
			http.SetCookie(w, &http.Cookie{Name: "dle_user_id", Value: "42", Path: "/"})
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(loggedInHTML))
	}))
	t.Cleanup(srv.Close)
	rec := &hostRecordingRewrite{target: strings.TrimPrefix(srv.URL, "http://")}
	p := &plugin{sessions: forumcommon.New(), domain: defaultDomain, transport: rec}

	creds := testCreds()
	if err := p.Login(context.Background(), creds); err != nil {
		t.Fatalf("first Login: %v", err)
	}
	if err := p.Login(context.Background(), creds); err != nil {
		t.Fatalf("second Login: %v", err)
	}

	if len(loginCookies) != 2 {
		t.Fatalf("expected 2 login POSTs, got %d", len(loginCookies))
	}
	if loginCookies[1] != "" {
		t.Errorf("second Login carried cookies from the first session (%q); "+
			"credential validation must start from a fresh jar or Verify is not independent",
			loginCookies[1])
	}
}

// TestLogin_DoesNotStrandConcurrentFetches is the regression test for the
// race the fresh-jar fix introduced.
//
// Sessions are keyed by tracker:userID, so every one of a user's anidub topics
// shares one jar, the scheduler calls Login on every check, and fetch
// re-resolves the jar by key on every call. Invalidating the shared entry
// therefore left a window — between the delete and the login response — where
// the store held an ANONYMOUS jar. A concurrent Download resolving in that
// window dials unauthenticated: on a login-gated download that yields an HTML
// page which becomes Payload.TorrentFile, and recordDelivery fails open, so
// nothing surfaces it.
//
// Validation must get a fresh jar without ever publishing an unauthenticated
// one to the shared store.
func TestLogin_DoesNotStrandConcurrentFetches(t *testing.T) {
	var mu sync.Mutex
	gatedCookie := ""
	blocked := make(chan struct{})
	resume := make(chan struct{})
	var parkOnce sync.Once
	warm := true

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			// Park the SECOND login mid-POST, holding the window open: the
			// shared entry has been swapped but no login response has landed.
			mu.Lock()
			isWarm := warm
			mu.Unlock()
			if !isWarm {
				parkOnce.Do(func() { close(blocked); <-resume })
			}
			http.SetCookie(w, &http.Cookie{Name: "dle_user_id", Value: "42", Path: "/"})
			w.WriteHeader(200)
			_, _ = w.Write([]byte(loggedInHTML))
		case strings.HasPrefix(r.URL.Path, "/engine/download.php"):
			mu.Lock()
			gatedCookie = r.Header.Get("Cookie")
			mu.Unlock()
			w.WriteHeader(200)
			_, _ = w.Write([]byte("d8:announce15:http://x/announcee"))
		default:
			w.WriteHeader(200)
			_, _ = w.Write([]byte(realTopicHTML))
		}
	}))
	t.Cleanup(srv.Close)
	rec := &hostRecordingRewrite{target: strings.TrimPrefix(srv.URL, "http://")}
	p := &plugin{sessions: forumcommon.New(), domain: defaultDomain, transport: rec}
	creds := testCreds()

	// Warm the shared session the way the scheduler does on every check.
	if err := p.Login(context.Background(), creds); err != nil {
		t.Fatalf("warm Login: %v", err)
	}
	mu.Lock()
	warm = false
	mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = p.Login(context.Background(), creds)
	}()
	<-blocked

	if _, err := p.fetch(context.Background(), "https://tr.anidub.com/engine/download.php?id=1", creds); err != nil {
		t.Fatalf("concurrent fetch: %v", err)
	}
	close(resume)
	<-done

	mu.Lock()
	defer mu.Unlock()
	if gatedCookie == "" {
		t.Error("a fetch concurrent with a login dialed with no session cookie; " +
			"validation must not publish an unauthenticated jar to the shared store")
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

// TestVerify_UnrecognisedPage_ReportsUnsupported is the fail-closed inversion.
//
// Only one half of the marker pair is measured: an anonymous page carries
// login_name and no logout link. That a SIGNED-IN page carries action=logout is
// DLE convention, not something observed against a real account. Treating the
// marker's absence as proof of rejection turns that unconfirmed assumption into
// a hard 422 that refuses to store the credential — so if the assumption is
// wrong (JS-injected marker, a different mobile template, an escaped attribute)
// a user with perfectly good credentials cannot add the account at all, and is
// told the credentials are probably wrong.
//
// A page matching neither marker is an unrecognised shape, not a rejection.
// Say so with the sentinel this branch introduced.
func TestVerify_UnrecognisedPage_ReportsUnsupported(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`<html><body><div>something else entirely</div></body></html>`))
	})
	ok, err := p.Verify(context.Background(), testCreds())
	if !errors.Is(err, registry.ErrVerifyUnsupported) {
		t.Fatalf("err = %v, want ErrVerifyUnsupported for a page matching neither marker", err)
	}
	if ok {
		t.Error("ok must be false alongside the sentinel")
	}
}

// TestCheck_IgnoresAnchorOnlyTorrentReferences pins the block-id anchoring.
// DLE templates reference the block id from in-page anchors and inline JS, so
// an unanchored torrent_\d+_info match can find "blocks" on a page that has
// none — letting the non-empty-fingerprint guard pass for a topic with no
// torrent at all.
func TestCheck_IgnoresAnchorOnlyTorrentReferences(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`<html><body>
<h1><span id="news-title">Gone</span></h1>
<a href="#torrent_41936_info">jump</a>
<script>document.getElementById('torrent_41936_info').hide()</script>
</body></html>`))
	})
	if _, err := p.Check(context.Background(), &domain.Topic{URL: "https://tr.anidub.com/anime_tv/x/1-a.html"}, nil); err == nil {
		t.Error("anchor and script references are not torrent blocks; Check must fail")
	}
}

// TestFingerprintInput_IsReadable pins the string the change token digests,
// so the golden hash in the e2e test is reviewable instead of self-confirming:
// a reader can judge this by eye, and the digest of it by running sha256sum.
func TestFingerprintInput_IsReadable(t *testing.T) {
	got := fingerprintInput([]byte(realTopicHTML))
	want := strings.Join([]string{
		"id=41936",
		"id=44590",
		"name=[AniDub]_Pokemon_Horizons_100+.torrent",
		"name=[AniDub]_Pokemon_Horizons.torrent",
		"size=35.34 GB",
		"size=66.16 GB",
	}, "\x00")
	if got != want {
		t.Errorf("fingerprintInput =\n  %q\nwant\n  %q", got, want)
	}
}

// TestCheck_HashSurvivesEntityEncodingChange guards a mass false-update. The
// filename is captured from a title attribute where the site writes [ as
// &#091;. If it ever emits the literal character instead, an encoding-sensitive
// token changes for every anidub topic at once and re-downloads all of them.
func TestCheck_HashSurvivesEntityEncodingChange(t *testing.T) {
	decoded := strings.NewReplacer("&#091;", "[", "&#093;", "]").Replace(realTopicHTML)
	if decoded == realTopicHTML {
		t.Fatal("fixture guard: entity substitution did not apply")
	}
	if got, want := checkFixture(t, decoded).Hash, checkFixture(t, realTopicHTML).Hash; got != want {
		t.Errorf("hash changed on entity encoding alone: %q vs %q", got, want)
	}
}

// TestCheck_TitleDoesNotCaptureMarkup: the h1 fallback runs only when og:title
// is absent, and whatever it returns is persisted by the scheduler's
// placeholder self-heal, which checks for non-empty but not for markup.
func TestCheck_TitleDoesNotCaptureMarkup(t *testing.T) {
	body := `<html><body>
<h1><a href="/x">back</a><span id="news-title">Real Title</span></h1>
<div id='torrent_7_info'>
<div class="list down">Размер: <span class="red">1.00 GB</span></div>
<div class="list torrentname"><span>Имя файла:</span> <span class="red" title="a.torrent">n</span></div>
</div></body></html>`
	got := checkFixture(t, body).DisplayName
	if strings.ContainsAny(got, "<>") {
		t.Errorf("DisplayName = %q, must not contain markup", got)
	}
	if got != "Real Title" {
		t.Errorf("DisplayName = %q, want %q", got, "Real Title")
	}
}

// TestCheck_NoTorrentBlocks_ReturnsError covers the remaining failure path:
// a page that parses but carries no torrent block at all (a deleted topic, or
// selector drift after a redesign) must error rather than report an empty
// change token, which would otherwise read as "nothing new" forever.
func TestCheck_NoTorrentBlocks_ReturnsError(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`<html><body><h1><span id="news-title">Gone</span></h1></body></html>`))
	})
	_, err := p.Check(context.Background(), &domain.Topic{URL: "https://tr.anidub.com/anime_tv/x/1-a.html"}, nil)
	if err == nil {
		t.Fatal("Check must fail when the page carries no torrent block")
	}
}

// TestResolveMetadata_RelativePoster exercises absoluteURL's relative branch —
// the site serves posters from a CDN today, but also emits site-relative
// /uploads/... paths, and a relative src stored raw would not render.
func TestResolveMetadata_RelativePoster(t *testing.T) {
	body := strings.Replace(realTopicHTML,
		`https://static2.statics.life/tracker/poster/6fe93608f3.jpg`,
		`/uploads/posts/local.jpg`, 1)
	if body == realTopicHTML {
		t.Fatal("fixture guard: poster substitution did not apply")
	}
	p := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(body))
	})
	meta, err := p.ResolveMetadata(context.Background(), "https://tr.anidub.com/anime_tv/x/1-a.html", nil)
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	if want := "https://tr.anidub.com/uploads/posts/local.jpg"; meta.ImageURL != want {
		t.Errorf("ImageURL = %q, want %q", meta.ImageURL, want)
	}
}

// TestFetch_RefusesOffSiteHost pins the dial-site SSRF guard: fetch takes an
// arbitrary string, so the allowlist must hold even when a caller forgets to
// route the URL through canonicalURL.
func TestFetch_RefusesOffSiteHost(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("off-site host must never be dialed")
		w.WriteHeader(200)
	})
	for _, target := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"file:///etc/passwd",
	} {
		if _, err := p.fetch(context.Background(), target, nil); err == nil {
			t.Errorf("fetch(%q) must be refused", target)
		}
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

// RoundTrip clones before rewriting. http.RoundTripper's contract forbids
// modifying the request, and mutating req.URL in place breaks cookie handling
// in particular: http.Client calls Jar.SetCookies with the same URL object
// after RoundTrip returns, so an in-place rewrite files the tracker's cookies
// under the test server's host and the jar silently never replays them.
func (h *hostRecordingRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	h.hosts = append(h.hosts, req.URL.Host)
	out := req.Clone(req.Context())
	out.URL.Scheme = "http"
	out.URL.Host = h.target
	return http.DefaultTransport.RoundTrip(out)
}

// TestCheck_RewritesToActiveDomain asserts that when the admin has
// configured an active domain override, Check fetches that host instead of
// the mirror recorded in the stored topic URL — anidub has no id-based
// rebuild, so canonicalURL is the only place this override actually takes
// effect.
func TestCheck_RewritesToActiveDomain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(realTopicHTML))
	}))
	t.Cleanup(srv.Close)

	// Active must also appear in Custom: PUT /system/trackers/{name}/domains
	// rejects an active_domain that is not a known or custom domain (422), and
	// domains.Store.Load drops one that no longer qualifies. An Active that is
	// absent from both cannot exist in production, and fetch's allowlist
	// (known ∪ custom) correctly refuses it.
	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "anidub" {
			return registry.DomainConfig{Active: "anidub.mirror", Custom: []string{"anidub.mirror"}}
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
