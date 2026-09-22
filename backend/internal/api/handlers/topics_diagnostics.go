package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
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
// — same session, same active domain, same character-set handling — keeps only
// the regions the plugin's parser reads, redacts each, and returns them for
// the user to attach to a bug report. See registry.WithPageExport for why it
// is regions and not the whole page.
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
	exporter, ok := tracker.(registry.WithPageExport)
	if !ok {
		// 409, not 501: the endpoint exists and the request was well formed —
		// this particular tracker just has not implemented the capability yet.
		// The frontend hides the action using supports_page_export from
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
	username := h.reporterUsername(ctx, uid, tracker.Name(), creds)

	regions, ferr := exporter.ExportRegions(ctx, topic.URL, creds)
	if ferr != nil {
		// ErrBadGateway, not ErrInternal: the failure is the tracker's or the
		// network's, and the detail is the whole point of the report.
		problem.Write(w, r, h.BaseURL, problem.ErrBadGateway(ferr.Error()))
		return
	}

	fetchedAt := time.Now().UTC()
	doc, meta, rerr := buildExport(exportHeader{
		tracker: tracker.Name(), url: topic.URL, at: fetchedAt, signedIn: creds != nil,
	}, regions, username)
	if rerr != nil {
		// Fail closed. The redactor only refuses when it could not examine a
		// whole region, and returning what it did not examine would hand the
		// user unchecked tracker bytes to publish. 422, not 500: nothing broke
		// on our side, this particular page cannot be exported.
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable(
			"this page could not be safely prepared for export, so nothing was returned"))
		return
	}

	if h.Audit != nil {
		h.Audit.Generic(&uid, "topic_diagnostics_page", "topic", id.String(), "success",
			map[string]any{"tracker": tracker.Name(), "bytes": len(doc)})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tracker":        tracker.Name(),
		"url":            topic.URL,
		"authenticated":  creds != nil,
		"bytes":          len(doc),
		"fetched_at":     fetchedAt,
		"redacted":       true,
		"redaction_mark": pageredact.Placeholder,
		"regions":        meta,
		"html":           string(doc),
	})
}

// exportedRegion is what the card shows about each region.
type exportedRegion struct {
	Name  string `json:"name"`
	Found bool   `json:"found"`
	Bytes int    `json:"bytes"`
}

type exportHeader struct {
	tracker, url string
	at           time.Time
	signedIn     bool
}

// buildExport assembles the file the user downloads: a header saying what it
// is and what was deliberately left out, then each region under a marker,
// each redacted on its own. Separate redaction is on purpose — a malformed
// region must not be able to affect how the next one is read.
//
// The header is redacted too. It carries the topic URL exactly as the user
// stored it, and a URL pasted from a browser can carry a session id.
func buildExport(h exportHeader, regions []registry.PageRegion, username string) ([]byte, []exportedRegion, error) {
	signedIn := "no — this is what a guest sees"
	if h.signedIn {
		signedIn = "yes"
	}
	header := fmt.Sprintf("<!--\n"+
		"  Marauder page export\n"+
		"  tracker:   %s\n"+
		"  topic:     %s\n"+
		"  fetched:   %s\n"+
		"  signed in: %s\n\n"+
		"  Only the parts of the page this tracker's parser reads are included,\n"+
		"  each byte for byte as the tracker sent it, with secrets replaced by\n"+
		"  %s. Everything else on the page was left out on purpose.\n"+
		"-->\n",
		commentSafe(h.tracker), commentSafe(h.url), h.at.Format(time.RFC3339),
		signedIn, pageredact.Placeholder)

	var b bytes.Buffer
	head, err := pageredact.Redact([]byte(header), username)
	if err != nil {
		return nil, nil, err
	}
	b.Write(head)

	meta := make([]exportedRegion, 0, len(regions))
	for _, r := range regions {
		name := commentSafe(r.Name)
		if r.HTML == nil {
			fmt.Fprintf(&b, "\n<!-- region: %s: NOT FOUND on this page -->\n", name)
			meta = append(meta, exportedRegion{Name: r.Name})
			continue
		}
		red, err := pageredact.Redact(r.HTML, username)
		if err != nil {
			return nil, nil, err
		}
		fmt.Fprintf(&b, "\n<!-- region: %s -->\n", name)
		b.Write(red)
		b.WriteByte('\n')
		meta = append(meta, exportedRegion{Name: r.Name, Found: true, Bytes: len(red)})
	}
	return b.Bytes(), meta, nil
}

// commentSafe keeps a value from closing the HTML comment it is written into.
// The topic URL is user-supplied and only its prefix is validated, so a stored
// `…?t=1--><script>` would otherwise break out of the header of a file the
// user then opens in a browser. It loops because one pass is not enough:
// ReplaceAll turns `---` into `- --`, which still closes a comment.
func commentSafe(s string) string {
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "- -")
	}
	return s
}

// reporterUsername returns the tracker account name to redact from the page.
//
// It is read from the STORED credential when warming did not produce one.
// Warming fails for ordinary reasons — a wrong password, a tracker that is
// down, an undecryptable blob — and the fetch then falls back to anonymous,
// which is right. But the account name is still known, and a guest page can
// still show it: the reporter's own posts, their uploads, a member list. Tying
// the redaction to a successful login left their identity in the one file they
// were about to publish, precisely when something had already gone wrong.
func (h *Topics) reporterUsername(ctx context.Context, uid uuid.UUID, tracker string, creds *domain.TrackerCredential) string {
	if creds != nil {
		return creds.Username
	}
	if h.Creds == nil {
		return ""
	}
	stored, err := h.Creds.GetForTracker(ctx, uid, tracker)
	if err != nil || stored == nil {
		return ""
	}
	return stored.Username
}
