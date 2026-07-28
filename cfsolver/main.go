// Command cfsolver is a tiny HTTP service that runs a headless Chromium
// instance and exposes a single endpoint that drives a target URL through
// any Cloudflare interstitial, then returns the resulting cookies and
// User-Agent so a regular HTTP client can re-use them.
//
// This is intentionally a separate process from the main Marauder backend:
//
//   - chromium is heavy (~150 MB image, ~200 MB resident set), and most
//     deployments will never use the solver at all
//   - the solver crashes and leaks much more often than the rest of the
//     stack, so it makes sense to be able to restart it independently
//   - the chromedp dependency tree is large, and bundling it into the
//     core backend would push the binary above 50 MB
//
// API:
//
//	POST /solve
//	{"url":"https://example.com/protected","timeout_seconds":30}
//
// Response:
//
//	{
//	  "ok": true,
//	  "user_agent": "Mozilla/5.0 (...)",
//	  "cookies": [
//	    {"name":"cf_clearance","value":"...","domain":".example.com",
//	     "path":"/","secure":true,"http_only":true,"expires":1234567890}
//	  ]
//	}
//
//	GET /health
//	200 "ok"
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/rs/zerolog"
)

const (
	// pollInterval is how often the solver re-checks whether the challenge
	// has cleared.
	pollInterval = 1 * time.Second

	// defaultUserAgent deliberately omits the "HeadlessChrome" token that
	// Chromium reports by default — Cloudflare fingerprints it directly.
	defaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"
)

// challengeMarkers are substrings unique to a Cloudflare interstitial. The
// solver inspects the rendered DOM because, inside the browser, the original
// response headers are not reachable.
var challengeMarkers = []string{
	"challenge-form",
	"cf-browser-verification",
	"_cf_chl_opt",
	"Just a moment",
	"cf-challenge-running",
}

// isChallengePage reports whether the rendered HTML is still a Cloudflare
// interstitial rather than the destination page.
func isChallengePage(html string) bool {
	for _, m := range challengeMarkers {
		if strings.Contains(html, m) {
			return true
		}
	}
	return false
}

// hasClearance reports whether Cloudflare has issued cf_clearance — the only
// cookie that actually proves the challenge was passed. It is HttpOnly, so it
// is visible over CDP but never via document.cookie.
//
// __cf_bm is deliberately NOT accepted: it is bot-management telemetry that
// Cloudflare sets on the interstitial itself, so honouring it would let the
// poll exit while still sitting on the challenge page and report success —
// the exact false-positive this service was repaired to stop producing.
func hasClearance(cookies []*network.Cookie) bool {
	for _, c := range cookies {
		if c.Name == "cf_clearance" {
			return true
		}
	}
	return false
}

type solveRequest struct {
	URL            string `json:"url"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	UserAgent      string `json:"user_agent,omitempty"`
}

type cookieView struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Secure   bool    `json:"secure"`
	HTTPOnly bool    `json:"http_only"`
	Expires  float64 `json:"expires"`
}

type solveResponse struct {
	OK        bool         `json:"ok"`
	UserAgent string       `json:"user_agent,omitempty"`
	Cookies   []cookieView `json:"cookies,omitempty"`
	Error     string       `json:"error,omitempty"`
}

func main() {
	logger := zerolog.New(os.Stdout).With().Timestamp().Str("service", "cfsolver").Logger()

	addr := envOrDefault("CFSOLVER_ADDR", ":9244")
	chromeURL := envOrDefault("CFSOLVER_CHROME_URL", "") // optional remote chrome
	logger.Info().Str("addr", addr).Str("chrome_url", chromeURL).Msg("starting")

	srv := &server{log: logger, chromeURL: chromeURL}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/solve", srv.handleSolve)

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal().Err(err).Msg("listen failed")
		}
	}()

	<-ctx.Done()
	logger.Info().Msg("shutting down")
	shCtx, shCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shCancel()
	_ = httpSrv.Shutdown(shCtx)
}

type server struct {
	log       zerolog.Logger
	chromeURL string

	// Serialise solve calls — chromium handles concurrent contexts but
	// the solver typically runs on tiny boxes so we keep it strict.
	mu sync.Mutex
}

func (s *server) handleSolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req solveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, solveResponse{Error: "invalid JSON"})
		return
	}
	if req.URL == "" {
		writeJSON(w, http.StatusBadRequest, solveResponse{Error: "url is required"})
		return
	}
	if req.TimeoutSeconds <= 0 {
		req.TimeoutSeconds = 45
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	resp, err := s.solve(r.Context(), req)
	if err != nil {
		s.log.Warn().Err(err).Str("url", req.URL).Msg("solve failed")
		writeJSON(w, http.StatusOK, solveResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) solve(parent context.Context, req solveRequest) (solveResponse, error) {
	allocOpts := append(
		chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("ignore-certificate-errors", true),
	)
	// Cloudflare binds a clearance cookie to the User-Agent that earned it,
	// so the UA must (a) not advertise automation and (b) be reported back to
	// the caller verbatim, which must replay it on every re-used request.
	ua := req.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}
	allocOpts = append(allocOpts,
		chromedp.UserAgent(ua),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(parent, allocOpts...)
	defer allocCancel()
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, time.Duration(req.TimeoutSeconds)*time.Second)
	defer timeout()

	var cookies []*network.Cookie
	var cleared bool
	// Captured for diagnostics: a timeout is far easier to interpret when the
	// error says what the browser was actually looking at.
	var lastTitle string
	var lastHTMLLen int

	err := chromedp.Run(ctx,
		chromedp.Navigate(req.URL),
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Poll until the challenge actually clears rather than sleeping a
			// fixed interval and hoping. Cloudflare only issues the clearance
			// cookie once its JS has run to completion.
			for {
				var html string
				if err := chromedp.Evaluate(`document.documentElement.outerHTML`, &html).Do(ctx); err != nil {
					return err
				}
				_ = chromedp.Evaluate(`document.title`, &lastTitle).Do(ctx)
				// network.GetCookies on the page context returns HttpOnly
				// cookies too (verified: it yields cf_clearance), so no
				// browser-level Storage call is needed here.
				c, err := network.GetCookies().Do(ctx)
				if err != nil {
					return err
				}
				cookies = c
				lastHTMLLen = len(html)
				if !isChallengePage(html) || hasClearance(c) {
					cleared = true
					return nil
				}
				select {
				case <-ctx.Done():
					// Deadline hit while still challenged: report honestly.
					return nil
				case <-time.After(pollInterval):
				}
			}
		}),
	)
	// A context deadline means "still challenged when time ran out", which is
	// a legitimate negative answer, not a transport failure.
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return solveResponse{}, fmt.Errorf("chromedp: %w", err)
	}
	if !cleared {
		return solveResponse{}, fmt.Errorf(
			"cloudflare challenge did not clear before timeout (title=%q html_bytes=%d cookies=%d)",
			lastTitle, lastHTMLLen, len(cookies))
	}

	s.log.Info().
		Str("url", req.URL).
		Str("title", lastTitle).
		Int("html_bytes", lastHTMLLen).
		Int("cookies", len(cookies)).
		Msg("solve completed")

	out := solveResponse{OK: true, UserAgent: ua}
	for _, c := range cookies {
		out.Cookies = append(out.Cookies, cookieView{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HTTPOnly: c.HTTPOnly,
			Expires:  c.Expires,
		})
	}
	return out, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func envOrDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
