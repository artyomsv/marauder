package tapochek

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// newTestPlugin is the unit-test twin of newE2EPlugin: same dial-redirect so
// the cookie jar files bb_data under tapochek.net, not under 127.0.0.1.
func newTestPlugin(t *testing.T, h http.HandlerFunc) *plugin {
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
	}
}

func testCreds() *domain.TrackerCredential {
	return &domain.TrackerCredential{
		UserID:    uuid.MustParse("11111111-2222-3333-4444-555555555555"),
		Username:  "someone",
		SecretEnc: []byte("secret"),
	}
}

// --- change token -------------------------------------------------------

// TestFingerprintInput_ReadsTheStableFields asserts the readable form of the
// token rather than only a golden digest, so a reviewer can see what a check
// actually depends on.
func TestFingerprintInput_ReadsTheStableFields(t *testing.T) {
	got := fingerprintInput(fixtureTorrentBlock)
	for _, want := range []string{
		"id=189409",
		"name=Lady Death Demonicron [FitGirl Repack] [tapochek.net].torrent",
		"size=1.39 GB",
		"registered=04-09-2026 00:16",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("fingerprint input %q missing %q", got, want)
		}
	}
}

// TestFingerprintInput_SizeIsTheReleaseNotTheTorrentFile pins the trap the
// block carries: "Размер .torrent файла 9 KB" sits ABOVE the release's own
// "Размер:" row, so a loose match reports the size of the .torrent instead of
// the release — a value that barely moves, making real re-uploads invisible.
func TestFingerprintInput_SizeIsTheReleaseNotTheTorrentFile(t *testing.T) {
	got := fingerprintInput(fixtureTorrentBlock)
	if strings.Contains(got, "size=9 KB") {
		t.Error("captured the .torrent file size instead of the release size")
	}
	if !strings.Contains(got, "size=1.39 GB") {
		t.Errorf("input = %q, want the release size", got)
	}
}

// TestFingerprintInput_DateIsNotTheTitleAttribute pins the other trap: the
// registration <span> carries title="10 часов", a relative age that changes
// every hour. Capturing it would make every check look like a new release.
func TestFingerprintInput_DateIsNotTheTitleAttribute(t *testing.T) {
	got := fingerprintInput(fixtureTorrentBlock)
	if strings.Contains(got, "часов") {
		t.Errorf("captured the relative-age title attribute: %q", got)
	}
}

// TestPageFingerprint_IgnoresCountersThatDriftOnTheirOwn. Download and thanks
// counts move without anything being re-uploaded; including them would
// re-deliver every topic on nearly every check.
func TestPageFingerprint_IgnoresCountersThatDriftOnTheirOwn(t *testing.T) {
	before := pageFingerprint(fixtureTorrentBlock)
	drifted := strings.NewReplacer(
		"12 раз", "913 раз",
		"скачана 12 раз", "скачана 913 раз",
		`<span id="VT189409">7</span>`, `<span id="VT189409">88</span>`,
	).Replace(fixtureTorrentBlock)
	if drifted == fixtureTorrentBlock {
		t.Fatal("the drift substitution matched nothing; the test proves nothing")
	}
	if after := pageFingerprint(drifted); after != before {
		t.Error("counter drift changed the token; every check would look like a new release")
	}
}

// TestPageFingerprint_MovesWhenTheUploaderReplacesTheTorrent is the event
// being watched. Each of these fields moves on a real re-upload.
func TestPageFingerprint_MovesWhenTheUploaderReplacesTheTorrent(t *testing.T) {
	base := pageFingerprint(fixtureTorrentBlock)
	for _, tc := range []struct{ name, from, to string }{
		{"new attachment id", "download.php?id=189409", "download.php?id=189999"},
		{"new registration date", "04-09-2026 00:16", "05-09-2026 09:00"},
		{"new release size", "1.39&nbsp;GB", "2.10&nbsp;GB"},
		{"renamed torrent file", "Lady Death Demonicron [FitGirl Repack]", "Lady Death Demonicron [Repack]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := strings.Replace(fixtureTorrentBlock, tc.from, tc.to, 1)
			if mutated == fixtureTorrentBlock {
				t.Fatalf("substitution %q matched nothing", tc.from)
			}
			if pageFingerprint(mutated) == base {
				t.Error("token did not move; the update would never be downloaded")
			}
		})
	}
}

