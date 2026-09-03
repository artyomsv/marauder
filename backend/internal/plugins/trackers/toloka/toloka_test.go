package toloka

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// The fixtures below are captured verbatim from toloka.to on 2026-09-03,
// only trimmed. The previous fixture was invented — it used a
// "Серіал :: Toloka.to" title and an English "Info hash:" label, neither of
// which exists on the site — so the tests it backed proved only that the
// regexes matched a page nobody had ever served.

// fixtureTorrentBlock is the release table, including the traps that broke
// the first draft: the size lives in a <span> whose title attribute holds a
// DIFFERENT size (the piece size), and the download link's href is jammed
// against the preceding attribute with no space.
const fixtureTorrentBlock = `<table width="95%" border="0" cellpadding="2" cellspacing="1" class="btTbl" align="center">
<tr class="row6_to"><td colspan="3" class="gen" align="center" style="padding: 3px"><b>&nbsp;Twisted.Metal.S02.WEBDL.1080p.Ukr.Eng.Dniprofilm.torrent&nbsp;</b></td></tr>
<tr class="row4_to"><td width="110" class="genmed">&nbsp;Трекер:&nbsp;</td><td class="genmed">&nbsp;
<!-- Кінець статусу роздачі --> Зареєстрований</td>
<td width="165" class="gensmall" rowspan="6" align="center" style="padding: 5px"><h3><strong><a title="Завантажити торрент" rel="nofollow" class="piwik_download"href="download.php?id=714902">Завантажити</a></strong></h3></td></tr>
<tr class="row4_to"><td class="genmed">&nbsp;Зареєстрований:&nbsp;</td><td class="genmed">&nbsp;2026-09-03 12:23</td></tr>
<tr class="row4_to"><td class="genmed">&nbsp;Розмір:&nbsp;</td><td class="genmed"><span title="Розмір частини: 2&nbsp;MB">&nbsp;22.01&nbsp;GB&nbsp;</span></td></tr>
<tr class="row4_to"><td class="genmed">&nbsp;Подякували:&nbsp;</td><td class="genmed">&nbsp;<span id="VT714902">2</span></td></tr>
</table>`

const fixtureTopicTitle = `Поплавлений метал (Покручений метал) (Сезон 2) / Twisted Metal (Season 2) (2026) WEB-DL 1080p Ukr/Eng | Sub Eng — HD українською`

var fixtureTopicHTML = `<html><head><title>` + fixtureTopicTitle + `</title>
<meta property="og:image" content="https://thumb.hurtom.com/image/w250/toloka.to/photos/120227013255132137_f0_0.jpg">
<link rel="image_src" href="https://thumb.hurtom.com/image/w250/toloka.to/photos/120227013255132137_f0_0.jpg">
</head><body>
<div class="postbody">Опис релізу</div>
` + fixtureTorrentBlock + `
</body></html>`

// fixtureGuestHTML is what an anonymous visitor gets for the SAME release:
// an empty title and no torrent block at all.
const fixtureGuestHTML = `<html><head><title></title></head><body>
<div class="genmed">Зареєструватися</div><div class="genmed">Вхід</div>
</body></html>`

// fixtureSearchHTML is one real result row. Both column traps are present:
// the size cell's title attribute holds a download SPEED (" 7&nbsp;MB/s "),
// and the uploader's profile link supplies the row's next </a>.
const fixtureSearchHTML = `<html><body><table>
<tr class="prow1">
<td align="center"></td>
<td align="center" class="gen"><a class="gen" href="tracker.php?f=173">Серіали в HD</a></td>
<td title="" class="topictitle genmed"><a class="genmed" href="t699998"><b>Поплавлений метал / Twisted Metal (Season 2) (2026) WEB-DL 1080p</b></td>
<td align="center" class="genmed"><a class="genmed" href="tracker.php?pid=824178">staf777</a></td>
<td align="center" class="genmed" nowrap="nowrap"><a class="genmed" href="download.php?id=714902">[ <span class="bold">DL</span> ]</a></td>
<td align="center" title=" 7&nbsp;MB/s " nowrap="nowrap" class="gensmall">22.01 GB</td>
<td align="center" title="Завантажено" class="gensmall">?</td>
<td align="center" title="Seeders" class="seedmed"><b>3</b></td>
<td align="center" title="Завантажують" class="leechmed"><b>8</b></td>
<td align="center" nowrap="nowrap" title="Написане" class="gensmall">2026-09-03</td>
</tr>
</table></body></html>`

