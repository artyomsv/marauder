package handlers

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
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
	searchQueryMaxRunes    = 200
	searchPerTrackerBudget = 15 * time.Second
)

// searchResultView is one row of GET /trackers/search — a registry result
// plus which tracker produced it.
type searchResultView struct {
	TrackerName        string `json:"tracker_name"`
	TrackerDisplayName string `json:"tracker_display_name"`
	registry.SearchResult
}

// searchErrorView reports one tracker's failure without failing the whole
// search (per-tracker fail-open).
type searchErrorView struct {
	TrackerName string `json:"tracker_name"`
	Error       string `json:"error"`
}

// Search handles GET /api/v1/trackers/search?q=<query>&trackers=<csv>.
// Fans out to every WithSearch tracker concurrently; per-tracker failures
// degrade to entries in `errors` (fail-open) — only a bad request shape
// produces a non-200. A per-user single-flight gate returns 429 on
// concurrent searches: every call triggers real scraping requests, and a
// runaway loop could get the instance's IP banned by trackers.
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
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("query too long (max 200 characters)"))
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
	done := make(chan struct{})
	for i, ws := range searchers {
		go func(i int, ws registry.WithSearch) {
			defer func() { done <- struct{}{} }()
			ctx, cancel := context.WithTimeout(r.Context(), searchPerTrackerBudget)
			defer cancel()
			name := ws.Name()
			creds := h.searchCredentials(ctx, uid, ws)
			results, err := ws.Search(ctx, q, creds)
			switch {
			case errors.Is(err, registry.ErrSearchRequiresCredentials):
				metrics.TrackerSearchTotal.WithLabelValues(name, "no_credentials").Inc()
				outcomes[i] = outcome{errView: &searchErrorView{TrackerName: name, Error: "search requires credentials"}}
			case err != nil:
				metrics.TrackerSearchTotal.WithLabelValues(name, "error").Inc()
				log.Warn().Str("tracker", name).Err(err).Msg("tracker search failed")
				outcomes[i] = outcome{errView: &searchErrorView{TrackerName: name, Error: err.Error()}}
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
	for range searchers {
		<-done
	}

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

// searchCredentials loads, decrypts, and warms the user's credential for a
// login-gated searchable tracker. Every failure degrades to nil (the plugin
// then reports ErrSearchRequiresCredentials) — search must never hard-fail
// on credential trouble. Ordering is Verify-first, Login-on-miss: on a warm
// in-process session Verify is one cheap GET; only a cold/dead session pays
// the Login round-trip. (Deliberately neither loginAndVerify — Login→Verify
// always, right for validating fresh credentials, wasteful per search — nor
// the scheduler's Login-only loadCredentials.)
func (h *Trackers) searchCredentials(ctx context.Context, uid uuid.UUID, t registry.Tracker) *domain.TrackerCredential {
	wc, needsCreds := t.(registry.WithCredentials)
	if !needsCreds || h.Creds == nil || h.Master == nil {
		return nil
	}
	stored, err := h.Creds.GetForTracker(ctx, uid, t.Name())
	if err != nil || stored == nil {
		return nil
	}
	// Decrypt secret + session like Credentials.Test does — session-cookie
	// trackers validate the session blob, not the password.
	plain, err := h.Master.Decrypt(stored.SecretEnc, stored.SecretNonce)
	if err != nil {
		log.Warn().Str("tracker", t.Name()).Err(err).Msg("search credential decrypt failed; searching anonymously")
		return nil
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
			return nil
		}
		transient.SessionEnc = sess
	}
	if ok, verr := wc.Verify(ctx, transient); verr == nil && ok {
		return transient
	}
	if lerr := wc.Login(ctx, transient); lerr != nil {
		log.Debug().Str("tracker", t.Name()).Err(lerr).Msg("search credential login failed; searching anonymously")
		return nil
	}
	return transient
}