// TestFingerprintInput_RequiresTheStructuralFields. The download id and the
// registration date are the two fields a template change must not be allowed
// to drop silently: doing so would move every stored token at once and
// re-deliver every Tapochek topic.
func TestFingerprintInput_RequiresTheStructuralFields(t *testing.T) {
	for _, tc := range []struct{ name, from, to string }{
		{"no download id", `href="download.php?id=189409"`, `href="profile.php?mode=register"`},
		{"no registration date", "Зарегистрирован", "Опубликован"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := strings.Replace(fixtureTorrentBlock, tc.from, tc.to, 1)
			if mutated == fixtureTorrentBlock {
				t.Fatalf("substitution %q matched nothing", tc.from)
			}
			if got := fingerprintInput(mutated); got != "" {
				t.Errorf("input = %q, want empty so the check fails loudly", got)
			}
		})
	}
}

// TestNormalizeCell_DecodesEntitiesAndCollapsesSpace. A token keyed to
// "&nbsp;" would change for every topic at once the day the template emits a
// plain space instead.
func TestNormalizeCell_DecodesEntitiesAndCollapsesSpace(t *testing.T) {
	if got := normalizeCell("1.39&nbsp;GB"); got != "1.39 GB" {
		t.Errorf("normalizeCell = %q, want %q", got, "1.39 GB")
	}
	if got := normalizeCell("  a \n b  "); got != "a b" {
		t.Errorf("normalizeCell = %q, want %q", got, "a b")
	}
}

// --- title and poster ---------------------------------------------------

// TestCleanTitle_DecodesNumericEntities. The site is windows-1251 but renders
// Cyrillic titles as numeric references, so an undecoded title would name the
// topic "&#1056;&#1077;...".
func TestCleanTitle_DecodesNumericEntities(t *testing.T) {
	got := cleanTitle(fixtureTopicTitle)
	if strings.Contains(got, "&#") {
		t.Errorf("title still entity-encoded: %q", got)
	}
	if !strings.HasPrefix(got, "Lady Death Demonicron") {
		t.Errorf("title = %q", got)
	}
	if !strings.Contains(got, "Репак") {
		t.Errorf("Cyrillic did not decode: %q", got)
	}
}

// TestPosterURL_PicksTheAlignedCoverNotTheBanner. Tapochek marks the cover
// with an alignment class; plain postImg is a banner, a screenshot or a rank
// badge. Taking "the first image" would pick the banner that precedes it.
func TestPosterURL_PicksTheAlignedCoverNotTheBanner(t *testing.T) {
	const want = "https://i1.imageban.ru/out/2026/09/03/cover.jpg"
	if got := posterURL([]byte(fixtureTopicHTML)); got != want {
		t.Errorf("posterURL = %q, want %q", got, want)
	}
}

// TestPosterURL_IgnoresACoverOnlyAReplyCarries pins the first-post scoping.
//
// The main fixture CANNOT pin it: there the opening post's cover precedes the
// reply's aligned image, so an unscoped whole-page scan finds the right one by
// accident and the test passes with the scoping deleted. This fixture inverts
// that — the opening post carries only a plain screenshot, and the only
// aligned image is in a reply.
func TestPosterURL_IgnoresACoverOnlyAReplyCarries(t *testing.T) {
	page := `<html><body>
<div class="post_body">
<var class="postImg" title="https://img.example/screenshot.jpg"></var>
</div><!--/post_body-->
<div class="post_body">
<var class="postImg postImgAligned img-right" title="https://img.example/reply-cover.jpg"></var>
</div><!--/post_body-->
</body></html>`
	if got := posterURL([]byte(page)); got != "" {
		t.Errorf("posterURL = %q, want empty — the opening post carries no cover", got)
	}
}

