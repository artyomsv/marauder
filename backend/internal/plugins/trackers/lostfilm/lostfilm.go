// Package lostfilm implements a tracker plugin for lostfilm.tv.
//
// LostFilm is one of the most-requested forum trackers in the CIS scene:
// it's where Russian-dubbed TV episodes are released. The site exposes
// per-show pages at /series/<slug> and per-episode download pages with
// quality buttons (SD, MP4, 1080p).
//
// **Validation status:** structurally complete with fixture-based unit
// tests. The selectors mirror the public LostFilm HTML as of 2026-04.
// Live validation requires a paid LostFilm account, which was not
// available in the original implementation session.
package lostfilm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
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

var urlPattern = regexp.MustCompile(`^https?://(?:www\.)?lostfilm\.(?:tv|win|run)/series/([^/]+)/?`)

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

// Quality is one of LostFilm's quality tiers.
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

var (
	episodeRe = regexp.MustCompile(`(?i)data-episode="(\d+)"`)
	titleRe   = regexp.MustCompile(`(?s)<title>([^<]+)</title>`)
)

// Check fetches the series page and uses the highest data-episode value
// as the hash. When a new episode is released, that number ticks up.
func (p *plugin) Check(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential) (*domain.Check, error) {
	body, err := p.fetch(ctx, topic.URL, creds)
	if err != nil {
		return nil, err
	}
	check := &domain.Check{}
	if m := titleRe.FindSubmatch(body); m != nil {
		check.DisplayName = strings.TrimSpace(string(m[1]))
	}
	matches := episodeRe.FindAllSubmatch(body, -1)
	if len(matches) == 0 {
		return nil, errors.New("lostfilm: no data-episode markers found")
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

// magnetRe matches a magnet URI in the page body.
var magnetRe = regexp.MustCompile(`(magnet:\?xt=urn:btih:[A-Fa-f0-9]+[^"'&\s]*)`)

// Download fetches the series page and extracts a magnet URI if one is
// present. If the page only exposes a redirector flow (which the real
// LostFilm site uses for paid users), we return a clear error so the
// failure is visible in the UI.
//
// The redirector flow is intentionally not implemented in v1.0 — it
// requires a live LostFilm account to validate, and the redirector
// changes shape periodically. CONTRIBUTING.md explains how a contributor
// with an account can flesh it out.
func (p *plugin) Download(ctx context.Context, topic *domain.Topic, _ *domain.Check, creds *domain.TrackerCredential) (*domain.Payload, error) {
	body, err := p.fetch(ctx, topic.URL, creds)
	if err != nil {
		return nil, err
	}
	if m := magnetRe.Find(body); m != nil {
		return &domain.Payload{MagnetURI: string(m)}, nil
	}
	return nil, errors.New("lostfilm Download: no magnet on the series page (the real redirector flow needs live-account validation)")
}

func (p *plugin) fetch(ctx context.Context, target string, creds *domain.TrackerCredential) ([]byte, error) {
	key := pluginName + ":nocreds"
	if creds != nil {
		key = forumcommon.SessionKey(pluginName, creds.UserID.String())
	}
	sess := p.sessions.GetOrCreate(key, userAgent)
	if p.transport != nil {
		sess.Client.Transport = p.transport
	}
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
