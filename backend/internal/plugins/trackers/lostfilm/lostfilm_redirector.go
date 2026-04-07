package lostfilm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/extra"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// Per-quality download link in the redirector destination page.
// LostFilm publishes three buttons (SD / 1080p_mp4 / 1080p), each
// linking to a .torrent file. We capture the href and the visible
// quality label (the text inside the <a>).
var qualityLinkRe = regexp.MustCompile(`(?is)<a[^>]+href="(https?://[^"]+\.torrent[^"]*)"[^>]*>([^<]*)</a>`)

// Meta-refresh redirect, e.g.
//
//	<meta http-equiv="refresh" content="0; url=https://retre.org/td.php?s=...">
var metaRefreshRe = regexp.MustCompile(`(?i)<meta\s+http-equiv="refresh"[^>]*url=([^"'\s>]+)`)

// allowedRedirectHosts is the allowlist of hosts the LostFilm redirector
// chain may legitimately reach. It is updated by tracking the live site
// over time. Anything else is rejected to defeat SSRF via a compromised
// redirector — the user's authenticated session cookies travel on every
// hop, so an attacker who controlled retre.org could otherwise point us
// at internal addresses to exfiltrate cookies or probe internal HTTP
// services.
var allowedRedirectHosts = map[string]struct{}{
	"www.lostfilm.tv":   {},
	"lostfilm.tv":       {},
	"lostfilm.win":      {},
	"lostfilm.run":      {},
	"retre.org":         {},
	"www.retre.org":     {},
	"tracktor.in":       {},
	"www.tracktor.in":   {},
	"lf-tracker.io":     {},
	"www.lf-tracker.io": {},
}

// validateRedirectURL parses target and rejects it if (a) it doesn't
// parse as an absolute http(s) URL, (b) its host isn't in the
// allowlist, or (c) it resolves to a private/loopback/link-local IP.
// The DNS check uses net.LookupIP — slow but tolerable on the
// once-per-tick code path.
func validateRedirectURL(target string) error {
	u, err := url.Parse(target)
	if err != nil {
		return fmt.Errorf("invalid redirect URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("redirect scheme must be http(s), got %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("redirect URL has no host")
	}
	if _, ok := allowedRedirectHosts[host]; !ok {
		return fmt.Errorf("redirect host %q is not in the LostFilm allowlist", host)
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("redirect host DNS lookup failed: %w", err)
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return fmt.Errorf("redirect host %q resolves to non-routable IP %s", host, ip)
		}
	}
	return nil
}

// validateRedirect is a small indirection so the e2e tests (which
// use httptest servers on 127.0.0.1) can plug in a permissive
// validator without disabling production safety. Production code
// always uses the real validateRedirectURL.
func (p *plugin) validateRedirect(target string) error {
	if p.redirectValidator != nil {
		return p.redirectValidator(target)
	}
	return validateRedirectURL(target)
}

// fetchTorrentByPacked walks the LostFilm v_search redirector chain
// for one packed episode ID and returns the matching-quality .torrent
// bytes.
//
// The flow:
//
//  1. GET https://www.lostfilm.tv/v_search.php?a=<packed>
//     with the session cookie + Referer set to the series page.
//     Auto-redirect is disabled so we can capture the Location
//     header (it points at an external host).
//  2. Follow the redirect manually. The destination is an HTML page
//     containing the per-quality download buttons.
//  3. Parse the destination for `<a href="…torrent">label</a>` pairs,
//     pick the one whose label matches topic.Extra["quality"].
//  4. GET the .torrent body and return it.
//
// Both the v_search hop and the final .torrent GET are validated
// against the redirector allowlist before any request is made.
func (p *plugin) fetchTorrentByPacked(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential, packed string) (*domain.Payload, error) {
	sess := p.session(creds)

	searchURL := "https://" + p.domain + "/v_search.php?a=" + packed
	next, err := p.resolveVSearchRedirect(ctx, sess, topic.URL, searchURL)
	if err != nil {
		return nil, err
	}
	if err := p.validateRedirect(next); err != nil {
		return nil, fmt.Errorf("lostfilm v_search redirect rejected: %w", err)
	}

	dest, err := p.fetchURL(ctx, next, sess, searchURL)
	if err != nil {
		return nil, fmt.Errorf("lostfilm download page: %w", err)
	}
	log.Debug().Str("plugin", pluginName).Str("step", "destination").Int("body_len", len(dest)).Msg("destination page fetched")

	wantStr := extra.String(topic.Extra, "quality", p.DefaultQuality())
	torrentURL, pickedLabel, err := pickQualityLink(dest, wantStr)
	if err != nil {
		return nil, err
	}
	if err := p.validateRedirect(torrentURL); err != nil {
		return nil, fmt.Errorf("lostfilm .torrent URL rejected: %w", err)
	}

	log.Debug().Str("plugin", pluginName).Str("step", "torrent").Str("url", torrentURL).Str("label", pickedLabel).Msg("GET .torrent")
	torrentBytes, err := p.fetchURL(ctx, torrentURL, sess, next)
	if err != nil {
		return nil, fmt.Errorf("lostfilm torrent fetch: %w", err)
	}
	log.Debug().Str("plugin", pluginName).Str("step", "torrent").Int("bytes", len(torrentBytes)).Msg(".torrent fetched")

	return &domain.Payload{
		TorrentFile: torrentBytes,
		FileName:    fmt.Sprintf("lostfilm-%s-%s.torrent", packed, sanitiseQuality(wantStr)),
	}, nil
}

