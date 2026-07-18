package anilibria

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/extra"
	"github.com/artyomsv/marauder/backend/internal/infohash"
)

const (
	aniLibertyProvider = "aniliberty"
	// AniLiberty intentionally separates its public site (aniliberty.top)
	// from the official API host (anilibria.top).
	defaultAniLibertyAPIBase = "https://anilibria.top/api/v1"
)

var trailingEpisodeRange = regexp.MustCompile(
	`(?i)\[(?:(?:E(?:P)?\s*)?[0-9]+(?:\s*[-–—]\s*(?:E(?:P)?\s*)?[0-9]+)?|Episodes?\s+[0-9]+(?:\s*[-–—]\s*[0-9]+)?)\]\s*$`,
)

type aniLibertyRelease struct {
	ID    int64  `json:"id"`
	Alias string `json:"alias"`
	Name  struct {
		Main string `json:"main"`
	} `json:"name"`
}

type aniLibertyTorrent struct {
	Hash      string            `json:"hash"`
	Label     string            `json:"label"`
	Magnet    string            `json:"magnet"`
	UpdatedAt time.Time         `json:"updated_at"`
	Release   aniLibertyRelease `json:"release"`
}

func aniLibertyAlias(rawURL string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "aniliberty.top" && host != "www.aniliberty.top" {
		return "", false
	}
	const prefix = "/anime/releases/release/"
	if !strings.HasPrefix(parsed.EscapedPath(), prefix) {
		return "", false
	}
	escapedAlias := strings.Trim(strings.TrimPrefix(parsed.EscapedPath(), prefix), "/")
	if escapedAlias == "" || strings.Contains(escapedAlias, "/") {
		return "", false
	}
	alias, err := url.PathUnescape(escapedAlias)
	if err != nil || strings.TrimSpace(alias) == "" {
		return "", false
	}
	return alias, true
}

func (p *plugin) checkAniLiberty(ctx context.Context, topic *domain.Topic) (*domain.Check, error) {
	alias := extra.String(topic.Extra, "alias", "")
	if alias == "" {
		return nil, errors.New("aniliberty: missing release alias")
	}
	release, err := p.findAniLibertyRelease(ctx, alias)
	if err != nil {
		return nil, err
	}
	torrents, err := p.fetchAniLibertyTorrents(ctx, release.ID, alias)
	if err != nil {
		return nil, err
	}
	selected, err := selectAniLibertyTorrent(torrents,
		extra.String(topic.Extra, domain.TopicExtraSonarrInfoHash, ""),
		extra.String(topic.Extra, domain.TopicExtraSonarrSourceTitle, ""),
	)
	if err != nil {
		return nil, err
	}
	displayName := strings.TrimSpace(release.Name.Main)
	if displayName == "" {
		displayName = alias
	}
	return &domain.Check{
		Hash:        strings.ToLower(selected.Hash),
		DisplayName: displayName,
		Extra: map[string]any{
			"magnet":      selected.Magnet,
			"label":       selected.Label,
			"variant_key": aniLibertyVariantKey(selected.Label),
		},
	}, nil
}

func (p *plugin) downloadAniLiberty(check *domain.Check) (*domain.Payload, error) {
	if check == nil {
		return nil, errors.New("aniliberty: missing check")
	}
	magnet := extra.String(check.Extra, "magnet", "")
	if !strings.HasPrefix(strings.ToLower(magnet), "magnet:") {
		return nil, errors.New("aniliberty: selected torrent has no magnet")
	}
	return &domain.Payload{MagnetURI: magnet}, nil
}

func (p *plugin) findAniLibertyRelease(ctx context.Context, alias string) (*aniLibertyRelease, error) {
	query := url.Values{}
	query.Set("query", alias)
	query.Set("include", "id,alias,name.main")
	endpoint := strings.TrimRight(p.aniLibertyBase(), "/") + "/app/search/releases?" + query.Encode()
	body, err := p.fetch(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("search aniliberty releases: %w", err)
	}
	releases, err := decodeAniLibertyReleases(body)
	if err != nil {
		return nil, err
	}
	for i := range releases {
		if releases[i].ID > 0 && releases[i].Alias == alias {
			return &releases[i], nil
		}
	}
	return nil, fmt.Errorf("aniliberty: release %q not found", alias)
}

