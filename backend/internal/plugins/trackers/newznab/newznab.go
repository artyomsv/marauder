// Package newznab implements a Marauder tracker plugin that talks to
// any Newznab Usenet indexer (NZBGeek, NZBPlanet, DOGnzb, NZBHydra2,
// or any indexer fronted by NZBHydra2).
//
// # The model
//
// Newznab is the original Usenet indexer protocol — Torznab is its
// torrent-flavoured fork. Both share the same RSS+attr response shape;
// the difference is the enclosure URL points to a `.nzb` file instead
// of a magnet URI.
//
// Marauder doesn't speak Usenet, so it cannot hand the resulting NZB
// off to a torrent client. The intended pipeline is:
//
//  1. User configures a Newznab topic in Marauder pointing at their
//     indexer + a search query (`newznab+https://nzb-indexer/api?...`)
//  2. User configures a `downloadfolder` client pointed at the
//     SABnzbd or NZBGet watch folder
//  3. When a new release matches the search, Marauder downloads the
//     `.nzb` and writes it to the watch folder
//  4. SABnzbd/NZBGet picks up the file and downloads the actual content
//
// This is the same workflow Sonarr/Radarr use, just with Marauder's
// scheduler driving the polling instead of the *arr internal one.
//
// # The URL prefix
//
// `newznab+https://nzbgeek.info/api?apikey=K&t=search&q=Some+Show`
//
// # Validation status
//
// E2E tested against a fixture-driven httptest server using the
// canonical Newznab feed shape. Live validation against a real
// indexer is straightforward — paste your apikey into the URL.
package newznab

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
	pluginName  = "newznab"
	displayName = "Newznab indexer (Usenet)"
	urlPrefix   = "newznab+"
	userAgent   = "Marauder/1.1 (+https://marauder.cc)"
)

// Plugin is exported so e2e tests can construct fresh instances.
type Plugin struct {
	HTTP *http.Client
}

// New constructs a Plugin with an optional custom http.Client.
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

func (p *Plugin) CanParse(rawURL string) bool {
	s := strings.TrimSpace(rawURL)
	return strings.HasPrefix(s, urlPrefix+"http://") || strings.HasPrefix(s, urlPrefix+"https://")
}

func (p *Plugin) Parse(_ context.Context, rawURL string) (*domain.Topic, error) {
	if !p.CanParse(rawURL) {
		return nil, errors.New("not a newznab+ URL")
	}
	s := strings.TrimSpace(rawURL)
	indexerURL := strings.TrimPrefix(s, urlPrefix)
	return &domain.Topic{
		TrackerName: pluginName,
		URL:         s,
		DisplayName: deriveDisplayName(indexerURL),
		Extra: map[string]any{
			"indexer_url": indexerURL,
		},
	}, nil
}

func (p *Plugin) Check(ctx context.Context, topic *domain.Topic, _ *domain.TrackerCredential) (*domain.Check, error) {
	indexerURL := indexerURLFor(topic)
	if indexerURL == "" {
		return nil, errors.New("newznab: missing indexer_url in topic.Extra")
	}
	items, err := p.fetchFeed(ctx, indexerURL)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, errors.New("newznab: indexer returned no items for the search")
	}
	first := items[0]
	hash := first.GUID
	if hash == "" {
		return nil, errors.New("newznab: first item has no GUID")
	}
	return &domain.Check{
		Hash:        hash,
		DisplayName: first.Title,
		Extra: map[string]any{
			"enclosure":      first.Enclosure,
			"enclosure_type": first.EnclosureType,
		},
	}, nil
}

// Download fetches the .nzb bytes and returns them in the Payload's
// TorrentFile field. The downloadfolder client will write them to a
// SABnzbd / NZBGet watch folder unchanged. The qbittorrent client
// will (correctly) reject NZB bytes — pair this plugin with a
// downloadfolder client.
func (p *Plugin) Download(ctx context.Context, topic *domain.Topic, _ *domain.Check, _ *domain.TrackerCredential) (*domain.Payload, error) {
	indexerURL := indexerURLFor(topic)
	if indexerURL == "" {
		return nil, errors.New("newznab: missing indexer_url in topic.Extra")
	}
	items, err := p.fetchFeed(ctx, indexerURL)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, errors.New("newznab: no items to download")
	}
	first := items[0]
	if first.Enclosure == "" {
		return nil, errors.New("newznab: newest item has no enclosure URL")
	}
	body, err := p.fetchBytes(ctx, first.Enclosure)
	if err != nil {
		return nil, err
	}
	return &domain.Payload{
		TorrentFile: body, // NZB bytes; the downloadfolder client writes them as-is
		FileName:    safeFilename(first.Title) + ".nzb",
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
		return nil, fmt.Errorf("newznab GET: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("newznab GET %s -> %d", indexerURL, resp.StatusCode)
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
		return nil, fmt.Errorf("newznab download %s -> %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

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
			return "Newznab: " + q
		}
	}
	if i := strings.Index(indexerURL, "://"); i >= 0 {
		host := indexerURL[i+3:]
		if slash := strings.Index(host, "/"); slash >= 0 {
			host = host[:slash]
		}
		return "Newznab indexer @ " + host
	}
	return "Newznab indexer"
}

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
		return "newznab"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	return name
}
