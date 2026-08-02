// Package kinozal implements the Kinozal.tv tracker plugin.
//
// Kinozal is another phpBB-derived tracker. The flow is similar to
// RuTracker but the URL pattern is /details.php?id=<topic_id> and the
// download link is /download.php?id=<topic_id>.
//
// **Validation status:** verified end-to-end against a live Kinozal account
// (2026-06) — login, infohash resolution via get_srv_details.php, metadata
// (title + poster), and download → torrent-client delivery all confirmed.
package kinozal

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

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// siteSuffixRe matches the " :: Кинозал.<BRAND>" tail Kinozal appends to every
// <title>, where <BRAND> tracks the mirror being served (.ТВ / .МЕ / .GURU).
// Anchored to the end so a release title that legitimately contains " :: " is
// left intact.
var siteSuffixRe = regexp.MustCompile(` :: Кинозал\.[^\s:]+$`)

const (
	pluginName  = "kinozal"
	displayName = "Kinozal"
	// defaultDomain is kinozal.me, not the original kinozal.tv: as of
	// 2026-08-03 kinozal.tv no longer resolves — both 8.8.8.8 and 1.1.1.1
	// return SERVFAIL (broken authoritative NS), so a fresh install starting
	// there could never reach the tracker. kinozal.tv stays in knownDomains
	// so topic URLs already stored against it still parse.
	defaultDomain = "kinozal.me"
	userAgent     = "Marauder/0.3 (+https://marauder.cc)"
)

// knownDomains are the hosts Marauder is willing to *fetch from*. Returned by
// Domains(), which feeds the admin's active-domain picker, the automatic
// rotation ring, and the boot-time re-validation that drops a persisted active
// domain a plugin no longer lists. The dead kinozal.tv is deliberately absent:
// listing it would let rotation land back on a host that cannot resolve, and
// would preserve it across an upgrade for anyone who had selected it
// explicitly.
var knownDomains = []string{"kinozal.me", "kinozal.guru"}

// parseDomains are the hosts a stored topic URL may legitimately carry. It is
// knownDomains plus retired hosts, and is used by CanParse only — never to
// build a request. kinozal.tv was the default for most of the project's life,
// so dropping it outright would orphan every topic added before 2026-08-03;
// keeping it here is safe because canonicalDetailsURL rebuilds every request
// from effectiveDomain(), so a stored kinozal.tv URL is never fetched as-is.
var parseDomains = []string{"kinozal.me", "kinozal.guru", "kinozal.tv"}

// urlPattern is host-agnostic; CanParse gates the captured host against the
// known + admin-configured domain allowlist (the SSRF barrier — see
// registry.DomainAllowed).
var urlPattern = regexp.MustCompile(`^https?://(?:www\.)?([^/]+)/details\.php\?id=(\d+)`)

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

func (p *plugin) CanParse(rawURL string) bool {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	return m != nil && registry.DomainAllowed(pluginName, m[1], parseDomains)
}

func (p *plugin) Parse(_ context.Context, rawURL string) (*domain.Topic, error) {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return nil, errors.New("not a kinozal details URL")
	}
	id, err := strconv.Atoi(m[2])
	if err != nil {
		return nil, fmt.Errorf("topic id: %w", err)
	}
	return &domain.Topic{
		TrackerName: pluginName,
		URL:         rawURL,
		DisplayName: fmt.Sprintf("Kinozal topic %d", id),
		Extra:       map[string]any{"topic_id": id},
	}, nil
}

// --- WithCredentials ---------------------------------------------------

func (p *plugin) Login(ctx context.Context, creds *domain.TrackerCredential) error {
	if creds == nil || creds.Username == "" {
		return errors.New("kinozal credentials are required")
	}
	sess := p.sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	if p.transport != nil {
		sess.Client.Transport = p.transport
	}
	form := url.Values{
		"username": {creds.Username},
		"password": {string(creds.SecretEnc)},
	}
	endpoint := "https://" + p.effectiveDomain() + "/takelogin.php"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return fmt.Errorf("kinozal login: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("kinozal login: read body: %w", err)
	}
	// Kinozal returns either a redirect to /index.php or a page with
	// "Неверный логин или пароль" on failure.
	if strings.Contains(string(body), "Неверный") {
		return errors.New("kinozal login failed: invalid credentials")
	}
	sess.LoggedIn = true
	return nil
}

