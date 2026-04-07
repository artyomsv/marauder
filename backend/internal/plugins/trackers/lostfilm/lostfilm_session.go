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

	"github.com/rs/zerolog/log"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// pluginName / displayName / defaultDomain are the canonical identifiers
// for this plugin. pluginName is the registry key the rest of the
// codebase looks up; displayName is the human-readable form for the UI;
// defaultDomain is the production hostname every URL is built against.
const (
	pluginName    = "lostfilm"
	displayName   = "LostFilm.tv"
	defaultDomain = "www.lostfilm.tv"

	// userAgent intentionally identifies Marauder honestly. LostFilm
	// does not bot-block the public series page, and per-tracker UA
	// spoofing would be inconsistent with the rest of the plugin
	// catalog.
	userAgent = "Marauder/1.1 (+https://marauder.cc)"
)

// urlPattern is the canonical lostfilm series URL shape. CanParse and
// Parse both rely on it; keeping it here next to the other constants
// makes the next domain rotation a one-line change.
var urlPattern = regexp.MustCompile(`^https?://(?:www\.)?lostfilm\.(?:tv|win|run)/series/([^/]+)/?`)

// Login implements registry.WithCredentials.
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
		return fmt.Errorf("lostfilm login: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return fmt.Errorf("lostfilm login: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("lostfilm login: read body: %w", err)
	}
	log.Debug().Str("plugin", pluginName).Str("step", "login").Int("status", resp.StatusCode).Int("body_len", len(body)).Msg("login response")
	if strings.Contains(string(body), `"error"`) {
		return errors.New("lostfilm login failed")
	}
	sess.LoggedIn = true
	return nil
}

// Verify implements registry.WithCredentials. It hits /my and looks for
// the logout link as a cheap "session still alive" probe.
func (p *plugin) Verify(ctx context.Context, creds *domain.TrackerCredential) (bool, error) {
	sess := p.sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	if p.transport != nil {
		sess.Client.Transport = p.transport
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+p.domain+"/my", nil)
	if err != nil {
		return false, fmt.Errorf("lostfilm verify: build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return false, fmt.Errorf("lostfilm verify: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if err != nil {
		return false, fmt.Errorf("lostfilm verify: read body: %w", err)
	}
	return strings.Contains(string(body), "logout"), nil
}

// session returns the per-user session, falling back to a no-credentials
// session for the magnet-fallback test fixture path. The transport hook
// is re-applied on every call so test plugins keep the host-rewriter
// active across cookie-jar refreshes.
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
// Referer if one is supplied. It is the workhorse used by both the
// series-page parser and the redirector chain.
func (p *plugin) fetchURL(ctx context.Context, target string, sess *forumcommon.Session, referer string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("lostfilm fetchURL build: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := sess.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lostfilm fetchURL: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("lostfilm GET %s -> %d", target, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("lostfilm fetchURL read: %w", err)
	}
	return body, nil
}

// fetch is the simpler GET used by Check to retrieve the series page.
// Like session(), it re-applies the transport hook on every call so the
// test rewriter survives session-jar refreshes.
func (p *plugin) fetch(ctx context.Context, target string, creds *domain.TrackerCredential) ([]byte, error) {
	sess := p.session(creds)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("lostfilm fetch build: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lostfilm fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("lostfilm GET %s -> %d", target, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("lostfilm fetch read: %w", err)
	}
	return body, nil
}
