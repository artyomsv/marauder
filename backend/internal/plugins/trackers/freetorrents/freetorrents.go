// Package freetorrents implements a tracker plugin for free-torrents.org.
//
// Free-torrents is a phpBB-derived Russian tracker very similar to
// RuTracker in shape: `viewtopic.php?t=NNN`, login form at `login.php`,
// download endpoint at `dl.php?id=NNN`. Same scrape-the-magnet-and-fall-
// back-to-dl.php strategy.
//
// **Validation status:** structurally complete with fixture-based unit
// tests. Live validation requires a real free-torrents.org account.
package freetorrents

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
	pluginName    = "freetorrents"
	displayName   = "Free-Torrents.org"
	defaultDomain = "free-torrents.org"
	userAgent     = "Marauder/1.0 (+https://marauder.cc)"
)

var urlPattern = regexp.MustCompile(`^https?://(?:www\.)?free-torrents\.org/forum/viewtopic\.php\?t=(\d+)`)

// Plugin is exported so e2e tests can construct fresh instances with a
// custom domain and transport.
type Plugin struct {
	Sessions  *forumcommon.SessionStore
	Domain    string
	Transport http.RoundTripper
}

// New constructs a Plugin with the given domain and transport.
// Pass an empty domain to use the default. Use this from tests.
func New(domain string, transport http.RoundTripper) *Plugin {
	if domain == "" {
		domain = defaultDomain
	}
	return &Plugin{
		Sessions:  forumcommon.New(),
		Domain:    domain,
		Transport: transport,
	}
}

func init() {
	registry.RegisterTracker(New("", nil))
}

func (p *Plugin) Name() string        { return pluginName }
func (p *Plugin) DisplayName() string { return displayName }

func (p *Plugin) CanParse(rawURL string) bool {
	return urlPattern.MatchString(strings.TrimSpace(rawURL))
}

func (p *Plugin) Parse(_ context.Context, rawURL string) (*domain.Topic, error) {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return nil, errors.New("not a free-torrents viewtopic URL")
	}
	id, _ := strconv.Atoi(m[1])
	return &domain.Topic{
		TrackerName: pluginName,
		URL:         rawURL,
		DisplayName: fmt.Sprintf("Free-Torrents topic %d", id),
		Extra:       map[string]any{"topic_id": id},
	}, nil
}

// --- WithCredentials ---------------------------------------------------

func (p *Plugin) Login(ctx context.Context, creds *domain.TrackerCredential) error {
	if creds == nil || creds.Username == "" {
		return errors.New("free-torrents credentials are required")
	}
	sess := p.Sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	if p.Transport != nil {
		sess.Client.Transport = p.Transport
	}
	form := url.Values{
		"login_username": {creds.Username},
		"login_password": {string(creds.SecretEnc)},
		"login":          {"submit"},
	}
	endpoint := "https://" + p.Domain + "/forum/login.php"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return fmt.Errorf("free-torrents login: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("free-torrents login: read body: %w", err)
	}
	if strings.Contains(string(body), "ucp.php?mode=login") || strings.Contains(string(body), "incorrect") {
		return errors.New("free-torrents login failed")
	}
	sess.LoggedIn = true
	return nil
}

func (p *Plugin) Verify(ctx context.Context, creds *domain.TrackerCredential) (bool, error) {
	sess := p.Sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+p.Domain+"/forum/index.php", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if err != nil {
		return false, fmt.Errorf("free-torrents verify: read body: %w", err)
	}
	return strings.Contains(string(body), "logout"), nil
}

// --- Check / Download --------------------------------------------------

var (
	titleRe  = regexp.MustCompile(`(?s)<title>([^<]+)</title>`)
	hashRe   = regexp.MustCompile(`(?i)Info[\s_-]?hash[^A-Z0-9]+([A-Fa-f0-9]{40})`)
	magnetRe = regexp.MustCompile(`(magnet:\?xt=urn:btih:[A-Fa-f0-9]+[^"'&\s]*)`)
	dlHrefRe = regexp.MustCompile(`href="(dl\.php\?id=\d+)"`)
)

func (p *Plugin) Check(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential) (*domain.Check, error) {
	body, err := p.fetch(ctx, topic.URL, creds)
	if err != nil {
		return nil, err
	}
	check := &domain.Check{}
	if m := titleRe.FindSubmatch(body); m != nil {
		check.DisplayName = strings.TrimSpace(string(m[1]))
		check.DisplayName = strings.TrimSuffix(check.DisplayName, " :: Free-Torrents.org")
	}
	if m := hashRe.FindSubmatch(body); m != nil {
		check.Hash = strings.ToLower(string(m[1]))
		return check, nil
	}
	if m := magnetRe.FindSubmatch(body); m != nil {
		// Fall back: extract BTIH from a magnet URI on the page
		if btih := regexp.MustCompile(`btih:([A-Fa-f0-9]+)`).FindSubmatch(m[1]); btih != nil {
			check.Hash = strings.ToLower(string(btih[1]))
			return check, nil
		}
	}
	return nil, errors.New("free-torrents: no infohash found in topic page")
}

func (p *Plugin) Download(ctx context.Context, topic *domain.Topic, _ *domain.Check, creds *domain.TrackerCredential) (*domain.Payload, error) {
	body, err := p.fetch(ctx, topic.URL, creds)
	if err != nil {
		return nil, err
	}
	if m := magnetRe.Find(body); m != nil {
		return &domain.Payload{MagnetURI: string(m)}, nil
	}
	if m := dlHrefRe.FindSubmatch(body); m != nil {
		dlURL := "https://" + p.Domain + "/forum/" + string(m[1])
		torrent, err := p.fetch(ctx, dlURL, creds)
		if err != nil {
			return nil, err
		}
		return &domain.Payload{TorrentFile: torrent, FileName: "freetorrents.torrent"}, nil
	}
	return nil, errors.New("free-torrents: no magnet or dl link in topic page")
}

func (p *Plugin) fetch(ctx context.Context, target string, creds *domain.TrackerCredential) ([]byte, error) {
	key := pluginName + ":nocreds"
	if creds != nil {
		key = forumcommon.SessionKey(pluginName, creds.UserID.String())
	}
	sess := p.Sessions.GetOrCreate(key, userAgent)
	if p.Transport != nil {
		sess.Client.Transport = p.Transport
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
		return nil, fmt.Errorf("free-torrents GET %s -> %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}
