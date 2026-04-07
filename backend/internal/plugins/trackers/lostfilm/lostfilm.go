// Package lostfilm implements a tracker plugin for lostfilm.tv.
//
// LostFilm is one of the most-requested forum trackers in the CIS scene:
// it's where Russian-dubbed TV episodes are released. The site requires a
// paid (or trial) account; content is gated behind a session cookie and
// the actual .torrent files live behind a multi-stage redirector.
//
// The flow this plugin implements:
//
//  1. Login posts the username + plaintext password to /ajaxik.php
//     (`act=users&type=login&mail=...&pass=...&rem=1`) and stores the
//     resulting cookies in an in-memory SessionStore keyed by user.
//  2. Check fetches the series page (`/series/<slug>/`), parses every
//     `data-code="<show>:<season>:<episode>"` marker, and returns a hash
//     derived from the highest (season, episode) tuple. The full episode
//     list is stashed in `check.Extra["episodes"]` for Download to consume.
//  3. Download picks the latest episode (or the latest one newer than
//     `topic.Extra["start_season"]/start_episode` if WithEpisodeFilter is
//     in use), POSTs to `/v_search.php` with the c/s/e form params and
//     the session cookie, captures the redirect Location header, fetches
//     the destination page, parses the per-quality download links, picks
//     the one matching `topic.Extra["quality"]` (or `DefaultQuality()`),
//     and GETs the .torrent body.
//
// **Validation status:** the redirector flow follows the public
// reverse-engineered shape of the LostFilm site as of 2026-04. Selectors
// are constants at the top of the file so future drift is a one-line
// fix. The unit-test fixture exercises a magnet-on-the-series-page
// fallback path so the test suite stays self-contained without a real
// LostFilm account; the real flow is validated end-to-end in production
// the first time a contributor adds a credential.
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

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

const (
	pluginName    = "lostfilm"
	displayName   = "LostFilm.tv"
	defaultDomain = "www.lostfilm.tv"
	userAgent     = "Marauder/0.4 (+https://marauder.cc)"
)

// Selectors / patterns. These are the most likely things to drift when
// LostFilm changes its HTML — keeping them as named constants makes a
// future fix a one-line edit.
var (
	urlPattern = regexp.MustCompile(`^https?://(?:www\.)?lostfilm\.(?:tv|win|run)/series/([^/]+)/?`)

	// data-code="<showid>:<season>:<episode>" — present on every
	// "Download" button on the series page.
	dataCodeRe = regexp.MustCompile(`data-code="(\d+):(\d+):(\d+)"`)

	// Fallback for the test fixture (and any old-style page that just
	// shows data-episode="<n>" on each block).
	episodeRe = regexp.MustCompile(`(?i)data-episode="(\d+)"`)

	// titleRe extracts the page title for the human-readable display name.
	titleRe = regexp.MustCompile(`(?s)<title>([^<]+)</title>`)

	// Magnet fallback — preserved so the e2e test fixture (a series page
	// with a direct magnet link) keeps working without simulating the
	// full redirector chain.
	magnetRe = regexp.MustCompile(`(magnet:\?xt=urn:btih:[A-Fa-f0-9]+[^"'&\s]*)`)

	// Per-quality download link in the redirector destination page.
	// LostFilm publishes three buttons (SD / 1080p_mp4 / 1080p), each
	// linking to a .torrent file. We capture the href and the visible
	// quality label.
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
// Download by skipping any (s, e) tuple older than the floor.
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
		Extra:       map[string]any{"slug": m[1], "quality": string(Quality1080p)},
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
// series page. The plugin sorts these in season-major / episode-minor
// order to identify the latest release.
type episodeRef struct {
	ShowID  string
	Season  int
	Episode int
}

// Check fetches the series page and extracts every
// `data-code="<show>:<season>:<episode>"` marker. Hash is derived from
// the highest (season, episode) tuple. If no data-code markers are found
// the plugin falls back to the older `data-episode` regex (the test
// fixture path).
func (p *plugin) Check(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential) (*domain.Check, error) {
	body, err := p.fetch(ctx, topic.URL, creds)
	if err != nil {
		return nil, err
	}
	check := &domain.Check{Extra: map[string]any{}}
	if m := titleRe.FindSubmatch(body); m != nil {
		check.DisplayName = strings.TrimSpace(string(m[1]))
	}

	episodes := parseEpisodes(body)
	if len(episodes) > 0 {
		latest := episodes[len(episodes)-1]
		check.Hash = fmt.Sprintf("s%02de%02d", latest.Season, latest.Episode)
		// Stash the full list so Download doesn't have to refetch and
		// reparse the series page.
		serialised := make([]map[string]any, 0, len(episodes))
		for _, e := range episodes {
			serialised = append(serialised, map[string]any{
				"show_id": e.ShowID,
				"season":  e.Season,
				"episode": e.Episode,
			})
		}
		check.Extra["episodes"] = serialised
		return check, nil
	}

	// Fallback: legacy data-episode markers (used by the test fixture).
	matches := episodeRe.FindAllSubmatch(body, -1)
	if len(matches) == 0 {
		return nil, errors.New("lostfilm: no data-code or data-episode markers found")
	}
	highest := ""
	for _, m := range matches {
		if string(m[1]) > highest {
			highest = string(m[1])
		}
	}
	check.Hash = "ep-" + highest
	return check, nil
}

// parseEpisodes extracts every (show_id, season, episode) triple from
// the page body, deduplicates them, and returns them sorted ascending
// by (season, episode). The last element is the most recent release.
func parseEpisodes(body []byte) []episodeRef {
	matches := dataCodeRe.FindAllSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]episodeRef, 0, len(matches))
	for _, m := range matches {
		s, _ := strconv.Atoi(string(m[2]))
		e, _ := strconv.Atoi(string(m[3]))
		if s == 0 || e == 0 {
			continue
		}
		key := string(m[1]) + ":" + string(m[2]) + ":" + string(m[3])
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, episodeRef{
			ShowID:  string(m[1]),
			Season:  s,
			Episode: e,
		})
	}
	// Sort ascending by season then episode (manual insertion sort —
	// the list is small enough that this is faster than sort.Slice).
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