// TestPosterURL_FailsOpenWhenTheScopeIsUnrecognisable covers the branch that
// runs on genuine template drift: no post_body wrapper at all. A cover from
// somewhere on the page beats no cover, because nothing backfills a stored
// image once the topic exists.
func TestPosterURL_FailsOpenWhenTheScopeIsUnrecognisable(t *testing.T) {
	const want = "https://img.example/cover.jpg"
	page := `<html><body><div class="renamed_body">
<var class="postImg postImgAligned img-right" title="` + want + `"></var>
</div></body></html>`
	if got := posterURL([]byte(page)); got != want {
		t.Errorf("posterURL = %q, want %q — the fail-open branch should still find a cover", got, want)
	}
}

// TestPosterURL_NoCoverIsEmptyNotAnError. Not every release carries artwork,
// and a missing cover must not cost the topic its real title too.
func TestPosterURL_NoCoverIsEmptyNotAnError(t *testing.T) {
	page := `<html><body><div class="post_body">
<var class="postImg" title="https://img.example/screenshot.jpg"></var>
</div><!--/post_body--></body></html>`
	if got := posterURL([]byte(page)); got != "" {
		t.Errorf("posterURL = %q, want empty", got)
	}
}

func TestResolveMetadata_ReturnsTitleAndCover(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: userCookie, Path: "/"})
		_, _ = w.Write([]byte(fixtureTopicHTML))
	})
	meta, err := p.ResolveMetadata(context.Background(), "https://tapochek.net/viewtopic.php?t=289113", testCreds())
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	if !strings.HasPrefix(meta.Title, "Lady Death Demonicron") {
		t.Errorf("Title = %q", meta.Title)
	}
	if meta.ImageURL != "https://i1.imageban.ru/out/2026/09/03/cover.jpg" {
		t.Errorf("ImageURL = %q", meta.ImageURL)
	}
}

// --- login and session --------------------------------------------------

func TestLogin_SuccessIsTheCookieNotThePage(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/login.php") {
			// The live shape: 302 with an EMPTY body. A body-matching check
			// would see nothing here and report success for a wrong password.
			http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: userCookie, Path: "/"})
			http.Redirect(w, r, "https://"+defaultDomain+"/index.php", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(`<html><body>ok</body></html>`))
	})
	if err := p.Login(context.Background(), testCreds()); err != nil {
		t.Fatalf("Login: %v", err)
	}
}

func TestLogin_WrongPasswordIsRejected(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		// No cookie, 200, and the site's real error wording.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(wrongPasswordHTML))
	})
	err := p.Login(context.Background(), testCreds())
	if err == nil {
		t.Fatal("a rejected password must not report success")
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("err = %v, want the credentials named", err)
	}
}

