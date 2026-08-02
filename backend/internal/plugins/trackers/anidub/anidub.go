// Package anidub implements a tracker plugin for tr.anidub.com.
//
// Anidub is the Russian-dub anime tracker that replaced lostfilm.tv for
// many users in 2019-2020. It runs on a phpBB-like forum at
// `tr.anidub.com/<category>/<slug>.html`.
//
// **Validation status:** structurally complete. The login path was measured
// against the live site on 2026-08-02: a rejected sign-in returns HTTP 200
// with the login form re-rendered and the banner "вход на сайт не был
// произведен", and an anonymous page carries `login_name` twice and
// `action=logout` zero times. The remaining assumption is that a *signed-in*
// page does carry `action=logout` (DLE's standard logout link) — that half
// still wants confirmation from a real account. Topic-page selectors mirror
// the public HTML as of 2026-04.
package anidub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

const (
	pluginName    = "anidub"
	displayName   = "Anidub"
	defaultDomain = "tr.anidub.com"
	userAgent     = "Marauder/0.4 (+https://marauder.cc)"

	// bodyReadLimit caps every response the plugin reads.
	bodyReadLimit = 4 << 20
	// maxTorrentBlocks bounds how many matches the fingerprint accumulates.
	// Real pages carry one block per quality — a handful. The cap keeps a
	// hostile or broken page from turning a 4 MB body into a large slice of
	// substrings on every check, across every scheduler worker.
	maxTorrentBlocks = 512
)

// urlPattern is host-agnostic; CanParse gates the captured host against the
// known + admin-configured domain allowlist (the SSRF barrier — see
// registry.DomainAllowed).
var urlPattern = regexp.MustCompile(`^https?://([^/]+)/(?:[a-z0-9_-]+/)+([a-z0-9_-]+)\.html`)

var knownDomains = []string{"tr.anidub.com"}

type plugin struct {
	sessions  *forumcommon.SessionStore
	domain    string
	transport http.RoundTripper
}

func init() {
	registry.RegisterTracker(&plugin{
		sessions: forumcommon.New(),
		domain:   defaultDomain,
	})
}

func (p *plugin) Name() string        { return pluginName }
func (p *plugin) DisplayName() string { return displayName }

// Qualities implements WithQuality.
func (p *plugin) Qualities() []string    { return []string{"HDTVRip", "HDTVRip-AVC", "BDRip"} }
func (p *plugin) DefaultQuality() string { return "HDTVRip" }

var _ registry.WithDomains = (*plugin)(nil)

// Domains implements registry.WithDomains; first entry is canonical.
func (p *plugin) Domains() []string { return knownDomains }

// effectiveDomain resolves the domain every request is built against:
// a test-injected p.domain wins (httptest servers), then the admin-
// configured active domain, then the compiled default.
func (p *plugin) effectiveDomain() string {
	if p.domain != defaultDomain {
		return p.domain
	}
	if d := registry.ActiveDomain(pluginName); d != "" {
		return d
	}
	return p.domain
}

// canonicalURL rewrites rawURL's host to p.effectiveDomain() when that
// differs from the URL's own host — the nnmclub/rutor canonicalURL
// approach adapted to anidub. Check/Download re-fetch the stored topic
// URL directly, so this is the only place an active-domain override or
// mirror switch actually takes effect for those fetches.
func (p *plugin) canonicalURL(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("anidub: invalid URL: %w", err)
	}
	if eff := p.effectiveDomain(); eff != "" && eff != u.Hostname() {
		u.Host = eff
	}
	return u.String(), nil
}

func (p *plugin) CanParse(rawURL string) bool {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	return m != nil && registry.DomainAllowed(pluginName, m[1], knownDomains)
}

func (p *plugin) Parse(_ context.Context, rawURL string) (*domain.Topic, error) {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return nil, errors.New("not an anidub URL")
	}
	return &domain.Topic{
		TrackerName: pluginName, URL: rawURL,
		DisplayName: "Anidub: " + m[2],
		Extra:       map[string]any{"slug": m[2], "quality": "HDTVRip"},
	}, nil
}

