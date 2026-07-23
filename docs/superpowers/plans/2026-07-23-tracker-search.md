# Tracker Search Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** In-app tracker search (issue #129): type a query, get results from searchable trackers, click one to pre-fill the AddTopic flow.

**Architecture:** New optional `registry.WithSearch` tracker capability implemented by Rutor (public) and RuTracker (login-gated); a concurrent fan-out handler at `GET /api/v1/trackers/search` with per-tracker fail-open; a search mode toggle hosted in `AddTopicCard` that feeds selected result URLs into the untouched `TopicForm` via key-remount. Spec: `docs/superpowers/plans/2026-07-23-tracker-search-spec.md` (verified revision).

**Tech Stack:** Go 1.25 (regex scraping, no new deps — `x/text/charmap` already present), chi router, React 19 + React Query + Vitest.

## Global Constraints

- Backend verify: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "gofmt -l . | tee /dev/stderr | (! grep .) && go build ./... && go vet ./... && go test -race ./..."`
- Frontend verify: `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test -- --run && npm run build"`
- Never edit `frontend/src/components/ui/*` (shadcn-managed).
- `TopicForm.tsx` delta must be **zero lines** (418 lines, over the 250 ceiling).
- Metrics label is `tracker` (NOT `tracker_name`).
- RuTracker query param `nm` must be cp1251-encoded then percent-escaped; rutor query must use `url.PathEscape` (path segment).
- No AI attribution anywhere (commits, comments, PR).
- Commit messages: imperative, ≤72-char subject, reference #129.
- Per-tracker result cap: 50. Per-tracker timeout: 15s. Query cap: 200 runes.

---

### Task 1: `registry.WithSearch` capability + sentinel

**Files:**
- Modify: `backend/internal/plugins/registry/registry.go` (after `WithAuthorComment`, ~line 148)
- Modify: `backend/internal/plugins/registry/errors.go` (append)

**Interfaces:**
- Produces: `registry.SearchResult{Title, URL, Size string; Seeders int; Category string}`, `registry.WithSearch{ Search(ctx, query string, creds *domain.TrackerCredential) ([]SearchResult, error) }`, `registry.ErrSearchRequiresCredentials`.

- [ ] **Step 1: Add the types** (pure declarations — no test; compile check is the gate)

In `registry.go` after the `WithAuthorComment` interface:

```go
// SearchResult is one release found by a tracker search (issue #129). URL is
// the topic page in the tracker's canonical form — exactly what a user would
// paste into the AddTopic form, so every result feeds the existing
// match → parse → create pipeline unchanged. Size and Category are
// display-only strings exactly as scraped (no byte parsing — a mis-parsed
// size would render as a confident wrong number). Seeders is -1 when the
// tracker doesn't expose a count.
type SearchResult struct {
	Title    string `json:"title"`
	URL      string `json:"url"`
	Size     string `json:"size,omitempty"`
	Seeders  int    `json:"seeders"`
	Category string `json:"category,omitempty"`
}

// WithSearch is an optional tracker capability: search the tracker by free-
// text query and return candidate topics to monitor. creds may be nil; a
// tracker whose search page is login-gated returns
// ErrSearchRequiresCredentials so callers can distinguish "needs an
// account" from "found nothing". Implementations cap results at the first
// page (≤50) and never treat an empty result set as an error.
type WithSearch interface {
	Tracker
	Search(ctx context.Context, query string, creds *domain.TrackerCredential) ([]SearchResult, error)
}
```

In `errors.go`:

```go
// ErrSearchRequiresCredentials is returned by a WithSearch tracker whose
// search page is login-gated (RuTracker's tracker.php) when no credential —
// or no longer-valid session — is available. The search handler surfaces it
// as a per-tracker "needs an account" notice instead of a hard failure.
var ErrSearchRequiresCredentials = errors.New("search requires credentials")
```

- [ ] **Step 2: Compile check** — backend verify command (build portion). Expected: clean.
- [ ] **Step 3: Commit** — `feat: add WithSearch tracker capability (#129)`

---

### Task 2: `forumcommon.EncodeWindows1251Query`

