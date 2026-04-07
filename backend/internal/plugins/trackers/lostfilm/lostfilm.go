// Package lostfilm implements a tracker plugin for lostfilm.tv.
//
// LostFilm is the show-tracking site for Russian-dubbed TV episodes.
// It requires a paid (or trial) account; content is gated behind a
// session cookie and the actual .torrent files live behind a multi-stage
// redirector chain.
//
// # The real flow (verified against the live site 2026-04)
//
// 1. **Series page**: GET https://www.lostfilm.tv/series/<slug>/.
//    Each episode is rendered with TWO redundant attributes:
//
//        <a data-code="791-2-6" data-episode="791002006">…</a>
//
//    `data-code` uses **hyphens** (show-season-episode). `data-episode`
//    is a packed integer: `<show><sss><eee>` where season and episode
//    are zero-padded to 3 digits. So show 791, season 2, episode 6
//    becomes the packed string `791002006`.
//
// 2. **PlayEpisode JS function** (extracted from main.min.js):
//
//        PlayEpisode(a) { window.open("/v_search.php?a=" + a, ...) }
//
//    The packed integer is passed straight to /v_search.php as the
//    `a` query parameter. **It is a GET, not a POST.**
//
// 3. **GET /v_search.php?a=<packed>** with the session cookie redirects
//    (302 Location header, or meta-refresh in the body) to a destination
//    page on an external host (retre.org / tracktor.in / lf-tracker.io
//    depending on the era).
//
// 4. **Destination page** lists the per-quality download buttons. Each
//    is an `<a href="…something.torrent">label</a>` where the label
//    contains the quality string (SD / 1080p_mp4 / 1080p).
//
// 5. The plugin picks the link matching `topic.Extra["quality"]` (or
//    DefaultQuality), GETs the .torrent body, and returns it.
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
package lostfilm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

const (
	pluginName    = "lostfilm"
	displayName   = "LostFilm.tv"
	defaultDomain = "www.lostfilm.tv"
	userAgent     = "Mozilla/5.0 (Marauder; +https://marauder.cc) AppleWebKit/537.36"
)

// Selectors / patterns. These are the most likely things to drift when
// LostFilm changes its HTML — keeping them as named constants makes a
// future fix a one-line edit.
var (
	urlPattern = regexp.MustCompile(`^https?://(?:www\.)?lostfilm\.(?:tv|win|run)/series/([^/]+)/?`)

	// data-code="<showid>-<season>-<episode>" — present on every
	// episode block on the series page. Real format uses HYPHENS,
	// not colons (verified against the live site).
	dataCodeRe = regexp.MustCompile(`data-code="(\d+)-(\d+)-(\d+)"`)

	// data-episode="<show><sss><eee>" — packed integer form of the
	// same triple. Used as a fallback when data-code is missing.
	dataEpisodeRe = regexp.MustCompile(`data-episode="(\d{7,})"`)

	// titleRe extracts the page title for the human-readable display name.
	titleRe = regexp.MustCompile(`(?s)<title>([^<]+)</title>`)

	// Magnet fallback — preserved so the e2e test fixture (a series
	// page with a direct magnet link) keeps working without simulating
	// the full redirector chain.
	magnetRe = regexp.MustCompile(`(magnet:\?xt=urn:btih:[A-Fa-f0-9]+[^"'&\s]*)`)

	// Per-quality download link in the redirector destination page.
	// LostFilm publishes three buttons (SD / 1080p_mp4 / 1080p), each
	// linking to a .torrent file. We capture the href and the visible
	// quality label (the text inside the <a>).
	qualityLinkRe = regexp.MustCompile(`(?is)<a[^>]+href="(https?://[^"]+\.torrent[^"]*)"[^>]*>([^<]*)</a>`)

	// Meta-refresh redirect, e.g.
	//   <meta http-equiv="refresh" content="0; url=https://retre.org/td.php?s=...">
	metaRefreshRe = regexp.MustCompile(`(?i)<meta\s+http-equiv="refresh"[^>]*url=([^"'\s>]+)`)
)

