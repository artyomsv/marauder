package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/metrics"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/problem"
)

const (
	searchQueryMaxRunes           = 200
	defaultSearchPerTrackerBudget = 15 * time.Second
	searchCooldown                = 2 * time.Second
)

// Stable per-tracker error codes for the search response. The frontend
// switches on these; the human-readable text is a canned message, never a
// raw Go error chain — transport errors embed admin-configured mirror
// hosts, resolved IPs, and proxy topology that non-admin users must not
// see (the raw error is server-logged instead).
const (
	searchErrNoCredentials = "no_credentials"
	searchErrLoginFailed   = "login_failed"
	// Neither of these is the account's fault. Reporting them as login_failed
	// sent a user with perfectly good credentials to re-enter them — issue #158
	// on the search surface, which is login-gated on RuTracker and so turns any
	// Cloudflare block into an apparent auth failure.
	searchErrSolverMissing = "solver_missing"
	searchErrSolver        = "solver"
	searchErrTimeout       = "timeout"
	searchErrFailed        = "failed"
)

var searchErrMessages = map[string]string{
	searchErrNoCredentials: "search requires credentials",
	searchErrLoginFailed:   "tracker login failed",
	searchErrSolverMissing: "no Cloudflare solver is configured",
	searchErrSolver:        "the Cloudflare solver did not answer",
	searchErrTimeout:       "search timed out",
	searchErrFailed:        "search failed",
}

// searchResultView is one row of GET /trackers/search — a registry result
// plus which tracker produced it.
type searchResultView struct {
	TrackerName        string `json:"tracker_name"`
	TrackerDisplayName string `json:"tracker_display_name"`
	registry.SearchResult
}

// searchErrorView reports one tracker's failure without failing the whole
// search (per-tracker fail-open). Code is one of the searchErr* constants;
// Error is the matching canned message (kept for display fallback).
type searchErrorView struct {
	TrackerName        string `json:"tracker_name"`
	TrackerDisplayName string `json:"tracker_display_name"`
	Code               string `json:"code"`
	Error              string `json:"error"`
}

func newSearchErrorView(ws registry.WithSearch, code string) *searchErrorView {
	return &searchErrorView{
		TrackerName:        ws.Name(),
		TrackerDisplayName: ws.DisplayName(),
		Code:               code,
		Error:              searchErrMessages[code],
	}
}

// Search handles GET /api/v1/trackers/search?q=<query>&trackers=<csv>.
// Fans out to every WithSearch tracker concurrently; per-tracker failures
// degrade to entries in `errors` (fail-open) — only a bad request shape
// produces a non-200. Two abuse gates protect the trackers (every call
// triggers real scraping requests, and hammering them is how instances
// get IP-banned — and a dead session means each search replays a real
// login): a per-user single-flight (concurrent search → 429) and a short
// per-user cooldown between sequential searches (→ 429).
//
// Deliberate non-behaviour: search failures do NOT feed
// domains.Store.ReportFailure — domain rotation stays scheduler-driven so
// an interactive search hitting a cold mirror can't spin the ring the
// scheduler depends on.
func (h *Trackers) Search(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("q query parameter is required"))
		return
	}
	if utf8.RuneCountInString(q) > searchQueryMaxRunes {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest(
			fmt.Sprintf("query too long (max %d characters)", searchQueryMaxRunes)))
		return
	}
	var filter map[string]bool
	if raw := strings.TrimSpace(r.URL.Query().Get("trackers")); raw != "" {
		filter = map[string]bool{}
		for _, n := range strings.Split(raw, ",") {
			if n = strings.TrimSpace(n); n != "" {
				filter[n] = true
			}
		}
	}
	if _, busy := h.searchInFlight.LoadOrStore(uid, struct{}{}); busy {
		problem.Write(w, r, h.BaseURL, problem.ErrTooManyRequests("a search is already running; wait for it to finish"))
		return
	}
	defer h.searchInFlight.Delete(uid)
	if last, ok := h.searchLast.Load(uid); ok && time.Since(last.(time.Time)) < searchCooldown {
		problem.Write(w, r, h.BaseURL, problem.ErrTooManyRequests("searching too fast; wait a moment"))
		return
	}
	// Stamp at completion, not admission: a search that itself takes longer
	// than the cooldown would otherwise leave no cooldown at all. Must be a
	// closure — a plain `defer Store(uid, time.Now())` evaluates time.Now()
	// at the defer statement (admission time again). Registered only after
	// the gate passes so rejected attempts can't keep extending the window.
	defer func() { h.searchLast.Store(uid, time.Now()) }()

	var searchers []registry.WithSearch
	for _, t := range registry.ListTrackers() {
		ws, ok := t.(registry.WithSearch)
		if !ok || (filter != nil && !filter[t.Name()]) {
			continue
		}
		searchers = append(searchers, ws)
	}

	type outcome struct {
		results []searchResultView
		errView *searchErrorView
	}
	outcomes := make([]outcome, len(searchers))
	var wg sync.WaitGroup
	for i, ws := range searchers {
		wg.Add(1)
		go func(i int, ws registry.WithSearch) {
			defer wg.Done()
			// A panic in a plugin's Search must not kill the process: chi's
			// Recoverer only covers the handler goroutine, not these children.
			defer func() {
				if rec := recover(); rec != nil {
					log.Error().Str("tracker", ws.Name()).Any("panic", rec).
						Msg("tracker search panicked")
					metrics.TrackerSearchTotal.WithLabelValues(ws.Name(), "error").Inc()
					outcomes[i] = outcome{errView: newSearchErrorView(ws, searchErrFailed)}
				}
			}()
			ctx, cancel := context.WithTimeout(r.Context(), h.perTrackerBudget())
			defer cancel()
			name := ws.Name()
			creds, loginFailed, loginErr := h.warmCredentials(ctx, uid, ws)
			results, err := ws.Search(ctx, q, creds)
			switch {
			case errors.Is(err, registry.ErrSearchRequiresCredentials):
				// A stored-but-unusable credential is a different user story
				// ("your login broke") than a missing one ("add an account") —
				// and a Cloudflare block is neither. On a login-gated searcher
				// the solver states arrive here wearing a login failure's
				// clothes, so they must be read off the login error rather than
				// inferred from the fact that warming failed.
				code := searchErrNoCredentials
				switch {
				case errors.Is(loginErr, registry.ErrClearanceNotConfigured):
					code = searchErrSolverMissing
				case errors.Is(loginErr, registry.ErrClearanceUnavailable):
					code = searchErrSolver
				case loginFailed:
					code = searchErrLoginFailed
				}
				metrics.TrackerSearchTotal.WithLabelValues(name, code).Inc()
				outcomes[i] = outcome{errView: newSearchErrorView(ws, code)}
			case err != nil:
				// The same three-way split as above, for a searcher that fails
				// outright rather than by reporting missing credentials.
				code := searchErrFailed
				switch {
				case errors.Is(err, registry.ErrClearanceNotConfigured):
					code = searchErrSolverMissing
				case errors.Is(err, registry.ErrClearanceUnavailable):
					code = searchErrSolver
				case errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil:
					code = searchErrTimeout
				}
				metrics.TrackerSearchTotal.WithLabelValues(name, "error").Inc()
				log.Warn().Str("tracker", name).Err(err).Msg("tracker search failed")
				outcomes[i] = outcome{errView: newSearchErrorView(ws, code)}
			default:
				metrics.TrackerSearchTotal.WithLabelValues(name, "ok").Inc()
				views := make([]searchResultView, 0, len(results))
				for _, res := range results {
					views = append(views, searchResultView{
						TrackerName:        name,
						TrackerDisplayName: ws.DisplayName(),
						SearchResult:       res,
					})
				}
				outcomes[i] = outcome{results: views}
			}
		}(i, ws)
	}
	wg.Wait()

	results := []searchResultView{}
	errViews := []searchErrorView{}
	for _, o := range outcomes {
		results = append(results, o.results...)
		if o.errView != nil {
			errViews = append(errViews, *o.errView)
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Seeders != results[j].Seeders {
			return results[i].Seeders > results[j].Seeders // unknown (-1) sorts last
		}
		return results[i].TrackerName < results[j].TrackerName
	})
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "errors": errViews})
}