// guestCookie / userCookie have the real toloka_data SHAPE, percent-encoded
// the way the server sends it, with invented values. The autologinid field is
// a phpBB persistent-login key: presenting it with a userid signs in without
// the password, so a captured one is a credential and must never be committed.
// The tests only need userid > 0.
const (
	guestCookie = `a%3A2%3A%7Bs%3A11%3A%22autologinid%22%3Bs%3A0%3A%22%22%3Bs%3A6%3A%22userid%22%3Bi%3A-1%3B%7D`
	userCookie  = `a%3A2%3A%7Bs%3A11%3A%22autologinid%22%3Bs%3A32%3A%2200000000000000000000000000000000%22%3Bs%3A6%3A%22userid%22%3Bi%3A42%3B%7D`
)

// wrongPasswordHTML is the live failure page's message. Note what it does
// NOT contain: the words "помилка" or "error", which the previous Login
// searched for — so a rejected password was reported as a success.
const wrongPasswordHTML = `<html><body><table><tr><td class="gen">
Такий псевдонім не існує, або не збігається пароль.
</td></tr></table></body></html>`

func testCreds() *domain.TrackerCredential {
	return &domain.TrackerCredential{
		UserID:    uuid.MustParse("11111111-2222-3333-4444-555555555555"),
		Username:  "someone",
		SecretEnc: []byte("secret"),
	}
}

// newTestPlugin points a plugin at srv while the request URL keeps saying
// https://toloka.to, so CanParse, the host guard and every built URL stay
// real.
//
// It redirects the DIAL rather than rewriting req.URL, which matters here
// specifically: net/http reads and writes the cookie jar keyed on req.URL,
// and a transport that rewrites that URL in place (the shared
// e2etest.HostRewriteTransport) makes the jar file cookies under 127.0.0.1
// while the plugin looks them up under toloka.to. Every session assertion
// would then fail for a reason that does not exist in production.
func newTestPlugin(t *testing.T, h http.HandlerFunc) (*plugin, *httptest.Server) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "https://")
	return &plugin{
		sessions: forumcommon.New(),
		domain:   defaultDomain,
		transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // httptest's self-signed cert
		},
	}, srv
}

func TestCanParse_KnownURLs_MatchExpected(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"topic", "https://toloka.to/t699998", true},
		{"topic with www", "https://www.toloka.to/t699998", true},
		{"http is still parseable", "http://toloka.to/t699998", true},
		{"not a topic path", "https://toloka.to/tracker.php?nm=x", false},
		{"off-site", "https://evil.example/t699998", false},
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

func TestParse_ExtractsTopicID(t *testing.T) {
	p := &plugin{}
	topic, err := p.Parse(context.Background(), "https://toloka.to/t699998")
	if err != nil {
		t.Fatal(err)
	}
	if topic.Extra["topic_id"] != 699998 {
		t.Errorf("topic_id = %v", topic.Extra["topic_id"])
	}
}

// TestCanonicalURL_ForcesHTTPS: urlPattern accepts http, and a stored
// http:// topic would put the session cookie on the wire in plaintext.
func TestCanonicalURL_ForcesHTTPS(t *testing.T) {
	registry.SetDomainResolver(nil)
	p := &plugin{domain: defaultDomain}
	got, err := p.canonicalURL("http://toloka.to/t699998")
	if err != nil {
		t.Fatalf("canonicalURL: %v", err)
	}
	if want := "https://toloka.to/t699998"; got != want {
		t.Errorf("canonicalURL = %q, want %q", got, want)
	}
}