func (p *plugin) fetchAniLibertyTorrents(ctx context.Context, releaseID int64, alias string) ([]aniLibertyTorrent, error) {
	if releaseID <= 0 {
		return nil, errors.New("aniliberty: invalid release id")
	}
	query := url.Values{}
	query.Set("include", "hash,label,magnet,updated_at,release.id,release.alias,release.name.main")
	endpoint := strings.TrimRight(p.aniLibertyBase(), "/") + "/anime/torrents/release/" +
		strconv.FormatInt(releaseID, 10) + "?" + query.Encode()
	body, err := p.fetch(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("fetch aniliberty torrents: %w", err)
	}
	torrents, err := decodeAniLibertyTorrents(body)
	if err != nil {
		return nil, err
	}
	valid := torrents[:0]
	for _, torrent := range torrents {
		hash := strings.ToLower(strings.TrimSpace(torrent.Hash))
		if torrent.Release.ID != releaseID || torrent.Release.Alias != alias ||
			!validInfoHash(hash) || strings.TrimSpace(torrent.Label) == "" ||
			!magnetMatchesInfoHash(torrent.Magnet, hash) ||
			torrent.UpdatedAt.IsZero() {
			continue
		}
		torrent.Hash = hash
		valid = append(valid, torrent)
	}
	if len(valid) == 0 {
		return nil, errors.New("aniliberty: release has no valid torrents")
	}
	return valid, nil
}

func decodeAniLibertyTorrents(body []byte) ([]aniLibertyTorrent, error) {
	return decodeAniLibertyItems[aniLibertyTorrent](body, "torrents")
}

func decodeAniLibertyReleases(body []byte) ([]aniLibertyRelease, error) {
	return decodeAniLibertyItems[aniLibertyRelease](body, "release search")
}

func decodeAniLibertyItems[T any](body []byte, resource string) ([]T, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("decode aniliberty %s: empty response", resource)
	}
	var items []T
	switch trimmed[0] {
	case '[':
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, fmt.Errorf("decode aniliberty %s: %w", resource, err)
		}
	case '{':
		var item T
		if err := json.Unmarshal(trimmed, &item); err != nil {
			return nil, fmt.Errorf("decode aniliberty %s: %w", resource, err)
		}
		items = []T{item}
	default:
		return nil, fmt.Errorf("decode aniliberty %s: unsupported response", resource)
	}
	return items, nil
}

func selectAniLibertyTorrent(torrents []aniLibertyTorrent, initialHash, sourceTitle string) (*aniLibertyTorrent, error) {
	initialHash = strings.ToLower(strings.TrimSpace(initialHash))
	if initialHash != "" {
		for i := range torrents {
			if strings.EqualFold(torrents[i].Hash, initialHash) {
				return &torrents[i], nil
			}
		}
	}

	if sourceTitle != "" {
		wanted := aniLibertyVariantKey(sourceTitleLabel(sourceTitle))
		var matches []aniLibertyTorrent
		for _, torrent := range torrents {
			if aniLibertyVariantKey(torrent.Label) == wanted {
				matches = append(matches, torrent)
			}
		}
		if len(matches) == 0 {
			// Fail closed rather than silently switching codec/quality. The
			// scheduler keeps retrying this topic on backoff, so it recovers if
			// the grabbed variant is published again without downloading a
			// different variant in the meantime.
			return nil, fmt.Errorf("aniliberty: no torrent variant matches Sonarr release %q", wanted)
		}
		torrents = matches
	}

	if len(torrents) == 0 {
		return nil, errors.New("aniliberty: no torrent variants")
	}
	sorted := slices.Clone(torrents)
	slices.SortStableFunc(sorted, func(a, b aniLibertyTorrent) int {
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			if a.UpdatedAt.After(b.UpdatedAt) {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Hash, b.Hash)
	})
	return &sorted[0], nil
}

func sourceTitleLabel(sourceTitle string) string {
	if index := strings.LastIndex(sourceTitle, " / "); index >= 0 {
		return strings.TrimSpace(sourceTitle[index+3:])
	}
	return strings.TrimSpace(sourceTitle)
}

func aniLibertyVariantKey(label string) string {
	return strings.TrimSpace(trailingEpisodeRange.ReplaceAllString(strings.TrimSpace(label), ""))
}

func validInfoHash(hash string) bool {
	if len(hash) != 40 {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}

func magnetMatchesInfoHash(raw, hash string) bool {
	magnet, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(magnet.Scheme, "magnet") {
		return false
	}
	magnetHash, err := infohash.FromMagnet(magnet.String())
	return err == nil && strings.EqualFold(magnetHash, hash)
}

func (p *plugin) aniLibertyBase() string {
	if strings.TrimSpace(p.aniLibertyAPIBase) != "" {
		return p.aniLibertyAPIBase
	}
	return defaultAniLibertyAPIBase
}
