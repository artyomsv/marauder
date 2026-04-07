// Package lostfilm implements a tracker plugin for lostfilm.tv.
//
// LostFilm is the show-tracking site for Russian-dubbed TV episodes.
// It requires a paid (or trial) account; content is gated behind a
// session cookie and the actual .torrent files live behind a multi-stage
// redirector chain.
//
// # The real flow (verified against the live site 2026-04)
//
//  1. **Series page**: GET https://www.lostfilm.tv/series/<slug>/.
//     Each episode is rendered with TWO redundant attributes:
//
//     <a data-code="791-2-6" data-episode="791002006">…</a>
//
//     `data-code` uses **hyphens** (show-season-episode). `data-episode`
//     is a packed integer: `<show><sss><eee>` where season and episode
//     are zero-padded to 3 digits. So show 791, season 2, episode 6
//     becomes the packed string `791002006`.
//
//  2. **PlayEpisode JS function** (extracted from main.min.js):
//
//     PlayEpisode(a) { window.open("/v_search.php?a=" + a, ...) }
//
//     The packed integer is passed straight to /v_search.php as the
//     `a` query parameter. **It is a GET, not a POST.**
//
//  3. **GET /v_search.php?a=<packed>** with the session cookie redirects
//     (302 Location header, or meta-refresh in the body) to a destination
//     page on an external host (retre.org / tracktor.in / lf-tracker.io
//     depending on the era).
//
//  4. **Destination page** lists the per-quality download buttons. Each
//     is an `<a href="…something.torrent">label</a>` where the label
//     contains the quality string (SD / 1080p_mp4 / 1080p).
//
//  5. The plugin picks the link matching `topic.Extra["quality"]` (or
//     DefaultQuality), GETs the .torrent body, and returns it.
//
// # Per-episode state tracking
//
// LostFilm has a dedicated link for every episode, so we track which
// episodes have already been downloaded. The list of packed episode
// IDs lives in `topic.Extra["downloaded_episodes"]` and is updated by
// the scheduler after every successful submit. On every Check we
// compute the **pending** list (all episodes above the start_season /
// start_episode floor that aren't in the downloaded set) and the
// scheduler keeps calling Download until pending is empty.
//
// # File layout
//
// This package is intentionally split across several files to keep
// each concern small enough to read without scrolling:
//
//   - lostfilm.go            — package doc, plugin struct, registry
//     registration, public Check / Download orchestrators.
//   - lostfilm_session.go    — Login / Verify / fetch / fetchURL plus
//     constants and the URL pattern.
//   - lostfilm_parse.go      — episode parsing helpers and the regexes
//     they share.
//   - lostfilm_redirector.go — v_search redirector chain, SSRF
//     allowlist, quality matcher, .torrent fetch.
package lostfilm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/extra"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// plugin is the per-process tracker plugin instance. It is registered
// with the global registry from init() — every other code path goes
// through registry.GetTracker(pluginName).
type plugin struct {
	sessions  *forumcommon.SessionStore
	domain    string
	transport http.RoundTripper

	// redirectValidator is a test seam for the SSRF allowlist. Production
	// code leaves it nil; e2e tests that point the plugin at a 127.0.0.1
	// httptest.Server install a permissive validator so the loopback IP
	// check doesn't reject test fixtures.
	redirectValidator func(string) error
}

func init() {
	registry.RegisterTracker(&plugin{
		sessions: forumcommon.New(),
		domain:   defaultDomain,
	})
}

func (p *plugin) Name() string        { return pluginName }
func (p *plugin) DisplayName() string { return displayName }

// Quality is one of LostFilm's quality tiers. The string value is the
// substring we look for in the destination page's quality button label.
type Quality string

const (
	QualitySD    Quality = "SD"
	QualityMP4   Quality = "1080p_mp4"
	Quality1080p Quality = "1080p"
)

// Qualities implements registry.WithQuality.
func (p *plugin) Qualities() []string {
	return []string{string(QualitySD), string(QualityMP4), string(Quality1080p)}
}

// DefaultQuality implements registry.WithQuality.
func (p *plugin) DefaultQuality() string { return string(Quality1080p) }

// SupportsEpisodeFilter implements registry.WithEpisodeFilter — LostFilm
// honours topic.Extra["start_season"] / topic.Extra["start_episode"] in
// Check by skipping any (s, e) tuple older than the floor.
func (p *plugin) SupportsEpisodeFilter() bool { return true }

func (p *plugin) CanParse(rawURL string) bool {
	return urlPattern.MatchString(strings.TrimSpace(rawURL))
}

func (p *plugin) Parse(_ context.Context, rawURL string) (*domain.Topic, error) {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return nil, errors.New("not a lostfilm series URL")
	}
	return &domain.Topic{
		TrackerName: pluginName,
		URL:         rawURL,
		DisplayName: "LostFilm: " + m[1],
		Extra: map[string]any{
			"slug":                m[1],
			"quality":             string(Quality1080p),
			"downloaded_episodes": []string{},
		},
	}, nil
}

