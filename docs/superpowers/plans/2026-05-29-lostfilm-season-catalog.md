# LostFilm Season Catalog Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Show released seasons/episodes in the AddTopic form and constrain the "Start from" inputs to dependent dropdowns (released values only).

**Architecture:** A `registry.WithSeasonCatalog` capability; LostFilm implements it by fetching the public `/series/<slug>/seasons` page and reusing the existing `parseEpisodes`. A `GET /trackers/seasons?url=` endpoint serves the catalog; the AddTopic form renders season → episode dependent dropdowns, falling back to text inputs if unavailable.

**Tech Stack:** Go 1.23, Postgres (no migration), React 19 + Vite + TS, Vitest. Docker for build/test.

**Spec:** `docs/superpowers/specs/2026-05-29-lostfilm-season-catalog-design.md`
**Conventions:** consumer-side interfaces; `interface` for TS object shapes; native `<select>` (match the existing Quality dropdown — there is no shadcn Select primitive); gofmt-clean changed files; backend gate after each backend task: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.23 sh -c "go build ./... && go vet ./... && go test -race ./..."`.

---

## Task C1: registry capability + LostFilm SeasonCatalog

**Files:** `registry/registry.go`; `plugins/trackers/lostfilm/lostfilm.go` (compile assertion); `lostfilm_season.go` or a new `lostfilm_catalog.go` (impl); `lostfilm_catalog_test.go`.

Context: `urlPattern` (in lostfilm.go) = `^https?://(?:www\.)?lostfilm\.(?:tv|win|run)/series/([^/]+)/?` — group 1 is the slug. `parseEpisodes(body []byte) []episodeRef{ShowID string; Season int; Episode int}` (already sorted+deduped). `p.fetch(ctx, target string, creds *domain.TrackerCredential) ([]byte,error)` fetches a URL on the creds' session; passing `nil` uses the no-credentials session (the page is public). `p.domain` is the host. Existing e2e tests use `e2etest.HostRewriteTransport{From:"www.lostfilm.tv", To: host, Inner:&allHostsToTest{To:host}}` + `permissiveRedirectValidator`.

- [ ] **Step 1: registry types** — in `registry.go`:
```go
// Season is one released season and its released episode numbers.
type Season struct {
	Number   int   `json:"number"`
	Episodes []int `json:"episodes"`
}

// WithSeasonCatalog is implemented by trackers that can enumerate a
// series' released seasons/episodes from its URL.
type WithSeasonCatalog interface {
	Tracker
	SeasonCatalog(ctx context.Context, url string) ([]Season, error)
}
```

- [ ] **Step 2: failing LostFilm test** — `lostfilm_catalog_test.go`:
```go
package lostfilm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

func TestSeasonCatalog_GroupsBySeasin(t *testing.T) {
	// data-code="442-<season>-<episode>" markers across 2 seasons.
	html := `<div data-code="442-1-1"></div><div data-code="442-1-2"></div>` +
		`<div data-code="442-2-1"></div><div data-code="442-2-2"></div><div data-code="442-2-3"></div>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/seasons") {
			_, _ = w.Write([]byte(html))
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	p := &plugin{
		sessions:          forumcommon.New(),
		domain:            "www.lostfilm.tv",
		transport:         &e2etest.HostRewriteTransport{From: "www.lostfilm.tv", To: host, Inner: &allHostsToTest{To: host}},
		redirectValidator: permissiveRedirectValidator,
	}
	seasons, err := p.SeasonCatalog(context.Background(), "https://www.lostfilm.tv/series/The_Boys")
	if err != nil {
		t.Fatalf("SeasonCatalog: %v", err)
	}
	if len(seasons) != 2 {
		t.Fatalf("seasons = %d, want 2", len(seasons))
	}
	if seasons[0].Number != 1 || len(seasons[0].Episodes) != 2 {
		t.Errorf("season1 = %+v, want {1,[1 2]}", seasons[0])
	}
	if seasons[1].Number != 2 || len(seasons[1].Episodes) != 3 {
		t.Errorf("season2 = %+v, want {2,[1 2 3]}", seasons[1])
	}
}

