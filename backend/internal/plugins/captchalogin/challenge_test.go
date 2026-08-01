package captchalogin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

func freshSession(context.Context) *forumcommon.Session {
	// A distinct store per call guarantees an independent cookie jar, the
	// invariant the engine relies on to keep concurrent logins apart.
	return forumcommon.New().GetOrCreate("interactive", "ua")
}

func TestBegin_ChallengeFrom_UsesPerChallengeImageURL(t *testing.T) {
	var captchaHits int32
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&captchaHits, 1)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xFF, 0xD8, 0xFF})
	}))
	defer imgSrv.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<input name="cap_sid" value="SID123">` +
			`<input name="cap_code_deadbeef" value="">`))
	}))
	defer srv.Close()

	cfg := Config{
		LoginURL:    srv.URL + "/login",
		CookieNames: []string{"bb_session"},
		BuildForm: func(c *domain.TrackerCredential, _ string, _ bool) url.Values {
			return url.Values{"login_username": {c.Username}}
		},
		Classify: func(body []byte) Outcome {
			if strings.Contains(string(body), "cap_sid") {
				return OutcomeNeedCaptcha
			}
			return OutcomeSuccess
		},
		ChallengeFrom: func([]byte) (ChallengeSpec, error) {
			return ChallengeSpec{
				ImageURL:    imgSrv.URL + "/captcha.jpg",
				Fields:      url.Values{"cap_sid": {"SID123"}},
				AnswerField: "cap_code_deadbeef",
			}, nil
		},
	}
	e := New(cfg, freshSession)

	challenge, cookies, err := e.Begin(context.Background(),
		&domain.TrackerCredential{Username: "bob"})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if cookies != nil {
		t.Fatal("cookies should be nil when a captcha is required")
	}
	if challenge == nil || challenge.ChallengeID == "" {
		t.Fatal("want a challenge with an ID")
	}
	if challenge.MIMEType != "image/jpeg" {
		t.Errorf("MIMEType = %q, want image/jpeg", challenge.MIMEType)
	}
	if atomic.LoadInt32(&captchaHits) != 1 {
		t.Errorf("captcha fetched %d times, want 1 from the per-challenge URL", captchaHits)
	}
}

func TestComplete_ChallengeFrom_SubmitsSidAndDynamicAnswerField(t *testing.T) {
	var posted url.Values
	var posts int32
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xFF})
	}))
	defer imgSrv.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if atomic.AddInt32(&posts, 1) == 1 {
			_, _ = w.Write([]byte(`<input name="cap_sid" value="SID123">`))
			return
		}
		posted = r.PostForm
		http.SetCookie(w, &http.Cookie{Name: "bb_session", Value: "SESS", Path: "/"})
		_, _ = w.Write([]byte(`<span id="logged-in-username">bob</span>`))
	}))
	defer srv.Close()

	cfg := Config{
		LoginURL:    srv.URL + "/login",
		CookieNames: []string{"bb_session"},
		BuildForm: func(c *domain.TrackerCredential, _ string, _ bool) url.Values {
			return url.Values{"login_username": {c.Username}}
		},
		Classify: func(body []byte) Outcome {
			if strings.Contains(string(body), "logged-in-username") {
				return OutcomeSuccess
			}
			return OutcomeNeedCaptcha
		},
		ChallengeFrom: func([]byte) (ChallengeSpec, error) {
			return ChallengeSpec{
				ImageURL:    imgSrv.URL + "/c.jpg",
				Fields:      url.Values{"cap_sid": {"SID123"}},
				AnswerField: "cap_code_deadbeef",
			}, nil
		},
	}
	e := New(cfg, freshSession)
	challenge, _, err := e.Begin(context.Background(), &domain.TrackerCredential{Username: "bob"})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	cookies, err := e.Complete(context.Background(), challenge.ChallengeID, "xxa")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if posted.Get("cap_sid") != "SID123" {
		t.Errorf("cap_sid = %q, want SID123", posted.Get("cap_sid"))
	}
	if posted.Get("cap_code_deadbeef") != "xxa" {
		t.Errorf("answer field = %q, want xxa", posted.Get("cap_code_deadbeef"))
	}
	if cookies["bb_session"] != "SESS" {
		t.Errorf("harvested = %v, want bb_session=SESS", cookies)
	}
}

// TestRefresh_ChallengeFrom_ReMintsChallenge covers the tracker shape where a
// new picture requires a new login attempt: RuTracker ties the image to the
// cap_sid issued with it, so re-fetching the old image URL would show the user
// a picture whose answer can no longer be submitted.
func TestRefresh_ChallengeFrom_ReMintsChallenge(t *testing.T) {
	var posts int32
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xFF})
	}))
	defer imgSrv.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&posts, 1)
		_, _ = w.Write([]byte(`<input name="cap_sid" value="SID">`))
	}))
	defer srv.Close()

	var specs int32
	e := New(Config{
		LoginURL:    srv.URL + "/login",
		CookieNames: []string{"bb_session"},
		BuildForm: func(c *domain.TrackerCredential, _ string, _ bool) url.Values {
			return url.Values{"login_username": {c.Username}}
		},
		Classify: func([]byte) Outcome { return OutcomeNeedCaptcha },
		ChallengeFrom: func([]byte) (ChallengeSpec, error) {
			atomic.AddInt32(&specs, 1)
			return ChallengeSpec{
				ImageURL:    imgSrv.URL + "/c.jpg",
				Fields:      url.Values{"cap_sid": {"SID"}},
				AnswerField: "cap_code_x",
			}, nil
		},
	}, freshSession)

	ch, _, err := e.Begin(context.Background(), &domain.TrackerCredential{Username: "bob"})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := e.Refresh(context.Background(), ch.ChallengeID); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if n := atomic.LoadInt32(&posts); n != 2 {
		t.Errorf("login posts = %d, want 2 (Refresh must re-post to mint a new captcha)", n)
	}
	if n := atomic.LoadInt32(&specs); n != 2 {
		t.Errorf("ChallengeFrom calls = %d, want 2", n)
	}
}

// TestComplete_StillAsksForCaptcha_IsRetryableAndKeepsPending covers a
// mistyped answer. RuTracker answers a bad code by re-rendering the login form
// with a FRESH cap_sid, which classifies as NeedCaptcha rather than
// WrongCaptcha. That must stay retryable: dropping it would delete the pending
// challenge and force the user to restart the whole flow over one typo.
func TestComplete_StillAsksForCaptcha_IsRetryableAndKeepsPending(t *testing.T) {
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xFF})
	}))
	defer imgSrv.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<input name="cap_sid" value="SID">`))
	}))
	defer srv.Close()

	e := New(Config{
		LoginURL:    srv.URL + "/login",
		CookieNames: []string{"bb_session"},
		BuildForm: func(c *domain.TrackerCredential, _ string, _ bool) url.Values {
			return url.Values{"login_username": {c.Username}}
		},
		Classify: func([]byte) Outcome { return OutcomeNeedCaptcha },
		ChallengeFrom: func([]byte) (ChallengeSpec, error) {
			return ChallengeSpec{
				ImageURL:    imgSrv.URL + "/c.jpg",
				Fields:      url.Values{"cap_sid": {"SID"}},
				AnswerField: "cap_code_x",
			}, nil
		},
	}, freshSession)

	ch, _, err := e.Begin(context.Background(), &domain.TrackerCredential{Username: "bob"})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := e.Complete(context.Background(), ch.ChallengeID, "wrong"); !errors.Is(err, ErrWrongCaptcha) {
		t.Fatalf("Complete err = %v, want ErrWrongCaptcha", err)
	}
	// The pending challenge must survive so Refresh can offer a new picture.
	if _, err := e.Refresh(context.Background(), ch.ChallengeID); err != nil {
		t.Fatalf("Refresh after a wrong answer: %v", err)
	}
}

