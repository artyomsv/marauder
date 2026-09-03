// Package rutor implements a tracker plugin for rutor topic pages.
//
// Rutor is a public no-account-required tracker. The flow is simple:
// fetch the topic page, read the release magnet, then upgrade that to the
// real .torrent bytes from the mirror's download host. Updates are detected
// by the magnet's BTIH changing.
//
// # Domains (measured 2026-09-03)
//
// The historical default, rutor.org, is a **stale clone** and is no longer
// a fetch target: its newest front-page release was id 1087871 while the
// live mirrors were already at 1104882 — roughly 17,000 releases behind —
// and it answers current topic ids with `404 Раздача не существует` from
// behind Cloudflare. Its download host d.rutor.org 404s in the same way.
// It survives in parseDomains so topics stored against it still parse and
// are transparently re-pointed by canonicalURL.
//
// rutor.info and new-rutor.org are live and share one id space, so a topic
// URL from either mirror resolves against the other. Only rutor.info serves
// .torrent bytes: d.new-rutor.org presents a certificate that does not cover
// it, and new-rutor.org's own /parse/d.rutor.org/download/<id> link returns
// an HTML page rather than a torrent.
//
// **Validation status:** verified end-to-end against the live site on
// 2026-09-03 with no account — Check, Download (real .torrent),
// ResolveMetadata and Search, on topics served by both live mirrors. The
// page magnet's infohash matched the downloaded .torrent exactly on every
// topic tested. See rutor_live_test.go (build tag `live`).
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

	"github.com/rs/zerolog/log"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/infohash"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

const (
	pluginName  = "rutor"
	displayName = "Rutor"
	// defaultDomain is rutor.info, not the original rutor.org: as of
	// 2026-09-03 rutor.org is a frozen clone that 404s every current topic
	// id (see the package doc), so a fresh install starting there could
	// never reach a release.
	defaultDomain = "rutor.info"
	// torrentHostPrefix is the subdomain a mirror serves .torrent bytes
	// from — https://d.<domain>/download/<id>.
	torrentHostPrefix = "d."
	userAgent         = "Marauder/0.4 (+https://marauder.cc)"
)

// urlPattern is host-agnostic; CanParse gates the captured host against the
// known + admin-configured domain allowlist (the SSRF barrier — see
// registry.DomainAllowed).
var urlPattern = regexp.MustCompile(`^https?://(?:www\.)?([^/]+)/torrent/(\d+)`)

// knownDomains are the hosts Marauder is willing to *fetch from*. Returned by
// Domains(), which feeds the admin's active-domain picker and the automatic
// rotation ring. The stale rutor.org is deliberately absent: listing it would
// let rotation land on a mirror that 404s every current release, and rotation
// never migrates back.
var knownDomains = []string{"rutor.info", "new-rutor.org"}

// parseDomains are the hosts a stored topic URL may legitimately carry. It is
// knownDomains plus the retired rutor.org, and is used by CanParse only —
// never to build a request. rutor.org was the compiled default for the whole
// life of the plugin, so dropping it outright would orphan every topic added
// before 2026-09-03; keeping it here is safe because canonicalURL rewrites
// every request onto effectiveDomain(), and the id space is shared.
var parseDomains = []string{"rutor.info", "new-rutor.org", "rutor.org"}

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
	return m != nil && registry.DomainAllowed(pluginName, m[1], parseDomains)
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
	titleRe      = regexp.MustCompile(`(?s)<title>([^<]+)</title>`)
	btihRe       = regexp.MustCompile(`magnet:\?xt=urn:btih:([A-Fa-f0-9]+)`)
	magnetLinkRe = regexp.MustCompile(`magnet:\?xt=urn:btih:[A-Fa-f0-9]+[^"'&\s]*`)

	// siteTitlePrefixRe strips the mirror's own branding from the page
	// <title> — every mirror renders "rutor.info :: Real Release Name".
	// Anchored at the start and requiring a hostname-shaped token so a
	// release name that legitimately contains " :: " is left intact.
	siteTitlePrefixRe = regexp.MustCompile(`^\s*[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+\s*::\s*`)

	// detailsTableRe isolates the release description table, which is where
	// the poster lives. Anchoring the image search to it keeps the site
	// chrome (logo, magnet/download icons, analytics pixels) out.
	detailsTableRe = regexp.MustCompile(`(?s)<table id="details".*?</table>`)
	imgSrcRe       = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
)

// cleanTitle turns "rutor.info :: Ирек Гильмутдинов - Привет магия!" into
// the release name alone.
func cleanTitle(raw string) string {
	return strings.TrimSpace(siteTitlePrefixRe.ReplaceAllString(strings.TrimSpace(raw), ""))
}

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

