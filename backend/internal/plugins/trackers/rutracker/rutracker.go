// Package rutracker implements the RuTracker.org tracker plugin.
//
// RuTracker is a phpBB-derived forum where each "topic" page hosts one
// torrent attachment. The flow Marauder uses is:
//
//  1. Login: POST /forum/login.php with login_username, login_password,
//     login=Вход. Sets a `bb_session` cookie that we keep in the cookie
//     jar.
//  2. Check: GET /forum/viewtopic.php?t=<topic_id>. Parse the topic
//     title from <title>, the magnet link from the page, and the
//     `dl_class_magnet-link` href.
//  3. Download: follow the dl.php link with the same session cookies.
//
// **Validation status:** structurally complete and unit-tested with
// recorded HTML fixtures. The selectors mirror the public RuTracker HTML
// as of 2026-04. Validating the plugin against a live RuTracker account
// requires credentials this session does not have, so the plugin is
// shipped as "alpha" — see CONTRIBUTING.md for the validation procedure.
package rutracker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/infohash"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

const (
	pluginName    = "rutracker"
	displayName   = "RuTracker.org"
	defaultDomain = "rutracker.org"
	userAgent     = "Marauder/0.3 (+https://marauder.cc)"
)

var knownDomains = []string{"rutracker.org", "rutracker.net", "rutracker.nl", "rutracker.cr"}

// urlPattern is host-agnostic; CanParse gates the captured host against the
// known + admin-configured domain allowlist (the SSRF barrier — see
// registry.DomainAllowed).
var urlPattern = regexp.MustCompile(`^https?://(?:www\.)?([^/]+)/forum/viewtopic\.php\?t=(\d+)`)

type plugin struct {
	sessions  *forumcommon.SessionStore
	domain    string // overridable for tests
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

// SupportsAnonymousDownload implements registry.WithAnonymousDownload: the
// topic page exposes a magnet without login (Download falls back to it when
// no credentials are present), so credentials are optional — they only
// enable the preferred .torrent path.
var _ registry.WithAnonymousDownload = (*plugin)(nil)

func (p *plugin) SupportsAnonymousDownload() bool { return true }

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

// CanParse — true for any rutracker viewtopic URL whose host is known or
// admin-allowlisted.
func (p *plugin) CanParse(rawURL string) bool {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	return m != nil && registry.DomainAllowed(pluginName, m[1], knownDomains)
}

// Parse extracts the topic ID and produces a placeholder Topic with the
// canonical URL form. The full title comes from the first Check() call.
func (p *plugin) Parse(_ context.Context, rawURL string) (*domain.Topic, error) {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return nil, errors.New("not a rutracker viewtopic URL")
	}
	topicID, err := strconv.Atoi(m[2])
	if err != nil {
		return nil, fmt.Errorf("topic id: %w", err)
	}
	return &domain.Topic{
		TrackerName: pluginName,
		URL:         rawURL,
		DisplayName: fmt.Sprintf("RuTracker topic %d", topicID),
		Extra:       map[string]any{"topic_id": topicID},
	}, nil
}

// --- WithCredentials ---------------------------------------------------

// Login posts the login form. The cookie jar attached to the session
// captures bb_session for subsequent calls.
func (p *plugin) Login(ctx context.Context, creds *domain.TrackerCredential) error {
	if creds == nil || creds.Username == "" {
		return errors.New("rutracker credentials are required")
	}
	sess := p.sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	if p.transport != nil {
		sess.Client.Transport = p.transport
	}
	form := url.Values{
		"login_username": {creds.Username},
		"login_password": {string(creds.SecretEnc)}, // secret already decrypted by caller in v0.4
		"login":          {"Вход"},
	}
	endpoint := "https://" + p.effectiveDomain() + "/forum/login.php"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return fmt.Errorf("rutracker login: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("rutracker login: read body: %w", err)
	}
	// Positive-indicator check: rutracker.org renders a "logged-in-username"
	// span ONLY on authenticated pages. The old `|| resp.StatusCode == 200`
	// escape hatch was a bug — the login form also returns 200 with an
	// error panel, so Login always succeeded regardless of credentials.
	if !strings.Contains(string(body), `id="logged-in-username"`) {
		return errors.New("rutracker login failed: invalid credentials (no logged-in marker in response)")
	}
	sess.LoggedIn = true
	return nil
}

// Verify quickly checks whether the cached session is still valid by
// hitting a known authenticated page.
func (p *plugin) Verify(ctx context.Context, creds *domain.TrackerCredential) (bool, error) {
	sess := p.sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+p.effectiveDomain()+"/forum/index.php", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if err != nil {
		return false, fmt.Errorf("rutracker verify: read body: %w", err)
	}
	return strings.Contains(string(body), `id="logged-in-username"`), nil
}

