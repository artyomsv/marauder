// Package rutracker implements the RuTracker.org tracker plugin.
//
// RuTracker is a phpBB-derived forum where each "topic" page hosts one
// torrent attachment. The flow Marauder uses is:
//
//  1. Login: POST /forum/login.php with login_username, login_password,
//     login=Вход. Sets a `bb_session` cookie that we keep in the cookie
//     jar.
//  2. Check: GET /forum/viewtopic.php?t=<topic_id>. Parse the topic
//     title from <title>, the magnet link from the page, and the
//     `dl_class_magnet-link` href.
//  3. Download: follow the dl.php link with the same session cookies.
//
// **Validation status:** structurally complete and unit-tested with
// recorded HTML fixtures. The selectors mirror the public RuTracker HTML
// as of 2026-04. Validating the plugin against a live RuTracker account
// requires credentials this session does not have, so the plugin is
// shipped as "alpha" — see CONTRIBUTING.md for the validation procedure.
package rutracker

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
	pluginName    = "rutracker"
	displayName   = "RuTracker.org"
	defaultDomain = "rutracker.org"
	userAgent     = "Marauder/0.3 (+https://marauder.cc)"
)

// urlPattern matches https://rutracker.org/forum/viewtopic.php?t=12345
var urlPattern = regexp.MustCompile(`^https?://(?:www\.)?rutracker\.(?:org|net|nl|cr)/forum/viewtopic\.php\?t=(\d+)`)

type plugin struct {
	sessions  *forumcommon.SessionStore
	domain    string // overridable for tests
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

// CanParse — true for any rutracker viewtopic URL.
func (p *plugin) CanParse(rawURL string) bool {
	return urlPattern.MatchString(strings.TrimSpace(rawURL))
}

// Parse extracts the topic ID and produces a placeholder Topic with the
// canonical URL form. The full title comes from the first Check() call.
func (p *plugin) Parse(_ context.Context, rawURL string) (*domain.Topic, error) {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return nil, errors.New("not a rutracker viewtopic URL")
	}
	topicID, err := strconv.Atoi(m[1])
	if err != nil {
		return nil, fmt.Errorf("topic id: %w", err)
	}
	return &domain.Topic{
		TrackerName: pluginName,
		URL:         rawURL,
		DisplayName: fmt.Sprintf("RuTracker topic %d", topicID),
		Extra:       map[string]any{"topic_id": topicID},
	}, nil
}

// --- WithCredentials ---------------------------------------------------

// Login posts the login form. The cookie jar attached to the session
// captures bb_session for subsequent calls.
func (p *plugin) Login(ctx context.Context, creds *domain.TrackerCredential) error {
	if creds == nil || creds.Username == "" {
		return errors.New("rutracker credentials are required")
	}
	sess := p.sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	if p.transport != nil {
		sess.Client.Transport = p.transport
	}
	form := url.Values{
		"login_username": {creds.Username},
		"login_password": {string(creds.SecretEnc)}, // secret already decrypted by caller in v0.4
		"login":          {"Вход"},
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
		return fmt.Errorf("rutracker login: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("rutracker login: read body: %w", err)
	}
	// Positive-indicator check: rutracker.org renders a "logged-in-username"
	// span ONLY on authenticated pages. The old `|| resp.StatusCode == 200`
	// escape hatch was a bug — the login form also returns 200 with an
	// error panel, so Login always succeeded regardless of credentials.
	if !strings.Contains(string(body), `id="logged-in-username"`) {
		return errors.New("rutracker login failed: invalid credentials (no logged-in marker in response)")
	}
	sess.LoggedIn = true
	return nil
}

// Verify quickly checks whether the cached session is still valid by
// hitting a known authenticated page.
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if err != nil {
		return false, fmt.Errorf("rutracker verify: read body: %w", err)
	}
	return strings.Contains(string(body), `id="logged-in-username"`), nil
}

// --- Check / Download --------------------------------------------------

var (
	titleRe         = regexp.MustCompile(`(?s)<title>([^<]+)</title>`)
	magnetRe        = regexp.MustCompile(`(magnet:\?xt=urn:btih:[A-Fa-f0-9]+[^"'&\s]*)`)
	dlHrefRe        = regexp.MustCompile(`href="(dl\.php\?t=\d+)"`)
	hashLooksLikeRe = regexp.MustCompile(`urn:btih:([A-Fa-f0-9]+)`)
)

// Check fetches the topic page and extracts a hash. The hash is the
// torrent BTIH from the magnet link, which changes whenever the uploader
// re-uploads.
func (p *plugin) Check(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential) (*domain.Check, error) {
	body, err := p.fetchTopicPage(ctx, topic, creds)
	if err != nil {
		return nil, err
	}
	check := &domain.Check{}
	if m := titleRe.FindSubmatch(body); m != nil {
		check.DisplayName = strings.TrimSpace(string(m[1]))
		check.DisplayName = strings.TrimSuffix(check.DisplayName, " :: RuTracker.org")
	}
	if m := hashLooksLikeRe.FindSubmatch(body); m != nil {
		check.Hash = strings.ToLower(string(m[1]))
	} else {
		return nil, errors.New("rutracker: no infohash found in topic page")
	}
	return check, nil
}

// Download returns the magnet URI for the current topic. RuTracker also
// exposes a downloadable .torrent file via dl.php, but the magnet alone
// is enough for v0.3 — every torrent client we support accepts magnets.
func (p *plugin) Download(ctx context.Context, topic *domain.Topic, _ *domain.Check, creds *domain.TrackerCredential) (*domain.Payload, error) {
	body, err := p.fetchTopicPage(ctx, topic, creds)
	if err != nil {
		return nil, err
	}
	if m := magnetRe.Find(body); m != nil {
		return &domain.Payload{MagnetURI: string(m)}, nil
	}
	// Fall back to dl.php — needs the session cookie.
	if m := dlHrefRe.FindSubmatch(body); m != nil {
		dlURL := "https://" + p.domain + "/forum/" + string(m[1])
		torrent, err := p.fetchBytes(ctx, topic, creds, dlURL)
		if err != nil {
			return nil, err
		}
		return &domain.Payload{TorrentFile: torrent, FileName: "rutracker.torrent"}, nil
	}
	return nil, errors.New("rutracker: no magnet link or dl.php link in topic page")
}

func (p *plugin) fetchTopicPage(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential) ([]byte, error) {
	return p.fetchBytes(ctx, topic, creds, topic.URL)
}

func (p *plugin) fetchBytes(ctx context.Context, _ *domain.Topic, creds *domain.TrackerCredential, target string) ([]byte, error) {
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
		return nil, fmt.Errorf("rutracker GET %s -> %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}