// topicID pulls the numeric topic id back out of a topic URL. Download
// re-derives it rather than trusting topic.Extra, which survives a JSON
// round-trip as an untyped value.
func topicID(rawURL string) string {
	if m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL)); m != nil {
		return m[2]
	}
	return ""
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
		check.DisplayName = cleanTitle(string(m[1]))
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
	magnet := string(magnetLinkRe.Find(body))
	if magnet == "" {
		return nil, errors.New("rutor: no magnet link")
	}
	// The magnet is the authority for what this topic *is* — Check derives
	// the topic hash from the same link — so a .torrent is only accepted
	// when it carries that exact infohash.
	want, err := infohash.FromMagnet(magnet)
	if err != nil {
		return nil, fmt.Errorf("rutor: unreadable magnet: %w", err)
	}
	if id := topicID(target); id != "" {
		if file := p.fetchTorrentFile(ctx, id, want); file != nil {
			return &domain.Payload{
				TorrentFile: file,
				FileName:    fmt.Sprintf("rutor-%s.torrent", id),
			}, nil
		}
	}
	// Fail open to the magnet. It carries the same infohash; it just needs
	// DHT/PEX peers to resolve metadata, which the file does not.
	return &domain.Payload{MagnetURI: magnet}, nil
}

// torrentURLs lists the .torrent endpoints to try, in order: the active
// domain's download host first, then the canonical one when it differs.
// The canonical fallback is what makes a topic stored against new-rutor.org
// deliver a real file — that mirror has no usable d.* host of its own.
func (p *plugin) torrentURLs(id string) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range []string{p.effectiveDomain(), knownDomains[0]} {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, "https://"+torrentHostPrefix+d+"/download/"+id)
	}
	return out
}

// fetchTorrentFile returns the .torrent bytes whose infohash is want, or nil
// when no mirror serves a usable file. Every failure is deliberately soft:
// the caller falls back to the magnet, which is always present and carries
// the same hash, so a download-host outage degrades rather than fails.
func (p *plugin) fetchTorrentFile(ctx context.Context, id, want string) []byte {
	for _, target := range p.torrentURLs(id) {
		body, err := p.fetch(ctx, target)
		if err != nil {
			log.Debug().Str("tracker", pluginName).Str("url", target).Err(err).
				Msg("rutor: torrent fetch failed, trying next host")
			continue
		}
		got, ierr := infohash.FromTorrent(body)
		if ierr != nil {
			// A mirror that proxies its download link can answer 200 with an
			// HTML page; new-rutor.org's /parse/… link does exactly that.
			log.Debug().Str("tracker", pluginName).Str("url", target).Err(ierr).
				Msg("rutor: download host did not return a torrent")
			continue
		}
		if got != want {
			log.Debug().Str("tracker", pluginName).Str("url", target).
				Str("got", got).Str("want", want).
				Msg("rutor: torrent infohash does not match the topic magnet")
			continue
		}
		return body
	}
	return nil
}

// --- WithMetadata -------------------------------------------------------

var _ registry.WithMetadata = (*plugin)(nil)

// ResolveMetadata returns the real release name and poster from the public
// topic page. Rutor needs no account, so creds is ignored.
func (p *plugin) ResolveMetadata(ctx context.Context, rawURL string, _ *domain.TrackerCredential) (*registry.Metadata, error) {
	target, err := p.canonicalURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("resolve metadata: %w", err)
	}
	body, err := p.fetch(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("resolve metadata: %w", err)
	}
	meta := &registry.Metadata{}
	if m := titleRe.FindSubmatch(body); m != nil {
		meta.Title = cleanTitle(string(m[1]))
	}
	meta.ImageURL = extractImageURL(body, p.effectiveDomain())
	return meta, nil
}

// extractImageURL returns the release poster: the first image inside the
// description table. Returns "" when the release has no poster — an empty
// string renders as no image rather than as a broken one.
func extractImageURL(body []byte, host string) string {
	details := detailsTableRe.Find(body)
	if details == nil {
		return ""
	}
	m := imgSrcRe.FindSubmatch(details)
	if m == nil {
		return ""
	}
	src := strings.TrimSpace(string(m[1]))
	switch {
	case src == "":
		return ""
	case strings.HasPrefix(src, "//"):
		return "https:" + src
	case strings.HasPrefix(src, "/"):
		return "https://" + host + src
	default:
		return src
	}
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

// fetchHostAllowed is the SSRF barrier for outgoing requests. It accepts a
// known/admin-configured rutor host, or that same host prefixed with the
// download subdomain (d.rutor.info). The prefix is stripped and the base
// re-checked, so d.evil.com is refused exactly like evil.com is.
func fetchHostAllowed(host string) bool {
	if registry.DomainAllowed(pluginName, host, knownDomains) {
		return true
	}
	base, ok := strings.CutPrefix(host, torrentHostPrefix)
	return ok && base != "" && registry.DomainAllowed(pluginName, base, knownDomains)
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
	if !fetchHostAllowed(u.Hostname()) {
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