func (p *plugin) Verify(ctx context.Context, creds *domain.TrackerCredential) (bool, error) {
	sess := p.sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+p.effectiveDomain()+"/", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return false, fmt.Errorf("kinozal verify: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if err != nil {
		return false, fmt.Errorf("kinozal verify: read body: %w", err)
	}
	// Kinozal shows a "Выход" link in the header when logged in.
	return strings.Contains(string(body), "logout.php") || strings.Contains(string(body), "Выход"), nil
}

// --- Check / Download --------------------------------------------------

var (
	titleRe = regexp.MustCompile(`(?s)<title>([^<]+)</title>`)
	// hashRe matches the live label "Инфо хеш" (хеш, Cyrillic е) and tolerates
	// the older "Инфо хэш" (э) spelling + variable spacing before the hash.
	hashRe    = regexp.MustCompile(`(?i)Инфо\s*х[эе]ш[^A-Za-z0-9]+([A-Fa-f0-9]{40})`)
	hashAltRe = regexp.MustCompile(`(?i)Info[\s_-]?hash[^A-Za-z0-9]+([A-Fa-f0-9]{40})`)

	// ogImageRe matches <meta property="og:image" content="..."> — the poster
	// source Kinozal emits in the page head (commonly a relative /i/poster/... URL).
	ogImageRe = regexp.MustCompile(`(?i)<meta[^>]+property="og:image"[^>]+content="([^"]+)"`)
)

// cleanTitle decodes a raw <title> match from cp1251 and strips the site
// suffix, so Check and ResolveMetadata stay consistent. Decoding is mandatory:
// undecoded cp1251 Cyrillic is invalid UTF-8 and Postgres rejects it
// (SQLSTATE 22021) on write.
//
// The suffix is matched by pattern rather than by the shared
// forumcommon.CleanTitle's fixed-string trim because each mirror brands the
// tail after its own domain — measured 2026-08-03: kinozal.me serves
// "Кинозал.МЕ" and kinozal.guru "Кинозал.GURU" where kinozal.tv served
// "Кинозал.ТВ". Trimming one literal would leave the other mirrors' tails on
// every topic name after a domain rotation.
func cleanTitle(raw string) string {
	t := strings.TrimSpace(forumcommon.DecodeWindows1251(raw))
	return strings.TrimSpace(siteSuffixRe.ReplaceAllString(t, ""))
}

// extractImageURL returns the og:image poster from a topic page body, made
// absolute against the trusted host (Kinozal emits relative /i/poster/...
// paths). Returns "" when the page exposes no poster — never fabricated.
func extractImageURL(body []byte, host string) string {
	m := ogImageRe.FindSubmatch(body)
	if m == nil {
		return ""
	}
	raw := strings.TrimSpace(string(m[1]))
	switch {
	case raw == "":
		return ""
	case strings.HasPrefix(raw, "http://"), strings.HasPrefix(raw, "https://"):
		return raw
	case strings.HasPrefix(raw, "//"):
		return "https:" + raw
	case strings.HasPrefix(raw, "/"):
		return "https://" + host + raw
	default:
		return "https://" + host + "/" + raw
	}
}

// parseTopicID extracts the numeric Kinozal topic id from the "id" query
// parameter of rawURL. Shared by canonicalDetailsURL and fetchInfohash so the
// url.Parse → id logic lives in exactly one place.
func parseTopicID(rawURL string) (int, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return 0, fmt.Errorf("kinozal: parse url: %w", err)
	}
	id, err := strconv.Atoi(u.Query().Get("id"))
	if err != nil {
		return 0, fmt.Errorf("kinozal: topic id: %w", err)
	}
	return id, nil
}

// canonicalDetailsURL rebuilds the details URL from the trusted host
// (p.effectiveDomain()) + the numeric id parsed from rawURL — never the raw
// user URL. Avoids request forgery (CodeQL go/request-forgery) and pins the
// request to the trusted host so Check's title matches ResolveMetadata's
// (issue #90).
func (p *plugin) canonicalDetailsURL(rawURL string) (string, error) {
	id, err := parseTopicID(rawURL)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("https://%s/details.php?id=%d", p.effectiveDomain(), id), nil
}