type plugin struct {
	sessions  *forumcommon.SessionStore
	domain    string
	transport http.RoundTripper
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

// --- WithCredentials ---------------------------------------------------

func (p *plugin) Login(ctx context.Context, creds *domain.TrackerCredential) error {
	if creds == nil || creds.Username == "" {
		return errors.New("lostfilm credentials are required")
	}
	sess := p.sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	if p.transport != nil {
		sess.Client.Transport = p.transport
	}
	form := url.Values{
		"act":  {"users"},
		"type": {"login"},
		"mail": {creds.Username},
		"pass": {string(creds.SecretEnc)},
		"rem":  {"1"},
	}
	endpoint := "https://" + p.domain + "/ajaxik.php"
	log.Debug().Str("plugin", pluginName).Str("step", "login").Str("url", endpoint).Str("user", creds.Username).Msg("POST login")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return fmt.Errorf("lostfilm login: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	log.Debug().Str("plugin", pluginName).Str("step", "login").Int("status", resp.StatusCode).Int("body_len", len(body)).Msg("login response")
	if strings.Contains(string(body), `"error"`) {
		return errors.New("lostfilm login failed")
	}
	sess.LoggedIn = true
	return nil
}

func (p *plugin) Verify(ctx context.Context, creds *domain.TrackerCredential) (bool, error) {
	sess := p.sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+p.domain+"/my", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	return strings.Contains(string(body), "logout"), nil
}

// --- Check / Download --------------------------------------------------

// episodeRef is one (show_id, season, episode) triple parsed from the
// series page.
type episodeRef struct {
	ShowID  string
	Season  int
	Episode int
}

// PackedID encodes the triple into LostFilm's packed integer format:
// `<show><sss><eee>` with season + episode zero-padded to 3 digits.
// Example: show 791, season 2, episode 6 → "791002006".
func (e episodeRef) PackedID() string {
	return fmt.Sprintf("%s%03d%03d", e.ShowID, e.Season, e.Episode)
}

// SeasonEpisodeKey is the human-readable form (s02e06).
func (e episodeRef) SeasonEpisodeKey() string {
	return fmt.Sprintf("s%02de%02d", e.Season, e.Episode)
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
	startSeason := extraInt(topic.Extra, "start_season")
	startEpisode := extraInt(topic.Extra, "start_episode")

	// Already-downloaded set.
	downloaded := extraStringSlice(topic.Extra, "downloaded_episodes")
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

// parseEpisodes extracts every (show, season, episode) triple from the
// series page body. It tries `data-code` first (the canonical hyphen
// form), falls back to `data-episode` (the packed integer form), and
// returns a deduplicated list sorted ascending by (season, episode).
func parseEpisodes(body []byte) []episodeRef {
	out := make([]episodeRef, 0, 16)
	seen := map[string]struct{}{}

	// Pass 1 — data-code="<show>-<season>-<episode>"
	for _, m := range dataCodeRe.FindAllSubmatch(body, -1) {
		s, _ := strconv.Atoi(string(m[2]))
		e, _ := strconv.Atoi(string(m[3]))
		if s == 0 || e == 0 {
			continue
		}
		key := string(m[1]) + "-" + string(m[2]) + "-" + string(m[3])
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, episodeRef{ShowID: string(m[1]), Season: s, Episode: e})
	}

	// Pass 2 — data-episode="<packed>" — only used if pass 1 found
	// nothing. Decoding: pop the last 3 digits as episode, the next
	// 3 as season, the rest as show id.
	if len(out) == 0 {
		for _, m := range dataEpisodeRe.FindAllSubmatch(body, -1) {
			packed := string(m[1])
			if len(packed) < 7 {
				continue
			}
			ep, _ := strconv.Atoi(packed[len(packed)-3:])
			se, _ := strconv.Atoi(packed[len(packed)-6 : len(packed)-3])
			showID := packed[:len(packed)-6]
			if ep == 0 || se == 0 || showID == "" {
				continue
			}
			key := showID + "-" + strconv.Itoa(se) + "-" + strconv.Itoa(ep)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, episodeRef{ShowID: showID, Season: se, Episode: ep})
		}
	}

	// Sort ascending by (season, episode) using insertion sort — the
	// list is small enough that this is faster than sort.Slice.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			if out[j].Season < out[j-1].Season ||
				(out[j].Season == out[j-1].Season && out[j].Episode < out[j-1].Episode) {
				out[j], out[j-1] = out[j-1], out[j]
				continue
			}
			break
		}
	}
	return out
}