// --- Check / Download --------------------------------------------------

var (
	titleRe         = regexp.MustCompile(`(?s)<title>([^<]+)</title>`)
	magnetRe        = regexp.MustCompile(`(magnet:\?xt=urn:btih:[A-Fa-f0-9]+[^"'&\s]*)`)
	dlHrefRe        = regexp.MustCompile(`href="(dl\.php\?t=\d+)"`)
	hashLooksLikeRe = regexp.MustCompile(`urn:btih:([A-Fa-f0-9]+)`)

	// ogImageRe matches <meta property="og:image" content="..."> — the most
	// robust poster source when RuTracker emits it.
	ogImageRe = regexp.MustCompile(`(?i)<meta[^>]+property="og:image"[^>]+content="([^"]+)"`)
	// postVarImgRe matches RuTracker's lazy-loaded poster:
	// <var class="postImg postImgAligned img-right" title="https://...jpg">.
	// The real URL lives in the title attribute (src is a placeholder).
	postVarImgRe = regexp.MustCompile(`(?i)<var[^>]+class="[^"]*postImg[^"]*"[^>]+title="([^"]+)"`)
	// postImgSrcRe matches the eager form: <img class="postImg" src="...">.
	postImgSrcRe = regexp.MustCompile(`(?i)<img[^>]+class="[^"]*postImg[^"]*"[^>]+src="([^"]+)"`)
)

// cleanTitle decodes a raw <title> match from cp1251 and strips the site
// suffix. Thin wrapper over the shared forumcommon helper so Check and
// ResolveMetadata stay consistent. Decoding is mandatory: undecoded cp1251
// Cyrillic is invalid UTF-8 and Postgres rejects it (SQLSTATE 22021) on write.
func cleanTitle(raw string) string {
	return forumcommon.CleanTitle(raw, " :: RuTracker.org")
}

// extractImageURL returns the first poster image URL from a topic page body,
// preferring og:image, then the lazy-loaded postImg <var title=...>, then an
// eager <img class="postImg" src=...>. Returns "" when none is present.
func extractImageURL(body []byte) string {
	if m := ogImageRe.FindSubmatch(body); m != nil {
		return strings.TrimSpace(string(m[1]))
	}
	if m := postVarImgRe.FindSubmatch(body); m != nil {
		return strings.TrimSpace(string(m[1]))
	}
	if m := postImgSrcRe.FindSubmatch(body); m != nil {
		return strings.TrimSpace(string(m[1]))
	}
	return ""
}

// Check fetches the topic page and extracts a hash. The hash is the
// torrent BTIH from the magnet link, which changes whenever the uploader
// re-uploads.
func (p *plugin) Check(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential) (*domain.Check, error) {
	body, err := p.fetchTopicPage(ctx, topic, creds)
	if err != nil {
		return nil, err
	}
	check := &domain.Check{}
	if m := titleRe.FindSubmatch(body); m != nil {
		check.DisplayName = cleanTitle(string(m[1]))
	}
	if m := hashLooksLikeRe.FindSubmatch(body); m != nil {
		check.Hash = strings.ToLower(string(m[1]))
	} else {
		return nil, errors.New("rutracker: no infohash found in topic page")
	}
	return check, nil
}

// --- WithMetadata ------------------------------------------------------

var _ registry.WithMetadata = (*plugin)(nil)

// ResolveMetadata fetches the topic page and extracts a human-readable
// title (the <title> tag minus the site suffix) and a poster image URL.
// creds may be nil — the public topic page is fetched without a session,
// mirroring how Check handles nil creds via fetchTopicPage. Image URL is
// "" when the page exposes no poster; the caller treats errors as fail-open.
func (p *plugin) ResolveMetadata(ctx context.Context, rawURL string, creds *domain.TrackerCredential) (*registry.Metadata, error) {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return nil, errors.New("resolve metadata: not a rutracker viewtopic URL")
	}
	id, err := strconv.Atoi(m[2])
	if err != nil {
		return nil, fmt.Errorf("resolve metadata: topic id: %w", err)
	}
	// Fetch a URL rebuilt from the trusted host (p.effectiveDomain()) + the
	// numeric topic id, never the raw user-supplied URL, so a crafted URL
	// cannot redirect the request to an arbitrary host (CodeQL
	// go/request-forgery). Mirrors how Download constructs its dl.php URL.
	canonical := fmt.Sprintf("https://%s/forum/viewtopic.php?t=%d", p.effectiveDomain(), id)
	body, err := p.fetchBytes(ctx, nil, creds, canonical)
	if err != nil {
		return nil, fmt.Errorf("resolve metadata: %w", err)
	}
	meta := &registry.Metadata{}
	if mt := titleRe.FindSubmatch(body); mt != nil {
		meta.Title = cleanTitle(string(mt[1]))
	}
	meta.ImageURL = extractImageURL(body)
	return meta, nil
}

