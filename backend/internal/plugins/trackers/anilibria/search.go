package anilibria

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

var _ registry.WithSearch = (*plugin)(nil)

// Search implements registry.WithSearch (issue #129). It reuses the same
// AniLiberty v1 endpoint the check path already depends on
// (/app/search/releases — see findAniLibertyRelease), but with the user's
// free-text query instead of an exact alias. Results are release pages in
// the canonical aniliberty.top form aniLibertyAlias/CanParse accept, so
// picking one subscribes to the release. Anonymous API; Seeders is -1 —
// the API reports releases, not swarm state.
func (p *plugin) Search(ctx context.Context, query string, _ *domain.TrackerCredential) ([]registry.SearchResult, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	qv := url.Values{}
	qv.Set("query", q)
	qv.Set("include", "id,alias,name.main")
	endpoint := strings.TrimRight(p.aniLibertyBase(), "/") + "/app/search/releases?" + qv.Encode()
	body, err := p.fetch(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("anilibria search: %w", err)
	}
	releases, err := decodeAniLibertyReleases(body)
	if err != nil {
		return nil, fmt.Errorf("anilibria search: %w", err)
	}
	var out []registry.SearchResult
	for _, rel := range releases {
		alias := strings.TrimSpace(rel.Alias)
		if rel.ID <= 0 || alias == "" {
			continue
		}
		title := strings.TrimSpace(rel.Name.Main)
		if title == "" {
			title = alias
		}
		out = append(out, registry.SearchResult{
			Title:    title,
			URL:      "https://aniliberty.top/anime/releases/release/" + url.PathEscape(alias) + "/",
			Seeders:  -1,
			Category: "Anime",
		})
		if len(out) == 50 {
			break
		}
	}
	return out, nil
}
