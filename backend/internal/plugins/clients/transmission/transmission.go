// Package transmission implements the Transmission torrent-client plugin.
//
// Transmission's RPC is JSON over HTTP at `/transmission/rpc`. The protocol
// has a tiny quirk: the first request returns 409 with a
// `X-Transmission-Session-Id` header — that header must be echoed back on
// every subsequent request. We handle that transparently in `do()`.
//
// Reference: https://github.com/transmission/transmission/blob/main/docs/rpc-spec.md
package transmission

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// Config is the persisted configuration for a Transmission client.
type Config struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type plugin struct {
	mu       sync.Mutex
	sessions map[string]string // url -> session id
	client   *http.Client
}

func init() {
	registry.RegisterClient(&plugin{
		sessions: map[string]string{},
		client:   &http.Client{Timeout: 15 * time.Second},
	})
}

func (p *plugin) Name() string        { return "transmission" }
func (p *plugin) DisplayName() string { return "Transmission" }

func (p *plugin) ConfigSchema() map[string]any {
	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"properties": map[string]any{
			"url":      map[string]any{"type": "string", "title": "RPC URL", "format": "uri"},
			"username": map[string]any{"type": "string", "title": "Username (optional)"},
			"password": map[string]any{"type": "string", "title": "Password (optional)", "format": "password"},
		},
		"required": []string{"url"},
	}
}

func (p *plugin) Test(ctx context.Context, rawConfig []byte) error {
	var c Config
	if err := json.Unmarshal(rawConfig, &c); err != nil {
		return fmt.Errorf("bad config: %w", err)
	}
	if c.URL == "" {
		return errors.New("url is required")
	}
	// session-get is the cheapest valid RPC call
	_, err := p.do(ctx, c, "session-get", nil)
	return err
}

func (p *plugin) Add(ctx context.Context, rawConfig []byte, payload *domain.Payload, opts domain.AddOptions) error {
	var c Config
	if err := json.Unmarshal(rawConfig, &c); err != nil {
		return fmt.Errorf("bad config: %w", err)
	}
	args := map[string]any{}
	switch {
	case payload.MagnetURI != "":
		args["filename"] = payload.MagnetURI
	case len(payload.TorrentFile) > 0:
		args["metainfo"] = base64.StdEncoding.EncodeToString(payload.TorrentFile)
	default:
		return errors.New("empty payload")
	}
	if opts.DownloadDir != "" {
		args["download-dir"] = opts.DownloadDir
	}
	if opts.Paused {
		args["paused"] = true
	}

	resp, err := p.do(ctx, c, "torrent-add", args)
	if err != nil {
		return err
	}
	// Transmission returns "result":"success" or an error string
	if result, _ := resp["result"].(string); result != "success" {
		return fmt.Errorf("transmission rejected torrent: %v", result)
	}
	return nil
}

// do performs a single RPC call, transparently handling the
// X-Transmission-Session-Id 409 dance.
func (p *plugin) do(ctx context.Context, c Config, method string, args map[string]any) (map[string]any, error) {
	rpcURL := c.URL
	body := map[string]any{"method": method}
	if args != nil {
		body["arguments"] = args
	}
	bodyBytes, _ := json.Marshal(body)

	makeReq := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if c.Username != "" || c.Password != "" {
			req.SetBasicAuth(c.Username, c.Password)
		}
		p.mu.Lock()
		if id, ok := p.sessions[rpcURL]; ok {
			req.Header.Set("X-Transmission-Session-Id", id)
		}
		p.mu.Unlock()
		return req, nil
	}

	for attempt := 0; attempt < 2; attempt++ {
		req, err := makeReq()
		if err != nil {
			return nil, err
		}
		resp, err := p.client.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode == http.StatusConflict {
			id := resp.Header.Get("X-Transmission-Session-Id")
			resp.Body.Close()
			if id == "" {
				return nil, errors.New("transmission 409 without session id header")
			}
			p.mu.Lock()
			p.sessions[rpcURL] = id
			p.mu.Unlock()
			continue
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusUnauthorized {
			return nil, errors.New("transmission auth failed")
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("transmission status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
		}

		var out map[string]any
		if err := json.Unmarshal(respBody, &out); err != nil {
			return nil, fmt.Errorf("decode rpc response: %w", err)
		}
		return out, nil
	}
	return nil, errors.New("transmission session id retry exhausted")
}