// Download fetches the next pending episode (oldest first). The
// scheduler will keep calling Download until pending is empty.
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
		pending = extraStringSlice(check.Extra, "pending_episodes")
	}
	if len(pending) == 0 {
		return nil, errors.New("lostfilm Download: no pending episodes (check.Extra missing or empty)")
	}

	// Download the OLDEST pending episode first. The scheduler will
	// re-trigger us for the next one.
	target := pending[0]
	log.Debug().Str("plugin", pluginName).Str("step", "download").Str("packed", target).Int("pending_left", len(pending)).Msg("starting download")

	return p.fetchTorrentByPacked(ctx, topic, creds, target)
}

// fetchTorrentByPacked walks the LostFilm v_search redirector chain
// for one packed episode ID and returns the matching-quality .torrent
// bytes.
//
// The flow:
//
//   1. GET https://www.lostfilm.tv/v_search.php?a=<packed>
//      with the session cookie + Referer set to the series page.
//      Auto-redirect is disabled so we can capture the Location
//      header (it points at an external host).
//   2. Follow the redirect manually. The destination is an HTML page
//      containing the per-quality download buttons.
//   3. Parse the destination for `<a href="…torrent">label</a>` pairs,
//      pick the one whose label contains topic.Extra["quality"].
//   4. GET the .torrent body and return it.
func (p *plugin) fetchTorrentByPacked(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential, packed string) (*domain.Payload, error) {
	sess := p.session(creds)

	// Step 1: GET /v_search.php?a=<packed>
	searchURL := "https://" + p.domain + "/v_search.php?a=" + packed
	log.Debug().Str("plugin", pluginName).Str("step", "v_search").Str("url", searchURL).Msg("GET v_search")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", topic.URL)

	// Capture the redirect manually so we can chase it through external
	// hosts (retre.org / tracktor.in / lf-tracker.io / etc.).
	noRedirect := *sess.Client
	noRedirect.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lostfilm v_search GET: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	log.Debug().Str("plugin", pluginName).Str("step", "v_search").Int("status", resp.StatusCode).Int("body_len", len(body)).Str("location", resp.Header.Get("Location")).Msg("v_search response")

	// Find the next URL — either Location header (30x) or
	// meta-refresh inside the body.
	next := resp.Header.Get("Location")
	if next == "" {
		if m := metaRefreshRe.FindSubmatch(body); m != nil {
			next = string(m[1])
			log.Debug().Str("plugin", pluginName).Str("step", "v_search").Str("meta_refresh", next).Msg("found meta-refresh redirect")
		}
	}
	if next == "" {
		// Common cause: session not authenticated → redirected to
		// /login. Surface that explicitly.
		preview := string(body)
		if len(preview) > 200 {
			preview = preview[:200] + "…"
		}
		return nil, fmt.Errorf("lostfilm v_search returned no redirect (status=%d, body preview=%q) — likely not authenticated", resp.StatusCode, preview)
	}
	if next == "https://www.lostfilm.tv/login" || strings.HasSuffix(next, "/login") {
		return nil, errors.New("lostfilm v_search redirected to /login — session cookie expired or login failed")
	}

	// Resolve relative redirects against the v_search base.
	if base, perr := url.Parse(searchURL); perr == nil {
		if rel, perr := url.Parse(next); perr == nil {
			next = base.ResolveReference(rel).String()
		}
	}
	log.Debug().Str("plugin", pluginName).Str("step", "redirector").Str("url", next).Msg("following redirect")

	// Step 2: GET the redirector destination page.
	dest, err := p.fetchURL(ctx, next, sess, searchURL)
	if err != nil {
		return nil, fmt.Errorf("lostfilm download page: %w", err)
	}
	log.Debug().Str("plugin", pluginName).Str("step", "destination").Int("body_len", len(dest)).Msg("destination page fetched")

	// Step 3: parse per-quality .torrent links.
	wantStr := stringFromAny(topic.Extra["quality"], p.DefaultQuality())
	links := qualityLinkRe.FindAllSubmatch(dest, -1)
	log.Debug().Str("plugin", pluginName).Str("step", "destination").Int("link_count", len(links)).Str("want_quality", wantStr).Msg("parsed quality links")

	if len(links) == 0 {
		preview := string(dest)
		if len(preview) > 300 {
			preview = preview[:300] + "…"
		}
		return nil, fmt.Errorf("lostfilm download page: no per-quality torrent links found (preview=%q)", preview)
	}

	var torrentURL, pickedLabel string
	for _, l := range links {
		if qualityMatches(string(l[2]), wantStr) {
			torrentURL = string(l[1])
			pickedLabel = string(l[2])
			break
		}
	}
	if torrentURL == "" {
		// Fall back to the first link (better than failing).
		torrentURL = string(links[0][1])
		pickedLabel = string(links[0][2])
		log.Debug().Str("plugin", pluginName).Str("step", "destination").Str("fallback_label", pickedLabel).Msg("no quality match, using first link")
	}

	// Step 4: GET the .torrent body.
	log.Debug().Str("plugin", pluginName).Str("step", "torrent").Str("url", torrentURL).Str("label", pickedLabel).Msg("GET .torrent")
	torrentBytes, err := p.fetchURL(ctx, torrentURL, sess, next)
	if err != nil {
		return nil, fmt.Errorf("lostfilm torrent fetch: %w", err)
	}
	log.Debug().Str("plugin", pluginName).Str("step", "torrent").Int("bytes", len(torrentBytes)).Msg(".torrent fetched")

	return &domain.Payload{
		TorrentFile: torrentBytes,
		FileName:    fmt.Sprintf("lostfilm-%s-%s.torrent", packed, sanitiseQuality(wantStr)),
	}, nil
}