// TestLogin_NoSessionAndNoMessageStillFails: an unrecognised failure page
// must not read as success just because the wording changed.
func TestLogin_NoSessionAndNoMessageStillFails(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<html><body>something else entirely</body></html>`))
	})
	err := p.Login(context.Background(), testCreds())
	if err == nil {
		t.Fatal("no session must be a failure")
	}
	if !strings.Contains(err.Error(), "no session") {
		t.Errorf("err = %v", err)
	}
}

// TestLogin_DoesNotRequestAutologin. autologin asks the tracker to mint a
// durable key that signs in WITHOUT the password and stays valid server-side.
// Marauder never persists the jar, so each scheduled login would strand
// another live credential for nothing.
func TestLogin_DoesNotRequestAutologin(t *testing.T) {
	var posted string
	p := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/login.php") {
			_ = r.ParseForm()
			posted = r.Form.Encode()
			http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: userCookie, Path: "/"})
			http.Redirect(w, r, "https://"+defaultDomain+"/index.php", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(`ok`))
	})
	if err := p.Login(context.Background(), testCreds()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if strings.Contains(posted, "autologin") {
		t.Errorf("login form carried autologin: %q", posted)
	}
	for _, want := range []string{"login_username=someone", "login_password=secret"} {
		if !strings.Contains(posted, want) {
			t.Errorf("login form %q missing %q", posted, want)
		}
	}
}

// TestLogin_RejectsMissingCredentials guards the two inputs that cannot
// produce a session.
func TestLogin_RejectsMissingCredentials(t *testing.T) {
	p := &plugin{sessions: forumcommon.New(), domain: defaultDomain}
	if err := p.Login(context.Background(), nil); err == nil {
		t.Error("Login must reject nil credentials")
	}
	if err := p.Login(context.Background(), &domain.TrackerCredential{}); err == nil {
		t.Error("Login must reject an empty username")
	}
}

// TestLogin_FailureDoesNotPublishAnAnonymousSession. The store is keyed by
// (tracker, user), so publishing a failed jar would hand every one of that
// user's topics an anonymous session.
func TestLogin_FailureDoesNotPublishAnAnonymousSession(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		// A guest cookie: if the failed jar were published, this is what the
		// user's other topics would inherit.
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: guestCookie, Path: "/"})
		_, _ = w.Write([]byte(wrongPasswordHTML))
	})
	creds := testCreds()
	if err := p.Login(context.Background(), creds); err == nil {
		t.Fatal("expected the login to fail")
	}
	key := forumcommon.SessionKey(pluginName, creds.UserID.String())
	// Assert on the JAR, not on LoggedIn. Login sets LoggedIn only after the
	// cookie check passes, so a regression that published the failed jar
	// early would publish it with LoggedIn false — and an assertion on that
	// field would stay green through exactly the bug it is named for.
	sess := p.sessions.GetOrCreateWith(key, userAgent, p.configure)
	base, err := url.Parse(p.baseURL())
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}
	if got := sess.Client.Jar.Cookies(base); len(got) != 0 {
		t.Errorf("a failed login published its jar under the user's key: %v", got)
	}
}

func TestVerify_ReadsTheServersView(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cookie string
		want   bool
	}{
		{"signed in", userCookie, true},
		{"uid zero is a guest", guestCookie, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
				http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tc.cookie, Path: "/"})
				_, _ = w.Write([]byte(`<html><body>ok</body></html>`))
			})
			got, err := p.Verify(context.Background(), testCreds())
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if got != tc.want {
				t.Errorf("Verify = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestVerify_NoCookieIsAGuest. The live site issues a guest NO bb_data at
// all, so its absence must read as "not signed in" rather than as an error.
func TestVerify_NoCookieIsAGuest(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body>guest</body></html>`))
	})
	got, err := p.Verify(context.Background(), testCreds())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got {
		t.Error("a jar with no bb_data must not report a live session")
	}
}

// --- gating -------------------------------------------------------------

// TestCheck_GuestPageReportsTheSessionNotAParseFailure. Everything Check
// needs is login-gated, so a missing torrent block is far more often a lost
// session than a changed page — and saying "no torrent block" sends the user
// hunting for a broken selector.
func TestCheck_GuestPageReportsTheSessionNotAParseFailure(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(fixtureGuestHTML))
	})
	_, err := p.Check(context.Background(), &domain.Topic{
		URL: "https://tapochek.net/viewtopic.php?t=289113",
	}, testCreds())
	if !errors.Is(err, registry.ErrSessionExpired) {
		t.Errorf("err = %v, want ErrSessionExpired", err)
	}
}

// TestCheck_AnonymousCallerGetsThePlainError. With no credentials there is no
// session to have expired, so the sentinel would be a lie.
func TestCheck_AnonymousCallerGetsThePlainError(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(fixtureGuestHTML))
	})
	_, err := p.Check(context.Background(), &domain.Topic{
		URL: "https://tapochek.net/viewtopic.php?t=289113",
	}, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, registry.ErrSessionExpired) {
		t.Error("no credentials means no session to expire")
	}
}

// TestDownload_RejectsTheLoginPage is the trap that would otherwise reach the
// user's torrent client: download.php answers a session it does not accept
// with 200 and an HTML login page, so a status check alone is not enough.
func TestDownload_RejectsTheLoginPage(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/download.php") {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html><body><form action="login.php"></form></body></html>`))
			return
		}
		_, _ = w.Write([]byte(fixtureTopicHTML))
	})
	_, err := p.Download(context.Background(), &domain.Topic{
		URL: "https://tapochek.net/viewtopic.php?t=289113",
	}, nil, testCreds())
	if err == nil {
		t.Fatal("an HTML body must not be delivered as a .torrent")
	}
	if !errors.Is(err, registry.ErrSessionExpired) {
		t.Errorf("err = %v, want ErrSessionExpired", err)
	}
}

