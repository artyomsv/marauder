// Package sonarr integrates Marauder with Sonarr: a typed API client plus a
// background poller that turns Sonarr grab-history records for supported
// forum trackers into auto-created Marauder topics. See issue #86.
package sonarr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Client is a typed, read-only Sonarr v3 API client.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// NewClient builds a Sonarr client. The base URL's trailing slash is trimmed
// once so path joins are unambiguous.
func NewClient(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:  strings.TrimSpace(apiKey),
		http: &http.Client{
			Timeout: timeout,
			// Refuse to follow redirects. The Sonarr URL is admin-supplied and
			// we make server-side requests to it, so following a redirect could
			// pivot the request to another internal host (SSRF). Sonarr's API
			// never redirects, so blocking is safe and a redirecting endpoint
			// fails cleanly as an unexpected status. A private-IP denylist is
			// intentionally NOT applied: Sonarr is normally reached on the
			// internal network (e.g. http://sonarr:8989, a private address by
			// design) and the config endpoint is admin-only.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// SystemStatus is the subset of GET /api/v3/system/status used by the
// connection-test button.
type SystemStatus struct {
	Version  string `json:"version"`
	AppName  string `json:"appName"`
	Instance string `json:"instanceName"`
}

// HistoryRecord is one Sonarr history entry. Date and Data.NzbInfoURL drive
// polling; SourceTitle plus Data.TorrentInfoHash let tracker plugins preserve
// the exact quality/codec variant Sonarr grabbed.
type HistoryRecord struct {
	ID          int         `json:"id"`
	Date        time.Time   `json:"date"`
	EventType   string      `json:"eventType"`
	SourceTitle string      `json:"sourceTitle"`
	Data        HistoryData `json:"data"`
}

// HistoryData is the nested data blob. NzbInfoURL is the tracker topic page
// (e.g. rutracker.org/forum/viewtopic.php?t=N) — the field the integration
// matches. Note: guid is deliberately NOT modelled — for Kinozal it is a
// download.php link, not the topic page.
type HistoryData struct {
	NzbInfoURL      string `json:"nzbInfoUrl"`
	Indexer         string `json:"indexer"`
	TorrentInfoHash string `json:"torrentInfoHash"`
}

// SystemStatus fetches Sonarr's status, validating connectivity + API key.
func (c *Client) SystemStatus(ctx context.Context) (*SystemStatus, error) {
	var out SystemStatus
	if err := c.getJSON(ctx, "/api/v3/system/status", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GrabHistorySince returns grabbed-event history records strictly newer than
// `since`, in chronological (oldest-first) order so the caller's cursor
// advances monotonically.
//
// It uses Sonarr's /history/since endpoint — the purpose-built incremental
// sync API that returns every record on or after `since` in a single
// unpaginated response. This avoids the page-cap pitfall of a descending
// scan: a capped page walk would keep only the newest N records and let the
// cursor advance past the older, never-fetched grabs (silent topic loss),
// whereas /history/since hands back the whole window since the cursor.
func (c *Client) GrabHistorySince(ctx context.Context, since time.Time) ([]HistoryRecord, error) {
	q := url.Values{}
	q.Set("date", since.UTC().Format(time.RFC3339))
	q.Set("eventType", "1") // grabbed

	var records []HistoryRecord
	if err := c.getJSON(ctx, "/api/v3/history/since", q, &records); err != nil {
		return nil, err
	}

	// The endpoint is inclusive of `since`; keep only strictly-newer records so
	// the boundary grab isn't reprocessed on every tick.
	var out []HistoryRecord
	for _, rec := range records {
		if rec.Date.After(since) {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date.Before(out[j].Date) })
	return out, nil
}

func (c *Client) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	u := c.baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("sonarr: GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("sonarr: unauthorized (check API key)")
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return fmt.Errorf("sonarr: GET %s: unexpected status %d", path, resp.StatusCode)
	}

	const maxBody = 16 << 20 // 16 MiB cap on the response body
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("sonarr: read %s: %w", path, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("sonarr: decode %s: %w", path, err)
	}
	return nil
}
