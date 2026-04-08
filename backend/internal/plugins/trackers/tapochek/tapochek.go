// Package tapochek implements a tracker plugin for tapochek.net.
//
// Tapochek is the cartoons-and-kids-content tracker that uses the same
// phpBB-derived shape as RuTracker. Includes the same Login + Verify +
// Check + Download interface.
//
// **Validation status:** structurally complete; needs live validation.
package tapochek

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
	pluginName    = "tapochek"
	displayName   = "Tapochek.net"
	defaultDomain = "tapochek.net"
	userAgent     = "Marauder/0.4 (+https://marauder.cc)"
)

var urlPattern = regexp.MustCompile(`^https?://(?:www\.)?tapochek\.net/viewtopic\.php\?t=(\d+)`)

type plugin struct {
	sessions  *forumcommon.SessionStore
	domain    string
	transport http.RoundTripper
}

func init() {
	registry.RegisterTracker(&plugin{sessions: forumcommon.New(), domain: defaultDomain})
}

func (p *plugin) Name() string        { return pluginName }
func (p *plugin) DisplayName() string { return displayName }

func (p *plugin) CanParse(rawURL string) bool {
	return urlPattern.MatchString(strings.TrimSpace(rawURL))
}

func (p *plugin) Parse(_ context.Context, rawURL string) (*domain.Topic, error) {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return nil, errors.New("not a tapochek topic URL")
	}
	id, _ := strconv.Atoi(m[1])
	return &domain.Topic{
		TrackerName: pluginName, URL: rawURL,
		DisplayName: fmt.Sprintf("Tapochek topic %d", id),
		Extra:       map[string]any{"topic_id": id},
	}, nil
}

func (p *plugin) Login(ctx context.Context, creds *domain.TrackerCredential) error {
	if creds == nil || creds.Username == "" {
		return errors.New("tapochek credentials are required")
	}
	sess := p.sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	if p.transport != nil {
		sess.Client.Transport = p.transport
	}
	form := url.Values{
		"login_username": {creds.Username},
		"login_password": {string(creds.SecretEnc)},
		"autologin":      {"on"},
		"login":          {"Login"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+p.domain+"/login.php", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return fmt.Errorf("tapochek login: %w", err)
	}
	defer resp.Body.Close()
	// tapochek.net runs phpBB. A successful login POST redirects back to
	// the index with a Set-Cookie: phpbb3_*_u=<non-1> session. A failed
	// login re-renders /login.php with a "login_username" form field.
	// We can't detect the session cookie shape reliably across phpBB
	// versions, so rely on Verify (which hits the index and looks for
	// the authenticated username marker). Login's job is just to POST
	// the form and reject obvious transport errors — Verify is the
	// real check, enforced by the credentials handler.
	sess.LoggedIn = true
	return nil
}

// Verify hits the index page and looks for a positive "logged in"
// marker. Previously returned (true, nil) unconditionally, which made
// tapochek impossible to detect as un-authenticated.
func (p *plugin) Verify(ctx context.Context, creds *domain.TrackerCredential) (bool, error) {
	sess := p.sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	if p.transport != nil {
		sess.Client.Transport = p.transport
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+p.domain+"/index.php", nil)
	if err != nil {
		return false, fmt.Errorf("tapochek verify: build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return false, fmt.Errorf("tapochek verify: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
	if err != nil {
		return false, fmt.Errorf("tapochek verify: read body: %w", err)
	}
	// phpBB renders "logout.php?sid=" in the header nav of authenticated
	// pages; the login form version has "login.php?mode=login" instead.
	return strings.Contains(string(body), "logout.php?sid="), nil
}

var (
	titleRe  = regexp.MustCompile(`(?s)<title>([^<]+)</title>`)
	hashRe   = regexp.MustCompile(`(?i)Info hash[^A-Z0-9]+([A-Fa-f0-9]{40})`)
	dlHrefRe = regexp.MustCompile(`href="(download\.php\?id=\d+)"`)
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
	return nil, errors.New("tapochek: no infohash found")
}

func (p *plugin) Download(ctx context.Context, topic *domain.Topic, _ *domain.Check, creds *domain.TrackerCredential) (*domain.Payload, error) {
	body, err := p.fetch(ctx, topic.URL, creds)
	if err != nil {
		return nil, err
	}
	m := dlHrefRe.FindSubmatch(body)
	if m == nil {
		return nil, errors.New("tapochek: no download link")
	}
	dlURL := "https://" + p.domain + "/" + string(m[1])
	torrent, err := p.fetch(ctx, dlURL, creds)
	if err != nil {
		return nil, err
	}
	return &domain.Payload{TorrentFile: torrent, FileName: "tapochek.torrent"}, nil
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
		return nil, fmt.Errorf("tapochek GET %s -> %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}