// Check makes TWO requests per tick: the details page for the display title,
// then get_srv_details.php for the infohash (Kinozal exposes the hash only
// there, not on the details page). Both run under the scheduler's single
// checkCtx, so they share one TrackerHTTPTimeout budget. The hash is the
// load-bearing field, so a hash-fetch failure fails the whole Check and the
// already-fetched title is intentionally discarded — title self-heal simply
// retries next tick rather than persisting a title with no matching hash.
func (p *plugin) Check(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential) (*domain.Check, error) {
	canonical, err := p.canonicalDetailsURL(topic.URL)
	if err != nil {
		return nil, err
	}
	body, err := p.fetch(ctx, canonical, creds)
	if err != nil {
		return nil, err
	}
	check := &domain.Check{}
	if m := titleRe.FindSubmatch(body); m != nil {
		check.DisplayName = cleanTitle(string(m[1]))
	}
	hash, err := p.fetchInfohash(ctx, topic.URL, creds)
	if err != nil {
		return nil, err
	}
	check.Hash = hash
	return check, nil
}

// fetchInfohash pulls the torrent infohash from get_srv_details.php?id=...&action=2.
// Kinozal does NOT render the hash on the details page — it lives only in this
// authenticated AJAX endpoint's response ("Инфо хеш: <40 hex>"). The URL is
// rebuilt from the trusted domain + numeric id (never the raw user URL) to
// avoid request forgery (CodeQL go/request-forgery), mirroring Download.
func (p *plugin) fetchInfohash(ctx context.Context, rawURL string, creds *domain.TrackerCredential) (string, error) {
	id, err := parseTopicID(rawURL)
	if err != nil {
		return "", err
	}
	srvURL := fmt.Sprintf("https://%s/get_srv_details.php?id=%d&action=2", p.effectiveDomain(), id)
	body, err := p.fetch(ctx, srvURL, creds)
	if err != nil {
		return "", fmt.Errorf("kinozal: get_srv_details: %w", err)
	}
	if m := hashRe.FindSubmatch(body); m != nil {
		return strings.ToLower(string(m[1])), nil
	}
	if m := hashAltRe.FindSubmatch(body); m != nil {
		return strings.ToLower(string(m[1])), nil
	}
	return "", errors.New("kinozal: no infohash found in topic page")
}

// --- WithMetadata ------------------------------------------------------

var _ registry.WithMetadata = (*plugin)(nil)

// ResolveMetadata fetches the public details page and extracts a human-readable
// title (the <title> tag, cp1251-decoded, minus the " :: Кинозал.ТВ" suffix)
// and the og:image poster (made absolute). creds may be nil — the details page
// is publicly viewable, so a session isn't required. Image URL is "" when the
// page exposes no poster; the caller treats errors as fail-open.
func (p *plugin) ResolveMetadata(ctx context.Context, rawURL string, creds *domain.TrackerCredential) (*registry.Metadata, error) {
	canonical, err := p.canonicalDetailsURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("resolve metadata: %w", err)
	}
	body, err := p.fetch(ctx, canonical, creds)
	if err != nil {
		return nil, fmt.Errorf("resolve metadata: %w", err)
	}
	meta := &registry.Metadata{}
	if mt := titleRe.FindSubmatch(body); mt != nil {
		meta.Title = cleanTitle(string(mt[1]))
	}
	meta.ImageURL = extractImageURL(body, p.effectiveDomain())
	return meta, nil
}

func (p *plugin) Download(ctx context.Context, topic *domain.Topic, _ *domain.Check, creds *domain.TrackerCredential) (*domain.Payload, error) {
	id, _ := topic.Extra["topic_id"].(int)
	if id == 0 {
		// Topic might have been deserialized from JSON; cast may produce float64.
		if f, ok := topic.Extra["topic_id"].(float64); ok {
			id = int(f)
		}
	}
	if id == 0 {
		return nil, errors.New("kinozal: no topic_id in extras")
	}
	dlURL := "https://dl." + p.effectiveDomain() + "/download.php?id=" + strconv.Itoa(id)
	body, err := p.fetch(ctx, dlURL, creds)
	if err != nil {
		// Some Kinozal mirrors host downloads on the main domain.
		dlURL = "https://" + p.effectiveDomain() + "/download.php?id=" + strconv.Itoa(id)
		body, err = p.fetch(ctx, dlURL, creds)
		if err != nil {
			return nil, err
		}
	}
	return &domain.Payload{TorrentFile: body, FileName: fmt.Sprintf("kinozal-%d.torrent", id)}, nil
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
		return nil, fmt.Errorf("kinozal GET: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("kinozal GET %s -> %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}