func TestSeasonCatalog_RejectsNonSeriesURL(t *testing.T) {
	p := &plugin{sessions: forumcommon.New(), domain: "www.lostfilm.tv"}
	if _, err := p.SeasonCatalog(context.Background(), "https://example.com/x"); err == nil {
		t.Error("want error for non-series URL")
	}
}
```

- [ ] **Step 3: implement** — add to `lostfilm_session.go` (or a new `lostfilm_catalog.go` in package lostfilm). Add `sort` + `registry` imports as needed (`registry` already imported in lostfilm_session.go):
```go
// SeasonCatalog implements registry.WithSeasonCatalog. It fetches the
// public /series/<slug>/seasons page and groups the parsed episode
// triples by season. No credentials needed — the catalog is public; only
// the .torrent links are gated.
func (p *plugin) SeasonCatalog(ctx context.Context, rawURL string) ([]registry.Season, error) {
	m := urlPattern.FindStringSubmatch(rawURL)
	if m == nil {
		return nil, fmt.Errorf("lostfilm: not a series URL: %s", rawURL)
	}
	body, err := p.fetch(ctx, "https://"+p.domain+"/series/"+m[1]+"/seasons", nil)
	if err != nil {
		return nil, err
	}
	bySeason := map[int][]int{}
	var order []int
	for _, e := range parseEpisodes(body) {
		if _, ok := bySeason[e.Season]; !ok {
			order = append(order, e.Season)
		}
		bySeason[e.Season] = append(bySeason[e.Season], e.Episode)
	}
	sort.Ints(order)
	out := make([]registry.Season, 0, len(order))
	for _, s := range order {
		ep := bySeason[s]
		sort.Ints(ep)
		out = append(out, registry.Season{Number: s, Episodes: ep})
	}
	return out, nil
}
```
Add the compile-time assertion in lostfilm.go near the others: `var _ registry.WithSeasonCatalog = (*plugin)(nil)`.

- [ ] **Step 4: gate** (full backend gate) — PASS, incl. the 2 new tests.
- [ ] **Step 5: commit** — `git add backend/internal/plugins backend/internal/plugins/registry && git commit -m "feat(lostfilm): SeasonCatalog capability from /seasons page"`

---

## Task C2: /trackers/seasons endpoint + capability flag

**Files:** `api/handlers/trackers.go`; `api/router.go`; `api/handlers/trackers_test.go` (create if absent).

Context: `trackerMatch` struct + `Match` handler in trackers.go; `registry.FindTrackerForURL(url)`, `registry.GetTracker`. `Trackers{BaseURL}` handler. `problem.Err*`, `writeJSON`. Route group for `/trackers/match` in router.go.

- [ ] **Step 1: capability flag** — add `SupportsSeasonCatalog bool json:"supports_season_catalog"` to `trackerMatch`; in `Match`, after the other capability checks: `if _, ok := t.(registry.WithSeasonCatalog); ok { out.SupportsSeasonCatalog = true }`.

- [ ] **Step 2: Seasons handler** — add to trackers.go:
```go
// Seasons handles GET /api/v1/trackers/seasons?url=<encoded> — the
// released season/episode catalog for the matched tracker.
func (h *Trackers) Seasons(w http.ResponseWriter, r *http.Request) {
	rawURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if rawURL == "" {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("url query parameter is required"))
		return
	}
	t := registry.FindTrackerForURL(rawURL)
	if t == nil {
		problem.Write(w, r, h.BaseURL, problem.ErrNotFound("no tracker plugin matches this URL"))
		return
	}
	sc, ok := t.(registry.WithSeasonCatalog)
	if !ok {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("tracker '"+t.Name()+"' has no season catalog"))
		return
	}
	seasons, err := sc.SeasonCatalog(r.Context(), rawURL)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadGateway("season catalog unavailable: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"seasons": seasons})
}
```
If `problem.ErrBadGateway` does not exist, check the problem package for the 502 constructor and use it; if there's none, add one mirroring the existing `Err*` constructors (502 "Bad Gateway"), or fall back to `problem.ErrUnprocessable`. Report which you used.

- [ ] **Step 2b: route** — in router.go next to `/trackers/match`: `r.Get("/trackers/seasons", trackersH.Seasons)`.

- [ ] **Step 3: test** — in `trackers_test.go` (mirror existing handler-test patterns; register a fake tracker implementing `registry.Tracker` + `WithSeasonCatalog` whose `CanParse` matches a test URL and `SeasonCatalog` returns 2 seasons). Assert: `/trackers/seasons?url=` → 200 with `seasons` JSON; a tracker without the capability → 422; unknown URL → 404; missing url → 400. If there is no existing trackers handler test harness, model it on `credentials_interactive_test.go` (httptest + the registry). Confirm `FindTrackerForURL` matches your fake (its `CanParse` must return true for the test URL).

- [ ] **Step 4: gate + commit** — `git add backend/internal/api && git commit -m "feat(api): GET /trackers/seasons + supports_season_catalog flag"`

---

## Task C3: AddTopic dependent dropdowns

**Files:** `frontend/src/lib/api.ts`; `frontend/src/pages/Topics.tsx`; `frontend/src/lib/queryKeys.ts` (add a `trackerSeasons` key); `Topics` has no test file — create `frontend/src/pages/Topics.test.tsx` OR add focused coverage (report which). Frontend gate: `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test && npm run build"`.