func (p *plugin) Login(ctx context.Context, creds *domain.TrackerCredential) error {
	if creds == nil || creds.Username == "" {
		return errors.New("anidub credentials are required")
	}
	// Validate against a FRESH jar. The store hands the same jar back for two
	// hours and the scheduler calls Login on every check, so a user with
	// working credentials always has a warm, authenticated session. Posting a
	// new password onto that jar proves nothing: the tracker renders the
	// signed-in page, no rejection marker matches, and Verify then confirms a
	// session this attempt never established — a false green on exactly the
	// paths that exist to catch a bad password (credential test, rotation).
	key := forumcommon.SessionKey(pluginName, creds.UserID.String())
	p.sessions.Invalidate(key)
	sess := p.sessions.GetOrCreate(key, userAgent)
	if p.transport != nil {
		sess.Client.Transport = p.transport
	}
	form := url.Values{
		"login_name":     {creds.Username},
		"login_password": {string(creds.SecretEnc)},
		"login":          {"submit"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+p.effectiveDomain()+"/index.php", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return fmt.Errorf("anidub login: %w", err)
	}
	defer resp.Body.Close()
	// Read the whole page, not a 64 KB prefix: loginRejected scans this body,
	// so a rejection banner past the cut-off would be missed and the login
	// would be reported as successful. Same ceiling as fetch.
	body, err := io.ReadAll(io.LimitReader(resp.Body, bodyReadLimit))
	if err != nil {
		return fmt.Errorf("anidub login: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("anidub login: unexpected status %d", resp.StatusCode)
	}
	if loginRejected(body) {
		return errors.New("anidub login failed: check the username and password")
	}
	sess.LoggedIn = true
	return nil
}

// loginFailureMarkers are the phrases the tracker emits when it refuses a
// sign-in. The first two are the verbatim DLE rejection banner captured from
// the live site on 2026-08-02 by posting a nonexistent account; the third
// covers a banned/forbidden account.
//
// The rejection arrives as **HTTP 200 with the login form re-rendered**, so
// the status check above cannot catch it — this list is the only thing
// standing between a wrong password and a reported success.
//
// The plugin previously looked for "не верный", which never matched: the site
// writes "неверное" (one word, neuter). That single mismatch is what made
// every failed anidub login report as green.
var loginFailureMarkers = [][]byte{
	[]byte("вход на сайт не был произведен"),
	[]byte("неверное имя пользователя или пароль"),
	[]byte("Доступ запрещён"),
}

func loginRejected(body []byte) bool {
	for _, m := range loginFailureMarkers {
		if bytes.Contains(body, m) {
			return true
		}
	}
	return false
}

// logoutMarker is DLE's logout link. Measured on the live site 2026-08-02:
// an anonymous page carries `login_name` twice and `action=logout` zero
// times, so its absence reliably means "not signed in".
var logoutMarker = []byte("action=logout")

// anonymousMarker is the login form itself — the measured half of the pair.
// Its presence is what licenses reporting "not signed in"; see Verify.
var anonymousMarker = []byte("login_name")

// Verify implements the positive-marker half of the credential check: it
// fetches the site root on the session Login established and reports whether
// that session is actually authenticated.
//
// This is the signal credentials.loginAndVerify depends on. It used to be a
// stub returning (true, nil), which silently defeated the handler's two-signal
// design — Login's negative check was then the only thing between a wrong
// password and a green badge, and it was matching the wrong string.
func (p *plugin) Verify(ctx context.Context, creds *domain.TrackerCredential) (bool, error) {
	body, err := p.fetch(ctx, "https://"+p.effectiveDomain()+"/", creds)
	if err != nil {
		return false, err
	}
	if bytes.Contains(body, logoutMarker) {
		return true, nil
	}
	// Only report a rejection on the marker that was actually measured: the
	// live anonymous page renders the login form (login_name x2, logout x0).
	if bytes.Contains(body, anonymousMarker) {
		return false, nil
	}
	// Neither marker: an unrecognised page shape. Reporting (false, nil) here
	// would turn the *unconfirmed* half of the assumption — that a signed-in
	// page carries the logout link — into a hard 422 that refuses to store the
	// credential, so a wrong guess would lock a user out of adding a working
	// account and blame their password. Say what is true instead: nothing here
	// verified anything.
	return false, registry.ErrVerifyUnsupported
}

var _ registry.WithMetadata = (*plugin)(nil)

// ResolveMetadata implements registry.WithMetadata — the AddTopic preview and
// the real display name at create time. Without it a new anidub topic showed
// the "Anidub: <slug>" placeholder and no poster.
func (p *plugin) ResolveMetadata(ctx context.Context, rawURL string, creds *domain.TrackerCredential) (*registry.Metadata, error) {
	target, err := p.canonicalURL(rawURL)
	if err != nil {
		return nil, err
	}
	body, err := p.fetch(ctx, target, creds)
	if err != nil {
		return nil, err
	}
	return &registry.Metadata{
		Title:    pageTitle(body),
		ImageURL: p.absoluteURL(posterURL(body)),
	}, nil
}

var (
	// The title sits inside <h1><span id="news-title">…</span></h1>. The
	// original pattern required text directly between the <h1> tags, so the
	// nested span left it matching nothing — which also disabled the
	// scheduler's placeholder self-heal.
	// The title lives in <h1><span id="news-title">…</span></h1>. Match the
	// span directly rather than "any span inside an h1": a sibling element
	// before it (a back-link, a breadcrumb) would otherwise be swallowed into
	// the capture, and whatever this returns is persisted by the scheduler's
	// placeholder self-heal, which checks for non-empty but not for markup.
	newsTitleRe = regexp.MustCompile(`(?s)<span[^>]*id="news-title"[^>]*>(.*?)</span>`)
	titleRe     = regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`)
	ogTitleRe   = regexp.MustCompile(`<meta\s+property="og:title"\s+content="([^"]*)"`)
	// The topic's own poster. Scoping to span.poster is load-bearing: sidebar
	// posters for unrelated titles appear EARLIER in the document, so an
	// unscoped "first poster image" match returns the wrong show's art.
	posterRe = regexp.MustCompile(`<span class="poster">\s*<img[^>]+src="([^"]+)"`)
	dlHrefRe = regexp.MustCompile(`href="(/engine/download\.php\?id=\d+)"`)

	// Change-token inputs. One torrent block per quality variant, each with a
	// stable id, filename and size. Seeder/leecher counts are deliberately
	// excluded — they move constantly, and feeding them in would make every
	// check look like a new release.
	// Anchored to the id ATTRIBUTE. DLE templates also reference the block id
	// from in-page anchors (href="#torrent_N_info") and inline scripts, so an
	// unanchored match finds "blocks" on a page that has none — which would let
	// the non-empty-fingerprint guard pass for a topic carrying no torrent.
	blockIDRe  = regexp.MustCompile(`id=['"]?torrent_(\d+)_info`)
	fileNameRe = regexp.MustCompile(`Имя файла:</span>\s*<span class="red" title="([^"]*)"`)
	sizeRe     = regexp.MustCompile(`Размер:\s*<span class="red">([^<]*)</span>`)
)

