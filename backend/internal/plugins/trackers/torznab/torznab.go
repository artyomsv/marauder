// Package torznab implements a Marauder tracker plugin that talks to
// any Torznab indexer (Jackett, Prowlarr, NZBHydra2, or a direct
// Torznab indexer like a Sonarr/Radarr-managed source).
//
// # The model
//
// Torznab is the Newznab-derived API that the *arr stack uses to talk
// to torrent indexers. It is search-based: you GET an HTTPS URL with
// `?t=search&apikey=...&q=...` and the response is an RSS XML feed
// listing matching releases newest-first.
//
// Marauder's tracker abstraction is "watch a URL, detect updates by
// hash". The two map cleanly:
//
//   - The Marauder topic URL IS the indexer search URL
//   - The hash is the GUID of the newest item in the feed
//   - When a new release lands at the top of the feed, the GUID
//     changes, Marauder treats it as an update, and downloads the
//     enclosure (a magnet URI for torrent indexers) to the user's
//     configured client.
//
// One Marauder topic per show or movie you want to follow. Same UX as
// Sonarr's wanted list, no separate database.
//
// # The URL prefix
//
// To make CanParse unambiguous and never collide with a forum-tracker
// plugin, Marauder uses an explicit `torznab+https://...` prefix:
//
//	torznab+https://prowlarr.example.com/.../api?apikey=K&t=search&q=Some+Show
//
// The plugin strips the `torznab+` prefix when making the actual
// HTTP request.
//
// # Validation status
//
// E2E tested against a fixture-driven httptest server using the
// canonical Torznab feed shape. Live validation against Jackett or
// Prowlarr is straightforward — see `docs/torznab-newznab.md`.
package torznab

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/torznabcommon"
)

const (
	pluginName  = "torznab"
	displayName = "Torznab indexer"
	urlPrefix   = "torznab+"
	userAgent   = "Marauder/1.1 (+https://marauder.cc)"
)

// Plugin is exported so e2e tests can construct fresh instances.
type Plugin struct {
	HTTP *http.Client
}

// New constructs a Plugin with an optional custom http.Client.
// Pass nil to use a sensible default.
func New(client *http.Client) *Plugin {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Plugin{HTTP: client}
}

func init() {
	registry.RegisterTracker(New(nil))
}

func (p *Plugin) Name() string        { return pluginName }
func (p *Plugin) DisplayName() string { return displayName }

// CanParse accepts only URLs that begin with "torznab+http://" or
// "torznab+https://". The strict prefix prevents accidental matches
// against forum URLs that another plugin would handle.
func (p *Plugin) CanParse(rawURL string) bool {
	s := strings.TrimSpace(rawURL)
	return strings.HasPrefix(s, urlPrefix+"http://") || strings.HasPrefix(s, urlPrefix+"https://")
}

// Parse extracts the underlying indexer URL.
func (p *Plugin) Parse(_ context.Context, rawURL string) (*domain.Topic, error) {
	if !p.CanParse(rawURL) {
		return nil, errors.New("not a torznab+ URL")
	}
	s := strings.TrimSpace(rawURL)
	indexerURL := strings.TrimPrefix(s, urlPrefix)
	display := deriveDisplayName(indexerURL)
	return &domain.Topic{
		TrackerName: pluginName,
		URL:         s,
		DisplayName: display,
		Extra: map[string]any{
			"indexer_url": indexerURL,
		},
	}, nil
}

