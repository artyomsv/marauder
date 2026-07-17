package anilibria

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/extra"
)

const (
	aniLibertyProvider       = "aniliberty"
	defaultAniLibertyAPIBase = "https://anilibria.top/api/v1"
	sonarrInfoHashKey        = "sonarr_infohash"
	sonarrSourceTitleKey     = "sonarr_source_title"
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
		extra.String(topic.Extra, sonarrInfoHashKey, ""),
		extra.String(topic.Extra, sonarrSourceTitleKey, ""),
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
	var releases []aniLibertyRelease
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, fmt.Errorf("decode aniliberty release search: %w", err)
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
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, errors.New("decode aniliberty torrents: empty response")
	}
	var torrents []aniLibertyTorrent
	switch trimmed[0] {
	case '[':
		if err := json.Unmarshal(trimmed, &torrents); err != nil {
			return nil, fmt.Errorf("decode aniliberty torrents: %w", err)
		}
	case '{':
		var torrent aniLibertyTorrent
		if err := json.Unmarshal(trimmed, &torrent); err != nil {
			return nil, fmt.Errorf("decode aniliberty torrent: %w", err)
		}
		torrents = []aniLibertyTorrent{torrent}
	default:
		return nil, errors.New("decode aniliberty torrents: unsupported response")
	}
	return torrents, nil
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
			return nil, fmt.Errorf("aniliberty: no torrent variant matches Sonarr release %q", wanted)
		}
		torrents = matches
	}

	if len(torrents) == 0 {
		return nil, errors.New("aniliberty: no torrent variants")
	}
	sort.SliceStable(torrents, func(i, j int) bool {
		if !torrents[i].UpdatedAt.Equal(torrents[j].UpdatedAt) {
			return torrents[i].UpdatedAt.After(torrents[j].UpdatedAt)
		}
		return torrents[i].Hash < torrents[j].Hash
	})
	return &torrents[0], nil
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
	for _, char := range hash {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}

func magnetMatchesInfoHash(raw, hash string) bool {
	magnet, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(magnet.Scheme, "magnet") {
		return false
	}
	for _, exactTopic := range magnet.Query()["xt"] {
		if strings.EqualFold(exactTopic, "urn:btih:"+hash) {
			return true
		}
	}
	return false
}

func (p *plugin) aniLibertyBase() string {
	if strings.TrimSpace(p.aniLibertyAPIBase) != "" {
		return p.aniLibertyAPIBase
	}
	return defaultAniLibertyAPIBase
}
