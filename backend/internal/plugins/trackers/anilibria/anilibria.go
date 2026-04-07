// Package anilibria implements a tracker plugin for anilibria.tv.
//
// Anilibria.tv is an open anime-tracker that publishes its release index
// over a JSON API at api.anilibria.tv/v3. We don't need to log in for
// public content. The flow is:
//
//  1. Parse the user-supplied https://anilibria.tv/release/<slug>.html URL
//     and store the slug.
//  2. Check: GET https://api.anilibria.tv/v3/title?code=<slug>&include=torrents
//     and use the highest torrent.id as the hash.
//  3. Download: GET https://api.anilibria.tv/v3/torrent/download?id=<id>
//     which returns a .torrent file.
//
// **Validation status:** structurally complete with httptest fixtures.
// The Anilibria API is public, so this is one of the easiest plugins to
// validate against the live site (just hit the URL with curl).
package anilibria

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

const (
	pluginName  = "anilibria"
	displayName = "Anilibria.tv"
	apiBase     = "https://api.anilibria.tv/v3"
	userAgent   = "Marauder/0.4 (+https://marauder.cc)"
)

var urlPattern = regexp.MustCompile(`^https?://(?:www\.)?anilibria\.tv/release/([^/]+?)\.html`)

type plugin struct {
	httpClient *http.Client
	apiBase    string
}

func init() {
	registry.RegisterTracker(&plugin{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		apiBase:    apiBase,
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
		return nil, errors.New("not an anilibria release URL")
	}
	return &domain.Topic{
		TrackerName: pluginName,
		URL:         rawURL,
		DisplayName: "Anilibria: " + m[1],
		Extra:       map[string]any{"slug": m[1]},
	}, nil
}

type titleResponse struct {
	Names struct {
		Ru string `json:"ru"`
		En string `json:"en"`
	} `json:"names"`
	Torrents struct {
		List []struct {
			ID      int `json:"torrent_id"`
			Quality struct {
				String string `json:"string"`
			} `json:"quality"`
			URL string `json:"url"`
		} `json:"list"`
	} `json:"torrents"`
}

func (p *plugin) Check(ctx context.Context, topic *domain.Topic, _ *domain.TrackerCredential) (*domain.Check, error) {
	slug, _ := topic.Extra["slug"].(string)
	if slug == "" {
		return nil, errors.New("anilibria: missing slug")
	}
	endpoint := p.apiBase + "/title?code=" + slug + "&include=torrents"
	body, err := p.fetch(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var resp titleResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode anilibria title: %w", err)
	}
	if len(resp.Torrents.List) == 0 {
		return nil, errors.New("anilibria: no torrents in title response")
	}
	highest := 0
	for _, t := range resp.Torrents.List {
		if t.ID > highest {
			highest = t.ID
		}
	}
	check := &domain.Check{Hash: "anilibria-" + strconv.Itoa(highest)}
	if resp.Names.Ru != "" {
		check.DisplayName = resp.Names.Ru
	} else if resp.Names.En != "" {
		check.DisplayName = resp.Names.En
	}
	return check, nil
}

func (p *plugin) Download(ctx context.Context, topic *domain.Topic, check *domain.Check, _ *domain.TrackerCredential) (*domain.Payload, error) {
	// Refetch to get the URL of the latest torrent
	slug, _ := topic.Extra["slug"].(string)
	endpoint := p.apiBase + "/title?code=" + slug + "&include=torrents"
	body, err := p.fetch(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var resp titleResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	highestID := 0
	highestURL := ""
	for _, t := range resp.Torrents.List {
		if t.ID > highestID {
			highestID = t.ID
			highestURL = t.URL
		}
	}
	if highestURL == "" {
		return nil, errors.New("anilibria: no torrent URL")
	}
	if !strings.HasPrefix(highestURL, "http") {
		highestURL = "https://anilibria.tv" + highestURL
	}
	torrent, err := p.fetch(ctx, highestURL)
	if err != nil {
		return nil, err
	}
	_ = check // unused — kept for interface symmetry
	return &domain.Payload{
		TorrentFile: torrent,
		FileName:    fmt.Sprintf("anilibria-%s-%d.torrent", slug, highestID),
	}, nil
}

func (p *plugin) fetch(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
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
		return nil, fmt.Errorf("anilibria GET %s -> %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}
