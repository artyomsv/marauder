// Package nnmclub implements the NNM-Club.to tracker plugin.
//
// NNM-Club is the third of the "big three" CIS forum trackers Marauder
// targets. The site sits behind Cloudflare so this plugin opts into
// the WithCloudflare capability — the scheduler will route HTTP failures
// through the cfsolver sidecar (when configured) and re-try.
//
// **Validation status:** structurally complete with fixture-based unit
// tests. Validation against the live site requires both an account and a
// running cfsolver, neither of which were available in the original
// implementation session.
package nnmclub

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
	pluginName    = "nnmclub"
	displayName   = "NNM-Club.to"
	defaultDomain = "nnmclub.to"
	userAgent     = "Marauder/0.3 (+https://marauder.cc)"
)

var urlPattern = regexp.MustCompile(`^https?://(?:www\.)?nnmclub\.(?:to|me)/forum/viewtopic\.php\?t=(\d+)`)

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

// UsesCloudflare implements registry.WithCloudflare.
func (p *plugin) UsesCloudflare() bool { return true }

func (p *plugin) CanParse(rawURL string) bool {
	return urlPattern.MatchString(strings.TrimSpace(rawURL))
}

func (p *plugin) Parse(_ context.Context, rawURL string) (*domain.Topic, error) {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return nil, errors.New("not a nnm-club viewtopic URL")
	}
	id, err := strconv.Atoi(m[1])
	if err != nil {
		return nil, fmt.Errorf("topic id: %w", err)
	}
	return &domain.Topic{
		TrackerName: pluginName,
		URL:         rawURL,
		DisplayName: fmt.Sprintf("NNM-Club topic %d", id),
		Extra:       map[string]any{"topic_id": id},
	}, nil
}

// --- WithCredentials ---------------------------------------------------

func (p *plugin) Login(ctx context.Context, creds *domain.TrackerCredential) error {
	if creds == nil || creds.Username == "" {
		return errors.New("nnm-club credentials are required")
	}
	sess := p.sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	if p.transport != nil {
		sess.Client.Transport = p.transport
	}
	form := url.Values{
		"username": {creds.Username},
		"password": {string(creds.SecretEnc)},
		"redirect": {""},
		"login":    {"%C2%F5%EE%E4"}, // "Вход" in cp1251
	}
	endpoint := "https://" + p.domain + "/forum/login.php"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return fmt.Errorf("nnm-club login: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if strings.Contains(string(body), "incorrect") || strings.Contains(string(body), "Неверный") {
		return errors.New("nnm-club login failed: invalid credentials")
	}
	sess.LoggedIn = true
	return nil
}

func (p *plugin) Verify(ctx context.Context, creds *domain.TrackerCredential) (bool, error) {
	sess := p.sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+p.domain+"/forum/index.php", nil)
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
	titleRe  = regexp.MustCompile(`(?s)<title>([^<]+)</title>`)
	hashRe   = regexp.MustCompile(`(?i)Info-?Hash[^A-Z0-9]+([A-Fa-f0-9]{40})`)
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
		check.DisplayName = strings.TrimSuffix(check.DisplayName, " :: NNM-Club")
	}
	if m := hashRe.FindSubmatch(body); m != nil {
		check.Hash = strings.ToLower(string(m[1]))
	} else {
		return nil, errors.New("nnm-club: no infohash found")
	}
	return check, nil
}

func (p *plugin) Download(ctx context.Context, topic *domain.Topic, _ *domain.Check, creds *domain.TrackerCredential) (*domain.Payload, error) {
	body, err := p.fetch(ctx, topic.URL, creds)
	if err != nil {
		return nil, err
	}
	m := dlHrefRe.FindSubmatch(body)
	if m == nil {
		return nil, errors.New("nnm-club: no download link in topic page")
	}
	dlURL := "https://" + p.domain + "/forum/" + string(m[1])
	torrent, err := p.fetch(ctx, dlURL, creds)
	if err != nil {
		return nil, err
	}
	return &domain.Payload{TorrentFile: torrent, FileName: "nnmclub.torrent"}, nil
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
		return nil, fmt.Errorf("nnm-club GET %s -> %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}
