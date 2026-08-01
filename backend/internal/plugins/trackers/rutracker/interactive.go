package rutracker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/captchalogin"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

var _ registry.WithInteractiveLogin = (*plugin)(nil)

var (
	capSidRe   = regexp.MustCompile(`name="cap_sid"[^>]*value="([^"]+)"`)
	capFieldRe = regexp.MustCompile(`name="(cap_code_[a-f0-9]+)"`)
	capImgRe   = regexp.MustCompile(`<img[^>]+src="(https?://[^"]*captcha[^"]*)"`)
)

// classifyLogin maps a RuTracker login response to an Outcome.
//
// The captcha is adaptive: RuTracker imposes it on a client it distrusts (a run
// of failed attempts is enough) and drops it again after a success. So
// NeedCaptcha is a normal, transient state rather than a property of the site,
// and a plain credential login is the expected path.
//
// A wrong answer is reported as NeedCaptcha rather than WrongCaptcha because
// RuTracker re-renders the form with a FRESH cap_sid. The previous pending
// challenge is dead either way, so the caller must be handed the new picture
// rather than allowed to retry against the old one.
func classifyLogin(body []byte) captchalogin.Outcome {
	if bytes.Contains(body, []byte(`id="logged-in-username"`)) {
		return captchalogin.OutcomeSuccess
	}
	if bytes.Contains(body, []byte("cap_sid")) {
		return captchalogin.OutcomeNeedCaptcha
	}
	return captchalogin.OutcomeFailed
}

// parseChallenge extracts one captcha instance from a login response.
//
// All three parts are per-attempt: the image lives on static.rutracker.cc
// under a content hash, cap_sid pairs the answer with that image, and the
// answer field is named cap_code_<md5>. None can be hard-coded in the config,
// which is why the engine grew ChallengeFrom.
func parseChallenge(body []byte) (captchalogin.ChallengeSpec, error) {
	sid := capSidRe.FindSubmatch(body)
	field := capFieldRe.FindSubmatch(body)
	img := capImgRe.FindSubmatch(body)
	if sid == nil || field == nil || img == nil {
		return captchalogin.ChallengeSpec{},
			errors.New("rutracker: login page has no parseable captcha challenge")
	}
	return captchalogin.ChallengeSpec{
		ImageURL:    string(img[1]),
		Fields:      url.Values{"cap_sid": {string(sid[1])}},
		AnswerField: string(field[1]),
	}, nil
}

// captchaConfig is the RuTracker-specific interactive-login configuration.
// CaptchaURL is intentionally empty: ChallengeFrom supplies a per-attempt URL.
func (p *plugin) captchaConfig() captchalogin.Config {
	return captchalogin.Config{
		LoginURL:      "https://" + p.effectiveDomain() + "/forum/login.php",
		CookieNames:   []string{"bb_session"},
		ChallengeFrom: parseChallenge,
		Classify:      classifyLogin,
		BuildForm: func(c *domain.TrackerCredential, _ string, _ bool) url.Values {
			// The answer is placed by the engine under the per-challenge
			// AnswerField name, so it is deliberately absent here.
			return url.Values{
				"login_username": {c.Username},
				"login_password": {string(c.SecretEnc)},
				"login":          {"вход"},
				"redirect":       {"index.php"},
			}
		},
	}
}

// newInteractiveSession returns a FRESH, independent session on every call —
// the invariant captchalogin.Engine relies on so concurrent logins cannot
// cross-contaminate captcha cookies.
//
// Each one is seeded with the Cloudflare clearance: without it login.php
// answers with a 403 challenge page rather than a form, and the captcha parse
// would fail with a confusing "no parseable captcha" error.
func (p *plugin) newInteractiveSession() *forumcommon.Session {
	sess := forumcommon.New().GetOrCreate(
		forumcommon.SessionKey(pluginName, "interactive"), userAgent)
	if tr := p.effectiveTransport(); tr != nil {
		sess.Client.Transport = tr
	}
	u, err := url.Parse("https://" + p.effectiveDomain() + "/forum/")
	if err != nil {
		return sess
	}
	if c := p.applyClearance(context.Background(), sess, u); c.Valid() {
		// The engine sends sess.UserAgent on both the login POST and the
		// captcha fetch; it must match the UA the clearance was minted with.
		sess.UserAgent = c.UserAgent
	}
	return sess
}

func (p *plugin) eng() *captchalogin.Engine {
	p.engineOnce.Do(func() {
		p.engine = captchalogin.New(p.captchaConfig(), p.newInteractiveSession)
	})
	return p.engine
}

// BeginLogin implements registry.WithInteractiveLogin. It returns a captcha
// only when RuTracker actually demands one; in the common case it returns the
// harvested session and the user never sees a picture.
func (p *plugin) BeginLogin(ctx context.Context, creds *domain.TrackerCredential) (*registry.LoginChallenge, registry.SessionCookies, error) {
	if creds == nil || creds.Username == "" {
		return nil, nil, fmt.Errorf("rutracker: credentials are required")
	}
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