func TestDomains_CanonicalFirst(t *testing.T) {
	p := &plugin{}
	if want := []string{"toloka.to"}; !reflect.DeepEqual(p.Domains(), want) {
		t.Errorf("Domains() = %v, want %v", p.Domains(), want)
	}
}

// TestCheckTarget_RejectsOffSiteHost closes the host guard this plugin never
// had: fetch would previously dial any host a stored URL named.
func TestCheckTarget_RejectsOffSiteHost(t *testing.T) {
	bad := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://localhost:6379/",
		"https://evil.com/t1",
		"https://toloka.to.evil.com/t1",
		"ftp://toloka.to/t1",
		// Plain http would let the jar attach the session cookie in the clear.
		"http://toloka.to/t1",
	}
	for _, raw := range bad {
		t.Run(raw, func(t *testing.T) {
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatalf("bad test URL: %v", err)
			}
			if err := checkTarget(u); err == nil {
				t.Errorf("checkTarget(%q) should be refused", raw)
			}
		})
	}
	u, _ := url.Parse("https://toloka.to/t1")
	if err := checkTarget(u); err != nil {
		t.Errorf("checkTarget on the real host failed: %v", err)
	}
}

func TestSessionUserID_CookieVariants_ReportTheServersView(t *testing.T) {
	tests := []struct {
		name    string
		cookie  string
		wantID  int
		wantOK  bool
		noStore bool
	}{
		{name: "guest", cookie: guestCookie, wantID: -1, wantOK: false},
		{name: "logged in", cookie: userCookie, wantID: 42, wantOK: true},
		{name: "no cookie at all", noStore: true},
		{name: "unparseable value", cookie: "not-a-php-blob", wantID: 0, wantOK: false},
	}
	u, _ := url.Parse("https://toloka.to")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := forumcommon.NewSession(userAgent)
			if !tt.noStore {
				sess.Client.Jar.SetCookies(u, []*http.Cookie{{Name: sessionCookie, Value: tt.cookie}})
			}
			id, ok := sessionUserID(sess, "https://toloka.to")
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if tt.wantOK && id != tt.wantID {
				t.Errorf("id = %d, want %d", id, tt.wantID)
			}
		})
	}
}

