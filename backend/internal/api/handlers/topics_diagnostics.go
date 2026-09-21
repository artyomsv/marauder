package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/pageredact"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/problem"
)

// diagnosticsPageTimeout bounds the live tracker fetch. Generous compared to
// a scheduled check because a human is watching the spinner and would rather
// wait than retry, but still bounded: the request holds the per-topic gate
// below for its whole duration.
const diagnosticsPageTimeout = 30 * time.Second

// DiagnosticsPage handles POST /topics/{id}/diagnostics/page.
//
// It re-fetches the topic's tracker page through the plugin's own Check path
// — same session, same active domain, same character-set handling — redacts
// the secrets out of it, and returns it for the user to attach to a bug
// report.
//
// This exists because issue #186 took five days and was finally solved by one
// CSS class in one table cell. The tracker serves different markup to an
// account that seeds the release than to one that does not, so no amount of
// checking from a maintainer's account could reproduce it; the only thing
// that could was the reporter's own bytes, which they had to find by hand in
// a 110 KB page. This turns that into a button.
//
// The page is fetched NOW rather than captured at the moment of failure. That
// is the right trade for the bug class this serves — markup that differs per
// account is stable, and storing every failing page would mean a migration, a
// size cap and a retention policy for data most installs never look at. It is
// the wrong tool for a genuinely transient page, and the UI says so.
func (h *Topics) DiagnosticsPage(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	id, ierr := uuid.Parse(chi.URLParam(r, "id"))
	if ierr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid topic id"))
		return
	}

	topic, gerr := h.Topics.GetByID(r.Context(), id, &uid)
	if gerr != nil {
		if errors.Is(gerr, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("topic not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(gerr.Error()))
		return
	}

	tracker := registry.FindTrackerForURL(topic.URL)
	if tracker == nil {
		problem.Write(w, r, h.BaseURL, problem.ErrConflict(
			"no tracker plugin handles this topic's URL"))
		return
	}
	raw, ok := tracker.(registry.WithRawPage)
	if !ok {
		// 409, not 501: the endpoint exists and the request was well formed —
		// this particular tracker just has not implemented the capability yet.
		// The frontend hides the action using supports_raw_page from
		// /system/info, so reaching this is a stale page rather than a bug.
		problem.Write(w, r, h.BaseURL, problem.ErrConflict(
			"this tracker cannot export its page yet"))
		return
	}

	// Per-topic single-flight, after the ownership check so a non-owner (404
	// either way) cannot use the 429 to probe someone else's topic. This
	// endpoint makes a live, authenticated request to a third-party tracker
	// on a user's behalf; several of them rate-limit hard enough to answer
	// 429 themselves (Toloka: 6 requests in 3s), so an impatient double-click
	// must not be forwarded. Released by defer, which also runs while a panic
	// unwinds, so the gate cannot latch shut.
	if _, busy := h.diagnosticsInFlight.LoadOrStore(id, struct{}{}); busy {
		problem.Write(w, r, h.BaseURL, problem.ErrTooManyRequests(
			"a page export is already running for this topic; wait for it to finish"))
		return
	}
	defer h.diagnosticsInFlight.Delete(id)

	ctx, cancel := context.WithTimeout(r.Context(), diagnosticsPageTimeout)
	defer cancel()

	// Fail-open on credentials, exactly like every other read-as-the-user
	// path: a tracker that answers a guest with a stub still produces a
	// report worth having, and "this is what an unauthenticated fetch sees"
	// is itself a useful line in a bug report.
	creds, _, _ := warmCredentials(ctx, h.Creds, h.Master, uid, tracker)
	username := ""
	if creds != nil {
		username = creds.Username
	}

	page, ferr := raw.RawPage(ctx, topic.URL, creds)
	if ferr != nil {
		// ErrBadGateway, not ErrInternal: the failure is the tracker's or the
		// network's, and the detail is the whole point of the report.
		problem.Write(w, r, h.BaseURL, problem.ErrBadGateway(ferr.Error()))
		return
	}

	redacted := pageredact.Redact(page, username)

	if h.Audit != nil {
		h.Audit.Generic(&uid, "topic_diagnostics_page", "topic", id.String(), "success",
			map[string]any{"tracker": tracker.Name(), "bytes": len(redacted)})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tracker":        tracker.Name(),
		"url":            topic.URL,
		"authenticated":  creds != nil,
		"bytes":          len(redacted),
		"fetched_at":     time.Now().UTC(),
		"redacted":       true,
		"redaction_mark": pageredact.Placeholder,
		"html":           string(redacted),
	})
}
