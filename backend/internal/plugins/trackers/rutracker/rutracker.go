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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/infohash"
	"github.com/artyomsv/marauder/backend/internal/plugins/captchalogin"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

const (
	pluginName    = "rutracker"
	displayName   = "RuTracker.org"
	defaultDomain = "rutracker.org"
	// userAgent identifies Marauder honestly and is used only when NO
	// Cloudflare clearance is configured. A clearance carries its own
	// User-Agent which MUST be sent verbatim — see requestUA.
	userAgent = "Marauder/0.3 (+https://marauder.cc)"
)

// knownDomains lists the mirrors the rotation ring may try, canonical first.
//
// Every entry is a domain rotation may silently migrate onto, so an entry that
// does not serve real content is worse than no entry at all. Two were removed
// after being checked against the live hosts:
//
//   - rutracker.cr (2026-07-28) — stopped resolving entirely (NXDOMAIN).
//   - rutracker.nl (2026-07-30) — resolves and answers 200, but serves a 5 KB
//     stub titled "Redirecting..." with no torrent content and no Cloudflare
//     challenge. Rotation reached it and every check then failed with "no
//     infohash found in topic page".
//
// Verify a candidate actually serves a topic page before adding one here;
// "it resolves" is not sufficient.
var knownDomains = []string{"rutracker.org", "rutracker.net"}

// urlPattern is host-agnostic; CanParse gates the captured host against the
// known + admin-configured domain allowlist (the SSRF barrier — see
// registry.DomainAllowed).
var urlPattern = regexp.MustCompile(`^https?://(?:www\.)?([^/]+)/forum/viewtopic\.php\?t=(\d+)`)

type plugin struct {
	sessions  *forumcommon.SessionStore
	domain    string // overridable for tests
	transport http.RoundTripper

	// engine backs the interactive captcha login (see interactive.go). Built
	// lazily because captchaConfig resolves the active domain, which is not
	// known at init() time, and rebuilt when that domain changes — engineDomain
	// records which one the cached engine was built for.
	engineMu     sync.Mutex
	engine       *captchalogin.Engine
	engineDomain string
}

func init() {
	registry.RegisterTracker(&plugin{
		sessions: forumcommon.New(),
		domain:   defaultDomain,
	})
}

func (p *plugin) Name() string        { return pluginName }
func (p *plugin) DisplayName() string { return displayName }

var _ registry.WithCloudflare = (*plugin)(nil)

// UsesCloudflare implements registry.WithCloudflare. Verified against the live
// site on 2026-08-01: every /forum/ path is answered with a 403 Cf-Mitigated
// interstitial (/forum/index.php is the sole exception).
//
// Declaring this marks the tracker as needing a Cloudflare clearance, which
// fetchBytes mints via the configured provider and replays together with the
// browser User-Agent that earned it. It no longer routes fetches through a
// browser: the earlier claim that a clearance could not be replayed from Go
// was a User-Agent mismatch, not a TLS-fingerprint binding.
func (p *plugin) UsesCloudflare() bool { return true }

// effectiveTransport returns the plugin-local transport used by tests to reach
// an httptest server.
//
// RuTracker is not fetched through a browser any more: it replays a minted
// Cloudflare clearance from ordinary Go requests, which is faster, does not
// serialise, carries binary bodies (so dl.php works) and permits a login (so
// search works).
func (p *plugin) effectiveTransport() http.RoundTripper { return p.transport }

// requestUA picks the User-Agent for an outbound request.
//
// A clearance is only honoured by Cloudflare when the request repeats the
// exact User-Agent the browser used to earn it; sending Marauder's own UA with
// a valid cf_clearance returns 403 (measured 2026-08-01). Without a clearance
// there is nothing to match, so we identify ourselves honestly.
func (p *plugin) requestUA(c registry.Clearance) string {
	if c.Valid() {
		return c.UserAgent
	}
	return userAgent
}

