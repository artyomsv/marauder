# Tracker Search — Specification

Date: 2026-07-23
Status: verified (subagent review 2026-07-23 — 1 blocker + 2 majors + 7 minors
found, all folded into this revision)
Origin: web-research sprint 2026-07-23 — in-app search was the single
feature-gap converged on by all four research tracks (Prowlarr's core UX,
monitorrent #316, RuTracker's own Telegram bot, qBittorrent search-plugins
6.4k★). Scoped here as *search-as-entry-point-to-monitoring*, not
search-as-product: results exist only to become monitored topics.

## Problem

Adding a topic today requires the user to already have the topic URL — which
means visiting the tracker in a browser, fighting Cloudflare/mirrors/login,
finding the release, and copy-pasting the URL back into Marauder. That is the
single biggest onboarding friction. The AddTopic form should let the user type
a title, see matching releases across their configured trackers, and click one
to monitor it.

## Goals

1. A user can type a query in the AddTopic flow, get live results from every
   searchable tracker, and one click pre-fills the topic URL — from there the
   existing match → preview → quality → create pipeline takes over unchanged.
2. Search works with zero configuration on public trackers (Rutor) and uses
   the user's stored credentials on login-gated trackers (RuTracker).
3. Per-tracker failures degrade gracefully: one broken/slow/unauthenticated
   tracker never blocks results from the others.

## Non-goals (explicitly deferred)

- **Prowlarr/Jackett-backed search** (consume their Torznab API as a search
  source). Good future add-on; kept out to limit scope.