func TestCleanTitle_StripsForumSection(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"real title", fixtureTopicTitle, "Поплавлений метал (Покручений метал) (Сезон 2) / Twisted Metal (Season 2) (2026) WEB-DL 1080p Ukr/Eng | Sub Eng"},
		{"another section", "Назва релізу — Переклад ігор українською", "Назва релізу"},
		{"no section", "Назва релізу", "Назва релізу"},
		{"entities decoded", "A &amp; B — HD українською", "A & B"},
		// A hyphen is not the template's spaced em dash.
		{"hyphenated name survives", "Spider-Man 2 — Ігри", "Spider-Man 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanTitle(tt.in); got != tt.want {
				t.Errorf("cleanTitle(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestFingerprintInput_ReadsTheRealBlock asserts the human-readable token
// input rather than a golden digest, so a reviewer can see what changed.
func TestFingerprintInput_ReadsTheRealBlock(t *testing.T) {
	block, ok := torrentBlock([]byte(fixtureTopicHTML))
	if !ok {
		t.Fatal("torrentBlock not found in the real fixture")
	}
	got := fingerprintInput(block)
	want := strings.Join([]string{
		"id=714902",
		"name=Twisted.Metal.S02.WEBDL.1080p.Ukr.Eng.Dniprofilm.torrent",
		// The size cell's span carries title="Розмір частини: 2 MB"; taking
		// that would track the piece size instead of the release size.
		"size=22.01 GB",
		"registered=2026-09-03 12:23",
	}, "\x00")
	if got != want {
		t.Errorf("fingerprintInput =\n  %q\nwant\n  %q", got, want)
	}
}

func TestPageFingerprint_ChangesOnlyWithTheRelease(t *testing.T) {
	block, _ := torrentBlock([]byte(fixtureTopicHTML))
	base := pageFingerprint(block)
	if len(base) != 64 {
		t.Fatalf("token = %q, want a sha256 hex digest", base)
	}
	// Same content, different encoding of the same whitespace: the token
	// must not move, or every Toloka topic re-downloads at once the day the
	// template changes an &nbsp; to a space.
	reencoded := strings.ReplaceAll(fixtureTopicHTML, "&nbsp;22.01&nbsp;GB&nbsp;", " 22.01 GB ")
	rb, _ := torrentBlock([]byte(reencoded))
	if got := pageFingerprint(rb); got != base {
		t.Error("token changed when only the whitespace encoding changed")
	}
	// A re-uploaded torrent must move it.
	for _, changed := range []string{
		strings.ReplaceAll(fixtureTopicHTML, "download.php?id=714902", "download.php?id=800000"),
		strings.ReplaceAll(fixtureTopicHTML, "22.01&nbsp;GB", "23.50&nbsp;GB"),
		strings.ReplaceAll(fixtureTopicHTML, "2026-09-03 12:23", "2026-09-04 09:00"),
		strings.ReplaceAll(fixtureTopicHTML, "Dniprofilm.torrent", "Other.torrent"),
	} {
		cb, _ := torrentBlock([]byte(changed))
		if got := pageFingerprint(cb); got == base {
			t.Error("token did not change for a re-uploaded torrent")
		}
	}
	// A counter the uploader did not touch must NOT move the token. The
	// trimmed fixture keeps the "Подякували" (thanks) row rather than the
	// seeder row, but both are the same class of drifting value.
	seeded := strings.ReplaceAll(fixtureTopicHTML, `<span id="VT714902">2</span>`, `<span id="VT714902">99</span>`)
	sb, _ := torrentBlock([]byte(seeded))
	if got := pageFingerprint(sb); got != base {
		t.Error("token moved on a counter the uploader did not touch")
	}
}

func TestCheck_ParsesTheRealTopicPage(t *testing.T) {
	registry.SetDomainResolver(nil)
	p, _ := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(fixtureTopicHTML))
	})
	check, err := p.Check(context.Background(), &domain.Topic{URL: "https://toloka.to/t699998"}, testCreds())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(check.Hash) != 64 {
		t.Errorf("Hash = %q, want a sha256 change token", check.Hash)
	}
	if strings.Contains(check.DisplayName, " — ") {
		t.Errorf("DisplayName still carries the forum section: %q", check.DisplayName)
	}
	if !strings.HasPrefix(check.DisplayName, "Поплавлений метал") {
		t.Errorf("DisplayName = %q", check.DisplayName)
	}
}

// TestCheck_GuestPage_BlamesTheSession: everything is login-gated, so a
// missing torrent block is far more often an expired session than a changed
// page. Reporting a parse failure sends the user hunting for a selector bug.
func TestCheck_GuestPage_BlamesTheSession(t *testing.T) {
	registry.SetDomainResolver(nil)
	p, _ := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: guestCookie, Path: "/"})
		_, _ = w.Write([]byte(fixtureGuestHTML))
	})
	_, err := p.Check(context.Background(), &domain.Topic{URL: "https://toloka.to/t699998"}, testCreds())
	if err == nil {
		t.Fatal("Check on a guest stub must fail")
	}
	// The sentinel is what the scheduler acts on; wording is not a contract.
	if !errors.Is(err, registry.ErrSessionExpired) {
		t.Errorf("error = %v, want registry.ErrSessionExpired", err)
	}
}

