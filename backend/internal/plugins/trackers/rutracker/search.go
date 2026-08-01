package rutracker

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

var _ registry.WithSearch = (*plugin)(nil)

var (
	// Attribute-order-agnostic: live rows are
	// <tr id="trs-tr-N" class="tCenter hl-tr" data-topic_id="N"> — id comes
	// BEFORE class, so anchoring the class to the tag name silently matches
	// nothing (the bug that shipped zero rutracker results on 2026-07-23).
	searchRowRe   = regexp.MustCompile(`(?s)<tr[^>]*class="[^"]*tCenter[^"]*"[^>]*>(.*?)</tr>`)
	searchLinkRe  = regexp.MustCompile(`(?s)<a[^>]*class="[^"]*tLink[^"]*"[^>]*>(.*?)</a>`)
	searchTIDRe   = regexp.MustCompile(`viewtopic\.php\?t=(\d+)`)
	searchSizeRe  = regexp.MustCompile(`(?s)<td[^>]*class="[^"]*tor-size[^"]*"[^>]*>(.*?)</td>`)
	searchSeedsRe = regexp.MustCompile(`<b class="seedmed">\s*(\d+)`)
	searchForumRe = regexp.MustCompile(`(?s)<a[^>]*href="tracker\.php\?f=\d+[^"]*"[^>]*>(.*?)</a>`)
)

// Search implements registry.WithSearch (issue #129). RuTracker's
// tracker.php is login-gated: with no credential (or a dead session the
// handler layer could not revive) it returns ErrSearchRequiresCredentials.
// The nm param must be cp1251-percent-encoded — UTF-8 encoding reads as
// mojibake server-side and returns zero results for Cyrillic queries.
func (p *plugin) Search(ctx context.Context, query string, creds *domain.TrackerCredential) ([]registry.SearchResult, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	if creds == nil {
		return nil, registry.ErrSearchRequiresCredentials
	}
	encoded := forumcommon.EncodeWindows1251Query(q)
	target := fmt.Sprintf("https://%s/forum/tracker.php?nm=%s", p.effectiveDomain(), encoded)
	body, err := p.fetchBytes(ctx, nil, creds, target)
	if err != nil {
		return nil, fmt.Errorf("rutracker search: %w", err)
	}
	page := forumcommon.DecodeWindows1251(string(body))
	if !strings.Contains(page, `id="logged-in-username"`) {
		// tracker.php served the anonymous login shell — session is dead.
		return nil, registry.ErrSearchRequiresCredentials
	}
	rows := searchRowRe.FindAllStringSubmatch(page, -1)
	// A zero-row authenticated page is ambiguous: genuinely no matches, markup
	// that stopped matching the row regex, or a query that was mangled before
	// it got here. Record enough to tell those apart without dumping the page,
	// which carries session-bearing markup.
	//
	// query_mangled is the signal that caught a real bug: a caller handing
	// non-UTF-8 text to the cp1251 encoder gets "?" per rune, so the tracker
	// searches for question marks and answers with a perfectly valid
	// zero-result page. The flag is logged rather than the query itself —
	// search terms are the user's business and do not belong in logs.
	log.Debug().Str("plugin", pluginName).Str("step", "search").
		Int("page_bytes", len(body)).Int("rows", len(rows)).
		Bool("markup_ok", strings.Contains(page, "data-topic_id")).
		Bool("query_mangled", strings.Contains(encoded, "%3F")).
		Msg("search page parsed")
	var out []registry.SearchResult
	for _, row := range rows {
		cell := row[1]
		linkM := searchLinkRe.FindStringSubmatchIndex(cell)
		if linkM == nil {
			continue
		}
		anchor := cell[linkM[0]:linkM[1]]
		tid := searchTIDRe.FindStringSubmatch(anchor)
		if tid == nil {
			continue
		}
		r := registry.SearchResult{
			Title:   forumcommon.HTMLToText(cell[linkM[2]:linkM[3]]),
			URL:     fmt.Sprintf("https://%s/forum/viewtopic.php?t=%s", p.effectiveDomain(), tid[1]),
			Seeders: -1,
		}
		if m := searchSizeRe.FindStringSubmatch(cell); m != nil {
			// Live size cells end with a download-arrow glyph ("1.4 GB ↓").
			r.Size = strings.TrimSpace(strings.TrimSuffix(forumcommon.HTMLToText(m[1]), "↓"))
		}
		if m := searchSeedsRe.FindStringSubmatch(cell); m != nil {
			if n, cerr := strconv.Atoi(m[1]); cerr == nil {
				r.Seeders = n
			}
		}
		if m := searchForumRe.FindStringSubmatch(cell); m != nil {
			r.Category = forumcommon.HTMLToText(m[1])
		}
		out = append(out, r)
		if len(out) == 50 {
			break
		}
	}
	return out, nil
}