- **Torznab search *output*** (`t=search` endpoint on Marauder's API).
- **NNM-Club search** — its search sits behind Cloudflare; the cfsolver round
  trip makes interactive search latency unacceptable. Revisit later.
- **Kinozal search** — deferred on scope, not plumbing: the plugin already
  has full `WithCredentials` + `forumcommon` session handling identical to
  RuTracker's, but its search page behaviour (login gating, selectors)
  could not be verified live from this session. Phase 2 is mostly
  copy-the-RuTracker-pattern once verified.
- Search-result caching, pagination beyond the first page, sort options.
- Telegram-bot search, Jellyseerr intake (both depend on this seam and come
  later).

## Design

### 1. Registry capability — `registry.WithSearch`

```go
// SearchResult is one release found by a tracker search. URL is the topic
// page URL in the tracker's canonical form — exactly what a user would paste
// into the AddTopic form, so every result feeds the existing
// match → parse → create pipeline unchanged.
type SearchResult struct {
    Title    string `json:"title"`
    URL      string `json:"url"`
    Size     string `json:"size,omitempty"`     // human-readable, as scraped ("1.4 GB")
    Seeders  int    `json:"seeders"`            // -1 = unknown
    Category string `json:"category,omitempty"` // tracker's own category label, as scraped
}

// WithSearch is an optional tracker capability: search the tracker by free-
// text query and return candidate topics to monitor. creds may be nil; a
// tracker whose search requires login returns ErrSearchRequiresCredentials
// so callers can distinguish "needs account" from "found nothing".
type WithSearch interface {
    Tracker
    Search(ctx context.Context, query string, creds *domain.TrackerCredential) ([]SearchResult, error)
}
```

New sentinel in `registry/errors.go`:

```go
// ErrSearchRequiresCredentials — the tracker's search page is login-gated
// and no (valid) credential was supplied.
var ErrSearchRequiresCredentials = errors.New("search requires credentials")
```

Design notes:

- `Size`/`Category` stay **strings as scraped** — no byte parsing, no
  normalization. They are display hints only; inventing a parse layer
  invites per-tracker drift bugs and violates the no-synthetic-data rule
  (a mis-parsed size would render as a confident wrong number).
- `Seeders` is the one numeric field because the UI sorts by it; `-1`
  (unknown) sorts last and renders as "—".
- Result count is capped at 50 per tracker inside each plugin (first result
  page only) to bound payload size.

### 2. Plugin implementations (phase 1: two trackers)

**Rutor** (public, no auth — the zero-config path):
- `GET https://<effectiveDomain>/search/0/0/000/0/<path-escaped query>`
  (rutor's positional search path: page 0, category 0, filter 000, sort 0).
  The query is a **path segment** — use `url.PathEscape` (escapes `/`,
  encodes space as `%20`), NOT `url.QueryEscape` (whose `+` for space is
  wrong inside a path). Rutor is UTF-8; no transcoding needed.
- Parse the results table: rows contain `href="/torrent/<id>/<slug>"` (the
  topic link + title text), a size cell, and `<span class="green">N</span>`
  seeders. Same regex-scanning style as the existing plugin (no new HTML
  dep). Result URL is rebuilt as
  `https://<effectiveDomain>/torrent/<id>` — canonical form the plugin's own
  `CanParse` accepts.
- Same SSRF guard as `fetch`: the built URL host is `effectiveDomain()`,
  which is already allowlist-resolved.

**RuTracker** (login-gated search):
- `GET https://<effectiveDomain>/forum/tracker.php?nm=<query>` using the
  user's `forumcommon` session (same session store as Check/Download —
  search reuses an already-warm session).
- **BLOCKER-grade encoding requirement:** RuTracker expects the `nm`
  param **cp1251-encoded, then percent-escaped** — Go's
  `url.Values.Encode()` emits UTF-8 percent-encoding, which turns every
  Cyrillic query (the primary use case) into mojibake and returns zero
  results. Add an `EncodeWindows1251` counterpart next to the existing
  `forumcommon.DecodeWindows1251` (`x/text/encoding/charmap` is already a
  dependency) and percent-escape the resulting bytes manually. Unit test
  with a Cyrillic query is mandatory.
- With `creds == nil` → return `ErrSearchRequiresCredentials` (RuTracker's
  tracker.php redirects anonymous requests to login).
- Parse result rows: `viewtopic.php?t=<id>` links with class `tLink` (title),
  `tor-size` cell (size, cp1251-decoded via `forumcommon`), `seedmed` cell
  (seeders), forum-name cell (category). Result URL rebuilt canonical:
  `https://<effectiveDomain>/forum/viewtopic.php?t=<id>`.
- cp1251 decoding is mandatory for title/category (same reason as
  `cleanTitle` — undecoded cp1251 is invalid UTF-8). Titles inside `tLink`
  anchors carry nested highlight spans — flatten tags (reuse the
  `forumcommon/posts.go` tag-flattening helpers) and unescape HTML
  entities; the same entity-unescape applies to rutor titles.

Both implementations get fixture-based unit tests (recorded/representative
HTML in `testdata/`, consistent with every existing plugin test) plus e2e
coverage via `HostRewriteTransport` where practical.

### 3. API — `GET /api/v1/trackers/search`

Authenticated route (same group as `/trackers/match`).

Query params:
- `q` (required, trimmed; 400 when empty; max 200 runes)
- `trackers` (optional CSV of tracker names to restrict; unknown names ignored)

Response `200`:

```json
{
  "results": [
    {
      "tracker_name": "rutor",
      "tracker_display_name": "Rutor.org",
      "title": "...",
      "url": "https://rutor.org/torrent/123",
      "size": "1.4 GB",
      "seeders": 17,
      "category": "..."
    }
  ],
  "errors": [
    { "tracker_name": "rutracker", "error": "search requires credentials" }
  ]
}
```

Handler behaviour:
- Iterate `registry.ListTrackers()`, keep `WithSearch` implementors,
  apply the `trackers` filter.
- For each searchable tracker that also implements `WithCredentials`: load
  the requesting user's credential for that tracker
  (`Creds.GetForTracker`), decrypt the secret **and, when present, the
  session blob** (`session_enc` — the credentials-test handler decrypts
  both; session-based trackers validate the session, not the password, so
  omitting it would plant a latent bug for a future searchable LostFilm),
  and ensure a live session: `Verify` first, `Login` only on failed
  verify. Note this Verify-first ordering is deliberately **new** — it is
  neither `loginAndVerify` (Login→Verify, always both — correct for
  validating fresh credentials, wasteful per search) nor the scheduler's
  `loadCredentials` (Login only). On a warm in-process session it costs
  one cheap GET; after a backend restart the first search takes the
  Verify-fail → Login path once. Tolerant degradation: any
  credential/login failure degrades to `creds = nil` and lets the plugin
  decide, so RuTracker reports `ErrSearchRequiresCredentials` instead of
  the whole request failing.
- Fan out concurrently (`errgroup` or plain WaitGroup), one goroutine per
  tracker, each with its own `context.WithTimeout(r.Context(), 15s)`.
  Collect results + per-tracker errors under a mutex. **Fail-open per
  tracker** — the endpoint returns 200 with partial results and an `errors`
  array; it only 4xxs on a bad request shape.
- Results are interleaved sorted by `seeders` descending (unknown last),
  stable by tracker name. No global cap beyond the per-tracker 50.
- The handler needs new deps (`Creds`, `Master`) on the `Trackers` handler
  struct — wire in `router.go`.

Timeout note: 15s per tracker mirrors the existing preview (15s) budget;
the fan-out means total latency ≈ slowest tracker, not the sum.

Abuse guard: a **per-user single-in-flight** gate (small in-memory
`map[userID]struct{}` under a mutex; concurrent second search → 429). Each
search fans out real scraping requests, and a scripted loop could get the
instance's IP banned by trackers. No general rate-limit middleware exists
in the codebase; this one-map guard is the proportionate version.

Decision (explicit): search failures do NOT feed
`domains.Store.ReportFailure` — domain rotation stays scheduler-driven.
An interactive search hitting a cold mirror shouldn't spin the ring the
scheduler depends on.

### 4. Capability discovery

`GET /api/v1/system/info` tracker entries gain
`"supports_search": bool` (same pattern as `supports_credentials`), so the
frontend can (a) show the search affordance only when ≥1 tracker is
searchable and (b) label which trackers are being searched / which need an
account.

### 5. Metrics

One counter, consistent with existing scheduler/tracker collectors:

```
marauder_tracker_search_total{tracker, result}  // result: ok|error|no_credentials
```

(Label is `tracker` — the code's actual convention across every existing
tracker-labelled collector; CLAUDE.md's `{tracker_name}` prose is stale.)

Registered in `internal/metrics`, incremented in the handler fan-out (not
in plugins).

### 6. Frontend

New component `frontend/src/components/topics/TrackerSearch.tsx`
(≤250 lines; sub-components split out if it grows):

- **Hosted in `AddTopicCard` (79 lines), NOT TopicForm** — TopicForm is
  418 lines, already over the 250 ceiling (tracked tech debt), and its
  add-mode URL lives in internal `addUrl` state initialized once from
  `initial.url`. AddTopicCard renders the mode toggle (two tab buttons —
  **By URL** default, **Search**) above either TrackerSearch or TopicForm.
  Picking a search result sets AddTopicCard-local `selectedUrl` state,
  switches to By-URL mode, and remounts the form via
  `<TopicForm key={selectedUrl} initial={{...EMPTY, url: selectedUrl}}>` —
  the existing debounced match/preview flow fires as if the URL was
  pasted. **TopicForm delta: zero lines.** Edit mode (EditTopicCard) never
  shows the toggle.
- Search mode: text input + explicit search button (Enter submits; **no
  search-as-you-type** — these are slow scraping calls, one request per
  deliberate action; React Query `useQuery` with `enabled: false` +
  `refetch()` on submit, key `QK.trackerSearch(q)` added to
  `queryKeys.ts`).
- Results list: each row shows title (truncated), tracker badge, size,
  seeders, category. Click → `onSelect(url)` up to AddTopicCard (see
  hosting note above).
- Per-tracker errors render as a dismissable muted line under the results
  ("RuTracker: needs a tracker account — add one under Accounts"), mapping
  `search requires credentials` to a friendly i18n string.
- Empty state after a search: honest "No results" (`—` style, per
  no-synthetic-data).
- i18n: new keys in both `en` and `ru` dictionaries.
- Tests: `TrackerSearch.test.tsx` — renders results from a mocked fetch,
  click fills URL, credential-error row shown, empty state.

### 7. Documentation & site

- `docs/trackers.md`: new "Searching trackers" section (which trackers,
  credential requirements).
- `CLAUDE.md`: registry capability list + handler/route/table updates.
- `CHANGELOG.md` `[Unreleased]`: feature bullet.
- `site/` (marauder.cc): add search to the feature list where the existing
  features are enumerated (keep copy consistent with the site's tone).
- `README.md` feature list: one bullet.

## Security considerations

- **SSRF**: search URLs are built exclusively from `effectiveDomain()`
  (allowlist-resolved) + URL-escaped query — the user controls only the
  query string, never the host. Same posture as existing canonical-URL
  builders.
- **Credential handling**: decrypt-in-handler mirrors the existing
  credentials-test endpoint; plaintext secrets never leave the process,
  never logged, never in responses.
- **XSS**: scraped titles/categories flow through React text rendering
  (auto-escaped); no `dangerouslySetInnerHTML`.
- **Injection into the pipeline**: a search result URL still passes
  through `CanParse` + `Parse` + the topics handler's normal validation —
  search grants no shortcut around URL validation.
- **Metrics cardinality**: `tracker_name` is a small closed set; `result`
  is a 3-value enum. No user data in labels.

## Success criteria (verifiable)

1. `go build ./... && go vet ./... && go test -race ./...` green in the
   golang:1.25 container.
2. `npm run typecheck && npm test && npm run build` green in node:20-alpine.
3. Live check against the local dev stack: `GET /api/v1/trackers/search?q=<real query>`
   returns real Rutor results (read-only live fetch — allowed by the
   no-synthetic-data rule); a click on a result in the UI creates a
   monitored topic end-to-end. **Known risk:** `rutor.org` (canonical
   domain) 403s some datacenter/VPS IPs while `rutor.info` serves fine —
   if the live check fails with 403, switch the active domain to
   `rutor.info` via the admin Tracker domains card (issue #126 machinery)
   rather than debugging the plugin.
4. `/code-review` findings fixed or explicitly triaged.

## Open questions resolved by default

- *Should search hit all trackers or only ones the user has credentials
  for?* → All searchable ones; credential-less login-gated trackers report
  a friendly per-tracker error instead of being silently absent.
- *Debounced live search?* → No; explicit submit. Scraping calls are
  expensive and slow; a keystroke-debounced fan-out would hammer trackers.
- *Cache results?* → React Query's in-memory cache only (default staleness);
  no server-side cache in phase 1.