// Check fetches the indexer feed and uses the newest item's GUID
// (or InfoHash, if available) as the hash.
func (p *Plugin) Check(ctx context.Context, topic *domain.Topic, _ *domain.TrackerCredential) (*domain.Check, error) {
	indexerURL := indexerURLFor(topic)
	if indexerURL == "" {
		return nil, errors.New("torznab: missing indexer_url in topic.Extra")
	}
	items, err := p.fetchFeed(ctx, indexerURL)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, errors.New("torznab: indexer returned no items for the search")
	}
	first := items[0]
	hash := first.InfoHash()
	if hash == "" {
		hash = first.GUID
	}
	if hash == "" {
		return nil, errors.New("torznab: first item has no infohash and no GUID")
	}
	return &domain.Check{
		Hash:        hash,
		DisplayName: first.Title,
		Extra: map[string]any{
			"enclosure":      first.Enclosure,
			"enclosure_type": first.EnclosureType,
			"guid":           first.GUID,
			"seeders":        first.Seeders(),
		},
	}, nil
}

// Download fetches the enclosure URL of the newest item and returns
// either a magnet URI (for `magnet:?` enclosures, the common case
// for direct Torznab indexers) or the raw `.torrent` bytes.
func (p *Plugin) Download(ctx context.Context, topic *domain.Topic, _ *domain.Check, _ *domain.TrackerCredential) (*domain.Payload, error) {
	indexerURL := indexerURLFor(topic)
	if indexerURL == "" {
		return nil, errors.New("torznab: missing indexer_url in topic.Extra")
	}
	items, err := p.fetchFeed(ctx, indexerURL)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, errors.New("torznab: no items to download")
	}
	first := items[0]
	if strings.HasPrefix(first.Enclosure, "magnet:") {
		return &domain.Payload{MagnetURI: first.Enclosure}, nil
	}
	if first.Enclosure == "" {
		return nil, errors.New("torznab: newest item has no enclosure URL")
	}
	body, err := p.fetchBytes(ctx, first.Enclosure)
	if err != nil {
		return nil, err
	}
	return &domain.Payload{
		TorrentFile: body,
		FileName:    safeFilename(first.Title) + ".torrent",
	}, nil
}

// --- helpers --------------------------------------------------------

func indexerURLFor(topic *domain.Topic) string {
	if topic == nil {
		return ""
	}
	if v, ok := topic.Extra["indexer_url"].(string); ok && v != "" {
		return v
	}
	// Fallback: strip the prefix from topic.URL.
	return strings.TrimPrefix(strings.TrimSpace(topic.URL), urlPrefix)
}

func (p *Plugin) fetchFeed(ctx context.Context, indexerURL string) ([]torznabcommon.Item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexerURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/rss+xml, text/xml, application/xml")
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("torznab GET: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("torznab GET %s -> %d", indexerURL, resp.StatusCode)
	}
	return torznabcommon.Parse(resp.Body)
}

func (p *Plugin) fetchBytes(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("torznab download %s -> %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// deriveDisplayName extracts a friendly label from the search query
// of the indexer URL ("?q=Some Show") if present, otherwise falls
// back to the host.
func deriveDisplayName(indexerURL string) string {
	const qMarker = "q="
	if i := strings.Index(indexerURL, qMarker); i >= 0 {
		q := indexerURL[i+len(qMarker):]
		if amp := strings.Index(q, "&"); amp >= 0 {
			q = q[:amp]
		}
		q = strings.ReplaceAll(q, "+", " ")
		q = strings.ReplaceAll(q, "%20", " ")
		if q != "" {
			return "Torznab: " + q
		}
	}
	if i := strings.Index(indexerURL, "://"); i >= 0 {
		host := indexerURL[i+3:]
		if slash := strings.Index(host, "/"); slash >= 0 {
			host = host[:slash]
		}
		return "Torznab indexer @ " + host
	}
	return "Torznab indexer"
}

// safeFilename trims a release title down to something safe to use as
// a file name suffix.
func safeFilename(s string) string {
	out := strings.Builder{}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			out.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			out.WriteRune(r)
		case r >= '0' && r <= '9':
			out.WriteRune(r)
		case r == '.' || r == '-' || r == '_':
			out.WriteRune(r)
		case r == ' ':
			out.WriteRune('.')
		}
	}
	name := out.String()
	if name == "" {
		return "torznab"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	return name
}
