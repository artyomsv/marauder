package rutracker

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

var _ registry.WithSearch = (*plugin)(nil)

var (
	searchRowRe   = regexp.MustCompile(`(?s)<tr class="[^"]*tCenter[^"]*"[^>]*>(.*?)</tr>`)
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
	target := fmt.Sprintf("https://%s/forum/tracker.php?nm=%s",
		p.effectiveDomain(), forumcommon.EncodeWindows1251Query(q))
	body, err := p.fetchBytes(ctx, nil, creds, target)
	if err != nil {
		return nil, fmt.Errorf("rutracker search: %w", err)
	}
	page := forumcommon.DecodeWindows1251(string(body))
	if !strings.Contains(page, `id="logged-in-username"`) {
		// tracker.php served the anonymous login shell — session is dead.
		return nil, registry.ErrSearchRequiresCredentials
	}
	var out []registry.SearchResult
	for _, row := range searchRowRe.FindAllStringSubmatch(page, -1) {
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
			r.Size = forumcommon.HTMLToText(m[1])
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