// TestFetchCaptcha_RefusesOffHostRedirect is the second half of the SSRF fix.
// The plugin allowlists the captcha URL's host, but that check covers only the
// FIRST hop — a 302 from an allowlisted host to an internal address would
// otherwise be followed, and the body handed to the browser as a data: URL.
func TestFetchCaptcha_RefusesOffHostRedirect(t *testing.T) {
	var internalHits int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&internalHits, 1)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("SECRET-INTERNAL-RESPONSE"))
	}))
	defer internal.Close()

	// The "allowlisted" captcha host redirects off-site.
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/captcha") {
			http.Redirect(w, r, internal.URL+"/latest/meta-data", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(`<input name="cap_sid" value="SID">`))
	}))
	defer redirector.Close()

	e := New(Config{
		LoginURL:    redirector.URL + "/login",
		CookieNames: []string{"bb_session"},
		BuildForm: func(c *domain.TrackerCredential, _ string, _ bool) url.Values {
			return url.Values{"login_username": {c.Username}}
		},
		Classify: func([]byte) Outcome { return OutcomeNeedCaptcha },
		ChallengeFrom: func([]byte) (ChallengeSpec, error) {
			return ChallengeSpec{
				ImageURL:    redirector.URL + "/captcha/x.jpg",
				Fields:      url.Values{"cap_sid": {"SID"}},
				AnswerField: "cap_code_x",
			}, nil
		},
	}, freshSession)

	_, _, err := e.Begin(context.Background(), &domain.TrackerCredential{Username: "bob"})
	if err == nil {
		t.Fatal("Begin succeeded; the off-host redirect must not be followed")
	}
	if n := atomic.LoadInt32(&internalHits); n != 0 {
		t.Errorf("internal host was fetched %d times — the redirect guard did not hold", n)
	}
}

// TestBegin_NilChallengeFrom_KeepsStaticCaptchaURL guards the LostFilm path: an
// unset ChallengeFrom must not change anything.
func TestBegin_NilChallengeFrom_KeepsStaticCaptchaURL(t *testing.T) {
	var staticHits int32
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&staticHits, 1)
		w.Header().Set("Content-Type", "image/gif")
		_, _ = w.Write([]byte("GIF89a"))
	}))
	defer imgSrv.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"need_captcha":true}`))
	}))
	defer srv.Close()

	e := New(Config{
		LoginURL:    srv.URL,
		CaptchaURL:  imgSrv.URL,
		CookieNames: []string{"lf_session"},
		BuildForm: func(c *domain.TrackerCredential, answer string, _ bool) url.Values {
			return url.Values{"mail": {c.Username}, "captcha": {answer}}
		},
		Classify: func([]byte) Outcome { return OutcomeNeedCaptcha },
	}, freshSession)

	ch, _, err := e.Begin(context.Background(), &domain.TrackerCredential{Username: "bob"})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if ch.MIMEType != "image/gif" {
		t.Errorf("MIMEType = %q", ch.MIMEType)
	}
	if atomic.LoadInt32(&staticHits) != 1 {
		t.Errorf("static captcha hits = %d, want 1", staticHits)
	}
}
