package kinozal

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

// Kinozal's browse.php mixes attribute quoting styles in one page
// (class="nam", class='s', class=bg — captured live 2026-07-23), so every
// cell regex tolerates single, double, or no quotes. The size cell is not
// positional: browse rows carry several class='s' cells (comments count,
// size, date) — the size is the one whose text looks like a size, i.e.
// number + cp1251-decoded unit (КБ/МБ/ГБ/ТБ).
var (
	searchRowRe   = regexp.MustCompile(`(?s)<tr[^>]*>(.*?)</tr>`)
	searchLinkRe  = regexp.MustCompile(`(?s)<td class=['"]?nam['"]?><a href="/details\.php\?id=(\d+)[^"]*"[^>]*>(.*?)</a>`)
	searchSizeRe  = regexp.MustCompile(`<td class=['"]?s['"]?>([\d.,]+\s*[КМГТ]Б)</td>`)
	searchSeedsRe = regexp.MustCompile(`<td class=['"]?sl_s['"]?>(\d+)</td>`)
)

// Search implements registry.WithSearch (issue #129). Kinozal's search is
// anonymous-friendly (browse.php serves results without login — verified
// live on kinozal.me 2026-07-23); creds are accepted only to reuse a warm
// session jar. The query must be cp1251-percent-encoded like RuTracker's.
func (p *plugin) Search(ctx context.Context, query string, creds *domain.TrackerCredential) ([]registry.SearchResult, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	target := fmt.Sprintf("https://%s/browse.php?s=%s",
		p.effectiveDomain(), forumcommon.EncodeWindows1251Query(q))
	body, err := p.fetch(ctx, target, creds)
	if err != nil {
		return nil, fmt.Errorf("kinozal search: %w", err)
	}
	page := forumcommon.DecodeWindows1251(string(body))
	var out []registry.SearchResult
	for _, row := range searchRowRe.FindAllStringSubmatch(page, -1) {
		cell := row[1]
		link := searchLinkRe.FindStringSubmatch(cell)
		if link == nil {
			continue // header/pagination rows
		}
		r := registry.SearchResult{
			Title:   strings.TrimSpace(forumcommon.HTMLToText(link[2])),
			URL:     fmt.Sprintf("https://%s/details.php?id=%s", p.effectiveDomain(), link[1]),
			Seeders: -1,
		}
		if m := searchSizeRe.FindStringSubmatch(cell); m != nil {
			r.Size = strings.TrimSpace(m[1])
		}
		if m := searchSeedsRe.FindStringSubmatch(cell); m != nil {
			if n, cerr := strconv.Atoi(m[1]); cerr == nil {
				r.Seeders = n
			}
		}
		out = append(out, r)
		if len(out) == 50 {
			break
		}
	}
	return out, nil
}