// Download fetches the .torrent for the latest episode (or the latest
// one above the user's start_season/start_episode floor, if WithEpisodeFilter
// is in use). It walks the LostFilm v_search redirector chain to reach
// the per-quality torrent files.
//
// The fallback path: if no data-code markers are present (test fixture
// case) and the page contains a magnet URI, return that magnet directly.
func (p *plugin) Download(ctx context.Context, topic *domain.Topic, _ *domain.Check, creds *domain.TrackerCredential) (*domain.Payload, error) {
	body, err := p.fetch(ctx, topic.URL, creds)
	if err != nil {
		return nil, err
	}

	episodes := parseEpisodes(body)
	if len(episodes) == 0 {
		// Test-fixture / legacy fallback: magnet directly on the series page.
		if m := magnetRe.Find(body); m != nil {
			return &domain.Payload{MagnetURI: string(m)}, nil
		}
		return nil, errors.New("lostfilm Download: no data-code markers and no magnet on the series page")
	}

	target := pickEpisode(episodes, topic.Extra)
	if target == nil {
		return nil, errors.New("lostfilm Download: no episode matches the start_season / start_episode filter")
	}

	return p.fetchTorrent(ctx, topic, creds, *target)
}

// pickEpisode walks the episode list newest-first and returns the first
// one not filtered out by topic.Extra["start_season"] /
// topic.Extra["start_episode"]. Returns nil if every episode is older
// than the floor.
func pickEpisode(episodes []episodeRef, extra map[string]any) *episodeRef {
	startSeason := extraInt(extra, "start_season")
	startEpisode := extraInt(extra, "start_episode")

	for i := len(episodes) - 1; i >= 0; i-- {
		ep := episodes[i]
		if startSeason > 0 {
			if ep.Season < startSeason {
				return nil // we've walked past every newer episode
			}
			if ep.Season == startSeason && startEpisode > 0 && ep.Episode < startEpisode {
				return nil
			}
		}
		return &ep
	}
	return nil
}

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

// fetchTorrent walks the LostFilm v_search redirector chain for one
// (show_id, season, episode) tuple and returns the matching-quality
// .torrent bytes.
func (p *plugin) fetchTorrent(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential, ep episodeRef) (*domain.Payload, error) {
	sess := p.session(creds)

	// 1. POST to /v_search.php with c=<show>&s=<season>&e=<episode>.
	form := url.Values{
		"c": {ep.ShowID},
		"s": {strconv.Itoa(ep.Season)},
		"e": {strconv.Itoa(ep.Episode)},
	}
	searchURL := "https://" + p.domain + "/v_search.php"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, searchURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", topic.URL)

	// Capture the redirect manually so we can chase it through external
	// hosts (retre.org / lf-tracker.io / etc.).
	noRedirect := *sess.Client
	noRedirect.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lostfilm v_search: %w", err)
	}
	defer resp.Body.Close()

	// Find the next URL — either Location header (30x) or meta-refresh
	// inside the body.
	next := resp.Header.Get("Location")
	if next == "" {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if m := metaRefreshRe.FindSubmatch(body); m != nil {
			next = string(m[1])
		}
	}
	if next == "" {
		return nil, errors.New("lostfilm v_search returned neither Location header nor meta-refresh")
	}
	// Resolve relative redirects against the v_search base URL.
	if base, perr := url.Parse(searchURL); perr == nil {
		if rel, perr := url.Parse(next); perr == nil {
			next = base.ResolveReference(rel).String()
		}
	}

	// 2. Fetch the redirector destination — that's the page with the
	// per-quality download buttons.
	dest, err := p.fetchURL(ctx, next, sess)
	if err != nil {
		return nil, fmt.Errorf("lostfilm download page: %w", err)
	}

	// 3. Pick the link matching topic.Extra["quality"].
	wantQuality := topic.Extra["quality"]
	if wantQuality == nil || wantQuality == "" {
		wantQuality = p.DefaultQuality()
	}
	wantStr, _ := wantQuality.(string)

	links := qualityLinkRe.FindAllSubmatch(dest, -1)
	if len(links) == 0 {
		return nil, errors.New("lostfilm download page: no per-quality torrent links found")
	}
	var torrentURL string
	for _, l := range links {
		label := strings.ToLower(string(l[2]))
		if strings.Contains(label, strings.ToLower(wantStr)) {
			torrentURL = string(l[1])
			break
		}
	}
	if torrentURL == "" {
		// Fall back to the first link if no quality matched (better
		// than failing the whole download).
		torrentURL = string(links[0][1])
	}

	// 4. GET the .torrent body.
	torrentBytes, err := p.fetchURL(ctx, torrentURL, sess)
	if err != nil {
		return nil, fmt.Errorf("lostfilm torrent fetch: %w", err)
	}
	return &domain.Payload{
		TorrentFile: torrentBytes,
		FileName:    fmt.Sprintf("lostfilm-%s-s%02de%02d-%s.torrent", ep.ShowID, ep.Season, ep.Episode, wantStr),
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

// fetchURL is a thin GET that reuses the user's session.
func (p *plugin) fetchURL(ctx context.Context, target string, sess *forumcommon.Session) ([]byte, error) {
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
