package captchalogin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

type Outcome int

const (
	OutcomeSuccess Outcome = iota
	OutcomeNeedCaptcha
	OutcomeWrongCaptcha
	OutcomeFailed
)

// ErrWrongCaptcha is returned by Complete when the answer was rejected;
// the pending session is kept so the caller can Refresh and retry.
var ErrWrongCaptcha = errors.New("captcha answer incorrect")

// Config is the tracker-specific configuration for an interactive login.
type Config struct {
	SeedURL     string // optional: GET'd first to seed a session cookie
	LoginURL    string
	CaptchaURL  string
	CookieNames []string
	BuildForm   func(c *domain.TrackerCredential, answer string, needCaptcha bool) url.Values
	Classify    func(body []byte) Outcome
}

// Engine runs the Config's interactive login flow.
type Engine struct {
	cfg   Config
	store *pendingStore
	// newSess MUST return a fresh, independent session (its own cookie jar) on every call. The engine holds one session per pending challenge for that challenge's lifetime and never shares a session across challenges; a newSess that returns a shared/cached session would let concurrent logins cross-contaminate captcha cookies.
	newSess func() *forumcommon.Session // injects a jar (+ test transport)
}

// New builds an Engine. newSess MUST return a fresh, independent session (its own cookie jar) on every call. The engine holds one session per pending challenge for that challenge's lifetime and never shares a session across challenges; a newSess that returns a shared/cached session would let concurrent logins cross-contaminate captcha cookies.
func New(cfg Config, newSess func() *forumcommon.Session) *Engine {
	return &Engine{cfg: cfg, store: newPendingStore(), newSess: newSess}
}

func (e *Engine) post(ctx context.Context, sess *forumcommon.Session, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.LoginURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", sess.UserAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 64*1024))
}

func (e *Engine) fetchCaptcha(ctx context.Context, sess *forumcommon.Session) (*registry.LoginChallenge, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.cfg.CaptchaURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", sess.UserAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	img, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	mime := resp.Header.Get("Content-Type")
	if mime == "" {
		mime = "image/gif"
	}
	return &registry.LoginChallenge{Image: img, MIMEType: mime}, nil
}

// Begin starts a login. Returns (challenge, nil) when a captcha is needed,
// or (nil, cookies) when login succeeded outright.
func (e *Engine) Begin(ctx context.Context, creds *domain.TrackerCredential) (*registry.LoginChallenge, registry.SessionCookies, error) {
	sess := e.newSess()
	if e.cfg.SeedURL != "" {
		if req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, e.cfg.SeedURL, nil); rerr == nil {
			req.Header.Set("User-Agent", sess.UserAgent)
			if resp, err := sess.Client.Do(req); err == nil {
				resp.Body.Close()
			}
		}
	}
	body, err := e.post(ctx, sess, e.cfg.BuildForm(creds, "", false))
	if err != nil {
		return nil, nil, fmt.Errorf("interactive begin: %w", err)
	}
	switch e.cfg.Classify(body) {
	case OutcomeSuccess:
		return nil, e.harvest(sess), nil
	case OutcomeNeedCaptcha:
		challenge, ferr := e.fetchCaptcha(ctx, sess)
		if ferr != nil {
			return nil, nil, fmt.Errorf("interactive begin: fetch captcha: %w", ferr)
		}
		id, perr := e.store.put(sess, creds)
		if perr != nil {
			return nil, nil, perr
		}
		challenge.ChallengeID = id
		return challenge, nil, nil
	default:
		return nil, nil, errors.New("interactive begin: login rejected")
	}
}

// Refresh re-fetches the captcha image on the existing pending jar.
func (e *Engine) Refresh(ctx context.Context, challengeID string) (*registry.LoginChallenge, error) {
	p, ok := e.store.get(challengeID)
	if !ok {
		return nil, errors.New("unknown or expired challenge")
	}
	challenge, err := e.fetchCaptcha(ctx, p.sess)
	if err != nil {
		return nil, err
	}
	challenge.ChallengeID = challengeID
	return challenge, nil
}

// Complete submits the answer on the pending jar and harvests cookies.
func (e *Engine) Complete(ctx context.Context, challengeID, answer string) (registry.SessionCookies, error) {
	p, ok := e.store.get(challengeID)
	if !ok {
		return nil, errors.New("unknown or expired challenge")
	}
	body, err := e.post(ctx, p.sess, e.cfg.BuildForm(p.creds, answer, true))
	if err != nil {
		e.store.del(challengeID)
		return nil, fmt.Errorf("interactive complete: %w", err)
	}
	switch e.cfg.Classify(body) {
	case OutcomeSuccess:
		cookies := e.harvest(p.sess)
		e.store.del(challengeID)
		return cookies, nil
	case OutcomeWrongCaptcha:
		return nil, ErrWrongCaptcha // keep pending for Refresh + retry
	default:
		e.store.del(challengeID)
		return nil, errors.New("interactive complete: login rejected")
	}
}

func (e *Engine) harvest(sess *forumcommon.Session) registry.SessionCookies {
	// Auth cookies are assumed to live on the LoginURL host. Trackers whose auth cookie is set on a different host than LoginURL are not supported by this harvest.
	u, _ := url.Parse(e.cfg.LoginURL)
	return registry.SessionCookies(forumcommon.CookiesByName(sess, u, e.cfg.CookieNames))
}
