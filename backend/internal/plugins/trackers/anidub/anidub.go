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
	sess := p.sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
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
	return bytes.Contains(body, logoutMarker), nil
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
	titleRe   = regexp.MustCompile(`(?s)<h1[^>]*>\s*(?:<span[^>]*>)?(.*?)(?:</span>)?\s*</h1>`)
	ogTitleRe = regexp.MustCompile(`<meta\s+property="og:title"\s+content="([^"]*)"`)
	// The topic's own poster. Scoping to span.poster is load-bearing: sidebar
	// posters for unrelated titles appear EARLIER in the document, so an
	// unscoped "first poster image" match returns the wrong show's art.
	posterRe = regexp.MustCompile(`<span class="poster">\s*<img[^>]+src="([^"]+)"`)
	dlHrefRe = regexp.MustCompile(`href="(/engine/download\.php\?id=\d+)"`)

	// Change-token inputs. One torrent block per quality variant, each with a
	// stable id, filename and size. Seeder/leecher counts are deliberately
	// excluded — they move constantly, and feeding them in would make every
	// check look like a new release.
	blockIDRe  = regexp.MustCompile(`torrent_(\d+)_info`)
	fileNameRe = regexp.MustCompile(`Имя файла:</span>\s*<span class="red" title="([^"]*)"`)
	sizeRe     = regexp.MustCompile(`Размер:\s*<span class="red">([^<]*)</span>`)
)

// pageTitle prefers og:title (clean, single-line) and falls back to the h1.
func pageTitle(body []byte) string {
	if m := ogTitleRe.FindSubmatch(body); m != nil {
		if s := strings.TrimSpace(html.UnescapeString(string(m[1]))); s != "" {
			return s
		}
	}
	if m := titleRe.FindSubmatch(body); m != nil {
		return strings.TrimSpace(html.UnescapeString(string(m[1])))
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
	var parts []string
	for _, m := range blockIDRe.FindAllSubmatch(body, -1) {
		parts = append(parts, "id="+string(m[1]))
	}
	for _, m := range fileNameRe.FindAllSubmatch(body, -1) {
		parts = append(parts, "name="+string(m[1]))
	}
	for _, m := range sizeRe.FindAllSubmatch(body, -1) {
		parts = append(parts, "size="+string(m[1]))
	}
	if len(parts) == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
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
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}
