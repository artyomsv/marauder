// Package hdclub implements a tracker plugin for hdclub.org.
//
// HD-Club is a TBDev/Gazelle-style private tracker focused on HD content.
// URL form: `https://hdclub.org/details.php?id=NNN`. Login form posts to
// `takelogin.php`. Download lives at `download.php?id=NNN`.
//
// **Validation status:** structurally complete with fixture-based unit
// tests. Live validation requires an HD-Club invitation.
package hdclub

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
	pluginName    = "hdclub"
	displayName   = "HD-Club.org"
	defaultDomain = "hdclub.org"
	userAgent     = "Marauder/1.0 (+https://marauder.cc)"
)

var urlPattern = regexp.MustCompile(`^https?://(?:www\.)?hdclub\.org/details\.php\?id=(\d+)`)

// Plugin is exported so e2e tests can construct fresh instances.
type Plugin struct {
	Sessions  *forumcommon.SessionStore
	Domain    string
	Transport http.RoundTripper
}

// New constructs a Plugin.
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
		return nil, errors.New("not an hdclub details URL")
	}
	id, _ := strconv.Atoi(m[1])
	return &domain.Topic{
		TrackerName: pluginName,
		URL:         rawURL,
		DisplayName: fmt.Sprintf("HD-Club torrent %d", id),
		Extra:       map[string]any{"topic_id": id},
	}, nil
}

func (p *Plugin) Login(ctx context.Context, creds *domain.TrackerCredential) error {
	if creds == nil || creds.Username == "" {
		return errors.New("hdclub credentials are required")
	}
	sess := p.Sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	if p.Transport != nil {
		sess.Client.Transport = p.Transport
	}
	form := url.Values{
		"username": {creds.Username},
		"password": {string(creds.SecretEnc)},
	}
	endpoint := "https://" + p.Domain + "/takelogin.php"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return fmt.Errorf("hdclub login: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if strings.Contains(string(body), "Login failed") || strings.Contains(string(body), "incorrect") {
		return errors.New("hdclub login failed")
	}
	sess.LoggedIn = true
	return nil
}

func (p *Plugin) Verify(ctx context.Context, creds *domain.TrackerCredential) (bool, error) {
	sess := p.Sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+p.Domain+"/", nil)
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
	return strings.Contains(string(body), "logout.php"), nil
}

// --- Check / Download --------------------------------------------------

var (
	titleRe = regexp.MustCompile(`(?s)<title>([^<]+)</title>`)
	hashRe  = regexp.MustCompile(`(?i)Info[\s_-]?hash[^A-Z0-9]+([A-Fa-f0-9]{40})`)
)

func (p *Plugin) Check(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential) (*domain.Check, error) {
	body, err := p.fetch(ctx, topic.URL, creds)
	if err != nil {
		return nil, err
	}
	check := &domain.Check{}
	if m := titleRe.FindSubmatch(body); m != nil {
		check.DisplayName = strings.TrimSpace(string(m[1]))
		check.DisplayName = strings.TrimSuffix(check.DisplayName, " :: HD-Club")
	}
	if m := hashRe.FindSubmatch(body); m != nil {
		check.Hash = strings.ToLower(string(m[1]))
		return check, nil
	}
	return nil, errors.New("hdclub: no infohash found")
}

func (p *Plugin) Download(ctx context.Context, topic *domain.Topic, _ *domain.Check, creds *domain.TrackerCredential) (*domain.Payload, error) {
	id, _ := topic.Extra["topic_id"].(int)
	if id == 0 {
		if f, ok := topic.Extra["topic_id"].(float64); ok {
			id = int(f)
		}
	}
	if id == 0 {
		return nil, errors.New("hdclub: no topic_id in extras")
	}
	dlURL := "https://" + p.Domain + "/download.php?id=" + strconv.Itoa(id)
	torrent, err := p.fetch(ctx, dlURL, creds)
	if err != nil {
		return nil, err
	}
	return &domain.Payload{TorrentFile: torrent, FileName: fmt.Sprintf("hdclub-%d.torrent", id)}, nil
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
		return nil, fmt.Errorf("hdclub GET %s -> %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}