// Download submits a usable torrent payload for the current topic.
//
// RuTracker page magnets are hash-only (no `&tr=` announce URLs). For a
// private tracker that is unusable: the client has no announce to reach
// peers and no DHT fallback, so it sits on "Downloading metadata" forever
// (#52). The authenticated `.torrent` from dl.php carries the announce
// list and the full info dict, so we prefer it whenever we can fetch it.
//
// Priority:
//  1. dl.php `.torrent` — when creds are configured (the link needs the
//     session cookie) and the response validates as a bencoded torrent.
//  2. page magnet — fallback when creds are absent, dl.php is missing, the
//     fetch fails, or the response is not a real torrent (e.g. an HTML
//     login page). A degraded magnet still beats a hard failure.
//
// Both fetches (topic page + dl.php) share the caller's context, which the
// scheduler bounds with TrackerHTTPTimeout per Download invocation — so the
// two sequential round-trips draw from one timeout budget.
func (p *plugin) Download(ctx context.Context, topic *domain.Topic, _ *domain.Check, creds *domain.TrackerCredential) (*domain.Payload, error) {
	body, err := p.fetchTopicPage(ctx, topic, creds)
	if err != nil {
		return nil, err
	}

	if creds != nil {
		if m := dlHrefRe.FindSubmatch(body); m != nil {
			dlURL := "https://" + p.effectiveDomain() + "/forum/" + string(m[1])
			// Validate the payload is a real bencoded torrent before
			// submitting it — guards against handing the client an HTML
			// login/error page that dl.php returns on a dead session.
			torrent, ferr := p.fetchBytes(ctx, topic, creds, dlURL)
			switch {
			case ferr != nil:
				// Fail-open: a degraded magnet still beats a hard failure,
				// but log it — the operator-visible symptom of a dead
				// session is the client stuck on metadata (#52), and a
				// silent fallback would leave no breadcrumb.
				log.Warn().Str("plugin", pluginName).Str("step", "download").
					Str("url", dlURL).Err(ferr).
					Msg("dl.php fetch failed; falling back to hash-only page magnet")
			default:
				if _, verr := infohash.FromTorrent(torrent); verr != nil {
					log.Warn().Str("plugin", pluginName).Str("step", "download").
						Str("url", dlURL).Err(verr).
						Msg("dl.php returned a non-torrent payload; falling back to hash-only page magnet")
					break
				}
				return &domain.Payload{TorrentFile: torrent, FileName: torrentFileName(string(m[1]))}, nil
			}
		}
	}

	if m := magnetRe.Find(body); m != nil {
		return &domain.Payload{MagnetURI: string(m)}, nil
	}
	return nil, errors.New("rutracker: topic page has no downloadable torrent or magnet link")
}

// dlTopicIDRe extracts the numeric topic id from a `dl.php?t=<id>` link.
var dlTopicIDRe = regexp.MustCompile(`t=(\d+)`)

// torrentFileName derives a per-topic .torrent filename from the dl.php
// link (`dl.php?t=<id>`) so deliveries are diagnosable instead of all
// sharing one static name. Host-agnostic, so it works under test rewrites.
// Falls back to a generic name when no id is present.
func torrentFileName(dlPath string) string {
	if m := dlTopicIDRe.FindStringSubmatch(dlPath); m != nil {
		return "rutracker-" + m[1] + ".torrent"
	}
	return "rutracker.torrent"
}

// canonicalTopicPageURL rebuilds the viewtopic URL from the trusted host
// (p.effectiveDomain()) + the numeric topic id parsed from rawURL — never the
// raw stored host. This makes the check/download loop follow an admin-selected
// active domain or an auto-rotated mirror (issue #126), and pins the request
// to a trusted host (CodeQL go/request-forgery), mirroring ResolveMetadata and
// the Download dl.php URL.
func (p *plugin) canonicalTopicPageURL(rawURL string) (string, error) {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return "", errors.New("not a rutracker viewtopic URL")
	}
	id, err := strconv.Atoi(m[2])
	if err != nil {
		return "", fmt.Errorf("topic id: %w", err)
	}
	return fmt.Sprintf("https://%s/forum/viewtopic.php?t=%d", p.effectiveDomain(), id), nil
}

func (p *plugin) fetchTopicPage(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential) ([]byte, error) {
	canonical, err := p.canonicalTopicPageURL(topic.URL)
	if err != nil {
		return nil, err
	}
	return p.fetchBytes(ctx, topic, creds, canonical)
}

func (p *plugin) fetchBytes(ctx context.Context, _ *domain.Topic, creds *domain.TrackerCredential, target string) ([]byte, error) {
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
		return nil, fmt.Errorf("rutracker GET %s -> %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}
