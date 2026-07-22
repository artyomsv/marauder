// Package rutor implements a tracker plugin for rutor.org/info pages.
//
// Rutor is a public no-account-required tracker. The flow is simple:
// fetch the topic page, extract the magnet URI, return it. Updates are
// detected by the magnet's BTIH changing.
//
// **Validation status:** structurally complete; should validate cleanly
// against the live site since no auth is required.
package rutor

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
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

const (
	pluginName    = "rutor"
	displayName   = "Rutor.org"
	defaultDomain = "rutor.org"
	userAgent     = "Marauder/0.4 (+https://marauder.cc)"
)

// urlPattern is host-agnostic; CanParse gates the captured host against the
// known + admin-configured domain allowlist (the SSRF barrier — see
// registry.DomainAllowed).
var urlPattern = regexp.MustCompile(`^https?://(?:www\.)?([^/]+)/torrent/(\d+)`)

var knownDomains = []string{"rutor.org", "rutor.info"}

type plugin struct {
	httpClient *http.Client
	domain     string
}

func init() {
	registry.RegisterTracker(&plugin{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		domain:     defaultDomain,
	})
}

func (p *plugin) Name() string        { return pluginName }
func (p *plugin) DisplayName() string { return displayName }

var _ registry.WithDomains = (*plugin)(nil)

// Domains implements registry.WithDomains; first entry is canonical.
func (p *plugin) Domains() []string { return knownDomains }

// effectiveDomain resolves the domain every request is built against: a
// test-injected p.domain wins (httptest servers), then the admin-configured
// active domain, then the compiled default. Unlike kinozal, rutor's zero
// value (unset p.domain, e.g. a plugin literal built without the field)
// must also fall through to the compiled default rather than resolving to
// an empty host.
func (p *plugin) effectiveDomain() string {
	if p.domain != "" && p.domain != defaultDomain {
		return p.domain
	}
	if d := registry.ActiveDomain(pluginName); d != "" {
		return d
	}
	return defaultDomain
}

func (p *plugin) CanParse(rawURL string) bool {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	return m != nil && registry.DomainAllowed(pluginName, m[1], knownDomains)
}

func (p *plugin) Parse(_ context.Context, rawURL string) (*domain.Topic, error) {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return nil, errors.New("not a rutor torrent URL")
	}
	return &domain.Topic{
		TrackerName: pluginName, URL: rawURL,
		DisplayName: "Rutor torrent " + m[2],
		Extra:       map[string]any{"topic_id": m[2]},
	}, nil
}

var (
	titleRe = regexp.MustCompile(`(?s)<title>([^<]+)</title>`)
	btihRe  = regexp.MustCompile(`magnet:\?xt=urn:btih:([A-Fa-f0-9]+)`)
)

// canonicalURL rewrites rawURL's host to p.effectiveDomain() when that
// differs from the URL's own host — the nnmclub canonicalURL approach
// adapted to rutor. Rutor has no id-based rebuild (Download/Check just
// re-fetch the stored topic URL), so this is the only place an active-
// domain override or mirror switch actually takes effect.
func (p *plugin) canonicalURL(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("rutor: invalid URL: %w", err)
	}
	if eff := p.effectiveDomain(); eff != "" && eff != u.Hostname() {
		u.Host = eff
	}
	return u.String(), nil
}

func (p *plugin) Check(ctx context.Context, topic *domain.Topic, _ *domain.TrackerCredential) (*domain.Check, error) {
	target, err := p.canonicalURL(topic.URL)
	if err != nil {
		return nil, err
	}
	body, err := p.fetch(ctx, target)
	if err != nil {
		return nil, err
	}
	check := &domain.Check{}
	if m := titleRe.FindSubmatch(body); m != nil {
		check.DisplayName = strings.TrimSpace(string(m[1]))
	}
	if m := btihRe.FindSubmatch(body); m != nil {
		check.Hash = strings.ToLower(string(m[1]))
		return check, nil
	}
	return nil, errors.New("rutor: no infohash found")
}

func (p *plugin) Download(ctx context.Context, topic *domain.Topic, _ *domain.Check, _ *domain.TrackerCredential) (*domain.Payload, error) {
	target, err := p.canonicalURL(topic.URL)
	if err != nil {
		return nil, err
	}
	body, err := p.fetch(ctx, target)
	if err != nil {
		return nil, err
	}
	if m := regexp.MustCompile(`(magnet:\?xt=urn:btih:[A-Fa-f0-9]+[^"'&\s]*)`).Find(body); m != nil {
		return &domain.Payload{MagnetURI: string(m)}, nil
	}
	return nil, errors.New("rutor: no magnet link")
}

// --- WithSearch ---------------------------------------------------------

var _ registry.WithSearch = (*plugin)(nil)

var (
	searchRowRe   = regexp.MustCompile(`(?s)<tr[^>]*>(.*?)</tr>`)
	searchLinkRe  = regexp.MustCompile(`(?s)<a href="/torrent/(\d+)[^"]*">(.*?)</a>`)
	searchSizeRe  = regexp.MustCompile(`<td align="right">([^<]+)</td>`)
	searchSeedsRe = regexp.MustCompile(`<span class="green">[^0-9]*(\d+)`)
)

// Search implements registry.WithSearch (issue #129). Rutor's search is
// public; the query is a path segment (page 0, category 0, filter 000,
// sort 0), so it needs url.PathEscape — QueryEscape's `+` for space is
// wrong inside a path.
func (p *plugin) Search(ctx context.Context, query string, _ *domain.TrackerCredential) ([]registry.SearchResult, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	target := fmt.Sprintf("https://%s/search/0/0/000/0/%s", p.effectiveDomain(), url.PathEscape(q))
	body, err := p.fetch(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("rutor search: %w", err)
	}
	var out []registry.SearchResult
	for _, row := range searchRowRe.FindAllSubmatch(body, -1) {
		cell := row[1]
		link := searchLinkRe.FindSubmatch(cell)
		if link == nil {
			continue // header/spacer row without a torrent link
		}
		r := registry.SearchResult{
			Title:   forumcommon.HTMLToText(string(link[2])),
			URL:     fmt.Sprintf("https://%s/torrent/%s", p.effectiveDomain(), link[1]),
			Seeders: -1,
		}
		if m := searchSizeRe.FindSubmatch(cell); m != nil {
			r.Size = forumcommon.HTMLToText(string(m[1]))
		}
		if m := searchSeedsRe.FindSubmatch(cell); m != nil {
			if n, cerr := strconv.Atoi(string(m[1])); cerr == nil {
				r.Seeders = n
			}
		}
		out = append(out, r)
		if len(out) == 50 {
			break
		}
	}
	return out, nil
}

func (p *plugin) fetch(ctx context.Context, target string) ([]byte, error) {
	// SSRF guard: `target` has already been canonicalized above, but fetch
	// is the last line of defense before dialing — refuse any host that
	// isn't a known or admin-configured rutor domain (closes rutor's
	// previously-missing host guard).
	u, err := url.Parse(strings.TrimSpace(target))
	if err != nil {
		return nil, fmt.Errorf("rutor: invalid URL: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("rutor: refusing URL scheme %q", u.Scheme)
	}
	if !registry.DomainAllowed(pluginName, u.Hostname(), knownDomains) {
		return nil, fmt.Errorf("rutor: refusing to fetch off-site host %q", u.Hostname())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("rutor GET %s -> %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}
