package qbittorrent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// Files implements registry.WithFileSelection. qBittorrent answers 404 for a
// torrent it has not finished adding (the add is asynchronous), which is
// reported as an empty list so the caller polls.
func (p *plugin) Files(ctx context.Context, rawConfig []byte, hash string) ([]domain.ClientFile, error) {
	var cfg Config
	if err := json.Unmarshal(rawConfig, &cfg); err != nil {
		return nil, fmt.Errorf("bad config: %w", err)
	}
	s, err := p.session(ctx, cfg)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(cfg.URL, "/")+"/api/v2/torrents/files?hash="+url.QueryEscape(hash), nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qbit files status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var raw []struct {
		Index    *int   `json:"index"`
		Name     string `json:"name"`
		Size     int64  `json:"size"`
		Priority int    `json:"priority"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode files: %w", err)
	}
	out := make([]domain.ClientFile, 0, len(raw))
	for i, f := range raw {
		// index arrived in qBittorrent 4.4; before that the position is the id.
		idx := i
		if f.Index != nil {
			idx = *f.Index
		}
		out = append(out, domain.ClientFile{Index: idx, Path: f.Name, Size: f.Size, Wanted: f.Priority != 0})
	}
	return out, nil
}

// SkipFiles implements registry.WithFileSelection: priority 0 is "do not
// download".
func (p *plugin) SkipFiles(ctx context.Context, rawConfig []byte, hash string, indices []int) error {
	if len(indices) == 0 {
		return nil
	}
	ids := make([]string, len(indices))
	for i, idx := range indices {
		ids[i] = strconv.Itoa(idx)
	}
	return p.postForm(ctx, rawConfig, "/api/v2/torrents/filePrio", url.Values{
		"hash":     {hash},
		"id":       {strings.Join(ids, "|")},
		"priority": {"0"},
	})
}

// Start implements registry.WithFileSelection. qBittorrent 5.0 renamed
// torrents/resume to torrents/start; a 404 from start means an older server.
func (p *plugin) Start(ctx context.Context, rawConfig []byte, hash string) error {
	form := url.Values{"hashes": {hash}}
	err := p.postForm(ctx, rawConfig, "/api/v2/torrents/start", form)
	var statusErr *qbitStatusError
	if errors.As(err, &statusErr) && statusErr.code == http.StatusNotFound {
		return p.postForm(ctx, rawConfig, "/api/v2/torrents/resume", form)
	}
	return err
}

type qbitStatusError struct {
	path string
	code int
	body string
}

func (e *qbitStatusError) Error() string {
	return fmt.Sprintf("qbit %s status %d: %s", e.path, e.code, e.body)
}

func (p *plugin) postForm(ctx context.Context, rawConfig []byte, path string, form url.Values) error {
	var cfg Config
	if err := json.Unmarshal(rawConfig, &cfg); err != nil {
		return fmt.Errorf("bad config: %w", err)
	}
	s, err := p.session(ctx, cfg)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(cfg.URL, "/")+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("qbit %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return &qbitStatusError{path: path, code: resp.StatusCode, body: strings.TrimSpace(string(b))}
	}
	return nil
}