func TestDownload_ReturnsTheFileAndItsName(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: userCookie, Path: "/"})
		if strings.HasPrefix(r.URL.Path, "/download.php") {
			_, _ = w.Write(fixtureTorrentBytes)
			return
		}
		_, _ = w.Write([]byte(fixtureTopicHTML))
	})
	payload, err := p.Download(context.Background(), &domain.Topic{
		URL: "https://tapochek.net/viewtopic.php?t=289113",
	}, nil, testCreds())
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(payload.TorrentFile) != string(fixtureTorrentBytes) {
		t.Error("torrent bytes did not survive")
	}
	if payload.FileName != "Lady Death Demonicron [FitGirl Repack] [tapochek.net].torrent" {
		t.Errorf("FileName = %q", payload.FileName)
	}
	// domain.Payload's "exactly one of" contract: a magnet alongside a file
	// would win in every client plugin and throw the file away.
	if payload.MagnetURI != "" {
		t.Errorf("MagnetURI = %q, want empty", payload.MagnetURI)
	}
}

// --- transport guards ---------------------------------------------------

func TestCheckTarget_RejectsOffSiteAndPlaintext(t *testing.T) {
	registry.SetDomainResolver(nil)
	p := &plugin{domain: defaultDomain}
	for _, tc := range []struct {
		raw     string
		wantErr bool
	}{
		{"https://tapochek.net/viewtopic.php?t=1", false},
		{"https://www.tapochek.net/viewtopic.php?t=1", false},
		// A redirect hop is the only way plaintext could be dialled, and Go's
		// jar would attach the session cookie to it.
		{"http://tapochek.net/viewtopic.php?t=1", true},
		{"https://evil.example/viewtopic.php?t=1", true},
		{"https://tapochek.net.evil.example/viewtopic.php?t=1", true},
		{"https://169.254.169.254/latest/meta-data/", true},
		{"ftp://tapochek.net/viewtopic.php?t=1", true},
	} {
		u, err := parseTestURL(tc.raw)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.raw, err)
		}
		if gotErr := p.checkTarget(u) != nil; gotErr != tc.wantErr {
			t.Errorf("checkTarget(%q) error = %v, want error = %v", tc.raw, gotErr, tc.wantErr)
		}
	}
}

// TestHostAllowed_AcceptsTheAdminsActiveDomain. registry.DomainAllowed
// consults the known and custom lists but NOT the active setting, so without
// this the plugin would build every URL against its configured host and then
// refuse to dial it.
func TestHostAllowed_AcceptsTheAdminsActiveDomain(t *testing.T) {
	registry.SetDomainResolver(func(string) registry.DomainConfig {
		return registry.DomainConfig{Active: "tapochek.mirror"}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{domain: defaultDomain}
	if !p.hostAllowed("tapochek.mirror") {
		t.Error("the resolved active domain must be dialable")
	}
	if p.hostAllowed("evil.example") {
		t.Error("an unrelated host must still be refused")
	}
}

// TestFetch_RefusesOffSiteRedirect covers the hop the initial-URL guard
// cannot see. Without it a mirror operator could 302 a topic page at an
// internal address and have its response parsed.
func TestFetch_RefusesOffSiteRedirect(t *testing.T) {
	registry.SetDomainResolver(nil)
	p := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		// https, so the scheme guard cannot be what stops it.
		http.Redirect(w, r, "https://169.254.169.254/latest/meta-data/", http.StatusFound)
	})
	_, err := p.fetch(context.Background(), "https://tapochek.net/viewtopic.php?t=1", nil)
	if err == nil || !strings.Contains(err.Error(), "refusing to fetch off-site host") {
		t.Errorf("err = %v, want the off-site refusal", err)
	}
}

// TestFetch_RefusesPlaintextRedirect: a downgrade to http on a host that IS
// ours would put the session cookie on the wire in the clear.
func TestFetch_RefusesPlaintextRedirect(t *testing.T) {
	registry.SetDomainResolver(nil)
	p := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://tapochek.net/viewtopic.php?t=1", http.StatusFound)
	})
	_, err := p.fetch(context.Background(), "https://tapochek.net/viewtopic.php?t=1", nil)
	if err == nil || !strings.Contains(err.Error(), "refusing non-https") {
		t.Errorf("err = %v, want the non-https refusal", err)
	}
}

