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

	// titleSuffixRe strips the trailing site suffix LostFilm appends to the
	// <title>, e.g. "Monarch (сериал) - LostFilm.TV" or
	// "The Boys :: LostFilm.tv". Matches " - LostFilm…" or " :: LostFilm…".
	titleSuffixRe = regexp.MustCompile(`(?i)\s*(?:-|::)\s*LostFilm.*$`)
)

// cleanSeriesTitle trims the raw <title> match and strips the LostFilm site
// suffix so only the show name remains.
func cleanSeriesTitle(raw string) string {
	t := strings.TrimSpace(raw)
	t = titleSuffixRe.ReplaceAllString(t, "")
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
	body, err := p.fetch(ctx, rawURL, creds)
	if err != nil {
		return nil, fmt.Errorf("resolve metadata: %w", err)
	}
	meta := &registry.Metadata{}
	if m := titleRe.FindSubmatch(body); m != nil {
		meta.Title = cleanSeriesTitle(string(m[1]))
	}
	if m := ogImageRe.FindSubmatch(body); m != nil {
		meta.ImageURL = p.absoluteImageURL(string(m[1]))
	} else if m := mainPosterImgRe.FindSubmatch(body); m != nil {
		meta.ImageURL = p.absoluteImageURL(string(m[1]))
	}
	return meta, nil
}