func TestDownload_ReturnsTheTorrentAndItsRealName(t *testing.T) {
	registry.SetDomainResolver(nil)
	const torrent = "d8:announce8:udp://x/4:infod6:lengthi1e4:name1:a12:piece lengthi16384e6:pieces0:ee"
	p, _ := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/download.php") || strings.Contains(r.URL.RawQuery, "id=714902") {
			w.Header().Set("Content-Type", "application/x-bittorrent")
			_, _ = w.Write([]byte(torrent))
			return
		}
		_, _ = w.Write([]byte(fixtureTopicHTML))
	})
	payload, err := p.Download(context.Background(), &domain.Topic{URL: "https://toloka.to/t699998"}, nil, testCreds())
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(payload.TorrentFile) != torrent {
		t.Error("TorrentFile is not the served bytes")
	}
	if payload.MagnetURI != "" {
		t.Errorf("MagnetURI = %q, want empty — Toloka publishes none", payload.MagnetURI)
	}
	if payload.FileName != "Twisted.Metal.S02.WEBDL.1080p.Ukr.Eng.Dniprofilm.torrent" {
		t.Errorf("FileName = %q", payload.FileName)
	}
}

// TestDownload_HTMLBodyIsNotATorrent: download.php answers 200 with a page
// when it does not accept the session, and that must never reach a client.
func TestDownload_HTMLBodyIsNotATorrent(t *testing.T) {
	registry.SetDomainResolver(nil)
	p, _ := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "id=714902") {
			http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: guestCookie, Path: "/"})
			_, _ = w.Write([]byte("<html><body>login required</body></html>"))
			return
		}
		_, _ = w.Write([]byte(fixtureTopicHTML))
	})
	_, err := p.Download(context.Background(), &domain.Topic{URL: "https://toloka.to/t699998"}, nil, testCreds())
	if err == nil {
		t.Fatal("an HTML body must not be returned as a torrent")
	}
	if !errors.Is(err, registry.ErrSessionExpired) {
		t.Errorf("error = %v, want registry.ErrSessionExpired — the gate page means a dead session", err)
	}
}

func TestLogin_SucceedsOnSessionCookie(t *testing.T) {
	registry.SetDomainResolver(nil)
	p, _ := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			// The live server answers 302 with an EMPTY body.
			http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: userCookie, Path: "/"})
			// The live server answers 302 with a Location, which the client follows.
			http.Redirect(w, r, "/index.php", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("<html><body>index</body></html>"))
	})
	creds := testCreds()
	if err := p.Login(context.Background(), creds); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if id, ok := sessionUserID(p.session(creds), p.baseURL()); !ok || id != 42 {
		t.Errorf("session userid = %d ok=%v, want 42 true", id, ok)
	}
}

// TestLogin_RejectsWrongPassword is the bug that made this plugin dangerous:
// the failure page contains neither "помилка" nor "error", so the old
// substring check reported a rejected password as a successful login.
func TestLogin_RejectsWrongPassword(t *testing.T) {
	registry.SetDomainResolver(nil)
	p, _ := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: guestCookie, Path: "/"})
		_, _ = w.Write([]byte(wrongPasswordHTML))
	})
	err := p.Login(context.Background(), testCreds())
	if err == nil {
		t.Fatal("a rejected password must not be reported as a successful login")
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("error = %v, want it to name the rejected credentials", err)
	}
	if strings.Contains(wrongPasswordHTML, "помилка") || strings.Contains(wrongPasswordHTML, "error") {
		t.Fatal("fixture drifted: the point is that the failure page contains neither marker")
	}
}

func TestVerify_ReflectsTheServersView(t *testing.T) {
	registry.SetDomainResolver(nil)
	for _, tt := range []struct {
		name   string
		cookie string
		want   bool
	}{
		{"signed in", userCookie, true},
		{"guest", guestCookie, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
				http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tt.cookie, Path: "/"})
				_, _ = w.Write([]byte("<html><body>index</body></html>"))
			})
			got, err := p.Verify(context.Background(), testCreds())
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if got != tt.want {
				t.Errorf("Verify = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSearch_ParsesTheRealRow(t *testing.T) {
	registry.SetDomainResolver(nil)
	var gotQuery string
	p, _ := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("nm")
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: userCookie, Path: "/"})
		_, _ = w.Write([]byte(fixtureSearchHTML))
	})
	results, err := p.Search(context.Background(), "Твістед", testCreds())
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Toloka serves UTF-8; cp1251 escaping would corrupt this.
	if gotQuery != "Твістед" {
		t.Errorf("query reached the tracker as %q", gotQuery)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	r := results[0]
	if r.Title != "Поплавлений метал / Twisted Metal (Season 2) (2026) WEB-DL 1080p" {
		t.Errorf("Title = %q — the uploader's profile link must not bleed in", r.Title)
	}
	if r.URL != "https://toloka.to/t699998" {
		t.Errorf("URL = %q", r.URL)
	}
	// The size cell's title attribute holds a download speed of 7 MB/s.
	if r.Size != "22.01 GB" {
		t.Errorf("Size = %q, want the cell text rather than the speed attribute", r.Size)
	}
	if r.Seeders != 3 {
		t.Errorf("Seeders = %d, want 3", r.Seeders)
	}
}