Context: `AddTopicCard` in Topics.tsx has `startSeason`/`startEpisode` `useState<string>` rendered as `<select>`/`<Input>` (the Quality field at ~line 467 is a native `<select>` — match it). It already debounces the URL and runs `trackerMatchQuery` (QK.trackerMatch). The submit maps `start_season`/`start_episode` to ints. `TrackerMatch` interface is local in Topics.tsx (line ~346).

- [ ] **Step 1: api + queryKey** — `api.ts`: `export interface SeasonInfo { number: number; episodes: number[] }`. Add `supports_season_catalog: boolean` to the `TrackerMatch` interface IN Topics.tsx (it's defined there). In `queryKeys.ts` add `trackerSeasons: (url: string) => ["trackerSeasons", url] as const`. (No api.ts method needed — call `api.get` directly, matching how trackerMatch is fetched.)

- [ ] **Step 2: catalog query** — in `AddTopicCard`, after `trackerMatchQuery`:
```tsx
const seasonsQuery = useQuery({
  queryKey: QK.trackerSeasons(debouncedUrl),
  queryFn: () => api.get<{ seasons: SeasonInfo[] }>(`/trackers/seasons?url=${encodeURIComponent(debouncedUrl)}`),
  enabled: debouncedUrl.length >= 8 && !!match?.supports_season_catalog,
  staleTime: 60_000,
  retry: false,
});
const seasons = seasonsQuery.data?.seasons ?? [];
const catalogReady = !!match?.supports_season_catalog && seasons.length > 0 && !seasonsQuery.isError;
const selectedSeasonEpisodes = seasons.find((s) => String(s.number) === startSeason)?.episodes ?? [];
```
Import `type SeasonInfo` from `@/lib/api`.

- [ ] **Step 3: summary + dependent dropdowns** — when `catalogReady`, render a summary hint (e.g. `{seasons.length} seasons · S{first}–S{last}`) and replace the two inputs with native `<select>`s styled exactly like the Quality `<select>`:
  - Season select: value `startSeason`; options = `["" → "From the start"]` + each `season.number`. On change, set `startSeason` and RESET `startEpisode` to `""`.
  - Episode select: value `startEpisode`; disabled when `!startSeason`; options = `["" → "From the start"]` + `selectedSeasonEpisodes`. 
  - When `!catalogReady` (no support / fetch error / empty), render the EXISTING free-text inputs unchanged (graceful fallback).
  Keep both optional; the submit payload mapping (`start_season`/`start_episode` → int or undefined) is unchanged.

- [ ] **Step 4: test** — add a test (new `Topics.test.tsx`, mirroring `Credentials.test.tsx` setup: mock `api.get` to return a match with `supports_season_catalog:true` for the match call and a 2-season catalog for the seasons call; QueryClientProvider). Assert: the season dropdown lists the released seasons; selecting a season populates the episode dropdown with that season's episodes; a failed seasons fetch falls back to text inputs. If a full Topics test harness is too heavy (the page has none today), write the smallest meaningful test and report the limitation.

- [ ] **Step 5: frontend gate + commit** — `git add frontend/src && git commit -m "feat(frontend): season/episode dependent dropdowns from catalog"`

---

## Task C4: Docs

**Files:** `CLAUDE.md`; `docs/plugin-development.md`.

- [ ] **Step 1:** CLAUDE.md — add `WithSeasonCatalog` to the registry capability list; note `GET /trackers/seasons`.
- [ ] **Step 2:** plugin-development.md — add `WithSeasonCatalog` to the optional-capabilities code block + a sentence on implementing it (fetch the catalog page, reuse the episode parser).
- [ ] **Step 3: commit** — `git add CLAUDE.md docs/plugin-development.md && git commit -m "docs: document WithSeasonCatalog capability"`

---

## Self-Review notes
- Spec coverage: capability+impl (C1), endpoint+flag (C2), dropdowns+fallback (C3), docs (C4).
- No migration (read-only feature).
- Graceful degradation: every failure path (no capability, fetch error, empty catalog) falls back to the existing text inputs — the form never breaks.
- Reuses `parseEpisodes` so catalog == what the scheduler sees.
- Type consistency: `registry.Season{Number,Episodes}`, `SeasonInfo{number,episodes}`, `SupportsSeasonCatalog`/`supports_season_catalog`, `SeasonCatalog`, `/trackers/seasons`.
