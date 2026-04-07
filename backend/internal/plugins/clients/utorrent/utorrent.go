// Package utorrent implements the µTorrent web API client plugin.
//
// µTorrent's WebUI exposes a token endpoint at /gui/token.html and a
// command endpoint at /gui/?token=...&action=add-url&s=<magnet>. The flow
// is:
//
//  1. GET /gui/token.html with HTTP basic auth.
//     The token is the only text content of a <div id="token">.
//  2. GET /gui/?token=...&list=1 to verify.
//  3. GET /gui/?token=...&action=add-url&s=<magnet> to add a magnet, or
//     POST /gui/?token=...&action=add-file to add a .torrent file
//     (multipart form).
//
// **Validation status:** structurally complete; needs validation against
// a real µTorrent install. The host machine doesn't run a uTorrent
// docker image easily — uTorrent has not had an official Docker release
// in years.
package utorrent

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
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// Config is the user-supplied config for a µTorrent client.
type Config struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type plugin struct {
	mu       sync.Mutex
	sessions map[string]*session
}

type session struct {
	client    *http.Client
	token     string
	expiresAt time.Time
}

func init() {
	registry.RegisterClient(&plugin{sessions: map[string]*session{}})
}

func (p *plugin) Name() string        { return "utorrent" }
func (p *plugin) DisplayName() string { return "µTorrent" }

func (p *plugin) ConfigSchema() map[string]any {
	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"properties": map[string]any{
			"url":      map[string]any{"type": "string", "title": "WebUI URL", "format": "uri"},
			"username": map[string]any{"type": "string", "title": "Username"},
			"password": map[string]any{"type": "string", "title": "Password", "format": "password"},
		},
		"required": []string{"url", "username", "password"},
	}
}

func (p *plugin) Test(ctx context.Context, raw []byte) error {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return fmt.Errorf("bad config: %w", err)
	}
	if c.URL == "" {
		return errors.New("url is required")
	}
	_, err := p.session(ctx, c)
	return err
}

func (p *plugin) Add(ctx context.Context, raw []byte, payload *domain.Payload, opts domain.AddOptions) error {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return fmt.Errorf("bad config: %w", err)
	}
	s, err := p.session(ctx, c)
	if err != nil {
		return err
	}
	switch {
	case payload.MagnetURI != "":
		q := url.Values{
			"token":  {s.token},
			"action": {"add-url"},
			"s":      {payload.MagnetURI},
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
			strings.TrimRight(c.URL, "/")+"/gui/?"+q.Encode(), nil)
		req.SetBasicAuth(c.Username, c.Password)
		resp, err := s.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("utorrent add-url status %d", resp.StatusCode)
		}
		return nil
	case len(payload.TorrentFile) > 0:
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, err := mw.CreateFormFile("torrent_file", nonEmpty(payload.FileName, "marauder.torrent"))
		if err != nil {
			return err
		}
		_, _ = io.Copy(fw, bytes.NewReader(payload.TorrentFile))
		_ = mw.Close()
		q := url.Values{"token": {s.token}, "action": {"add-file"}}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
			strings.TrimRight(c.URL, "/")+"/gui/?"+q.Encode(), &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.SetBasicAuth(c.Username, c.Password)
		resp, err := s.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("utorrent add-file status %d", resp.StatusCode)
		}
		_ = opts // utorrent has no per-add download dir override
		return nil
	default:
		return errors.New("empty payload")
	}
}

var tokenRe = regexp.MustCompile(`<div id=['"]token['"][^>]*>([^<]+)</div>`)

func (p *plugin) session(ctx context.Context, c Config) (*session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.sessions[c.URL]; ok && time.Now().Before(s.expiresAt) {
		return s, nil
	}
	jar, _ := cookiejar.New(nil)
	s := &session{client: &http.Client{Jar: jar, Timeout: 15 * time.Second}}

	tokenURL := strings.TrimRight(c.URL, "/") + "/gui/token.html"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	req.SetBasicAuth(c.Username, c.Password)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("utorrent token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return nil, errors.New("utorrent auth failed")
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	m := tokenRe.FindSubmatch(body)
	if m == nil {
		return nil, errors.New("utorrent: token not found in /gui/token.html")
	}
	s.token = string(m[1])
	s.expiresAt = time.Now().Add(15 * time.Minute)
	p.sessions[c.URL] = s
	return s, nil
}

func nonEmpty(s, fb string) string {
	if s == "" {
		return fb
	}
	return s
}