// TestSearch_WithoutCredentials_ReportsTheRealReason: tracker.php serves a
// guest the form with zero rows and no error, so "no results" would be a lie.
func TestSearch_WithoutCredentials_ReportsTheRealReason(t *testing.T) {
	registry.SetDomainResolver(nil)
	called := false
	p, _ := newTestPlugin(t, func(http.ResponseWriter, *http.Request) { called = true })
	_, err := p.Search(context.Background(), "anything", nil)
	if err == nil {
		t.Fatal("a search with no account must not report an empty result set")
	}
	if called {
		t.Error("a credential-less search must not hit the tracker at all")
	}
}

func TestSearch_EmptyQuery_NoRequest(t *testing.T) {
	called := false
	p, _ := newTestPlugin(t, func(http.ResponseWriter, *http.Request) { called = true })
	results, err := p.Search(context.Background(), "   ", testCreds())
	if err != nil || results != nil {
		t.Fatalf("empty query: results=%v err=%v, want nil,nil", results, err)
	}
	if called {
		t.Error("empty query must not hit the tracker")
	}
}

func TestDo_RateLimitIsNamed(t *testing.T) {
	registry.SetDomainResolver(nil)
	p, _ := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := p.fetch(context.Background(), "https://toloka.to/t1", testCreds())
	if err == nil {
		t.Fatal("a 429 must be an error")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("error = %v, want it to name the rate limit", err)
	}
}

// TestFetch_RefusesOffSiteRedirect proves the redirect half of the SSRF
// barrier is actually wired onto the session's client, not merely that
// checkTarget rejects the URL when called directly.
func TestFetch_RefusesOffSiteRedirect(t *testing.T) {
	registry.SetDomainResolver(nil)
	p, _ := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.example/x", http.StatusFound)
	})
	_, err := p.fetch(context.Background(), "https://toloka.to/t699998", testCreds())
	if err == nil {
		t.Fatal("an off-site redirect must be refused")
	}
	if !strings.Contains(err.Error(), "refusing to fetch off-site host") {
		t.Errorf("error = %v, want the off-site host refusal", err)
	}
}

// TestFingerprintInput_WithoutDownloadID_IsEmpty: the download id is the one
// structurally stable field, so losing it must fail the check rather than
// quietly move every stored token and re-deliver every topic.
func TestFingerprintInput_WithoutDownloadID_IsEmpty(t *testing.T) {
	block := strings.ReplaceAll(fixtureTorrentBlock, "download.php?id=714902", "somewhere.php")
	inner, ok := torrentBlock([]byte(block))
	if !ok {
		t.Fatal("fixture block not found")
	}
	if got := fingerprintInput(inner); got != "" {
		t.Errorf("fingerprintInput without a download id = %q, want empty", got)
	}
}

// TestDo_OversizedBodyIsRefused: io.ReadAll on a bare LimitReader cannot tell
// a body that ended from one that was cut off, so a truncated .torrent would
// still pass isTorrent's first-byte check on its way to a client.
func TestDo_OversizedBodyIsRefused(t *testing.T) {
	registry.SetDomainResolver(nil)
	p, _ := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		big := make([]byte, maxBodyBytes+1024)
		for i := range big {
			big[i] = 'd'
		}
		_, _ = w.Write(big)
	})
	_, err := p.fetch(context.Background(), "https://toloka.to/t1", testCreds())
	if err == nil {
		t.Fatal("an oversized body must be refused, not silently truncated")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error = %v, want it to name the size limit", err)
	}
}

