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
//   POST /solve
//   {"url":"https://example.com/protected","timeout_seconds":30}
//
// Response:
//
//   {
//     "ok": true,
//     "user_agent": "Mozilla/5.0 (...)",
//     "cookies": [
//       {"name":"cf_clearance","value":"...","domain":".example.com",
//        "path":"/","secure":true,"http_only":true,"expires":1234567890}
//     ]
//   }
//
//   GET /health
//   200 "ok"
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/rs/zerolog"
)

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
	if req.UserAgent != "" {
		allocOpts = append(allocOpts, chromedp.UserAgent(req.UserAgent))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(parent, allocOpts...)
	defer allocCancel()
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, time.Duration(req.TimeoutSeconds)*time.Second)
	defer timeout()

	var ua string
	var cookies []*network.Cookie

	err := chromedp.Run(ctx,
		chromedp.Navigate(req.URL),
		chromedp.Sleep(8*time.Second),
		chromedp.Evaluate(`navigator.userAgent`, &ua),
		chromedp.ActionFunc(func(ctx context.Context) error {
			c, err := network.GetCookies().Do(ctx)
			if err != nil {
				return err
			}
			cookies = c
			return nil
		}),
	)
	if err != nil {
		return solveResponse{}, fmt.Errorf("chromedp: %w", err)
	}

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
