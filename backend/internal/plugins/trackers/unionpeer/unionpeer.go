// Package unionpeer implements a tracker plugin for unionpeer.org.
//
// Unionpeer is yet another phpBB tracker. Same shape as toloka and
// rutracker but with its own login URL and topic page selectors.
//
// **Validation status:** structurally complete; needs live validation.
package unionpeer

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
	pluginName    = "unionpeer"
	displayName   = "Unionpeer.org"
	defaultDomain = "unionpeer.org"
	userAgent     = "Marauder/0.4 (+https://marauder.cc)"
)

var urlPattern = regexp.MustCompile(`^https?://(?:www\.)?unionpeer\.(?:org|net|com)/forum/viewtopic\.php\?t=(\d+)`)

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
		return nil, errors.New("not a unionpeer topic URL")
	}
	id, _ := strconv.Atoi(m[1])
	return &domain.Topic{
		TrackerName: pluginName, URL: rawURL,
		DisplayName: fmt.Sprintf("Unionpeer topic %d", id),
		Extra:       map[string]any{"topic_id": id},
	}, nil
}

func (p *plugin) Login(ctx context.Context, creds *domain.TrackerCredential) error {
	if creds == nil || creds.Username == "" {
		return errors.New("unionpeer credentials are required")
	}
	sess := p.sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	if p.transport != nil {
		sess.Client.Transport = p.transport
	}
	form := url.Values{
		"login_username": {creds.Username},
		"login_password": {string(creds.SecretEnc)},
		"login":          {"Вход"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+p.domain+"/forum/login.php", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return fmt.Errorf("unionpeer login: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("unionpeer login: read body: %w", err)
	}
	if strings.Contains(string(body), "Invalid") {
		return errors.New("unionpeer login failed")
	}
	sess.LoggedIn = true
	return nil
}

func (p *plugin) Verify(_ context.Context, _ *domain.TrackerCredential) (bool, error) {
	return true, nil
}

var (
	titleRe  = regexp.MustCompile(`(?s)<title>([^<]+)</title>`)
	hashRe   = regexp.MustCompile(`(?i)Info hash[^A-Z0-9]+([A-Fa-f0-9]{40})`)
	dlHrefRe = regexp.MustCompile(`href="(dl\.php\?t=\d+)"`)
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
	return nil, errors.New("unionpeer: no infohash found")
}

func (p *plugin) Download(ctx context.Context, topic *domain.Topic, _ *domain.Check, creds *domain.TrackerCredential) (*domain.Payload, error) {
	body, err := p.fetch(ctx, topic.URL, creds)
	if err != nil {
		return nil, err
	}
	m := dlHrefRe.FindSubmatch(body)
	if m == nil {
		return nil, errors.New("unionpeer: no download link")
	}
	dlURL := "https://" + p.domain + "/forum/" + string(m[1])
	torrent, err := p.fetch(ctx, dlURL, creds)
	if err != nil {
		return nil, err
	}
	return &domain.Payload{TorrentFile: torrent, FileName: "unionpeer.torrent"}, nil
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
		return nil, fmt.Errorf("unionpeer GET %s -> %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}
