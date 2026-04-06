// Package qbittorrent implements the qBittorrent WebUI API v2 client plugin.
//
// Reference: https://github.com/qbittorrent/qBittorrent/wiki/WebUI-API-(qBittorrent-4.1)
//
// The plugin supports both magnet URIs and raw .torrent files via
// /api/v2/torrents/add (multipart form).
package qbittorrent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// Config is the user-provided config for a qBittorrent client.
type Config struct {
	URL      string `json:"url"`       // e.g. http://qbittorrent:8080
	Username string `json:"username"`
	Password string `json:"password"`
	Category string `json:"category"`
}

type plugin struct {
	mu       sync.Mutex
	sessions map[string]*session // keyed by Config.URL
}

type session struct {
	client    *http.Client
	cfg       Config
	loggedIn  bool
	expiresAt time.Time
}

func init() {
	registry.RegisterClient(&plugin{sessions: map[string]*session{}})
}

func (p *plugin) Name() string        { return "qbittorrent" }
func (p *plugin) DisplayName() string { return "qBittorrent" }

func (p *plugin) ConfigSchema() map[string]any {
	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"properties": map[string]any{
			"url":      map[string]any{"type": "string", "format": "uri", "title": "URL"},
			"username": map[string]any{"type": "string", "title": "Username"},
			"password": map[string]any{"type": "string", "title": "Password", "format": "password"},
			"category": map[string]any{"type": "string", "title": "Category (optional)"},
		},
		"required": []string{"url", "username", "password"},
	}
}

func (p *plugin) Test(ctx context.Context, rawConfig []byte) error {
	var cfg Config
	if err := json.Unmarshal(rawConfig, &cfg); err != nil {
		return fmt.Errorf("bad config: %w", err)
	}
	if cfg.URL == "" {
		return errors.New("url is required")
	}
	s, err := p.session(ctx, cfg)
	if err != nil {
		return err
	}
	// Ping /api/v2/app/version
	resp, err := s.client.Get(strings.TrimRight(cfg.URL, "/") + "/api/v2/app/version")
	if err != nil {
		return fmt.Errorf("ping qbit: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("unexpected status %d from version endpoint", resp.StatusCode)
	}
	return nil
}

func (p *plugin) Add(ctx context.Context, rawConfig []byte, payload *domain.Payload, opts domain.AddOptions) error {
	var cfg Config
	if err := json.Unmarshal(rawConfig, &cfg); err != nil {
		return fmt.Errorf("bad config: %w", err)
	}
	s, err := p.session(ctx, cfg)
	if err != nil {
		return err
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	switch {
	case payload.MagnetURI != "":
		_ = mw.WriteField("urls", payload.MagnetURI)
	case len(payload.TorrentFile) > 0:
		fw, err := mw.CreateFormFile("torrents", nonEmpty(payload.FileName, "file.torrent"))
		if err != nil {
			return err
		}
		if _, err := io.Copy(fw, bytes.NewReader(payload.TorrentFile)); err != nil {
			return err
		}
	default:
		return errors.New("empty payload (no magnet and no torrent file)")
	}
	if opts.DownloadDir != "" {
		_ = mw.WriteField("savepath", opts.DownloadDir)
	}
	if cfg.Category != "" {
		_ = mw.WriteField("category", cfg.Category)
	}
	if opts.Paused {
		_ = mw.WriteField("paused", "true")
	}
	_ = mw.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(cfg.URL, "/")+"/api/v2/torrents/add", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("add torrent: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(b))
	}
	// qBittorrent returns 200 "Ok." on success, 200 "Fails." on invalid
	// torrent (yes really). Check the body.
	b, _ := io.ReadAll(resp.Body)
	if strings.Contains(strings.ToLower(string(b)), "fail") {
		return fmt.Errorf("qbittorrent rejected torrent: %s", string(b))
	}
	return nil
}

// session returns a logged-in session, logging in if necessary.
func (p *plugin) session(ctx context.Context, cfg Config) (*session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if s, ok := p.sessions[cfg.URL]; ok && s.loggedIn && time.Now().Before(s.expiresAt) {
		return s, nil
	}

	jar, _ := cookiejar.New(nil)
	s := &session{
		client: &http.Client{Jar: jar, Timeout: 15 * time.Second},
		cfg:    cfg,
	}
	form := url.Values{"username": {cfg.Username}, "password": {cfg.Password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(cfg.URL, "/")+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", cfg.URL)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.EqualFold(strings.TrimSpace(string(body)), "ok.") {
		return nil, fmt.Errorf("login failed: status=%d body=%q", resp.StatusCode, string(body))
	}
	s.loggedIn = true
	s.expiresAt = time.Now().Add(10 * time.Minute)
	p.sessions[cfg.URL] = s
	return s, nil
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
