// Package cfsolver is the in-process client for the Cloudflare solver
// sidecar. Tracker plugins use this when they're configured to bypass
// Cloudflare interstitials.
package cfsolver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Cookie mirrors the JSON shape returned by the sidecar.
type Cookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Secure   bool    `json:"secure"`
	HTTPOnly bool    `json:"http_only"`
	Expires  float64 `json:"expires"`
}

// Solution is the cookies + user-agent that the solver returned.
type Solution struct {
	UserAgent string
	Cookies   []Cookie
}

// CookieHeader formats the solution's cookies into a Cookie: header
// value suitable for an http.Request.
func (s *Solution) CookieHeader() string {
	parts := make([]string, 0, len(s.Cookies))
	for _, c := range s.Cookies {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

// Client is a thin wrapper over the sidecar's HTTP API.
type Client struct {
	URL  string
	HTTP *http.Client
}

// New constructs a Client. URL is the sidecar root, e.g.
// "http://cfsolver:9244". An empty URL produces a Client whose Solve()
// always returns ErrDisabled — useful so callers don't have to special-case.
func New(url string) *Client {
	return &Client{
		URL:  url,
		HTTP: &http.Client{Timeout: 90 * time.Second},
	}
}

// ErrDisabled is returned by Solve() when no sidecar URL is configured.
var ErrDisabled = errors.New("cloudflare solver is disabled")

type solveRequest struct {
	URL            string `json:"url"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	UserAgent      string `json:"user_agent,omitempty"`
}

type solveResponse struct {
	OK        bool     `json:"ok"`
	UserAgent string   `json:"user_agent,omitempty"`
	Cookies   []Cookie `json:"cookies,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// Solve asks the sidecar to drive `targetURL` through any Cloudflare
// challenge and returns the resulting cookies and user-agent.
func (c *Client) Solve(ctx context.Context, targetURL string) (*Solution, error) {
	if c == nil || c.URL == "" {
		return nil, ErrDisabled
	}
	body, _ := json.Marshal(solveRequest{URL: targetURL, TimeoutSeconds: 60})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.URL, "/")+"/solve", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call cfsolver: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("cfsolver status %d", resp.StatusCode)
	}
	respBody, _ := io.ReadAll(resp.Body)
	var sr solveResponse
	if err := json.Unmarshal(respBody, &sr); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if !sr.OK {
		return nil, fmt.Errorf("cfsolver: %s", sr.Error)
	}
	return &Solution{UserAgent: sr.UserAgent, Cookies: sr.Cookies}, nil
}