// session returns the per-user (or anonymous) session with the test transport
// applied.
//
// Centralised so Login, Verify and the fetch path cannot drift apart: Verify
// previously built its own session and forgot the transport, which made it
// dial the live site and misreport a healthy session as dead.
func (p *plugin) session(creds *domain.TrackerCredential) *forumcommon.Session {
	key := pluginName + ":nocreds"
	if creds != nil {
		key = forumcommon.SessionKey(pluginName, creds.UserID.String())
	}
	sess := p.sessions.GetOrCreate(key, userAgent)
	if tr := p.effectiveTransport(); tr != nil {
		sess.Client.Transport = tr
	}
	return sess
}

var _ registry.WithChallengeProbe = (*plugin)(nil)

// ChallengeProbeURL implements registry.WithChallengeProbe: the page the solver
// is pointed at to earn a clearance.
//
// It must be a page Cloudflare actually challenges. A clearance is scoped to
// the rule that issued it, not to the whole host: minting from the site root
// returns a cf_clearance — the root 301s to /forum/index.php, which carries no
// challenge — but that cookie is still rejected with 403 on /forum/login.php
// and /forum/tracker.php (measured 2026-08-01). login.php is challenged, cheap
// to render and has no side effects on GET, so it is the probe.
func (p *plugin) ChallengeProbeURL() string {
	return "https://" + p.effectiveDomain() + "/forum/login.php"
}

// applyClearance seeds the session jar with the Cloudflare clearance cookie
// for u's host and returns it so the caller can match the User-Agent, plus the
// provider's error if the mint failed.
//
// A solver failure is not fatal to the REQUEST: plenty of RuTracker paths are
// ungated, so the caller proceeds without a clearance. It is fatal to the
// DIAGNOSIS, which is why the error comes back instead of being logged and
// dropped. A caller that then hits a challenge must blame the solver rather
// than report ErrCloudflareChallenge — see challengeCause.
func (p *plugin) applyClearance(ctx context.Context, sess *forumcommon.Session, u *url.URL) (registry.Clearance, error) {
	c, err := registry.ClearanceFor(ctx, p.ChallengeProbeURL())
	if err != nil {
		log.Warn().Str("plugin", pluginName).Err(err).
			Msg("cloudflare clearance unavailable; requesting without one")
		return registry.Clearance{}, err
	}
	if !c.Valid() {
		return registry.Clearance{}, nil
	}
	jarCookies := make([]*http.Cookie, 0, len(c.Cookies))
	for name, val := range c.Cookies {
		jarCookies = append(jarCookies, &http.Cookie{Name: name, Value: val, Path: "/"})
	}
	root := &url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/"}
	sess.Client.Jar.SetCookies(root, jarCookies)
	return c, nil
}

// challengeCause names the culprit for a request the tracker just blocked.
//
// clearErr is applyClearance's second return: non-nil means the configured
// solver could not mint, so this request was always going to be challenged and
// the tracker's wall says nothing about the tracker. Reporting
// ErrCloudflareChallenge there produces the "this tracker needs a browser to
// get through" message while the browser is the thing that is broken — the
// 2026-08-05 boot race, where FlareSolverr's ~9s Chrome startup parked two
// topics on a 30-minute backoff.
func challengeCause(clearErr error) error {
	if clearErr != nil {
		return fmt.Errorf("%w: %w", registry.ErrClearanceUnavailable, clearErr)
	}
	return registry.ErrCloudflareChallenge
}

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

// isCloudflareChallenge reports whether a response is a Cloudflare
// interstitial rather than the page we asked for. Cloudflare labels these
// with Cf-Mitigated (403 for the challenge, 503 for the legacy "checking
// your browser" page), which is a far more reliable signal than sniffing the
// body — and, unlike the body, it is available before reading it.
func isCloudflareChallenge(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	if resp.Header.Get("Cf-Mitigated") == "" {
		return false
	}
	return resp.StatusCode == http.StatusForbidden ||
		resp.StatusCode == http.StatusServiceUnavailable
}

// --- WithCredentials ---------------------------------------------------

