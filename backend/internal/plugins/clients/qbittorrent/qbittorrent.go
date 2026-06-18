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
	URL      string `json:"url"` // e.g. http://qbittorrent:8080
	Username string `json:"username"`
	Password string `json:"password"`
	// DownloadDir is the base save folder. A topic's category nests under it
	// (see registry.EffectiveDownloadDir); a topic's explicit DownloadDir
	// overrides both.
	DownloadDir string `json:"download_dir"`
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

// Compile-time guarantee the plugin reports live status.
var _ registry.WithStatus = (*plugin)(nil)

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
			"url":          map[string]any{"type": "string", "format": "uri", "title": "URL"},
			"username":     map[string]any{"type": "string", "title": "Username"},
			"password":     map[string]any{"type": "string", "title": "Password", "format": "password"},
			"download_dir": map[string]any{"type": "string", "title": "Base download folder (optional)"},
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
	if resp.StatusCode != http.StatusOK {
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
	if dir := registry.EffectiveDownloadDir(cfg.DownloadDir, opts.DownloadDir, opts.Category); dir != "" {
		_ = mw.WriteField("savepath", dir)
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
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(b))
	}
	// qBittorrent's /torrents/add success contract varies by version:
	//   - legacy: 200 with body "Ok." (and "Fails." on a rejected torrent).
	//   - newer:  200 with a JSON summary, e.g.
	//     {"added_torrent_ids":[..],"failure_count":0,"pending_count":0,"success_count":1}
	// Detect failure precisely. A naive substring check for "fail" trips on
	// the JSON field name "failure_count" and turns a success into a false
	// rejection (observed against linuxserver/qbittorrent on the multipart
	// add path).
	b, _ := io.ReadAll(resp.Body)
	respBody := strings.TrimSpace(string(b))
	if strings.HasPrefix(respBody, "{") {
		var summary struct {
			SuccessCount int `json:"success_count"`
			FailureCount int `json:"failure_count"`
			PendingCount int `json:"pending_count"`
		}
		if err := json.Unmarshal([]byte(respBody), &summary); err == nil {
			if summary.FailureCount > 0 || (summary.SuccessCount == 0 && summary.PendingCount == 0) {
				return fmt.Errorf("qbittorrent rejected torrent: %s", respBody)
			}
			return nil
		}
		// Unparseable JSON falls through to the legacy string check below.
	}
	if strings.Contains(strings.ToLower(respBody), "fails") {
		return fmt.Errorf("qbittorrent rejected torrent: %s", respBody)
	}
	return nil
}

// Status implements registry.WithStatus. It queries
// /api/v2/torrents/info filtered to the requested infohashes and maps
// qBittorrent's native state strings onto Marauder's normalised vocabulary.
func (p *plugin) Status(ctx context.Context, rawConfig []byte, hashes []string) ([]registry.TorrentStatus, error) {
	if len(hashes) == 0 {
		return nil, nil
	}
	var cfg Config
	if err := json.Unmarshal(rawConfig, &cfg); err != nil {
		return nil, fmt.Errorf("bad config: %w", err)
	}
	s, err := p.session(ctx, cfg)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(cfg.URL, "/") + "/api/v2/torrents/info?hashes=" +
		url.QueryEscape(strings.Join(hashes, "|"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qbit status: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("qbit status %d: %s", resp.StatusCode, string(b))
	}
	var list []struct {
		Hash     string  `json:"hash"`
		Progress float64 `json:"progress"`
		State    string  `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("decode qbit info: %w", err)
	}
	out := make([]registry.TorrentStatus, 0, len(list))
	for _, it := range list {
		out = append(out, registry.TorrentStatus{
			Hash:        strings.ToLower(it.Hash),
			PercentDone: it.Progress,
			State:       qbitState(it.State),
		})
	}
	return out, nil
}

// qbitState maps a qBittorrent native state string to the normalised
// lifecycle vocabulary. See the WebUI API torrent-management docs for the
// full state list.
func qbitState(state string) string {
	switch state {
	case "error", "missingFiles":
		return registry.StateError
	case "uploading", "stalledUP", "forcedUP":
		return registry.StateSeeding
	case "pausedUP", "pausedDL", "stoppedUP", "stoppedDL":
		return registry.StateStopped
	case "queuedUP", "queuedDL":
		return registry.StateQueued
	case "checkingUP", "checkingDL", "checkingResumeData", "allocating", "moving":
		return registry.StateChecking
	case "downloading", "metaDL", "forcedDL", "stalledDL":
		return registry.StateDownloading
	default:
		return registry.StateUnknown
	}
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
	if !loginSucceeded(resp.StatusCode, body) {
		return nil, fmt.Errorf("login failed: status=%d body=%q", resp.StatusCode, string(body))
	}
	s.loggedIn = true
	s.expiresAt = time.Now().Add(10 * time.Minute)
	p.sessions[cfg.URL] = s
	return s, nil
}

// loginSucceeded reports whether a /api/v2/auth/login response indicates a
// successful authentication. qBittorrent changed its contract across versions:
//   - <=5.1.x: 200 OK with body "Ok." (and "Fails." on bad credentials)
//   - >=5.2.x: 204 No Content with an empty body
//
// Both are accepted; everything else (including a 204 carrying an unexpected
// body) is treated as a failure. See issue #38.
func loginSucceeded(status int, body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	switch status {
	case http.StatusNoContent:
		return trimmed == ""
	case http.StatusOK:
		return strings.EqualFold(trimmed, "ok.")
	default:
		return false
	}
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
