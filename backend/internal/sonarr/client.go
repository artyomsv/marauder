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
	"strconv"
	"strings"
	"time"
)

// historyPageSize is the page size for history fetches.
const historyPageSize = 100

// maxHistoryPages bounds a single poll so an enormous Sonarr history (or a
// far-back cursor) can't fetch unbounded pages in one tick.
const maxHistoryPages = 50

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
		http:    &http.Client{Timeout: timeout},
	}
}

// SystemStatus is the subset of GET /api/v3/system/status used by the
// connection-test button.
type SystemStatus struct {
	Version  string `json:"version"`
	AppName  string `json:"appName"`
	Instance string `json:"instanceName"`
}

// HistoryRecord is one Sonarr history entry. Only Date and Data.NzbInfoURL
// are load-bearing for the integration.
type HistoryRecord struct {
	ID        int         `json:"id"`
	Date      time.Time   `json:"date"`
	EventType string      `json:"eventType"`
	Data      HistoryData `json:"data"`
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

type historyPage struct {
	Page         int             `json:"page"`
	PageSize     int             `json:"pageSize"`
	TotalRecords int             `json:"totalRecords"`
	Records      []HistoryRecord `json:"records"`
}

// SystemStatus fetches Sonarr's status, validating connectivity + API key.
func (c *Client) SystemStatus(ctx context.Context) (*SystemStatus, error) {
	var out SystemStatus
	if err := c.getJSON(ctx, "/api/v3/system/status", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GrabHistorySince returns grabbed-event history records newer than `since`,
// in chronological (oldest-first) order so the caller's cursor advances
// monotonically. Records are fetched newest-first and paging stops as soon
// as a record at or before `since` is seen.
func (c *Client) GrabHistorySince(ctx context.Context, since time.Time) ([]HistoryRecord, error) {
	var collected []HistoryRecord
	for page := 1; page <= maxHistoryPages; page++ {
		q := url.Values{}
		q.Set("page", strconv.Itoa(page))
		q.Set("pageSize", strconv.Itoa(historyPageSize))
		q.Set("eventType", "1") // grabbed
		q.Set("sortKey", "date")
		q.Set("sortDirection", "descending")

		var pg historyPage
		if err := c.getJSON(ctx, "/api/v3/history", q, &pg); err != nil {
			return nil, err
		}

		reachedCursor := false
		for _, rec := range pg.Records {
			if !rec.Date.After(since) {
				reachedCursor = true
				break
			}
			collected = append(collected, rec)
		}
		if reachedCursor || page*historyPageSize >= pg.TotalRecords || len(pg.Records) == 0 {
			break
		}
	}

	// Reverse to chronological order.
	for i, j := 0, len(collected)-1; i < j; i, j = i+1, j-1 {
		collected[i], collected[j] = collected[j], collected[i]
	}
	return collected, nil
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

	const maxBody = 16 << 20 // 16 MiB cap on a history page
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("sonarr: read %s: %w", path, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("sonarr: decode %s: %w", path, err)
	}
	return nil
}
