// Package anidub implements a tracker plugin for tr.anidub.com.
//
// Anidub is the Russian-dub anime tracker that replaced lostfilm.tv for
// many users in 2019-2020. It runs on a phpBB-like forum at
// `tr.anidub.com/<category>/<slug>.html`.
//
// **Validation status:** structurally complete; needs live-account
// validation. Selectors mirror the public HTML as of 2026-04.
package anidub

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
	pluginName    = "anidub"
	displayName   = "Anidub"
	defaultDomain = "tr.anidub.com"
	userAgent     = "Marauder/0.4 (+https://marauder.cc)"
)

var urlPattern = regexp.MustCompile(`^https?://tr\.anidub\.com/(?:[a-z0-9_-]+/)+([a-z0-9_-]+)\.html`)

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

// Qualities implements WithQuality.
func (p *plugin) Qualities() []string    { return []string{"HDTVRip", "HDTVRip-AVC", "BDRip"} }
func (p *plugin) DefaultQuality() string { return "HDTVRip" }

func (p *plugin) CanParse(rawURL string) bool {
	return urlPattern.MatchString(strings.TrimSpace(rawURL))
}

func (p *plugin) Parse(_ context.Context, rawURL string) (*domain.Topic, error) {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return nil, errors.New("not an anidub URL")
	}
	return &domain.Topic{
		TrackerName: pluginName, URL: rawURL,
		DisplayName: "Anidub: " + m[1],
		Extra:       map[string]any{"slug": m[1], "quality": "HDTVRip"},
	}, nil
}

func (p *plugin) Login(ctx context.Context, creds *domain.TrackerCredential) error {
	if creds == nil || creds.Username == "" {
		return errors.New("anidub credentials are required")
	}
	sess := p.sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	if p.transport != nil {
		sess.Client.Transport = p.transport
	}
	form := url.Values{
		"login_name":     {creds.Username},
		"login_password": {string(creds.SecretEnc)},
		"login":          {"submit"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+p.domain+"/index.php", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return fmt.Errorf("anidub login: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if strings.Contains(string(body), "Доступ запрещён") || strings.Contains(string(body), "не верный") {
		return errors.New("anidub login failed")
	}
	sess.LoggedIn = true
	return nil
}

func (p *plugin) Verify(_ context.Context, _ *domain.TrackerCredential) (bool, error) {
	return true, nil
}

var (
	titleRe   = regexp.MustCompile(`(?s)<h1[^>]*>([^<]+)</h1>`)
	hashRe    = regexp.MustCompile(`(?i)data-hash="([A-Fa-f0-9]{40})"`)
	dlHrefRe  = regexp.MustCompile(`href="(/engine/download\.php\?id=\d+)"`)
)

func (p *plugin) Check(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential) (*domain.Check, error) {
	body, err := p.fetch(ctx, topic.URL, creds)
	if err != nil {
		return nil, err
	}
	check := &domain.Check{}
	if m := titleRe.FindSubmatch(body); m != nil {
		check.DisplayName = strings.TrimSpace(string(m[1]))
	}
	if m := hashRe.FindSubmatch(body); m != nil {
		check.Hash = strings.ToLower(string(m[1]))
		return check, nil
	}
	return nil, errors.New("anidub: no infohash found")
}

func (p *plugin) Download(ctx context.Context, topic *domain.Topic, _ *domain.Check, creds *domain.TrackerCredential) (*domain.Payload, error) {
	body, err := p.fetch(ctx, topic.URL, creds)
	if err != nil {
		return nil, err
	}
	m := dlHrefRe.FindSubmatch(body)
	if m == nil {
		return nil, errors.New("anidub: no download link")
	}
	dlURL := "https://" + p.domain + string(m[1])
	torrent, err := p.fetch(ctx, dlURL, creds)
	if err != nil {
		return nil, err
	}
	return &domain.Payload{TorrentFile: torrent, FileName: "anidub.torrent"}, nil
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
		return nil, fmt.Errorf("anidub GET %s -> %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}