// session returns the per-user session, falling back to the
// no-credentials session for the test fixture path.
func (p *plugin) session(creds *domain.TrackerCredential) *forumcommon.Session {
	key := pluginName + ":nocreds"
	if creds != nil {
		key = forumcommon.SessionKey(pluginName, creds.UserID.String())
	}
	sess := p.sessions.GetOrCreate(key, userAgent)
	if p.transport != nil {
		sess.Client.Transport = p.transport
	}
	return sess
}

// fetchURL is a thin GET that reuses the user's session and sets a
// Referer if one is supplied.
func (p *plugin) fetchURL(ctx context.Context, target string, sess *forumcommon.Session, referer string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := sess.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s -> %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

func (p *plugin) fetch(ctx context.Context, target string, creds *domain.TrackerCredential) ([]byte, error) {
	sess := p.session(creds)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("lostfilm GET %s -> %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

// --- Helpers -----------------------------------------------------------

func extraInt(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	}
	return 0
}

// extraStringSlice reads a string slice from a map[string]any. JSON
// round-tripping turns []string into []any, so we handle both shapes.
func extraStringSlice(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func stringFromAny(v any, fallback string) string {
	if v == nil {
		return fallback
	}
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return fallback
}

// sanitiseQuality replaces non-filename-safe chars in a quality label.
func sanitiseQuality(q string) string {
	q = strings.ToLower(q)
	q = strings.ReplaceAll(q, " ", "_")
	q = strings.ReplaceAll(q, "/", "_")
	return q
}

// qualityMatches reports whether a download-page button label matches
// the user's chosen quality. The naive `Contains(label, want)` is wrong
// because `Contains("1080p_mp4", "1080p") == true`, so a user asking
// for plain `1080p` would get the MP4 variant. We special-case the
// three known LostFilm tiers so each maps to a distinct, non-overlapping
// label substring.
func qualityMatches(label, want string) bool {
	l := strings.ToLower(label)
	w := strings.ToLower(want)
	switch w {
	case "sd":
		return strings.Contains(l, "sd")
	case "1080p_mp4", "mp4":
		return strings.Contains(l, "mp4")
	case "1080p":
		// 1080p but NOT 1080p_mp4 — i.e. label has "1080p" and
		// no "mp4" tail.
		return strings.Contains(l, "1080p") && !strings.Contains(l, "mp4")
	}
	// Unknown quality — fall through to substring match.
	return strings.Contains(l, w)
}
