// Package generictorrentfile implements a tracker plugin that treats any
// HTTP(S) URL as a pointer to a .torrent file. It monitors the SHA-1 of the
// file body. Useful for simple static torrent hosts and as a stand-in when
// no tracker-specific plugin exists.
package generictorrentfile

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

type plugin struct {
	httpClient *http.Client
}

func init() {
	registry.RegisterTracker(&plugin{
		httpClient: &http.Client{Timeout: 30 * time.Second},
	})
}

func (p *plugin) Name() string        { return "generictorrentfile" }
func (p *plugin) DisplayName() string { return "Generic .torrent URL" }

func (p *plugin) CanParse(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	// Heuristic: must look like a torrent file path.
	return strings.HasSuffix(strings.ToLower(u.Path), ".torrent")
}

func (p *plugin) Parse(_ context.Context, rawURL string) (*domain.Topic, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	name := path.Base(u.Path)
	return &domain.Topic{
		TrackerName: p.Name(),
		URL:         rawURL,
		DisplayName: strings.TrimSuffix(name, ".torrent"),
		Extra:       map[string]any{},
	}, nil
}

func (p *plugin) Check(ctx context.Context, topic *domain.Topic, _ *domain.TrackerCredential) (*domain.Check, error) {
	body, err := p.fetch(ctx, topic.URL)
	if err != nil {
		return nil, err
	}
	sum := sha1.Sum(body)
	return &domain.Check{
		Hash:        hex.EncodeToString(sum[:]),
		DisplayName: topic.DisplayName,
	}, nil
}

func (p *plugin) Download(ctx context.Context, topic *domain.Topic, _ *domain.Check, _ *domain.TrackerCredential) (*domain.Payload, error) {
	body, err := p.fetch(ctx, topic.URL)
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(topic.URL)
	name := path.Base(u.Path)
	if name == "" {
		name = "download.torrent"
	}
	return &domain.Payload{TorrentFile: body, FileName: name}, nil
}

func (p *plugin) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Marauder/0.1 (+https://marauder.cc)")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	const maxSize = 8 << 20 // 8 MiB cap on torrent file size
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSize))
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, errors.New("empty response body")
	}
	return body, nil
}