**Files:**
- Modify: `backend/internal/plugins/trackers/forumcommon/text.go`
- Test: `backend/internal/plugins/trackers/forumcommon/text_test.go` (append)

**Interfaces:**
- Produces: `forumcommon.EncodeWindows1251Query(s string) string` — cp1251-transcode then percent-escape every byte outside RFC-3986 unreserved.

- [ ] **Step 1: Failing test**

```go
func TestEncodeWindows1251Query(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"ascii passthrough", "dune", "dune"},
		{"space escaped", "dune part", "dune%20part"},
		// Д=0xC4 ю=0xFE н=0xED а=0xE0 in cp1251.
		{"cyrillic cp1251 bytes", "Дюна", "%C4%FE%ED%E0"},
		{"mixed", "Дюна 2", "%C4%FE%ED%E0%202"},
		{"unreserved kept", "a-b_c.d~e", "a-b_c.d~e"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EncodeWindows1251Query(tt.in); got != tt.want {
				t.Errorf("EncodeWindows1251Query(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (undefined function)
- [ ] **Step 3: Implement**

```go
// EncodeWindows1251Query percent-escapes s for use as a query parameter on a
// cp1251 site (RuTracker's tracker.php?nm=). Go's url.Values.Encode emits
// UTF-8 percent-encoding, which cp1251 backends read as mojibake — every
// Cyrillic search would return zero results. So: transcode to cp1251 first
// (unmappable runes become the encoder's substitute byte rather than failing
// the whole query), then percent-escape everything outside RFC-3986
// unreserved.
func EncodeWindows1251Query(s string) string {
	enc, err := encoding.ReplaceUnsupported(charmap.Windows1251.NewEncoder()).String(s)
	if err != nil {
		enc = s // fall back to UTF-8 bytes; a degraded search beats an error
	}
	var b strings.Builder
	for i := 0; i < len(enc); i++ {
		c := enc[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
```

Add imports: `fmt`, `golang.org/x/text/encoding`.

- [ ] **Step 4: Run — expect PASS**
- [ ] **Step 5: Commit** — `feat: add cp1251 query encoder for forum tracker search (#129)`

---

### Task 3: Rutor `Search`

**Files:**
- Modify: `backend/internal/plugins/trackers/rutor/rutor.go`
- Test: `backend/internal/plugins/trackers/rutor/rutor_test.go` (append; follow the file's existing httptest/domain-injection pattern — read it first)

**Interfaces:**
- Consumes: `registry.SearchResult`, `registry.WithSearch` (Task 1); existing `p.effectiveDomain()`, `p.fetch`.
- Produces: rutor implements `registry.WithSearch`.

- [ ] **Step 1: Failing test** — serve a fixture search page from an httptest server (reuse the existing test-injection style in `rutor_test.go` — the tests there already build a `plugin` literal with a test domain/client). Fixture: during implementation first try to capture real HTML (`curl -s "https://rutor.info/search/0/0/000/0/test"`), trim to a few rows; if unreachable, hand-write rows matching the verified live markup:

```html
<tr class="gai"><td>22&nbsp;Июл&nbsp;26</td><td>
<a class="downgif" href="/download/975045"><img src="/s/i/d.gif" alt="D"></a>
<a href="magnet:?xt=urn:btih:aaaabbbbccccddddeeeeffff0000111122223333"><img src="/s/i/m.gif" alt="M"></a>
<a href="/torrent/975045/test-release-1080p">Test release <b>1080p</b></a></td>
<td align="right">1.4&nbsp;GB</td>
<td align="center"><span class="green">&nbsp;17&nbsp;</span>&nbsp;<span class="red">3</span></td></tr>
```

Test asserts: one result; `Title == "Test release 1080p"` (tags flattened, entities unescaped); `URL == "<server-host>/torrent/975045"` canonical rebuild; `Size == "1.4 GB"` (nbsp normalized to space); `Seeders == 17`; empty query returns `(nil, nil)`; a page with no rows returns empty slice, no error.

- [ ] **Step 2: Run — expect FAIL**
- [ ] **Step 3: Implement**

```go
var _ registry.WithSearch = (*plugin)(nil)

var (
	searchRowRe   = regexp.MustCompile(`(?s)<tr[^>]*>(.*?)</tr>`)
	searchLinkRe  = regexp.MustCompile(`(?s)<a href="/torrent/(\d+)[^"]*">(.*?)</a>`)
	searchSizeRe  = regexp.MustCompile(`<td align="right">([^<]+)</td>`)
	searchSeedsRe = regexp.MustCompile(`<span class="green">[^0-9]*(\d+)`)
)

// Search implements registry.WithSearch. Rutor's search is public; the
// query is a path segment (page 0, category 0, filter 000, sort 0).
func (p *plugin) Search(ctx context.Context, query string, _ *domain.TrackerCredential) ([]registry.SearchResult, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	target := fmt.Sprintf("https://%s/search/0/0/000/0/%s", p.effectiveDomain(), url.PathEscape(q))
	body, err := p.fetch(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("rutor search: %w", err)
	}
	var out []registry.SearchResult
	for _, row := range searchRowRe.FindAllSubmatch(body, -1) {
		cell := row[1]
		link := searchLinkRe.FindSubmatch(cell)
		if link == nil {
			continue // header/spacer row
		}
		r := registry.SearchResult{
			Title:   htmlFlatten(string(link[2])),
			URL:     fmt.Sprintf("https://%s/torrent/%s", p.effectiveDomain(), link[1]),
			Seeders: -1,
		}
		if m := searchSizeRe.FindSubmatch(cell); m != nil {
			r.Size = htmlFlatten(string(m[1]))
		}
		if m := searchSeedsRe.FindSubmatch(cell); m != nil {
			if n, err := strconv.Atoi(string(m[1])); err == nil {
				r.Seeders = n
			}
		}
		out = append(out, r)
		if len(out) == 50 {
			break
		}
	}
	return out, nil
}

// htmlFlatten strips tags and entities from a scraped fragment and
// collapses the &nbsp; runs rutor uses as padding.
func htmlFlatten(s string) string {
	return strings.TrimSpace(forumcommon.HTMLToText(s))
}
```

NOTE for implementer: check `forumcommon.HTMLToText` first — if it already trims/unescapes, keep the wrapper trivial; if it doesn't unescape entities, add `html.UnescapeString`. The test (nbsp → space) is the arbiter. Add imports as needed (`strconv`, `forumcommon`).

- [ ] **Step 4: Run — expect PASS** (whole rutor package)
- [ ] **Step 5: Commit** — `feat: rutor search support (#129)`

---

### Task 4: RuTracker `Search`

**Files:**
- Create: `backend/internal/plugins/trackers/rutracker/search.go`
- Test: `backend/internal/plugins/trackers/rutracker/search_test.go`

**Interfaces:**
- Consumes: Task 1 types, Task 2 `EncodeWindows1251Query`, existing `p.fetchBytes`, `p.effectiveDomain()`, `forumcommon.DecodeWindows1251`.
- Produces: rutracker implements `registry.WithSearch`.

- [ ] **Step 1: Failing tests** (new file; use the package's existing transport-injection pattern — `p.transport` + httptest, see `rutracker_test.go`):
  - nil creds → `errors.Is(err, registry.ErrSearchRequiresCredentials)`, no HTTP call made.
  - Authenticated page (body contains `id="logged-in-username"`) with two `tCenter` result rows → titles cp1251-decoded and tag-flattened, URLs rebuilt `https://<host>/forum/viewtopic.php?t=<id>`, size from `tor-size` cell, seeders from `seedmed`, category from the forum-link cell.
  - Response WITHOUT the logged-in marker (anonymous redirect to login form) → `ErrSearchRequiresCredentials`.
  - Cyrillic query: assert the request URL's `nm=` equals the cp1251 escape (`%C4%FE%ED%E0` for `Дюна`) — capture via the test server handler.

Fixture rows (cp1251-encode the file bytes in the test server response, or serve UTF-8 and rely on the decode passthrough — prefer real cp1251 bytes via `charmap.Windows1251.NewEncoder()` in the test to exercise decoding):

```html
<table id="tor-tbl"><tr class="tCenter hl-tr">
<td class="row1 t-ico"></td>
<td class="row1 f-name-col"><div class="f-name"><a class="gen f ts-text" href="tracker.php?f=313">Зарубежное кино</a></div></td>
<td class="row4 med tLeft t-title-col"><div class="t-title"><a data-topic_id="100" class="med tLink tt-text ts-text hl-tags bold" href="viewtopic.php?t=100">Дюна <span class="brackets-pair">[2026]</span></a></div></td>
<td class="row1 t-author-col"></td>
<td class="row4 med tor-size" data-ts_text="1500000000"><a class="small tr-dl dl-stub" href="dl.php?t=100">1.4&nbsp;GB</a></td>
<td class="row4 nowrap" data-ts_text="17"><b class="seedmed">17</b></td>
<td class="row4 leechmed bold">3</td><td class="row4 small nowrap">22-Июл-26</td>
</tr></table>
```

- [ ] **Step 2: Run — expect FAIL**
- [ ] **Step 3: Implement** `search.go`:

```go
package rutracker

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

var _ registry.WithSearch = (*plugin)(nil)

var (
	searchRowRe   = regexp.MustCompile(`(?s)<tr class="[^"]*tCenter[^"]*"[^>]*>(.*?)</tr>`)
	searchLinkRe  = regexp.MustCompile(`(?s)<a[^>]*class="[^"]*tLink[^"]*"[^>]*>(.*?)</a>`)
	searchTIDRe   = regexp.MustCompile(`viewtopic\.php\?t=(\d+)`)
	searchSizeRe  = regexp.MustCompile(`(?s)<td[^>]*class="[^"]*tor-size[^"]*"[^>]*>(.*?)</td>`)
	searchSeedsRe = regexp.MustCompile(`<b class="seedmed">\s*(\d+)`)
	searchForumRe = regexp.MustCompile(`(?s)<a[^>]*href="tracker\.php\?f=\d+[^"]*"[^>]*>(.*?)</a>`)
)

// Search implements registry.WithSearch. RuTracker's tracker.php is
// login-gated: with no credential (or a dead session that Login could not
// revive at the handler layer) it returns ErrSearchRequiresCredentials.
// The nm param must be cp1251-percent-encoded — UTF-8 encoding reads as
// mojibake server-side and returns zero results for Cyrillic queries.
func (p *plugin) Search(ctx context.Context, query string, creds *domain.TrackerCredential) ([]registry.SearchResult, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	if creds == nil {
		return nil, registry.ErrSearchRequiresCredentials
	}
	target := fmt.Sprintf("https://%s/forum/tracker.php?nm=%s",
		p.effectiveDomain(), forumcommon.EncodeWindows1251Query(q))
	body, err := p.fetchBytes(ctx, nil, creds, target)
	if err != nil {
		return nil, fmt.Errorf("rutracker search: %w", err)
	}
	page := forumcommon.DecodeWindows1251(string(body))
	if !strings.Contains(page, `id="logged-in-username"`) {
		// tracker.php served the anonymous login shell — session is dead.
		return nil, registry.ErrSearchRequiresCredentials
	}
	var out []registry.SearchResult
	for _, row := range searchRowRe.FindAllStringSubmatch(page, -1) {
		cell := row[1]
		linkM := searchLinkRe.FindStringSubmatchIndex(cell)
		if linkM == nil {
			continue
		}
		anchor := cell[linkM[0]:linkM[1]]
		tid := searchTIDRe.FindStringSubmatch(anchor)
		if tid == nil {
			continue
		}
		r := registry.SearchResult{
			Title:   htmlFlatten(cell[linkM[2]:linkM[3]]),
			URL:     fmt.Sprintf("https://%s/forum/viewtopic.php?t=%s", p.effectiveDomain(), tid[1]),
			Seeders: -1,
		}
		if m := searchSizeRe.FindStringSubmatch(cell); m != nil {
			r.Size = htmlFlatten(m[1])
		}
		if m := searchSeedsRe.FindStringSubmatch(cell); m != nil {
			if n, cerr := strconv.Atoi(m[1]); cerr == nil {
				r.Seeders = n
			}
		}
		if m := searchForumRe.FindStringSubmatch(cell); m != nil {
			r.Category = htmlFlatten(m[1])
		}
		out = append(out, r)
		if len(out) == 50 {
			break
		}
	}
	return out, nil
}

// htmlFlatten strips nested highlight spans and entities from scraped cells.
func htmlFlatten(s string) string {
	return strings.TrimSpace(forumcommon.HTMLToText(s))
}
```

(Same `HTMLToText` caveat as Task 3 — one shared verification, adjust both.)

- [ ] **Step 4: Run — expect PASS** (whole rutracker package)
- [ ] **Step 5: Commit** — `feat: rutracker login-gated search (#129)`

---

### Task 5: Search API handler + metrics + router

**Files:**
- Modify: `backend/internal/metrics/metrics.go` (tracker metrics section)
- Create: `backend/internal/api/handlers/trackers_search.go`
- Modify: `backend/internal/api/handlers/trackers.go` (struct fields only)
- Modify: `backend/internal/api/router.go` (~line 132 handler ctor, ~line 172 route)
- Test: `backend/internal/api/handlers/trackers_search_test.go`

**Interfaces:**
- Consumes: Task 1 types; `credentialStore` seam + `currentUserID` + `writeJSON` + `problem` (all already in package `handlers`); `crypto.MasterKey` decrypt calls — copy the exact decrypt sequence from `Credentials.Test` (`credentials.go`, secret + session blob).
- Produces: `GET /api/v1/trackers/search?q=&trackers=` → `{results: [...], errors: [...]}`; `metrics.TrackerSearchTotal`.

- [ ] **Step 1: Metrics counter** in the tracker section of `metrics.go`:

```go
	// TrackerSearchTotal counts interactive tracker searches (issue #129),
	// partitioned by tracker and outcome.
	TrackerSearchTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "marauder_tracker_search_total",
			Help: "Number of tracker search attempts, partitioned by tracker and result.",
		},
		[]string{"tracker", "result"}, // "ok" | "error" | "no_credentials"
	)
```

- [ ] **Step 2: Failing handler tests.** Register fakes via `registry.RegisterTracker` + `t.Cleanup(registry.Reset)` (existing `trackers_test.go` pattern; note Reset nukes real plugins for the process — the existing tests already accept that). Fakes: `fakeSearchTracker` (returns two results, seeders 5 and 12), `fakeCredsSearchTracker` (implements `WithCredentials` + `WithSearch`, returns `ErrSearchRequiresCredentials` when creds nil), `fakeSlowSearchTracker` (blocks on ctx.Done). Cases:
  - missing `q` → 400.
  - q > 200 runes → 400.
  - happy path → 200, results sorted seeders desc across trackers, `errors` empty.
  - creds-tracker with no stored credential (fake store returns not-found) → result set from the public fake only + `errors: [{tracker_name, error: "search requires credentials"}]`.
  - `trackers=fake-a` filter → only that tracker searched.
  - second concurrent request for same user → 429 (start slow search in goroutine, assert second returns 429, cancel).

Fake credential store: implement the package's existing `credentialStore` interface; only `GetForTracker` matters (return `nil, pgx.ErrNoRows`-equivalent error).

- [ ] **Step 3: Run — expect FAIL**
- [ ] **Step 4: Implement.** Extend the struct in `trackers.go`:

```go
type Trackers struct {
	BaseURL string
	Creds   credentialStore   // nil-safe: skipped when absent (tests)
	Master  *crypto.MasterKey // used only to decrypt stored credentials for login-gated search
	searchInFlight sync.Map   // userID -> struct{}; per-user single-flight gate
}
```

`trackers_search.go` — full handler:

```go
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

	type outcome struct {
		results []searchResultView
		errView *searchErrorView
	}
	var (
		searchers []registry.WithSearch
	)
	for _, t := range registry.ListTrackers() {
		ws, ok := t.(registry.WithSearch)
		if !ok || (filter != nil && !filter[t.Name()]) {
			continue
		}
		searchers = append(searchers, ws)
	}
	outcomes := make([]outcome, len(searchers))
	done := make(chan int)
	for i, ws := range searchers {
		go func(i int, ws registry.WithSearch) {
			defer func() { done <- i }()
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
		si, sj := results[i].Seeders, results[j].Seeders
		if si != sj {
			return si > sj // unknown (-1) sorts last
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
	// Decrypt secret + session exactly like Credentials.Test does — session
	// trackers validate the session blob, not the password.
	// >>> copy the decrypt sequence from credentials.go Test verbatim here <<<
	if ok, verr := wc.Verify(ctx, stored); verr == nil && ok {
		return stored
	}
	if lerr := wc.Login(ctx, stored); lerr != nil {
		log.Debug().Str("tracker", t.Name()).Err(lerr).Msg("search credential login failed; searching anonymously")
		return nil
	}
	return stored
}
```

The `>>> <<<` marker is the one intentional lookup: replicate `Credentials.Test`'s decrypt calls (secret into `SecretEnc`, session blob when non-empty) with identical error handling — degrade to `return nil` instead of writing a problem. If `problem.ErrTooManyRequests` doesn't exist yet, add it to `backend/internal/problem` following `ErrBadRequest`'s shape with status 429.

Router wiring (`router.go`): `trackersH := &handlers.Trackers{BaseURL: ..., Creds: d.Creds, Master: d.Master}` and `r.Get("/trackers/search", trackersH.Search)` next to the other `/trackers/*` routes.

- [ ] **Step 5: Run — expect PASS** (handlers package, race on)
- [ ] **Step 6: Commit** — `feat: tracker search API with per-tracker fail-open fan-out (#129)`

---

### Task 6: `supports_search` in /system/info

**Files:**
- Modify: `backend/internal/api/handlers/system.go:101-114` (`listTrackerInfos`)
- Test: existing system handler test file (append one assertion; find it via `grep -l listTrackerInfos backend/internal/api/handlers/*_test.go` — if none tests this function, add a focused test registering one fake `WithSearch` tracker and asserting the flag)

- [ ] **Step 1: Failing test** — fake tracker with `Search` method registered → `/system/info` tracker entry has `"supports_search": true`; plain fake → `false`.
- [ ] **Step 2: Implement** — add to `listTrackerInfos`:

```go
	_, hasSearch := t.(registry.WithSearch)
	// inside the map literal:
	"supports_search": hasSearch,
```

- [ ] **Step 3: Run — expect PASS**
- [ ] **Step 4: Commit** — `feat: expose supports_search in system info (#129)`

---

### Task 7: Frontend — search mode in AddTopicCard

**Files:**
- Modify: `frontend/src/lib/queryKeys.ts` (add key)
- Create: `frontend/src/components/topics/TrackerSearch.tsx`
- Modify: `frontend/src/components/topics/AddTopicCard.tsx` (host toggle + remount plumbing)
- Modify: `frontend/src/i18n/en.ts`, `frontend/src/i18n/ru.ts`
- Test: `frontend/src/components/topics/TrackerSearch.test.tsx`
- **Do NOT touch** `TopicForm.tsx`.

**Interfaces:**
- Consumes: `GET /trackers/search?q=` response `{results: [{tracker_name, tracker_display_name, title, url, size, seeders, category}], errors: [{tracker_name, error}]}`.
- Produces: `<TrackerSearch onSelect={(url: string) => void} />`.

- [ ] **Step 1: queryKeys.ts**

```ts
  // /trackers/search?q=… cross-tracker release search (issue #129). Used by
  // the AddTopic search mode.
  trackerSearch: (q: string) => ["trackerSearch", q] as const,
```

- [ ] **Step 2: Failing tests** (`TrackerSearch.test.tsx`, Vitest + RTL + userEvent, mock `api.get`):
  - typing a query + pressing the search button calls `/trackers/search?q=…` once (no search-as-you-type: typing alone must NOT fire).
  - results render title, tracker badge, size, seeders; clicking a row calls `onSelect` with the result URL.
  - per-tracker error `search requires credentials` renders the friendly needs-account line.
  - empty results after a search → "No results" empty state.

- [ ] **Step 3: Implement `TrackerSearch.tsx`** — shape (final code may adjust classes to match sibling components; follow `TopicForm`'s input/button primitives):

```tsx
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Loader2, Search as SearchIcon } from "lucide-react";

import { api } from "@/lib/api";
import { QK } from "@/lib/queryKeys";
import { useT } from "@/i18n";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

interface SearchResultRow {
  tracker_name: string;
  tracker_display_name: string;
  title: string;
  url: string;
  size?: string;
  seeders: number;
  category?: string;
}

interface SearchResponse {
  results: SearchResultRow[];
  errors: { tracker_name: string; error: string }[];
}

interface TrackerSearchProps {
  onSelect: (url: string) => void;
}

// Cross-tracker release search (issue #129). Explicit submit only — every
// search fans out real scraping requests server-side, so no
// search-as-you-type. A picked result is handed up to AddTopicCard, which
// switches back to URL mode with the URL prefilled.
export function TrackerSearch({ onSelect }: TrackerSearchProps) {
  const t = useT();
  const [query, setQuery] = useState("");
  const [submitted, setSubmitted] = useState("");

  const search = useQuery({
    queryKey: QK.trackerSearch(submitted),
    queryFn: () =>
      api.get<SearchResponse>(`/trackers/search?q=${encodeURIComponent(submitted)}`),
    enabled: submitted.length > 0,
    staleTime: 60_000,
    retry: false,
  });

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const q = query.trim();
    if (q) setSubmitted(q);
  };

  const results = search.data?.results ?? [];
  const trackerErrors = search.data?.errors ?? [];

  return (
    <div className="space-y-3">
      <form onSubmit={handleSubmit} className="flex gap-2">
        <Input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t("topics.search.placeholder")}
          autoFocus
        />
        <Button type="submit" disabled={search.isFetching || !query.trim()}>
          {search.isFetching ? (
            <Loader2 className="size-4 animate-spin" />
          ) : (
            <SearchIcon className="size-4" />
          )}
          {t("topics.search.button")}
        </Button>
      </form>

      {search.isError && (
        <p className="text-xs text-destructive">{t("topics.search.failed")}</p>
      )}

      {results.length > 0 && (
        <ul className="max-h-80 divide-y divide-border/60 overflow-y-auto rounded-md border border-border/60">
          {results.map((r) => (
            <li key={`${r.tracker_name}-${r.url}`}>
              <button
                type="button"
                onClick={() => onSelect(r.url)}
                className="flex w-full items-center gap-3 px-3 py-2 text-left hover:bg-muted/50"
              >
                <span className="min-w-0 flex-1 truncate text-sm">{r.title}</span>
                <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
                  {r.tracker_display_name}
                </span>
                {r.size && (
                  <span className="shrink-0 text-xs text-muted-foreground">{r.size}</span>
                )}
                <span className="shrink-0 text-xs tabular-nums text-success">
                  {r.seeders >= 0 ? `↑${r.seeders}` : "—"}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}

      {search.isSuccess && results.length === 0 && (
        <p className="py-4 text-center text-sm text-muted-foreground">
          {t("topics.search.noResults")}
        </p>
      )}

      {trackerErrors.map((e) => (
        <p key={e.tracker_name} className="text-xs text-muted-foreground">
          {e.tracker_name}:{" "}
          {e.error === "search requires credentials"
            ? t("topics.search.needsAccount")
            : e.error}
        </p>
      ))}
    </div>
  );
}
```

- [ ] **Step 4: AddTopicCard toggle.** Replace the card body with mode state; TopicForm remounts via `key`:

```tsx
type AddMode = "url" | "search";

export function AddTopicCard({ onClose, onCreated }: AddTopicCardProps) {
  const [error, setError] = useState<string | null>(null);
  const [mode, setMode] = useState<AddMode>("url");
  const [prefillUrl, setPrefillUrl] = useState("");
  const t = useT();
  // create mutation unchanged …

  return (
    <motion.div /* unchanged animation props */>
      <Card className="overflow-hidden">
        <div className="flex gap-1 px-6 pt-4">
          <ModeTab active={mode === "url"} onClick={() => setMode("url")}>
            {t("topics.search.byUrl")}
          </ModeTab>
          <ModeTab active={mode === "search"} onClick={() => setMode("search")}>
            {t("topics.search.tab")}
          </ModeTab>
        </div>
        {mode === "search" ? (
          <div className="p-6 pt-4">
            <TrackerSearch
              onSelect={(url) => {
                setPrefillUrl(url);
                setMode("url");
              }}
            />
          </div>
        ) : (
          <TopicForm
            key={prefillUrl} /* remount when a search result is picked */
            mode="add"
            heading="Add a new topic"
            submitLabel="Add topic"
            initial={{ ...EMPTY, url: prefillUrl }}
            isPending={create.isPending}
            error={error}
            onClose={onClose}
            onSubmit={(v) => {
              setError(null);
              create.mutate(v);
            }}
          />
        )}
      </Card>
    </motion.div>
  );
}
```

`ModeTab` is a tiny local styled button component in the same file (~10 lines). Heading/submitLabel strings stay as-is (they're currently English literals in the existing code — do not convert unrelated strings in this PR).

- [ ] **Step 5: i18n keys** — `en.ts`:

```ts
  "topics.search.tab": "Search trackers",
  "topics.search.byUrl": "By URL",
  "topics.search.placeholder": "Search releases across your trackers…",
  "topics.search.button": "Search",
  "topics.search.failed": "Search failed. Try again.",
  "topics.search.noResults": "No results",
  "topics.search.needsAccount": "needs a tracker account — add one under Accounts",
```

`ru.ts`:

```ts
  "topics.search.tab": "Поиск по трекерам",
  "topics.search.byUrl": "По ссылке",
  "topics.search.placeholder": "Поиск релизов по вашим трекерам…",
  "topics.search.button": "Найти",
  "topics.search.failed": "Поиск не удался. Попробуйте ещё раз.",
  "topics.search.noResults": "Ничего не найдено",
  "topics.search.needsAccount": "нужен аккаунт трекера — добавьте его в разделе «Аккаунты»",
```

(Match the surrounding dictionaries' key style on sight — flat dotted keys.)

- [ ] **Step 6: Run frontend verify — expect PASS** (typecheck + tests + build)
- [ ] **Step 7: Commit** — `feat: tracker search mode in the add-topic flow (#129)`

---

### Task 8: Docs, site, changelog

**Files:**
- Modify: `docs/trackers.md` (new "Searching trackers" section: which trackers are searchable, RuTracker needs an account, results cap/timeout)
- Modify: `CLAUDE.md` (registry capability list + `WithSearch` blurb, trackers handler routes, metrics mention, frontend component)
- Modify: `README.md` (one feature bullet)
- Modify: `CHANGELOG.md` `[Unreleased]` (feature bullet linking #129)
- Modify: `site/` — find the feature enumeration (`grep -ri "tracker" site/src/pages/index.astro site/src/data/` first) and add search phrasing consistent with existing copy
- Test: site builds — `docker run --rm -v "E:/Projects/Stukans/Marauder/site:/site" -w //site node:20 sh -c "npm run build"` (Linux container mandatory — node_modules is a Linux install)

- [ ] **Step 1: Write all doc edits**
- [ ] **Step 2: Site build — expect PASS**
- [ ] **Step 3: Commit** — `docs: tracker search guide, changelog, site copy (#129)`

---

### Task 9: Full verification + live check + local docker update

- [ ] **Step 1:** Backend verify command (Global Constraints) — expect all green.
- [ ] **Step 2:** Frontend verify command — expect green.
- [ ] **Step 3:** Update the local dev stack (per memory: `--no-deps` so postgres isn't recreated):

```bash
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml up -d --build --no-deps backend frontend
```

- [ ] **Step 4:** Live check (real data only): login to http://localhost:34080 API, `GET /api/v1/trackers/search?q=ubuntu` → expect real rutor results (fallback: switch rutor active domain to `rutor.info` via admin domains card if `rutor.org` 403s). Verify `errors` contains the rutracker needs-credentials entry when no account is configured. No test topics created unless immediately deleted.
- [ ] **Step 5:** Fix anything found; re-run; commit fixes.

---

### Task 10: Code review, PR

- [ ] **Step 1:** Run `/code-review` (skill) on the branch; fix ALL findings; re-verify.
- [ ] **Step 2:** Push branch `129-tracker-search`; open PR titled `feat: in-app tracker search to add topics (#129)` — body: problem/solution summary, research evidence line, screenshots note for morning verification, `Closes #129`. No AI attribution.
