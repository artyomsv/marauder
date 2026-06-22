//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type marauderClient struct {
	cfg   config
	token string
	hc    *http.Client
}

func newMarauder(cfg config) *marauderClient {
	return &marauderClient{cfg: cfg, hc: &http.Client{Timeout: 30 * time.Second}}
}

// do performs a JSON request against the Marauder API, attaches the bearer
// token when present, and fails the test on any non-2xx response.
func (m *marauderClient) do(t *testing.T, method, path string, body any) []byte {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal %s %s: %v", method, path, err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, strings.TrimRight(m.cfg.BaseURL, "/")+path, rdr)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if m.token != "" {
		req.Header.Set("Authorization", "Bearer "+m.token)
	}
	resp, err := m.hc.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("%s %s -> HTTP %d: %s", method, path, resp.StatusCode, string(data))
	}
	return data
}

func (m *marauderClient) Login(t *testing.T) {
	t.Helper()
	data := m.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": m.cfg.AdminUser,
		"password": m.cfg.AdminPass,
	})
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode login: %v (%s)", err, string(data))
	}
	if out.AccessToken == "" {
		t.Fatalf("login returned empty access_token: %s", string(data))
	}
	m.token = out.AccessToken
}

// CreateQbitClient creates a qBittorrent client with a known base download_dir
// (so the resulting save path is deterministic) and returns its id.
func (m *marauderClient) CreateQbitClient(t *testing.T, downloadDir string) string {
	t.Helper()
	clientCfg := map[string]string{
		"url":          m.cfg.QbitURL,
		"username":     m.cfg.QbitUser,
		"password":     m.cfg.QbitPassword,
		"download_dir": downloadDir,
	}
	data := m.do(t, http.MethodPost, "/api/v1/clients", map[string]any{
		"client_name":  "qbittorrent",
		"display_name": "E2E qBit",
		"is_default":   true,
		"config":       clientCfg,
	})
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.ID == "" {
		t.Fatalf("decode create-client: %v (%s)", err, string(data))
	}
	return out.ID
}

// CreateTopic adds a topic and returns its id. domain.Topic has no JSON tags,
// so the id field marshals as "ID".
func (m *marauderClient) CreateTopic(t *testing.T, url, clientID, category string) string {
	t.Helper()
	data := m.do(t, http.MethodPost, "/api/v1/topics", map[string]any{
		"url":       url,
		"client_id": clientID,
		"category":  category,
	})
	var out struct {
		ID string `json:"ID"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.ID == "" {
		t.Fatalf("decode create-topic: %v (%s)", err, string(data))
	}
	return out.ID
}

// StatusHasInfohash reports whether the topic's status endpoint lists a
// delivery for infohash (case-insensitive).
func (m *marauderClient) StatusHasInfohash(t *testing.T, topicID, infohash string) bool {
	t.Helper()
	data := m.do(t, http.MethodGet, fmt.Sprintf("/api/v1/topics/%s/status", topicID), nil)
	var out struct {
		Deliveries []struct {
			Infohash string `json:"infohash"`
		} `json:"deliveries"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode status: %v (%s)", err, string(data))
	}
	for _, d := range out.Deliveries {
		if strings.EqualFold(d.Infohash, infohash) {
			return true
		}
	}
	return false
}