// TestDo_RefusesAnOversizedBody. io.ReadAll on a bare LimitReader cannot tell
// a body that ended from one that was cut off, so a truncated .torrent would
// still pass isTorrent's first-byte check on its way to a client.
func TestDo_RefusesAnOversizedBody(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		big := make([]byte, maxBodyBytes+64)
		big[0] = 'd'
		_, _ = w.Write(big)
	})
	_, err := p.fetch(context.Background(), "https://tapochek.net/viewtopic.php?t=1", nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("err = %v, want the size refusal", err)
	}
}

// TestDo_ErrorNamesOnlyThePath. A search or redirect target can carry a query
// string, which has no business in an error or a log.
func TestDo_ErrorNamesOnlyThePath(t *testing.T) {
	p := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := p.fetch(context.Background(), "https://tapochek.net/viewtopic.php?t=289113", nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "t=289113") {
		t.Errorf("error leaked the query string: %v", err)
	}
	// The status must survive: the scheduler's classifyError reads it out of
	// the message text.
	if !strings.Contains(err.Error(), "-> 500") {
		t.Errorf("err = %v, want the status in the message", err)
	}
}

// parseTestURL keeps the table above readable.
func parseTestURL(raw string) (*url.URL, error) { return url.Parse(raw) }

// TestLogin_WrongPasswordIsRejected_Cp1251 serves the failure page the way
// the site actually serves it: windows-1251 bytes, not UTF-8. The plain
// fixture above passes either way, so only this one catches a Login that
// matches the message without decoding — which is what shipped first, and
// what turned "username or password rejected" into the vague "no session was
// established" against the live site.
func TestLogin_WrongPasswordIsRejected_Cp1251(t *testing.T) {
	encoded, err := charmap.Windows1251.NewEncoder().Bytes([]byte(wrongPasswordHTML))
	if err != nil {
		t.Fatalf("encode fixture to cp1251: %v", err)
	}
	if bytes.Equal(encoded, []byte(wrongPasswordHTML)) {
		t.Fatal("the fixture is pure ASCII; this test would prove nothing")
	}
	p := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=windows-1251")
		_, _ = w.Write(encoded)
	})
	err = p.Login(context.Background(), testCreds())
	if err == nil {
		t.Fatal("a rejected password must not report success")
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("err = %v, want the credentials named, not a generic failure", err)
	}
}

// --- selector robustness (Greptile P2) ----------------------------------

// TestClassToken_MatchesTokenNotSubstring. `class="[^"]*attach[^"]*"` would
// match `class="unattached"`, and a prefix-anchored pattern would miss
// `class="bordered attach"`. Both directions are wrong, so both are pinned.
func TestClassToken_MatchesTokenNotSubstring(t *testing.T) {
	re := regexp.MustCompile(classToken("attach"))
	for _, tc := range []struct {
		attr string
		want bool
	}{
		{`class="attach"`, true},
		{`class="attach bordered med"`, true},
		{`class="bordered attach med"`, true},
		{`class="bordered med attach"`, true},
		{`class="unattached"`, false},
		{`class="attachment"`, false},
		{`class="no-attach"`, false},
		{`class="bordered med"`, false},
	} {
		if got := re.MatchString(tc.attr); got != tc.want {
			t.Errorf("classToken(attach) on %s = %v, want %v", tc.attr, got, tc.want)
		}
	}
}

// TestTorrentBlock_SurvivesReorderedClasses. CSS class order carries no
// meaning, so a purely cosmetic template edit must not stop every Tapochek
// check. The original selector required `attach` to be the FIRST class.
func TestTorrentBlock_SurvivesReorderedClasses(t *testing.T) {
	for _, class := range []string{
		`attach bordered med`,
		`bordered attach med`,
		`bordered med attach`,
		`attach`,
	} {
		page := strings.Replace(fixtureTopicHTML,
			`<table class="attach bordered med">`,
			`<table class="`+class+`">`, 1)
		if page == fixtureTopicHTML && class != `attach bordered med` {
			t.Fatalf("substitution for %q matched nothing", class)
		}
		if _, ok := torrentBlock([]byte(page)); !ok {
			t.Errorf("class=%q: torrent block not found", class)
		}
	}
}

