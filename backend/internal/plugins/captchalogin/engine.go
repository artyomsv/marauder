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

// ChallengeSpec describes one captcha instance extracted from a login
// response. It exists for trackers whose captcha is not a fixed endpoint:
// RuTracker mints a new image URL per attempt on a separate static host,
// echoes a cap_sid back, and names the answer field cap_code_<md5>.
type ChallengeSpec struct {
	// ImageURL is the absolute URL of this challenge's captcha image.
	ImageURL string
	// Fields are hidden inputs that must be replayed alongside the answer.
	Fields url.Values
	// AnswerField is the form field the user's answer belongs in. Empty means
	// BuildForm already places the answer itself.
	AnswerField string
}

// Config is the tracker-specific configuration for an interactive login.
type Config struct {
	SeedURL     string // optional: GET'd first to seed a session cookie
	LoginURL    string
	CaptchaURL  string
	CookieNames []string
	BuildForm   func(c *domain.TrackerCredential, answer string, needCaptcha bool) url.Values
	Classify    func(body []byte) Outcome
	// ChallengeFrom, when set, extracts the per-challenge captcha details from
	// the body of the login response that demanded one. Leave nil for trackers
	// with a fixed CaptchaURL and a fixed answer field name (LostFilm).
	ChallengeFrom func(body []byte) (ChallengeSpec, error)
}

// Engine runs the Config's interactive login flow.
type Engine struct {
	cfg   Config
	store *pendingStore
	// newSess MUST return a fresh, independent session (its own cookie jar) on every call. The engine holds one session per pending challenge for that challenge's lifetime and never shares a session across challenges; a newSess that returns a shared/cached session would let concurrent logins cross-contaminate captcha cookies.
	newSess func(context.Context) *forumcommon.Session // injects a jar (+ test transport)
}

// New builds an Engine. newSess MUST return a fresh, independent session (its own cookie jar) on every call. The engine holds one session per pending challenge for that challenge's lifetime and never shares a session across challenges; a newSess that returns a shared/cached session would let concurrent logins cross-contaminate captcha cookies.
func New(cfg Config, newSess func(context.Context) *forumcommon.Session) *Engine {
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

func (e *Engine) fetchCaptcha(ctx context.Context, sess *forumcommon.Session, imageURL string) (*registry.LoginChallenge, error) {
	if imageURL == "" {
		return nil, errors.New("captcha image URL is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", sess.UserAgent)
	// Fetch through a client that refuses to follow a redirect off the host we
	// were pointed at. When ChallengeFrom supplies the URL it comes from
	// tracker-controlled HTML; the plugin allowlists the host, but a redirect
	// would sidestep that check and let the response body — returned to the
	// browser as a data: URL — come from an internal address instead. Same jar
	// and transport, so cookies and any test transport still apply.
	client := &http.Client{
		Jar:       sess.Client.Jar,
		Transport: sess.Client.Transport,
		Timeout:   sess.Client.Timeout,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) > 0 && r.URL.Host != via[0].URL.Host {
				return fmt.Errorf("captcha redirected off-host to %q", r.URL.Host)
			}
			return nil
		},
	}
	resp, err := client.Do(req)
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
	sess := e.newSess(ctx)
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
		spec, serr := e.specFrom(body)
		if serr != nil {
			return nil, nil, fmt.Errorf("interactive begin: parse challenge: %w", serr)
		}
		challenge, ferr := e.fetchCaptcha(ctx, sess, spec.ImageURL)
		if ferr != nil {
			return nil, nil, fmt.Errorf("interactive begin: fetch captcha: %w", ferr)
		}
		id, perr := e.store.put(sess, creds, spec)
		if perr != nil {
			return nil, nil, perr
		}
		challenge.ChallengeID = id
		return challenge, nil, nil
	default:
		return nil, nil, errors.New("interactive begin: login rejected")
	}
}

// specFrom resolves the challenge details for a login response. With no
// ChallengeFrom configured this is just the statically configured CaptchaURL,
// which is the LostFilm shape.
func (e *Engine) specFrom(body []byte) (ChallengeSpec, error) {
	if e.cfg.ChallengeFrom == nil {
		return ChallengeSpec{ImageURL: e.cfg.CaptchaURL}, nil
	}
	spec, err := e.cfg.ChallengeFrom(body)
	if err != nil {
		return ChallengeSpec{}, err
	}
	if spec.ImageURL == "" {
		spec.ImageURL = e.cfg.CaptchaURL
	}
	return spec, nil
}

// Refresh obtains a new captcha for a pending challenge.
//
// With ChallengeFrom set the image cannot simply be re-fetched: RuTracker ties
// the picture to the cap_sid issued alongside it, so showing a new image
// without a new cap_sid would give the user a puzzle whose answer can no
// longer be submitted. Re-post the login on the pending jar and re-extract.
func (e *Engine) Refresh(ctx context.Context, challengeID string) (*registry.LoginChallenge, error) {
	p, ok := e.store.get(challengeID)
	if !ok {
		return nil, errors.New("unknown or expired challenge")
	}
	spec := p.spec
	if e.cfg.ChallengeFrom != nil {
		body, err := e.post(ctx, p.sess, e.cfg.BuildForm(p.creds, "", false))
		if err != nil {
			return nil, fmt.Errorf("interactive refresh: %w", err)
		}
		if e.cfg.Classify(body) != OutcomeNeedCaptcha {
			return nil, errors.New("interactive refresh: tracker no longer offers a captcha")
		}
		if spec, err = e.specFrom(body); err != nil {
			return nil, fmt.Errorf("interactive refresh: parse challenge: %w", err)
		}
		e.store.updateSpec(challengeID, spec)
	}
	challenge, err := e.fetchCaptcha(ctx, p.sess, spec.ImageURL)
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
	form := e.cfg.BuildForm(p.creds, answer, true)
	// Replay the hidden fields that pair this answer with its picture, and
	// place the answer under the per-challenge field name when the tracker
	// mints one (RuTracker's cap_code_<md5>).
	for k, vs := range p.spec.Fields {
		form[k] = vs
	}
	if p.spec.AnswerField != "" {
		form.Set(p.spec.AnswerField, answer)
	}
	body, err := e.post(ctx, p.sess, form)
	if err != nil {
		e.store.del(challengeID)
		return nil, fmt.Errorf("interactive complete: %w", err)
	}
	switch e.cfg.Classify(body) {
	case OutcomeSuccess:
		cookies := e.harvest(p.sess)
		e.store.del(challengeID)
		return cookies, nil
	case OutcomeWrongCaptcha, OutcomeNeedCaptcha:
		// Still asking for a captcha after we answered one means the answer
		// did not take. Treat it as a wrong answer and KEEP the pending
		// session so the caller can Refresh for a new picture and retry.
		//
		// NeedCaptcha lands here because some trackers do not distinguish the
		// two: RuTracker simply re-renders the login form with a fresh
		// cap_sid. Dropping it into the default branch below would delete the
		// pending challenge and force the user to restart the whole flow after
		// a single mistyped character.
		return nil, ErrWrongCaptcha
	default:
		e.store.del(challengeID)
		return nil, errors.New("interactive complete: login rejected")
	}
}

func (e *Engine) harvest(sess *forumcommon.Session) registry.SessionCookies {
	// Auth cookies are assumed to live on the LoginURL host. Trackers whose auth cookie is set on a different host than LoginURL are not supported by this harvest. LoginURL is trusted plugin config, so the parse error is unreachable.
	u, _ := url.Parse(e.cfg.LoginURL)
	return registry.SessionCookies(forumcommon.CookiesByName(sess, u, e.cfg.CookieNames))
}