// perTrackerBudget returns the per-tracker search timeout — the test seam
// SearchBudget overrides the 15s default.
func (h *Trackers) perTrackerBudget() time.Duration {
	if h.SearchBudget > 0 {
		return h.SearchBudget
	}
	return defaultSearchPerTrackerBudget
}

// searchCredentials loads, decrypts, and warms the user's credential for a
// login-gated searchable tracker. Every failure degrades to nil creds
// (the plugin then reports ErrSearchRequiresCredentials) — search must
// never hard-fail on credential trouble. The second return distinguishes
// "no stored credential" (false) from "stored credential exists but could
// not be warmed" (true) so the caller can report login_failed instead of
// telling a user with an account to go add one.
//
// Ordering is Verify-first, Login-on-miss: on a warm in-process session
// Verify is one cheap GET; only a cold/dead session pays the Login round-
// trip. (Deliberately neither loginAndVerify — Login→Verify always, right
// for validating fresh credentials, wasteful per search — nor the
// scheduler's Login-only loadCredentials.)
func (h *Trackers) warmCredentials(ctx context.Context, uid uuid.UUID, t registry.Tracker) (creds *domain.TrackerCredential, loginFailed bool, loginErr error) {
	wc, needsCreds := t.(registry.WithCredentials)
	if !needsCreds || h.Creds == nil || h.Master == nil {
		return nil, false, nil
	}
	stored, err := h.Creds.GetForTracker(ctx, uid, t.Name())
	if err != nil || stored == nil {
		return nil, false, nil
	}
	// Decrypt secret + session like Credentials.Test does — session-cookie
	// trackers validate the session blob, not the password.
	plain, err := h.Master.Decrypt(stored.SecretEnc, stored.SecretNonce)
	if err != nil {
		log.Warn().Str("tracker", t.Name()).Err(err).Msg("search credential decrypt failed; searching anonymously")
		return nil, true, err
	}
	transient := &domain.TrackerCredential{
		ID:          stored.ID,
		UserID:      uid,
		TrackerName: stored.TrackerName,
		Username:    stored.Username,
		SecretEnc:   plain,
	}
	if len(stored.SessionEnc) > 0 {
		sess, derr := h.Master.Decrypt(stored.SessionEnc, stored.SessionNonce)
		if derr != nil {
			log.Warn().Str("tracker", t.Name()).Err(derr).Msg("search session decrypt failed; searching anonymously")
			return nil, true, derr
		}
		transient.SessionEnc = sess
	}
	if ok, verr := wc.Verify(ctx, transient); verr == nil && ok {
		return transient, false, nil
	}
	if lerr := wc.Login(ctx, transient); lerr != nil {
		log.Debug().Str("tracker", t.Name()).Err(lerr).Msg("search credential login failed; searching anonymously")
		return nil, true, lerr
	}
	return transient, false, nil
}