// pageTitle prefers og:title (clean, single-line), then the news-title span,
// then any h1. Every fallback is flattened through forumcommon.HTMLToText so a
// nested tag cannot end up in a stored display name.
func pageTitle(body []byte) string {
	if m := ogTitleRe.FindSubmatch(body); m != nil {
		if s := strings.TrimSpace(html.UnescapeString(string(m[1]))); s != "" {
			return s
		}
	}
	for _, re := range []*regexp.Regexp{newsTitleRe, titleRe} {
		if m := re.FindSubmatch(body); m != nil {
			if s := forumcommon.HTMLToText(string(m[1])); s != "" {
				return s
			}
		}
	}
	return ""
}

func posterURL(body []byte) string {
	if m := posterRe.FindSubmatch(body); m != nil {
		return strings.TrimSpace(string(m[1]))
	}
	return ""
}

// absoluteURL resolves a possibly-relative asset path against the active
// domain. Posters are served from a separate CDN host today, but the site also
// emits site-relative /uploads/... paths.
func (p *plugin) absoluteURL(ref string) string {
	if ref == "" || strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	return "https://" + p.effectiveDomain() + "/" + strings.TrimPrefix(ref, "/")
}

// pageFingerprint derives the change token for a topic.
//
// anidub publishes no infohash on the page — not on movie pages, not on series
// pages (verified against the live site 2026-08-02). The plugin previously
// looked for a data-hash attribute that does not exist anywhere on the site, so
// every check failed with "no infohash found" and no anidub topic could work.
//
// domain.Check.Hash is a change token, not an infohash: the scheduler only
// compares it to the previous value to decide whether something was published.
// The real infohash used for delivery tracking is computed downstream from the
// .torrent by the infohash package, so nothing needs one here. Torrent id +
// filename + size covers exactly the re-upload case that means "new release".
func pageFingerprint(body []byte) string {
	input := fingerprintInput(body)
	if input == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}