// resolveVSearchRedirect performs steps 1+2 of the redirector chain:
// it GETs /v_search.php?a=<packed> with auto-redirect disabled and
// returns the absolute next-hop URL extracted from either the Location
// header or a meta-refresh in the body. Errors are surfaced verbatim
// without leaking response body content (the upstream login page can
// contain CSRF tokens that we don't want persisted in topics.last_error).
func (p *plugin) resolveVSearchRedirect(ctx context.Context, sess *forumcommon.Session, seriesURL, searchURL string) (string, error) {
	log.Debug().Str("plugin", pluginName).Str("step", "v_search").Str("url", searchURL).Msg("GET v_search")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return "", fmt.Errorf("lostfilm v_search build: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", seriesURL)

	// Capture the redirect manually so we can chase it through external
	// hosts (retre.org / tracktor.in / lf-tracker.io / etc.).
	noRedirect := *sess.Client
	noRedirect.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		return "", fmt.Errorf("lostfilm v_search GET: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return "", fmt.Errorf("lostfilm v_search read: %w", err)
	}
	log.Debug().Str("plugin", pluginName).Str("step", "v_search").Int("status", resp.StatusCode).Int("body_len", len(body)).Str("location", resp.Header.Get("Location")).Msg("v_search response")

	next := resp.Header.Get("Location")
	if next == "" {
		if m := metaRefreshRe.FindSubmatch(body); m != nil {
			next = string(m[1])
			log.Debug().Str("plugin", pluginName).Str("step", "v_search").Str("meta_refresh", next).Msg("found meta-refresh redirect")
		}
	}
	if next == "" {
		// Common cause: session not authenticated → redirected to
		// /login. We deliberately do NOT include the body preview
		// in the error: the scheduler persists this string into
		// topics.last_error and a CSRF token landing in the DB
		// would be a real leak.
		return "", errors.New("lostfilm v_search returned no redirect — likely not authenticated, please re-add credentials")
	}
	if next == "https://www.lostfilm.tv/login" || strings.HasSuffix(next, "/login") {
		return "", errors.New("lostfilm v_search redirected to /login — session cookie expired or login failed")
	}

	// Resolve relative redirects against the v_search base.
	if base, perr := url.Parse(searchURL); perr == nil {
		if rel, perr := url.Parse(next); perr == nil {
			next = base.ResolveReference(rel).String()
		}
	}
	log.Debug().Str("plugin", pluginName).Str("step", "redirector").Str("url", next).Msg("following redirect")
	return next, nil
}

// pickQualityLink scans the destination HTML for per-quality torrent
// links and returns the URL+label of the link matching want. It falls
// back to the first link if no exact quality match is found — better
// to return *something* than to fail an episode the user already paid
// for.
func pickQualityLink(dest []byte, want string) (string, string, error) {
	links := qualityLinkRe.FindAllSubmatch(dest, -1)
	log.Debug().Str("plugin", pluginName).Str("step", "destination").Int("link_count", len(links)).Str("want_quality", want).Msg("parsed quality links")

	if len(links) == 0 {
		return "", "", errors.New("lostfilm download page: no per-quality torrent links found")
	}

	for _, l := range links {
		if qualityMatches(string(l[2]), want) {
			return string(l[1]), string(l[2]), nil
		}
	}

	// Fall back to the first link.
	first, label := string(links[0][1]), string(links[0][2])
	log.Debug().Str("plugin", pluginName).Str("step", "destination").Str("fallback_label", label).Msg("no quality match, using first link")
	return first, label, nil
}

// sanitiseQuality replaces non-filename-safe chars in a quality label.
func sanitiseQuality(q string) string {
	q = strings.ToLower(q)
	q = strings.ReplaceAll(q, " ", "_")
	q = strings.ReplaceAll(q, "/", "_")
	return q
}

// qualityMatches reports whether a download-page button label matches
// the user's chosen quality. The naive `Contains(label, want)` is wrong
// because `Contains("1080p_mp4", "1080p") == true`, so a user asking
// for plain `1080p` would get the MP4 variant. We special-case the
// three known LostFilm tiers so each maps to a distinct, non-overlapping
// label substring.
func qualityMatches(label, want string) bool {
	l := strings.ToLower(label)
	w := strings.ToLower(want)
	switch w {
	case "sd":
		return strings.Contains(l, "sd")
	case "1080p_mp4", "mp4":
		return strings.Contains(l, "mp4")
	case "1080p":
		// 1080p but NOT 1080p_mp4 — i.e. label has "1080p" and
		// no "mp4" tail.
		return strings.Contains(l, "1080p") && !strings.Contains(l, "mp4")
	default:
		// Unknown quality — refuse rather than risk a 1080p/1080p_mp4
		// false-match. Add new tiers explicitly to this switch.
		return false
	}
}