// Login posts the login form. The cookie jar attached to the session
// captures bb_session for subsequent calls.
//
// This runs even when a solver is configured. An earlier revision skipped the
// POST entirely in that case, on the belief that a login could not survive the
// browser hop and unlocked only dl.php anyway. Both halves were wrong:
// tracker.php search is login-gated too, and a clearance replayed with its own
// User-Agent lets an ordinary Go POST reach the login form (measured
// 2026-08-01). Skipping it left every session anonymous, which is what made
// search report "needs a tracker account".
func (p *plugin) Login(ctx context.Context, creds *domain.TrackerCredential) error {
	if creds == nil || creds.Username == "" {
		return errors.New("rutracker credentials are required")
	}
	// A stored session from the interactive captcha flow wins: rehydrating it
	// avoids a login POST entirely, and RuTracker imposes a captcha on clients
	// it sees logging in repeatedly. A dead stored session falls through to a
	// fresh credential login rather than failing outright.
	if len(creds.SessionEnc) > 0 {
		if err := p.restoreSession(ctx, creds); err == nil {
			return nil
		}
	}
	_, err := retryOnChallenge(p, func() (struct{}, error) {
		return struct{}{}, p.loginOnce(ctx, creds)
	})
	return err
}

// loginOnce performs a single credential POST. Wrapped by Login's
// retryOnChallenge so a rejected clearance is re-minted rather than reported as
// a permanent block.
func (p *plugin) loginOnce(ctx context.Context, creds *domain.TrackerCredential) error {
	endpoint := "https://" + p.effectiveDomain() + "/forum/login.php"
	u, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	sess := p.session(creds)
	clearance, clearErr := p.applyClearance(ctx, sess, u)

	form := url.Values{
		"login_username": {creds.Username},
		"login_password": {string(creds.SecretEnc)}, // secret already decrypted by caller in v0.4
		"login":          {"вход"},
		"redirect":       {"index.php"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", p.requestUA(clearance))
	req.Header.Set("Origin", "https://"+p.effectiveDomain())
	req.Header.Set("Referer", endpoint)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return fmt.Errorf("rutracker login: %w", err)
	}
	defer resp.Body.Close()
	// Check for the interstitial BEFORE testing for the logged-in marker: a
	// challenge page naturally lacks that marker, and reporting it as bad
	// credentials is the misdiagnosis this guard exists to prevent.
	if isCloudflareChallenge(resp) {
		return fmt.Errorf("rutracker login: %w", challengeCause(clearErr))
	}
	// Same reasoning as the guard above, for a different failure: an error page
	// has no logged-in marker either, so falling through to the marker check
	// would report a healthy account as "invalid credentials". Observed live
	// while RuTracker's origin was serving 520s — the UI told the user their
	// password was wrong when nothing was wrong with it.
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("rutracker login: tracker responded %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("rutracker login: read body: %w", err)
	}
	// Positive-indicator check: rutracker.org renders a "logged-in-username"
	// span ONLY on authenticated pages. The old `|| resp.StatusCode == 200`
	// escape hatch was a bug — the login form also returns 200 with an
	// error panel, so Login always succeeded regardless of credentials.
	if bytes.Contains(body, []byte(`id="logged-in-username"`)) {
		sess.LoggedIn = true
		return nil
	}
	// RuTracker's login captcha is adaptive: imposed on a client it distrusts
	// (a run of failed attempts is enough) and dropped again after a success.
	// Report it distinctly so the caller can run the interactive flow instead
	// of telling the user their password is wrong.
	if bytes.Contains(body, []byte("cap_sid")) {
		return fmt.Errorf("rutracker login: %w", registry.ErrCaptchaRequired)
	}
	return errors.New("rutracker login failed: invalid credentials (no logged-in marker in response)")
}

// restoreSession rehydrates cookies harvested by the interactive login and
// confirms they still work. Mirrors the LostFilm pattern.
func (p *plugin) restoreSession(ctx context.Context, creds *domain.TrackerCredential) error {
	var cookies map[string]string
	if err := json.Unmarshal(creds.SessionEnc, &cookies); err != nil {
		return fmt.Errorf("rutracker: corrupt stored session: %w", registry.ErrSessionExpired)
	}
	sess := p.session(creds)
	// bb_session is scoped to /forum/, so it must be set against that path —
	// setting it on the origin root would leave it out of every forum request.
	u, err := url.Parse("https://" + p.effectiveDomain() + "/forum/")
	if err != nil {
		return err
	}
	jarCookies := make([]*http.Cookie, 0, len(cookies))
	for name, val := range cookies {
		jarCookies = append(jarCookies, &http.Cookie{Name: name, Value: val, Path: "/forum/"})
	}
	sess.Client.Jar.SetCookies(u, jarCookies)
	ok, err := p.Verify(ctx, creds)
	if err != nil {
		return err
	}
	if !ok {
		return registry.ErrSessionExpired
	}
	sess.LoggedIn = true
	return nil
}

// Verify quickly checks whether the cached session is still valid by hitting a
// page that renders the logged-in marker.
//
// It goes through fetchBytes rather than dialling directly so it picks up the
// Cloudflare clearance, the matching User-Agent and the test transport. The
// direct dial it used before ignored all three, which made it report "session
// invalid" for a perfectly good session whenever the site was challenged.
func (p *plugin) Verify(ctx context.Context, creds *domain.TrackerCredential) (bool, error) {
	body, err := p.fetchBytes(ctx, nil, creds,
		"https://"+p.effectiveDomain()+"/forum/index.php")
	if err != nil {
		return false, err
	}
	return bytes.Contains(body, []byte(`id="logged-in-username"`)), nil
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

// retryOnChallenge runs attempt; if it reports a Cloudflare challenge, the
// cached clearance is dropped and attempt runs exactly once more.
//
// A challenge despite an attached clearance means the clearance died — the
// egress IP moved, or Cloudflare rotated it. Retrying exactly once is
// deliberate: if the second attempt is challenged too, something else is wrong
// and looping would spin up a browser on every check.
//
// This lives in one place because it originally did not. The fetch path had the
// retry and Login did not, so a rotated clearance healed itself for checks
// while wedging every login permanently behind "blocked by Cloudflare".
func retryOnChallenge[T any](p *plugin, attempt func() (T, error)) (T, error) {
	out, err := attempt()
	if !errors.Is(err, registry.ErrCloudflareChallenge) {
		return out, err
	}
	registry.InvalidateClearance(p.ChallengeProbeURL())
	return attempt()
}

// fetchBytes GETs target, re-minting once if the clearance turns out to be dead.
func (p *plugin) fetchBytes(ctx context.Context, _ *domain.Topic, creds *domain.TrackerCredential, target string) ([]byte, error) {
	return retryOnChallenge(p, func() ([]byte, error) {
		return p.fetchOnce(ctx, creds, target)
	})
}

func (p *plugin) fetchOnce(ctx context.Context, creds *domain.TrackerCredential, target string) ([]byte, error) {
	// SSRF guard: every caller builds target from p.effectiveDomain(), but
	// this is the last line of defense before dialing — refuse any
	// host that isn't the resolved effective domain (covers the test-
	// injected p.domain) or a known/admin-configured rutracker domain, and
	// any non-HTTP scheme (mirrors the rutor fetch guard).
	u, err := url.Parse(strings.TrimSpace(target))
	if err != nil {
		return nil, fmt.Errorf("rutracker: invalid URL: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("rutracker: refusing URL scheme %q", u.Scheme)
	}
	if u.Host != p.effectiveDomain() && !registry.DomainAllowed(pluginName, u.Hostname(), knownDomains) {
		return nil, fmt.Errorf("rutracker: refusing to fetch off-site host %q", u.Hostname())
	}

	sess := p.session(creds)
	clearance, clearErr := p.applyClearance(ctx, sess, u)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", p.requestUA(clearance))
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	resp, err := sess.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if isCloudflareChallenge(resp) {
		return nil, fmt.Errorf("rutracker GET %s: %w", target, challengeCause(clearErr))
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("rutracker GET %s -> %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}