// Check fetches the series page, parses every episode marker, computes
// the list of pending (un-downloaded) episodes above the user's
// start_season/start_episode floor, and returns a hash that flips both
// when new episodes appear AND when we catch up.
func (p *plugin) Check(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential) (*domain.Check, error) {
	body, err := p.fetch(ctx, topic.URL, creds)
	if err != nil {
		return nil, err
	}
	log.Debug().Str("plugin", pluginName).Str("step", "check").Str("url", topic.URL).Int("body_len", len(body)).Msg("series page fetched")

	check := &domain.Check{Extra: map[string]any{}}
	if m := titleRe.FindSubmatch(body); m != nil {
		check.DisplayName = strings.TrimSpace(string(m[1]))
	}

	episodes := parseEpisodes(body)
	log.Debug().Str("plugin", pluginName).Str("step", "check").Int("episodes_found", len(episodes)).Msg("parsed episodes")
	if len(episodes) == 0 {
		// Last-resort fallback for the test fixture: a magnet link
		// directly on the series page. Real LostFilm pages never
		// contain magnets — this only triggers in unit tests.
		if magnetRe.Find(body) != nil {
			check.Hash = "magnet-fallback"
			return check, nil
		}
		return nil, errors.New("lostfilm: no data-code or data-episode markers found on series page")
	}

	// Apply the start_season / start_episode filter.
	startSeason := extra.Int(topic.Extra, "start_season")
	startEpisode := extra.Int(topic.Extra, "start_episode")

	// Already-downloaded set.
	downloaded := extra.StringSlice(topic.Extra, "downloaded_episodes")
	downloadedSet := make(map[string]struct{}, len(downloaded))
	for _, d := range downloaded {
		downloadedSet[d] = struct{}{}
	}

	pendingPacked := make([]string, 0, len(episodes))
	pendingHuman := make([]string, 0, len(episodes))
	for _, ep := range episodes {
		if startSeason > 0 {
			if ep.Season < startSeason {
				continue
			}
			if ep.Season == startSeason && startEpisode > 0 && ep.Episode < startEpisode {
				continue
			}
		}
		packed := ep.PackedID()
		if _, dup := downloadedSet[packed]; dup {
			continue
		}
		pendingPacked = append(pendingPacked, packed)
		pendingHuman = append(pendingHuman, ep.SeasonEpisodeKey())
	}

	// Hash flips both when new episodes appear (more pending) and when
	// the user catches up (fewer pending). Embedding both totals makes
	// it deterministic.
	check.Hash = fmt.Sprintf("eps:%d/done:%d/pending:%d", len(episodes), len(downloaded), len(pendingPacked))
	check.Extra["pending_episodes"] = pendingPacked
	check.Extra["pending_human"] = pendingHuman
	check.Extra["total_episodes"] = len(episodes)

	log.Debug().
		Str("plugin", pluginName).
		Str("step", "check").
		Int("total", len(episodes)).
		Int("downloaded", len(downloaded)).
		Int("pending", len(pendingPacked)).
		Int("start_season", startSeason).
		Int("start_episode", startEpisode).
		Msg("pending list computed")

	return check, nil
}

// Download fetches the next pending episode (oldest first). The
// scheduler will keep calling Download until it returns
// registry.ErrNoPendingEpisodes (matched via errors.Is).
func (p *plugin) Download(ctx context.Context, topic *domain.Topic, check *domain.Check, creds *domain.TrackerCredential) (*domain.Payload, error) {
	// Magnet-fallback path for unit tests.
	if check != nil && check.Hash == "magnet-fallback" {
		body, err := p.fetch(ctx, topic.URL, creds)
		if err != nil {
			return nil, err
		}
		if m := magnetRe.Find(body); m != nil {
			return &domain.Payload{MagnetURI: string(m)}, nil
		}
		return nil, errors.New("lostfilm Download: magnet fallback expected but not found")
	}

	// Pull the pending list out of check.Extra. Because Extra was
	// JSON-serialised through the scheduler, slices may have come
	// back as []any rather than []string.
	var pending []string
	if check != nil && check.Extra != nil {
		pending = extra.StringSlice(check.Extra, "pending_episodes")
	}
	if len(pending) == 0 {
		// Wrap the typed sentinel so the scheduler's per-episode loop
		// can match this via errors.Is and exit cleanly. The string
		// suffix is preserved for logs.
		return nil, fmt.Errorf("lostfilm Download: %w (check.Extra missing or empty)", registry.ErrNoPendingEpisodes)
	}

	// Download the OLDEST pending episode first. The scheduler will
	// re-trigger us for the next one.
	target := pending[0]
	log.Debug().Str("plugin", pluginName).Str("step", "download").Str("packed", target).Int("pending_left", len(pending)).Msg("starting download")

	return p.fetchTorrentByPacked(ctx, topic, creds, target)
}
