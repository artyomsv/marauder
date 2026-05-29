package lostfilm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/captchalogin"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
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

// captchaConfig is the LostFilm-specific configuration for the shared
// interactive captcha-login engine. LostFilm's login endpoint is the
// ajaxik users handler, and its captcha image is served by
// simple_captcha.php on the same host.
func (p *plugin) captchaConfig() captchalogin.Config {
	base := "https://" + p.domain
	return captchalogin.Config{
		LoginURL:    base + "/ajaxik.users.php",
		CaptchaURL:  base + "/simple_captcha.php",
		CookieNames: []string{"lf_session"},
		BuildForm: func(c *domain.TrackerCredential, answer string, needCaptcha bool) url.Values {
			nc := "0"
			if needCaptcha {
				nc = "1"
			}
			return url.Values{
				"act": {"users"}, "type": {"login"},
				"mail": {c.Username}, "pass": {string(c.SecretEnc)},
				"rem": {"1"}, "need_captcha": {nc}, "captcha": {answer},
			}
		},
		Classify: classifyLostfilmLogin,
	}
}

// classifyLostfilmLogin maps LostFilm's ajaxik JSON to an Outcome. It
// parses the JSON rather than substring-matching so a multi-digit error
// code (e.g. {"error":40}) is not mistaken for the captcha-specific
// error 4 — that misclassification would loop the user on a captcha.
// Success is parsed as `any` because LostFilm returns either
// {"success":true} or a user object; non-nil means logged in.
func classifyLostfilmLogin(body []byte) captchalogin.Outcome {
	var r struct {
		Success     any  `json:"success"`
		Error       *int `json:"error"`
		NeedCaptcha bool `json:"need_captcha"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return captchalogin.OutcomeFailed
	}
	switch {
	case r.Error != nil && *r.Error == 4:
		return captchalogin.OutcomeWrongCaptcha
	case r.NeedCaptcha:
		return captchalogin.OutcomeNeedCaptcha
	case r.Success != nil:
		return captchalogin.OutcomeSuccess
	default:
		return captchalogin.OutcomeFailed
	}
}

// BeginLogin implements registry.WithInteractiveLogin.
func (p *plugin) BeginLogin(ctx context.Context, creds *domain.TrackerCredential) (*registry.LoginChallenge, registry.SessionCookies, error) {
	return p.eng().Begin(ctx, creds)
}

// CompleteLogin implements registry.WithInteractiveLogin.
func (p *plugin) CompleteLogin(ctx context.Context, challengeID, answer string) (registry.SessionCookies, error) {
	return p.eng().Complete(ctx, challengeID, answer)
}

// RefreshChallenge implements registry.WithInteractiveLogin.
func (p *plugin) RefreshChallenge(ctx context.Context, challengeID string) (*registry.LoginChallenge, error) {
	return p.eng().Refresh(ctx, challengeID)
}

// Login implements registry.WithCredentials. LostFilm logins go through
// the interactive captcha flow (BeginLogin / CompleteLogin), which
// persists the harvested session cookies into creds.SessionEnc. Login
// therefore no longer POSTs credentials: it rehydrates the stored
// cookies into the per-user session jar and confirms they are still
// valid via Verify. A missing or dead session surfaces as
// registry.ErrSessionExpired so the caller can re-run the captcha flow.
func (p *plugin) Login(ctx context.Context, creds *domain.TrackerCredential) error {
	if creds == nil || len(creds.SessionEnc) == 0 {
		return fmt.Errorf("lostfilm: no stored session; add the account via the captcha login flow: %w", registry.ErrSessionExpired)
	}
	var cookies map[string]string
	if err := json.Unmarshal(creds.SessionEnc, &cookies); err != nil {
		return fmt.Errorf("lostfilm: corrupt stored session: %w", registry.ErrSessionExpired)
	}
	sess := p.sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	if p.transport != nil {
		sess.Client.Transport = p.transport
	}
	// p.domain is a trusted constant; parse cannot fail.
	u, _ := url.Parse("https://" + p.domain + "/")
	jarCookies := make([]*http.Cookie, 0, len(cookies))
	for name, val := range cookies {
		jarCookies = append(jarCookies, &http.Cookie{Name: name, Value: val})
	}
	sess.Client.Jar.SetCookies(u, jarCookies)
	ok, err := p.Verify(ctx, creds)
	if err != nil {
		return fmt.Errorf("lostfilm: session validation: %w", err)
	}
	if !ok {
		return fmt.Errorf("lostfilm: stored session no longer valid; re-run the captcha login flow: %w", registry.ErrSessionExpired)
	}
	sess.LoggedIn = true
	return nil
}

// Verify implements registry.WithCredentials.
//
// Approach: hit /my and rely on TWO signals:
//
//  1. The FINAL request URL after redirect-following. LostFilm
//     redirects /my to a login page when the session is dead. Go's
//     http.Client follows redirects by default, so resp.Request.URL
//     reflects the final destination — if its path contains "login"
//     we know the session is gone, no body parsing required.
//  2. As a positive secondary check, the response body should mention
//     "logout" somewhere (the authenticated nav has a logout
//     link/button in some form). This is a loose check on purpose:
//     LostFilm's markup has changed multiple times and a stricter
//     pattern (`href="/logout"`) was empirically too narrow — the
//     real anchor varies between `href="//www.lostfilm.tv/logout"`,
//     `href="/logout/"`, button-style links, JS-driven menus, etc.
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

	// Signal 1: were we redirected to a login page?
	if resp.Request != nil && resp.Request.URL != nil {
		finalPath := strings.ToLower(resp.Request.URL.Path)
		if strings.Contains(finalPath, "login") {
			log.Debug().Str("plugin", pluginName).Str("step", "verify").
				Str("final_url", resp.Request.URL.String()).
				Msg("verify failed: redirected to login")
			return false, nil
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
	if err != nil {
		return false, fmt.Errorf("lostfilm verify: read body: %w", err)
	}

	// Signal 2: positive substring. Loose by design.
	if !strings.Contains(string(body), "logout") {
		log.Debug().Str("plugin", pluginName).Str("step", "verify").
			Int("body_len", len(body)).
			Msg("verify failed: no 'logout' substring in /my body")
		return false, nil
	}
	return true, nil
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