// TestTorrentBlock_SurvivesAttributesBeforeClass. HTML attribute order
// carries no meaning either.
func TestTorrentBlock_SurvivesAttributesBeforeClass(t *testing.T) {
	page := strings.Replace(fixtureTopicHTML,
		`<table class="attach bordered med">`,
		`<table id="tor" cellpadding="0" class="attach bordered med" width="100%">`, 1)
	if page == fixtureTopicHTML {
		t.Fatal("substitution matched nothing")
	}
	if _, ok := torrentBlock([]byte(page)); !ok {
		t.Error("torrent block not found when other attributes precede class")
	}
}

// TestPosterURL_SurvivesReorderedClassesAndAttributes. The original selector
// demanded `class="postImg postImgAligned img-right"` in that exact order,
// with `title` immediately after it — so a cosmetic edit silently dropped
// the cover art rather than failing loudly.
func TestPosterURL_SurvivesReorderedClassesAndAttributes(t *testing.T) {
	const want = "https://i1.imageban.ru/out/2026/09/03/cover.jpg"
	const original = `<var class="postImg postImgAligned img-right" title="` + want + `"></var>`
	for _, variant := range []string{
		`<var class="postImgAligned img-right postImg" title="` + want + `"></var>`,
		`<var class="img-left postImgAligned postImg" title="` + want + `"></var>`,
		`<var title="` + want + `" class="postImg postImgAligned img-right"></var>`,
		`<var id="cover" class="postImg  postImgAligned  img-right" data-x="1" title="` + want + `"></var>`,
		`<var class='postImg postImgAligned img-right' title='` + want + `'></var>`,
	} {
		page := strings.Replace(fixtureTopicHTML, original, variant, 1)
		if page == fixtureTopicHTML {
			t.Fatalf("substitution matched nothing for %q", variant)
		}
		if got := posterURL([]byte(page)); got != want {
			t.Errorf("variant %q: posterURL = %q, want %q", variant, got, want)
		}
	}
}

// TestPosterURL_StillRejectsUnalignedImages is the inverse guard: loosening
// the match must not turn every banner and screenshot into a cover.
func TestPosterURL_StillRejectsUnalignedImages(t *testing.T) {
	page := `<html><body><div class="post_body">
<var class="postImg" title="https://img.example/banner.png"></var>
<var class="postImg img-center" title="https://img.example/inline.png"></var>
<var class="postImgAlignedExtra img-right" title="https://img.example/nearly.png"></var>
</div><!--/post_body--></body></html>`
	if got := posterURL([]byte(page)); got != "" {
		t.Errorf("posterURL = %q, want empty — none of these is a cover", got)
	}
}

// TestFirstPostBody_SurvivesExtraClasses. `<div class="post_body">` was
// matched literally, so `class="post_body signed"` would have silently
// widened the poster search to the whole page, including replies.
func TestFirstPostBody_SurvivesExtraClasses(t *testing.T) {
	page := strings.Replace(fixtureTopicHTML,
		`<div class="post_body">`,
		`<div class="post_body signed" id="p1">`, 1)
	if page == fixtureTopicHTML {
		t.Fatal("substitution matched nothing")
	}
	scope, ok := firstPostBody([]byte(page))
	if !ok {
		t.Fatal("opening post body not found")
	}
	if strings.Contains(scope, "reply-image") {
		t.Error("scope leaked into the reply")
	}
}

// TestFileName_SurvivesExtraClasses on the block's header cell.
func TestFileName_SurvivesExtraClasses(t *testing.T) {
	block := strings.Replace(fixtureTorrentBlock,
		`<th colspan="3" class="genmed">`,
		`<th colspan="3" class="genmed bold">`, 1)
	if block == fixtureTorrentBlock {
		t.Fatal("substitution matched nothing")
	}
	m := fileNameRe.FindStringSubmatch(block)
	if m == nil {
		t.Fatal("filename not found with an extra class")
	}
	if !strings.HasSuffix(normalizeCell(m[1]), ".torrent") {
		t.Errorf("filename = %q", m[1])
	}
}

// --- gaps a mutation probe found (round 1) -------------------------------

