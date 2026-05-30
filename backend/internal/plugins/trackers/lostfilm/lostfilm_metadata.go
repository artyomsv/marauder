package lostfilm

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// Compile-time guarantee that LostFilm resolves topic metadata.
var _ registry.WithMetadata = (*plugin)(nil)

var (
	// ogImageRe matches <meta property="og:image" content="..."> — the most
	// robust poster source. LostFilm renders it on every series page.
	ogImageRe = regexp.MustCompile(`(?i)<meta[^>]+property="og:image"[^>]+content="([^"]+)"`)

	// mainPosterImgRe matches the series poster block:
	// <div class="main_poster"> ... <img ... src="..."> ... </div>.
	// (?s) so the <img> can sit on a following line from the opening div.
	mainPosterImgRe = regexp.MustCompile(`(?is)<div[^>]+class="[^"]*main_poster[^"]*"[^>]*>.*?<img[^>]+src="([^"]+)"`)

	// ogTitleRe matches <meta property="og:title" content="..."> — LostFilm's
	// clean show name, preferred over the SEO-bloated <title> tag.
	ogTitleRe = regexp.MustCompile(`(?i)<meta[^>]+property="og:title"[^>]+content="([^"]+)"`)

	// titleSuffixRe strips the trailing site suffix LostFilm appends, e.g.
	// "Monarch (сериал) - LostFilm.TV", "The Boys :: LostFilm.tv", or with an
	// en-dash "… – LostFilm.TV." Matches " - / – / :: LostFilm…".
	titleSuffixRe = regexp.MustCompile(`(?i)\s*(?:-|–|::)\s*LostFilm.*$`)
)

// cleanSeriesTitle reduces a raw title to just the show name. LostFilm's
// <title> is a full SEO sentence ("<Name>. Сериал <Name> … – LostFilm.TV."),
// so we strip the site suffix and keep only the leading segment before the
// first sentence boundary. An og:title (already just the name) passes through
// unchanged because it has neither.
func cleanSeriesTitle(raw string) string {
	t := strings.TrimSpace(raw)
	t = titleSuffixRe.ReplaceAllString(t, "")
	if i := strings.Index(t, ". "); i > 0 {
		t = t[:i]
	}
	return strings.TrimSpace(t)
}

// absoluteImageURL makes a possibly-relative poster URL absolute against the
// plugin's configured domain. Protocol-relative ("//host/x") and already
// absolute ("https://host/x") URLs are returned unchanged; root-relative
// ("/x") and bare paths get the scheme+host prefix.
func (p *plugin) absoluteImageURL(raw string) string {
	u := strings.TrimSpace(raw)
	if u == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(u, "http://"), strings.HasPrefix(u, "https://"):
		return u
	case strings.HasPrefix(u, "//"):
		return "https:" + u
	case strings.HasPrefix(u, "/"):
		return "https://" + p.domain + u
	default:
		return "https://" + p.domain + "/" + u
	}
}

// ResolveMetadata fetches the series page and extracts the clean show title
// (from <title>, minus the site suffix) and the poster image URL (og:image
// preferred, then the main_poster block). creds may be nil — the public
// series page exposes both. Relative image URLs are made absolute against
// p.domain. The caller treats any error as fail-open.
func (p *plugin) ResolveMetadata(ctx context.Context, rawURL string, creds *domain.TrackerCredential) (*registry.Metadata, error) {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return nil, fmt.Errorf("resolve metadata: not a lostfilm series URL")
	}
	// Rebuild the series URL from the trusted host (p.domain) + the parsed
	// slug rather than fetching the raw user-supplied URL, so a crafted URL
	// cannot redirect the request to an arbitrary host (CodeQL
	// go/request-forgery). Mirrors SeasonCatalog.
	seriesURL := "https://" + p.domain + "/series/" + m[1] + "/"
	body, err := p.fetch(ctx, seriesURL, creds)
	if err != nil {
		return nil, fmt.Errorf("resolve metadata: %w", err)
	}
	meta := &registry.Metadata{}
	// Prefer og:title (already the bare show name); fall back to the bloated
	// <title> tag, which cleanSeriesTitle trims down to the leading segment.
	if m := ogTitleRe.FindSubmatch(body); m != nil {
		meta.Title = cleanSeriesTitle(string(m[1]))
	} else if m := titleRe.FindSubmatch(body); m != nil {
		meta.Title = cleanSeriesTitle(string(m[1]))
	}
	if m := ogImageRe.FindSubmatch(body); m != nil {
		meta.ImageURL = p.absoluteImageURL(string(m[1]))
	} else if m := mainPosterImgRe.FindSubmatch(body); m != nil {
		meta.ImageURL = p.absoluteImageURL(string(m[1]))
	}
	return meta, nil
}
