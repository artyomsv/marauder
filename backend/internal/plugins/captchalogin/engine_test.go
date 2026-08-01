package captchalogin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

func testEngine(t *testing.T, handler http.HandlerFunc) *Engine {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	cfg := Config{
		LoginURL:    srv.URL + "/login",
		CaptchaURL:  srv.URL + "/captcha",
		CookieNames: []string{"sid"},
		BuildForm: func(c *domain.TrackerCredential, answer string, needCaptcha bool) url.Values {
			v := url.Values{"mail": {c.Username}, "captcha": {answer}}
			if needCaptcha {
				v.Set("need_captcha", "1")
			}
			return v
		},
		Classify: func(body []byte) Outcome {
			s := string(body)
			switch {
			case strings.Contains(s, "wrong"):
				return OutcomeWrongCaptcha
			case strings.Contains(s, "captcha"):
				return OutcomeNeedCaptcha
			case strings.Contains(s, "ok"):
				return OutcomeSuccess
			default:
				return OutcomeFailed
			}
		},
	}
	return New(cfg, func(context.Context) *forumcommon.Session { return forumcommon.New().GetOrCreate("t", "ua") })
}

func TestBegin_SuccessNoCaptcha(t *testing.T) {
	e := testEngine(t, func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "S1"})
		w.Write([]byte(`{"result":"ok"}`))
	})
	ch, cookies, err := e.Begin(context.Background(), &domain.TrackerCredential{Username: "a"})
	if err != nil || ch != nil {
		t.Fatalf("want no challenge, got ch=%v err=%v", ch, err)
	}
	if cookies["sid"] != "S1" {
		t.Errorf("sid = %q, want S1", cookies["sid"])
	}
}

func TestBeginComplete_CaptchaHappyPath(t *testing.T) {
	e := testEngine(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			r.ParseForm()
			if r.Form.Get("need_captcha") == "1" {
				http.SetCookie(w, &http.Cookie{Name: "sid", Value: "S2"})
				w.Write([]byte(`{"result":"ok"}`))
				return
			}
			w.Write([]byte(`{"need_captcha":true}`))
		case "/captcha":
			w.Header().Set("Content-Type", "image/gif")
			w.Write([]byte("GIF87a-bytes"))
		}
	})
	ch, _, err := e.Begin(context.Background(), &domain.TrackerCredential{Username: "a"})
	if err != nil || ch == nil {
		t.Fatalf("want challenge, got ch=%v err=%v", ch, err)
	}
	if ch.ChallengeID == "" || string(ch.Image) != "GIF87a-bytes" || ch.MIMEType != "image/gif" {
		t.Fatalf("bad challenge: %+v", ch)
	}
	cookies, err := e.Complete(context.Background(), ch.ChallengeID, "1234")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if cookies["sid"] != "S2" {
		t.Errorf("sid = %q, want S2", cookies["sid"])
	}
}

func TestComplete_WrongCaptchaKeepsPending(t *testing.T) {
	e := testEngine(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			r.ParseForm()
			if r.Form.Get("need_captcha") == "1" {
				w.Write([]byte(`{"wrong":4}`))
				return
			}
			w.Write([]byte(`{"need_captcha":true}`))
		case "/captcha":
			w.Write([]byte("img"))
		}
	})
	ch, _, _ := e.Begin(context.Background(), &domain.TrackerCredential{Username: "a"})
	_, err := e.Complete(context.Background(), ch.ChallengeID, "bad")
	if !errors.Is(err, ErrWrongCaptcha) {
		t.Fatalf("err = %v, want ErrWrongCaptcha", err)
	}
	if _, err := e.Refresh(context.Background(), ch.ChallengeID); err != nil {
		t.Errorf("Refresh after wrong captcha should work, got %v", err)
	}
}

func TestComplete_UnknownChallenge(t *testing.T) {
	e := testEngine(t, func(w http.ResponseWriter, r *http.Request) {})
	if _, err := e.Complete(context.Background(), "nope", "x"); err == nil {
		t.Error("want error for unknown challenge")
	}
}

func TestPending_TTLEviction(t *testing.T) {
	e := testEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			w.Write([]byte(`{"need_captcha":true}`))
		} else {
			w.Write([]byte("img"))
		}
	})
	ch, _, _ := e.Begin(context.Background(), &domain.TrackerCredential{Username: "a"})
	e.store.mu.Lock()
	for _, v := range e.store.m {
		v.expires = v.expires.Add(-2 * pendingTTL)
	}
	e.store.mu.Unlock()
	if _, ok := e.store.get(ch.ChallengeID); ok {
		t.Error("expired entry should have been evicted")
	}
}

func TestBegin_HardFailRejected(t *testing.T) {
	e := testEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"nope":1}`))
	})
	ch, cookies, err := e.Begin(context.Background(), &domain.TrackerCredential{Username: "a"})
	if err == nil {
		t.Fatal("want error for hard-fail login, got nil")
	}
	if ch != nil || cookies != nil {
		t.Errorf("want nil challenge and cookies, got ch=%v cookies=%v", ch, cookies)
	}
}

func TestBegin_FetchCaptchaError(t *testing.T) {
	e := testEngine(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			w.Write([]byte(`{"need_captcha":true}`))
		case "/captcha":
			// Abort the response mid-flight to force a client-side transport error.
			panic(http.ErrAbortHandler)
		}
	})
	ch, cookies, err := e.Begin(context.Background(), &domain.TrackerCredential{Username: "a"})
	if err == nil {
		t.Fatal("want error when captcha fetch fails, got nil")
	}
	if !strings.Contains(err.Error(), "fetch captcha") {
		t.Errorf("err = %v, want mention of \"fetch captcha\"", err)
	}
	if ch != nil || cookies != nil {
		t.Errorf("want nil challenge and cookies, got ch=%v cookies=%v", ch, cookies)
	}
}