// TestCheck_DecodesTheCp1251TopicPage serves the topic page the way the site
// actually serves it: windows-1251 bytes, not UTF-8.
//
// Every fixture in this file is a Go string literal and therefore valid
// UTF-8, and forumcommon.DecodeWindows1251 returns valid UTF-8 unchanged — so
// deleting the decode from fetchPage leaves the whole rest of the suite
// green. Only this test fails. That is not hypothetical: the decode was
// missing in the first draft, and the first live run found the torrent block
// and then reported it as carrying no fields, because every label the parser
// anchors on is Cyrillic.
func TestCheck_DecodesTheCp1251TopicPage(t *testing.T) {
	// ReplaceUnsupported: the fixture's download button carries "⇩", which has
	// no windows-1251 code point. It is not what this test is about.
	encoded, err := encoding.ReplaceUnsupported(charmap.Windows1251.NewEncoder()).Bytes([]byte(fixtureTopicHTML))
	if err != nil {
		t.Fatalf("encode fixture to cp1251: %v", err)
	}
	if bytes.Equal(encoded, []byte(fixtureTopicHTML)) {
		t.Fatal("the fixture round-tripped unchanged; this test would prove nothing")
	}
	p := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=windows-1251")
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: userCookie, Path: "/"})
		_, _ = w.Write(encoded)
	})

	check, err := p.Check(context.Background(), &domain.Topic{
		URL: "https://tapochek.net/viewtopic.php?t=289113",
	}, testCreds())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if want := pageFingerprint(fixtureTorrentBlock); check.Hash != want {
		t.Errorf("Hash = %q, want %q — the cp1251 page was not decoded before parsing", check.Hash, want)
	}
}

// TestFetch_RefusesAnOffSiteInitialURL covers the guard on the FIRST request,
// not only on a redirect hop. Every other host test goes through
// checkRedirect or calls checkTarget directly, so removing the checkTarget
// call from do() leaves them all green — and a stored topic URL is precisely
// an input the plugin does not control.
func TestFetch_RefusesAnOffSiteInitialURL(t *testing.T) {
	registry.SetDomainResolver(nil)
	p := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(fixtureTopicHTML))
	})
	if _, err := p.fetch(context.Background(), "https://evil.example/viewtopic.php?t=1", nil); err == nil ||
		!strings.Contains(err.Error(), "refusing to fetch off-site host") {
		t.Errorf("err = %v, want the off-site refusal", err)
	}
	if _, err := p.fetch(context.Background(), "http://tapochek.net/viewtopic.php?t=1", nil); err == nil ||
		!strings.Contains(err.Error(), "refusing non-https") {
		t.Errorf("err = %v, want the non-https refusal", err)
	}
}

// TestDownload_FileNameCannotEscapeAWatchFolder. The name is scraped off the
// page, so the uploader controls it, and the downloadfolder client turns it
// into a path. The client sanitises too — that is where the guarantee lives
// for every plugin — but a name carrying separators should not travel that
// far in the first place.
func TestDownload_FileNameCannotEscapeAWatchFolder(t *testing.T) {
	block := strings.Replace(fixtureTorrentBlock,
		`<th colspan="3" class="genmed">Lady Death Demonicron [FitGirl Repack] [tapochek.net].torrent</th>`,
		`<th colspan="3" class="genmed">../../../etc/cron.d/evil.torrent</th>`, 1)
	if block == fixtureTorrentBlock {
		t.Fatal("substitution matched nothing")
	}
	page := strings.Replace(fixtureTopicHTML, fixtureTorrentBlock, block, 1)
	p := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: userCookie, Path: "/"})
		if strings.HasPrefix(r.URL.Path, "/download.php") {
			_, _ = w.Write(fixtureTorrentBytes)
			return
		}
		_, _ = w.Write([]byte(page))
	})

	payload, err := p.Download(context.Background(), &domain.Topic{
		URL: "https://tapochek.net/viewtopic.php?t=289113",
	}, nil, testCreds())
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if strings.ContainsAny(payload.FileName, `/\`) {
		t.Errorf("FileName = %q, want no path separators", payload.FileName)
	}
	if payload.FileName == "." || payload.FileName == ".." {
		t.Errorf("FileName = %q, which is not a file name", payload.FileName)
	}
}
