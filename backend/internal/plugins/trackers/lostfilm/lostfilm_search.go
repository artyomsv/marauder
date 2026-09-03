package lostfilm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

var _ registry.WithSearch = (*plugin)(nil)

// searchSeriesEntry is one series row in the /ajaxik.php search response.
// The endpoint also returns a "names" (actors) list which is irrelevant
// here — only data.series entries become results.
type searchSeriesEntry struct {
	Title     string `json:"title"`
	TitleOrig string `json:"title_orig"`
	Link      string `json:"link"` // "/series/<slug>"
}

// Search implements registry.WithSearch (issue #129). LostFilm's search is
// the public JSON endpoint the site's own search box uses
// (GET /ajaxik.php?act=common&type=search&val=<q> — verified live
// 2026-07-23); no login needed, creds are accepted only to reuse a warm
// session jar. Results are series pages, not individual releases — the
// emitted URL is the canonical /series/<slug>/ form the plugin's CanParse
// accepts, so picking one subscribes to the series like pasting its URL
// would. Seeders is always -1: a series subscription has no swarm count.
func (p *plugin) Search(ctx context.Context, query string, creds *domain.TrackerCredential) ([]registry.SearchResult, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	target := "https://" + p.effectiveDomain() + "/ajaxik.php?" + url.Values{
		"act":  {"common"},
		"type": {"search"},
		"val":  {q},
	}.Encode()
	body, err := p.fetch(ctx, target, creds)
	if err != nil {
		return nil, fmt.Errorf("lostfilm search: %w", err)
	}
	// `data` is decoded in two steps because LostFilm changes its SHAPE with
	// the result count: a search that matches returns
	// {"data":{"series":[…]}}, one that matches nothing returns
	// {"data":[],"result":"ok"} — PHP encodes an empty associative array as
	// a JSON array. Decoding straight into the object form made every
	// zero-result query fail with "cannot unmarshal array into Go struct
	// field .data", so the user was told the search broke when in fact
	// nothing matched.
	var resp struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("lostfilm search: decode response: %w", err)
	}
	var payload struct {
		Series []searchSeriesEntry `json:"series"`
	}
	// Only those two shapes are tolerated. Anything else — a bare string, a
	// number, null, a missing key — is the endpoint having changed or errored,
	// and swallowing it would report "nothing matched" for a search that never
	// ran.
	switch trimmed := bytes.TrimSpace(resp.Data); {
	case len(trimmed) > 0 && trimmed[0] == '{': // the matched-results object
		if err := json.Unmarshal(trimmed, &payload); err != nil {
			return nil, fmt.Errorf("lostfilm search: decode response: %w", err)
		}
	case len(trimmed) > 0 && trimmed[0] == '[': // PHP's empty associative array
	default:
		return nil, fmt.Errorf("lostfilm search: unexpected data shape %.20q", trimmed)
	}
	var out []registry.SearchResult
	for _, s := range payload.Series {
		if !strings.HasPrefix(s.Link, "/series/") {
			continue // defensive: never emit a URL CanParse would reject
		}
		title := strings.TrimSpace(s.Title)
		if orig := strings.TrimSpace(s.TitleOrig); orig != "" && orig != title {
			title = title + " / " + orig
		}
		out = append(out, registry.SearchResult{
			Title:    title,
			URL:      "https://" + p.effectiveDomain() + s.Link + "/",
			Seeders:  -1,
			Category: "Series",
		})
		if len(out) == 50 {
			break
		}
	}
	return out, nil
}
