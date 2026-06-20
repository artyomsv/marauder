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

// siteSuffix is the " :: Кинозал.ТВ" tail Kinozal appends to every <title>.
const siteSuffix = " :: Кинозал.ТВ"

const (
	pluginName    = "kinozal"
	displayName   = "Kinozal.tv"
	defaultDomain = "kinozal.tv"
	userAgent     = "Marauder/0.3 (+https://marauder.cc)"
)

var urlPattern = regexp.MustCompile(`^https?://(?:www\.)?kinozal\.(?:tv|me|guru)/details\.php\?id=(\d+)`)

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

func (p *plugin) CanParse(rawURL string) bool {
	return urlPattern.MatchString(strings.TrimSpace(rawURL))
}

func (p *plugin) Parse(_ context.Context, rawURL string) (*domain.Topic, error) {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return nil, errors.New("not a kinozal details URL")
	}
	id, err := strconv.Atoi(m[1])
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
	endpoint := "https://" + p.domain + "/takelogin.php"
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+p.domain+"/", nil)
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
// suffix. Thin wrapper over the shared forumcommon helper so Check and
// ResolveMetadata stay consistent. Decoding is mandatory: undecoded cp1251
// Cyrillic is invalid UTF-8 and Postgres rejects it (SQLSTATE 22021) on write.
func cleanTitle(raw string) string {
	return forumcommon.CleanTitle(raw, siteSuffix)
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

// Check makes TWO requests per tick: the details page for the display title,
// then get_srv_details.php for the infohash (Kinozal exposes the hash only
// there, not on the details page). Both run under the scheduler's single
// checkCtx, so they share one TrackerHTTPTimeout budget. The hash is the
// load-bearing field, so a hash-fetch failure fails the whole Check and the
// already-fetched title is intentionally discarded — title self-heal simply
// retries next tick rather than persisting a title with no matching hash.
func (p *plugin) Check(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential) (*domain.Check, error) {
	body, err := p.fetch(ctx, topic.URL, creds)
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
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("kinozal: parse url: %w", err)
	}
	id, err := strconv.Atoi(u.Query().Get("id"))
	if err != nil {
		return "", fmt.Errorf("kinozal: topic id: %w", err)
	}
	srvURL := fmt.Sprintf("https://%s/get_srv_details.php?id=%d&action=2", p.domain, id)
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
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return nil, errors.New("resolve metadata: not a kinozal details URL")
	}
	id, err := strconv.Atoi(m[1])
	if err != nil {
		return nil, fmt.Errorf("resolve metadata: topic id: %w", err)
	}
	// Rebuild the URL from the trusted host (p.domain) + the numeric id, never
	// the raw user-supplied URL, so a crafted URL cannot redirect the request
	// to an arbitrary host (CodeQL go/request-forgery). Mirrors Download.
	canonical := fmt.Sprintf("https://%s/details.php?id=%d", p.domain, id)
	body, err := p.fetch(ctx, canonical, creds)
	if err != nil {
		return nil, fmt.Errorf("resolve metadata: %w", err)
	}
	meta := &registry.Metadata{}
	if mt := titleRe.FindSubmatch(body); mt != nil {
		meta.Title = cleanTitle(string(mt[1]))
	}
	meta.ImageURL = extractImageURL(body, p.domain)
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
	dlURL := "https://dl." + p.domain + "/download.php?id=" + strconv.Itoa(id)
	body, err := p.fetch(ctx, dlURL, creds)
	if err != nil {
		// Some Kinozal mirrors host downloads on the main domain.
		dlURL = "https://" + p.domain + "/download.php?id=" + strconv.Itoa(id)
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
