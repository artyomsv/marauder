// Package deluge implements the Deluge torrent-client plugin.
//
// Deluge has two RPC surfaces:
//
//  1. The "deluged" daemon over a custom TCP RPC protocol on port 58846.
//     We do NOT use this — it requires a TLS handshake and a custom
//     length-prefixed protocol.
//
//  2. The Deluge Web UI ("deluge-web") which exposes a *JSON-RPC* layer
//     at `/json` on port 8112. This is what we target. The flow is:
//     a) POST {"method":"auth.login","params":["password"],"id":1}
//     b) Cookie: _session_id=...   (set by the response)
//     c) POST {"method":"web.connect","params":["<host_id>"],"id":2}
//     d) POST {"method":"core.add_torrent_magnet","params":[uri,opts],"id":3}
//
// Reference: https://deluge.readthedocs.io/en/latest/reference/web.html
package deluge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// Config is the user-supplied config for a Deluge web UI client.
type Config struct {
	URL      string `json:"url"` // e.g. http://deluge:8112
	Password string `json:"password"`
}

type plugin struct {
	mu       sync.Mutex
	sessions map[string]*session
}

type session struct {
	client *http.Client
	idSeq  int
}

func init() {
	registry.RegisterClient(&plugin{sessions: map[string]*session{}})
}

func (p *plugin) Name() string        { return "deluge" }
func (p *plugin) DisplayName() string { return "Deluge" }

func (p *plugin) ConfigSchema() map[string]any {
	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"properties": map[string]any{
			"url":      map[string]any{"type": "string", "title": "Web URL", "format": "uri"},
			"password": map[string]any{"type": "string", "title": "Password", "format": "password"},
		},
		"required": []string{"url", "password"},
	}
}

func (p *plugin) Test(ctx context.Context, rawConfig []byte) error {
	var c Config
	if err := json.Unmarshal(rawConfig, &c); err != nil {
		return fmt.Errorf("bad config: %w", err)
	}
	if c.URL == "" || c.Password == "" {
		return errors.New("url and password are required")
	}
	_, err := p.session(ctx, c)
	return err
}

func (p *plugin) Add(ctx context.Context, rawConfig []byte, payload *domain.Payload, opts domain.AddOptions) error {
	var c Config
	if err := json.Unmarshal(rawConfig, &c); err != nil {
		return fmt.Errorf("bad config: %w", err)
	}
	s, err := p.session(ctx, c)
	if err != nil {
		return err
	}

	dlOpts := map[string]any{}
	if opts.DownloadDir != "" {
		dlOpts["download_location"] = opts.DownloadDir
	}
	if opts.Paused {
		dlOpts["add_paused"] = true
	}

	switch {
	case payload.MagnetURI != "":
		_, err = p.call(ctx, s, c.URL, "core.add_torrent_magnet", []any{payload.MagnetURI, dlOpts})
	case len(payload.TorrentFile) > 0:
		filename := payload.FileName
		if filename == "" {
			filename = "marauder.torrent"
		}
		b64 := base64.StdEncoding.EncodeToString(payload.TorrentFile)
		_, err = p.call(ctx, s, c.URL, "core.add_torrent_file", []any{filename, b64, dlOpts})
	default:
		return errors.New("empty payload")
	}
	return err
}

func (p *plugin) session(ctx context.Context, c Config) (*session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.sessions[c.URL]; ok {
		return s, nil
	}
	jar, _ := cookiejar.New(nil)
	s := &session{client: &http.Client{Jar: jar, Timeout: 15 * time.Second}}

	// auth.login
	resp, err := p.callOnce(ctx, s, c.URL, "auth.login", []any{c.Password})
	if err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}
	loggedIn, _ := resp["result"].(bool)
	if !loggedIn {
		return nil, errors.New("deluge auth.login returned false")
	}

	// web.connected — if not, web.get_hosts + web.connect
	connResp, err := p.callOnce(ctx, s, c.URL, "web.connected", nil)
	if err == nil {
		if connected, _ := connResp["result"].(bool); !connected {
			hostsResp, herr := p.callOnce(ctx, s, c.URL, "web.get_hosts", nil)
			if herr == nil {
				if hosts, ok := hostsResp["result"].([]any); ok && len(hosts) > 0 {
					if firstHost, ok := hosts[0].([]any); ok && len(firstHost) > 0 {
						hostID := firstHost[0]
						_, _ = p.callOnce(ctx, s, c.URL, "web.connect", []any{hostID})
					}
				}
			}
		}
	}

	p.sessions[c.URL] = s
	return s, nil
}

// call is a thin convenience that locks p.mu only briefly to bump idSeq.
func (p *plugin) call(ctx context.Context, s *session, url, method string, params []any) (map[string]any, error) {
	return p.callOnce(ctx, s, url, method, params)
}

func (p *plugin) callOnce(ctx context.Context, s *session, url, method string, params []any) (map[string]any, error) {
	s.idSeq++
	if params == nil {
		params = []any{}
	}
	body, _ := json.Marshal(map[string]any{
		"method": method,
		"params": params,
		"id":     s.idSeq,
	})
	endpoint := strings.TrimRight(url, "/") + "/json"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("deluge status %d", resp.StatusCode)
	}
	respBody, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if eobj, ok := out["error"]; ok && eobj != nil {
		return nil, fmt.Errorf("deluge rpc error: %v", eobj)
	}
	return out, nil
}
