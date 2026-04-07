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
	"regexp"
	"strings"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

const (
	pluginName  = "rutor"
	displayName = "Rutor.org"
	userAgent   = "Marauder/0.4 (+https://marauder.cc)"
)

var urlPattern = regexp.MustCompile(`^https?://(?:www\.)?rutor\.(?:org|info)/torrent/(\d+)`)

type plugin struct {
	httpClient *http.Client
}

func init() {
	registry.RegisterTracker(&plugin{
		httpClient: &http.Client{Timeout: 30 * time.Second},
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
		return nil, errors.New("not a rutor torrent URL")
	}
	return &domain.Topic{
		TrackerName: pluginName, URL: rawURL,
		DisplayName: "Rutor torrent " + m[1],
		Extra:       map[string]any{"topic_id": m[1]},
	}, nil
}

var (
	titleRe = regexp.MustCompile(`(?s)<title>([^<]+)</title>`)
	btihRe  = regexp.MustCompile(`magnet:\?xt=urn:btih:([A-Fa-f0-9]+)`)
)

func (p *plugin) Check(ctx context.Context, topic *domain.Topic, _ *domain.TrackerCredential) (*domain.Check, error) {
	body, err := p.fetch(ctx, topic.URL)
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
	body, err := p.fetch(ctx, topic.URL)
	if err != nil {
		return nil, err
	}
	if m := regexp.MustCompile(`(magnet:\?xt=urn:btih:[A-Fa-f0-9]+[^"'&\s]*)`).Find(body); m != nil {
		return &domain.Payload{MagnetURI: string(m)}, nil
	}
	return nil, errors.New("rutor: no magnet link")
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
		return nil, fmt.Errorf("rutor GET %s -> %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}
