// Package toloka implements a tracker plugin for toloka.to.
//
// Toloka is a Ukrainian phpBB tracker; the flow is the same as RuTracker.
// **Validation status:** structurally complete; needs live validation.
package toloka

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

const (
	pluginName    = "toloka"
	displayName   = "Toloka.to"
	defaultDomain = "toloka.to"
	userAgent     = "Marauder/0.4 (+https://marauder.cc)"
)

// urlPattern is host-agnostic; CanParse gates the captured host against the
// known + admin-configured domain allowlist (the SSRF barrier — see
// registry.DomainAllowed).
var urlPattern = regexp.MustCompile(`^https?://(?:www\.)?([^/]+)/t(\d+)`)

var knownDomains = []string{"toloka.to"}

type plugin struct {
	sessions  *forumcommon.SessionStore
	domain    string
	transport http.RoundTripper
}

func init() {
	registry.RegisterTracker(&plugin{sessions: forumcommon.New(), domain: defaultDomain})
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

// canonicalURL rewrites rawURL's host to p.effectiveDomain() when that
// differs from the URL's own host — the nnmclub/rutor canonicalURL
// approach adapted to toloka. Check/Download re-fetch the stored topic
// URL directly, so this is the only place an active-domain override or
// mirror switch actually takes effect for those fetches.
func (p *plugin) canonicalURL(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("toloka: invalid URL: %w", err)
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
		return nil, errors.New("not a toloka topic URL")
	}
	id, _ := strconv.Atoi(m[2])
	return &domain.Topic{
		TrackerName: pluginName, URL: rawURL,
		DisplayName: fmt.Sprintf("Toloka topic %d", id),
		Extra:       map[string]any{"topic_id": id},
	}, nil
}

func (p *plugin) Login(ctx context.Context, creds *domain.TrackerCredential) error {
	if creds == nil || creds.Username == "" {
		return errors.New("toloka credentials are required")
	}
	sess := p.sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	if p.transport != nil {
		sess.Client.Transport = p.transport
	}
	form := url.Values{
		"username": {creds.Username},
		"password": {string(creds.SecretEnc)},
		"login":    {"submit"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+p.effectiveDomain()+"/login.php", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return fmt.Errorf("toloka login: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("toloka login: read body: %w", err)
	}
	if strings.Contains(string(body), "помилка") || strings.Contains(string(body), "error") {
		return errors.New("toloka login failed")
	}
	sess.LoggedIn = true
	return nil
}

func (p *plugin) Verify(_ context.Context, _ *domain.TrackerCredential) (bool, error) {
	return true, nil
}

var (
	titleRe  = regexp.MustCompile(`(?s)<title>([^<]+)</title>`)
	hashRe   = regexp.MustCompile(`(?i)Info hash[^A-Z0-9]+([A-Fa-f0-9]{40})`)
	dlHrefRe = regexp.MustCompile(`href="(download\.php\?id=\d+)"`)
)

func (p *plugin) Check(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential) (*domain.Check, error) {
	target, err := p.canonicalURL(topic.URL)
	if err != nil {
		return nil, err
	}
	body, err := p.fetch(ctx, target, creds)
	if err != nil {
		return nil, err
	}
	check := &domain.Check{}
	if m := titleRe.FindSubmatch(body); m != nil {
		check.DisplayName = strings.TrimSpace(string(m[1]))
	}
	if m := hashRe.FindSubmatch(body); m != nil {
		check.Hash = strings.ToLower(string(m[1]))
		return check, nil
	}
	return nil, errors.New("toloka: no infohash found")
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
		return nil, errors.New("toloka: no download link")
	}
	dlURL := "https://" + p.effectiveDomain() + "/" + string(m[1])
	torrent, err := p.fetch(ctx, dlURL, creds)
	if err != nil {
		return nil, err
	}
	return &domain.Payload{TorrentFile: torrent, FileName: "toloka.torrent"}, nil
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
		return nil, fmt.Errorf("toloka GET %s -> %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}
