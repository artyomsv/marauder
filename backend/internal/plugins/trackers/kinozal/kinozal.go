// Package kinozal implements the Kinozal.tv tracker plugin.
//
// Kinozal is another phpBB-derived tracker. The flow is similar to
// RuTracker but the URL pattern is /details.php?id=<topic_id> and the
// download link is /download.php?id=<topic_id>.
//
// **Validation status:** structurally complete with fixture-based unit
// tests. Live validation requires Kinozal credentials and was not
// performed in the original implementation session — see CONTRIBUTING.md
// for the procedure to validate against a real account.
package kinozal

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
	pluginName    = "kinozal"
	displayName   = "Kinozal.tv"
	defaultDomain = "kinozal.tv"
	userAgent     = "Marauder/0.3 (+https://marauder.cc)"
)

var urlPattern = regexp.MustCompile(`^https?://(?:www\.)?kinozal\.(?:tv|me|guru)/details\.php\?id=(\d+)`)

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

func (p *plugin) CanParse(rawURL string) bool {
	return urlPattern.MatchString(strings.TrimSpace(rawURL))
}

func (p *plugin) Parse(_ context.Context, rawURL string) (*domain.Topic, error) {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return nil, errors.New("not a kinozal details URL")
	}
	id, err := strconv.Atoi(m[1])
	if err != nil {
		return nil, fmt.Errorf("topic id: %w", err)
	}
	return &domain.Topic{
		TrackerName: pluginName,
		URL:         rawURL,
		DisplayName: fmt.Sprintf("Kinozal topic %d", id),
		Extra:       map[string]any{"topic_id": id},
	}, nil
}

// --- WithCredentials ---------------------------------------------------

func (p *plugin) Login(ctx context.Context, creds *domain.TrackerCredential) error {
	if creds == nil || creds.Username == "" {
		return errors.New("kinozal credentials are required")
	}
	sess := p.sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	if p.transport != nil {
		sess.Client.Transport = p.transport
	}
	form := url.Values{
		"username": {creds.Username},
		"password": {string(creds.SecretEnc)},
	}
	endpoint := "https://" + p.domain + "/takelogin.php"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return fmt.Errorf("kinozal login: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("kinozal login: read body: %w", err)
	}
	// Kinozal returns either a redirect to /index.php or a page with
	// "Неверный логин или пароль" on failure.
	if strings.Contains(string(body), "Неверный") {
		return errors.New("kinozal login failed: invalid credentials")
	}
	sess.LoggedIn = true
	return nil
}

func (p *plugin) Verify(ctx context.Context, creds *domain.TrackerCredential) (bool, error) {
	sess := p.sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+p.domain+"/", nil)
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
		return false, fmt.Errorf("kinozal verify: read body: %w", err)
	}
	// Kinozal shows a "Выход" link in the header when logged in.
	return strings.Contains(string(body), "logout.php") || strings.Contains(string(body), "Выход"), nil
}

// --- Check / Download --------------------------------------------------

var (
	titleRe   = regexp.MustCompile(`(?s)<title>([^<]+)</title>`)
	hashRe    = regexp.MustCompile(`(?i)Инфо хэш[^A-Z0-9]+([A-Fa-f0-9]{40})`)
	hashAltRe = regexp.MustCompile(`(?i)Info[\s_-]?hash[^A-Z0-9]+([A-Fa-f0-9]{40})`)
)

func (p *plugin) Check(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential) (*domain.Check, error) {
	body, err := p.fetch(ctx, topic.URL, creds)
	if err != nil {
		return nil, err
	}
	check := &domain.Check{}
	if m := titleRe.FindSubmatch(body); m != nil {
		check.DisplayName = strings.TrimSpace(string(m[1]))
		check.DisplayName = strings.TrimSuffix(check.DisplayName, " / Кинозал.ТВ")
	}
	if m := hashRe.FindSubmatch(body); m != nil {
		check.Hash = strings.ToLower(string(m[1]))
	} else if m := hashAltRe.FindSubmatch(body); m != nil {
		check.Hash = strings.ToLower(string(m[1]))
	} else {
		return nil, errors.New("kinozal: no infohash found in topic page")
	}
	return check, nil
}

func (p *plugin) Download(ctx context.Context, topic *domain.Topic, _ *domain.Check, creds *domain.TrackerCredential) (*domain.Payload, error) {
	id, _ := topic.Extra["topic_id"].(int)
	if id == 0 {
		// Topic might have been deserialized from JSON; cast may produce float64.
		if f, ok := topic.Extra["topic_id"].(float64); ok {
			id = int(f)
		}
	}
	if id == 0 {
		return nil, errors.New("kinozal: no topic_id in extras")
	}
	dlURL := "https://dl." + p.domain + "/download.php?id=" + strconv.Itoa(id)
	body, err := p.fetch(ctx, dlURL, creds)
	if err != nil {
		// Some Kinozal mirrors host downloads on the main domain.
		dlURL = "https://" + p.domain + "/download.php?id=" + strconv.Itoa(id)
		body, err = p.fetch(ctx, dlURL, creds)
		if err != nil {
			return nil, err
		}
	}
	return &domain.Payload{TorrentFile: body, FileName: fmt.Sprintf("kinozal-%d.torrent", id)}, nil
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
		return nil, fmt.Errorf("kinozal GET %s -> %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}