// fingerprintInput builds the human-readable string the token digests. Split
// out from pageFingerprint so a test can assert something a reviewer can
// actually read ("id=42\x00name=…") instead of only a golden digest.
//
// Values are decoded before hashing: the filename is captured from a title
// attribute where the site writes "[" as "&#091;", and a token that depends on
// the ENCODING rather than the content would change for every anidub topic at
// once — re-downloading all of them — the day the site emits the literal
// character. The NUL separator cannot occur in HTML text, so no value can
// forge a field boundary.
func fingerprintInput(body []byte) string {
	var parts []string
	for _, m := range blockIDRe.FindAllSubmatch(body, maxTorrentBlocks) {
		parts = append(parts, "id="+string(m[1]))
	}
	for _, m := range fileNameRe.FindAllSubmatch(body, maxTorrentBlocks) {
		parts = append(parts, "name="+html.UnescapeString(string(m[1])))
	}
	for _, m := range sizeRe.FindAllSubmatch(body, maxTorrentBlocks) {
		parts = append(parts, "size="+html.UnescapeString(string(m[1])))
	}
	return strings.Join(parts, "\x00")
}

func (p *plugin) Check(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential) (*domain.Check, error) {
	target, err := p.canonicalURL(topic.URL)
	if err != nil {
		return nil, err
	}
	body, err := p.fetch(ctx, target, creds)
	if err != nil {
		return nil, err
	}
	check := &domain.Check{DisplayName: pageTitle(body)}
	if fp := pageFingerprint(body); fp != "" {
		check.Hash = fp
		return check, nil
	}
	return nil, errors.New("anidub: no torrent block found on the topic page")
}

func (p *plugin) Download(ctx context.Context, topic *domain.Topic, _ *domain.Check, creds *domain.TrackerCredential) (*domain.Payload, error) {
	target, err := p.canonicalURL(topic.URL)
	if err != nil {
		return nil, err
	}
	body, err := p.fetch(ctx, target, creds)
	if err != nil {
		return nil, err
	}
	m := dlHrefRe.FindSubmatch(body)
	if m == nil {
		return nil, errors.New("anidub: no download link")
	}
	dlURL := "https://" + p.effectiveDomain() + string(m[1])
	torrent, err := p.fetch(ctx, dlURL, creds)
	if err != nil {
		return nil, err
	}
	return &domain.Payload{TorrentFile: torrent, FileName: "anidub.torrent"}, nil
}

func (p *plugin) fetch(ctx context.Context, target string, creds *domain.TrackerCredential) ([]byte, error) {
	key := pluginName + ":nocreds"
	if creds != nil {
		key = forumcommon.SessionKey(pluginName, creds.UserID.String())
	}
	sess := p.sessions.GetOrCreate(key, userAgent)
	if p.transport != nil {
		sess.Client.Transport = p.transport
	}
	// SSRF guard: `target` originates from a user-supplied topic URL. Callers
	// launder it through canonicalURL, but the barrier belongs at the dial
	// site — fetch takes an arbitrary string, and ResolveMetadata is reachable
	// straight from a query parameter. Mirrors nnmclub.fetch; the request is
	// built from the parsed, host-checked URL so what is dialed is what passed
	// the guard.
	u, perr := url.Parse(strings.TrimSpace(target))
	if perr != nil {
		return nil, fmt.Errorf("anidub: invalid URL: %w", perr)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("anidub: refusing URL scheme %q", u.Scheme)
	}
	if !registry.DomainAllowed(pluginName, u.Hostname(), knownDomains) {
		return nil, fmt.Errorf("anidub: refusing to fetch off-site host %q", u.Hostname())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("anidub GET %s -> %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, bodyReadLimit))
}
