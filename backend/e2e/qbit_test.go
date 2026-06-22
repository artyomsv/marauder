//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

type qbitClient struct {
	cfg config
	hc  *http.Client
}

func newQbit(t *testing.T, cfg config) *qbitClient {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &qbitClient{cfg: cfg, hc: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
}

// Login authenticates against the qBittorrent WebUI; the SID cookie is stored
// in the client's jar for subsequent calls. qBittorrent answers 200 "Ok."
// (<=5.1) or 204 No Content (>=5.2) on success.
func (q *qbitClient) Login(t *testing.T) {
	t.Helper()
	resp, err := q.hc.PostForm(strings.TrimRight(q.cfg.QbitURL, "/")+"/api/v2/auth/login",
		url.Values{"username": {q.cfg.QbitUser}, "password": {q.cfg.QbitPassword}})
	if err != nil {
		t.Fatalf("qbit login: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("qbit login HTTP %d: %s", resp.StatusCode, string(body))
	}
}

type qbitTorrent struct {
	Category string `json:"category"`
	SavePath string `json:"save_path"`
	State    string `json:"state"`
}

// TorrentInfo returns the qBittorrent record for infohash and whether it was
// found. An unknown hash yields an empty 200 array, i.e. (zero, false).
func (q *qbitClient) TorrentInfo(t *testing.T, infohash string) (qbitTorrent, bool) {
	t.Helper()
	resp, err := q.hc.Get(strings.TrimRight(q.cfg.QbitURL, "/") +
		"/api/v2/torrents/info?hashes=" + infohash)
	if err != nil {
		t.Fatalf("qbit info: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("qbit info HTTP %d: %s", resp.StatusCode, string(data))
	}
	var arr []qbitTorrent
	if err := json.Unmarshal(data, &arr); err != nil {
		t.Fatalf("decode qbit info: %v (%s)", err, string(data))
	}
	if len(arr) == 0 {
		return qbitTorrent{}, false
	}
	return arr[0], true
}