// TestDo_RateLimitCarriesTheStatusMarker: the scheduler's classifyError reads
// the status out of the message text, so a Toloka 429 must carry the same
// "-> 429" marker every other tracker's does or it classifies as "unknown".
// The search query must NOT appear — it is the user's own text.
func TestDo_RateLimitCarriesTheStatusMarker(t *testing.T) {
	registry.SetDomainResolver(nil)
	p, _ := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := p.Search(context.Background(), "секретний запит", testCreds())
	if err == nil {
		t.Fatal("a 429 must be an error")
	}
	if !strings.Contains(err.Error(), "-> 429") {
		t.Errorf("error = %v, want the -> 429 marker classifyError looks for", err)
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("error = %v, want it to name the rate limit", err)
	}
	if strings.Contains(err.Error(), "секретний") {
		t.Errorf("the search query leaked into the error: %v", err)
	}
}

// TestResolveMetadata_ReadsTitleAndOgImage. The poster was twice reported
// missing and twice written off as "Toloka has none", because searching the
// post BODY for an <img> finds only site chrome, avatars and smilies. It is in
// the <head>, as og:image, served through thumb.hurtom.com. This pins that,
// offline — the function was previously reachable only under `-tags=live`,
// which is exactly the test nobody may run.
func TestResolveMetadata_ReadsTitleAndOgImage(t *testing.T) {
	p, _ := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: userCookie})
		_, _ = w.Write([]byte(fixtureTopicHTML))
	})

	meta, err := p.ResolveMetadata(context.Background(), "https://toloka.to/t699998", testCreds())
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	if meta.Title != cleanTitle(fixtureTopicTitle) {
		t.Errorf("Title = %q, want the cleaned page title", meta.Title)
	}
	const wantImage = "https://thumb.hurtom.com/image/w250/toloka.to/photos/120227013255132137_f0_0.jpg"
	if meta.ImageURL != wantImage {
		t.Errorf("ImageURL = %q, want %q", meta.ImageURL, wantImage)
	}
}

// TestResolveMetadata_GuestStubReportsTheSessionNotAParseFailure. A guest gets
// <title></title>. Reporting that as "no title on the page" points the user at
// a broken selector; the honest answer is that the request was never signed
// in, which is what nothing-visible-without-an-account means here.
func TestResolveMetadata_GuestStubReportsTheSessionNotAParseFailure(t *testing.T) {
	p, _ := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: guestCookie})
		_, _ = w.Write([]byte(fixtureGuestHTML))
	})

	_, err := p.ResolveMetadata(context.Background(), "https://toloka.to/t699998", testCreds())
	if !errors.Is(err, registry.ErrSessionExpired) {
		t.Errorf("err = %v, want ErrSessionExpired", err)
	}
}

// TestResolveMetadata_NoPoster_IsAnEmptyStringNotAnError. Not every release
// carries artwork, and topics.SafeImageURL stores "" honestly rather than
// inventing a placeholder — so a missing og:image must not fail the resolve
// and cost the topic its real title too.
func TestResolveMetadata_NoPoster_IsAnEmptyStringNotAnError(t *testing.T) {
	page := `<html><head><title>` + fixtureTopicTitle + `</title></head><body>` +
		fixtureTorrentBlock + `</body></html>`
	p, _ := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: userCookie})
		_, _ = w.Write([]byte(page))
	})

	meta, err := p.ResolveMetadata(context.Background(), "https://toloka.to/t699998", testCreds())
	if err != nil {
		t.Fatalf("a release with no poster must still resolve: %v", err)
	}
	if meta.ImageURL != "" {
		t.Errorf("ImageURL = %q, want empty", meta.ImageURL)
	}
	if meta.Title == "" {
		t.Error("the title must survive a missing poster")
	}
}
