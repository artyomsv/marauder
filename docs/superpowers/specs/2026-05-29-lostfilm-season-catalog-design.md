# LostFilm season/episode catalog in the AddTopic form

Date: 2026-05-29
Status: Approved design — ready for planning
Branch: `feature/lostfilm-season-catalog`

## Problem

The AddTopic form's "Start from season / episode" inputs are free-text
(`e.g. 2` / `e.g. 5`). The user can type a season/episode that doesn't
exist, and there's no indication of how many seasons/episodes a series
actually has. For LostFilm we *can* know: `/series/<slug>/seasons` is a
public page listing every released `(season, episode)` via the same
`data-code` markers the scheduler already parses.

## Goal

When the matched tracker exposes a season catalog, the AddTopic form:
- shows a summary of what's released (e.g. "5 seasons · S01–S05"), and
- constrains the "Start from" inputs to **dependent dropdowns**: a Season
  picker (released seasons only) and an Episode picker showing only the
  selected season's released episodes. Both remain optional (blank = from
  the beginning).

Non-goals: changing how the scheduler filters episodes (the existing
`WithEpisodeFilter` + `start_season`/`start_episode` semantics are
unchanged — this only improves how those two values are *chosen*);
season catalogs for non-LostFilm trackers (the capability is generic so
others can adopt it later).

## Decisions (confirmed)
- Dependent dropdowns (not validated free-text).
- Built on a fresh branch off main (re-auth feature already merged).

## Architecture

### Backend

New optional capability in `registry`:
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

LostFilm implements `SeasonCatalog(ctx, url)`:
1. Extract the series slug from `url` via the existing `urlPattern`
   (group 1). Reject non-matching URLs with an error.
2. Build `https://<p.domain>/series/<slug>/seasons`.
3. GET it on a **no-credentials** session (`p.session(nil)` — the page is
   public; the transport hook still applies for tests).
4. Run the existing `parseEpisodes(body)` → `[]episodeRef{ShowID, Season,
   Episode}`.
5. Group by `Season`, collect sorted unique `Episode` numbers, return
   `[]registry.Season` sorted ascending by `Number`.

Handler (`api/handlers/trackers.go`):
- `trackerMatch` gains `SupportsSeasonCatalog bool json:"supports_season_catalog"`,
  set when the plugin implements `registry.WithSeasonCatalog`.
- New `GET /api/v1/trackers/seasons?url=<encoded>`:
  - 400 if `url` missing; 404 if no plugin matches; 422 if the matched
    plugin lacks `WithSeasonCatalog`.
  - else call `SeasonCatalog`; on error → 502 (upstream fetch/parse
    failed); on success → 200 `{seasons:[{number, episodes:[…]}]}`.
- Route registered next to `/trackers/match` (same authenticated group).

### Frontend (`frontend/src/pages/Topics.tsx`, AddTopicCard)

- `api.ts`: `SeasonInfo { number: number; episodes: number[] }`;
  `trackerSeasons(url) => GET /trackers/seasons`; add
  `supports_season_catalog: boolean` to the `TrackerMatch` interface.
- When `match.supports_season_catalog` and a URL is detected, fetch the
  catalog (React Query, keyed by URL, lazy/`enabled` on the flag).
- Summary hint above the selectors: e.g. "5 seasons · S01–S05".
- Replace the two free-text inputs with shadcn `Select` dropdowns:
  - **Season**: options = released season numbers (+ an empty "From the
    start" option). 
  - **Episode**: options = the episodes of the chosen season (+ empty);
    disabled until a season is chosen; resets if the season changes.
  - Both optional. The submitted `start_season`/`start_episode` payload
    is unchanged (still ints or omitted).
- If the catalog fetch fails or the tracker lacks the capability, fall
  back to the current free-text inputs (graceful degradation).

## Error handling

| Condition | Behavior |
|---|---|
| Tracker has no `WithSeasonCatalog` | `/trackers/seasons` → 422; UI falls back to text inputs |
| `/seasons` fetch/parse fails upstream | 502; UI falls back to text inputs + a subtle "couldn't load seasons" note |
| URL doesn't match the plugin's pattern | `SeasonCatalog` returns an error → 502/422; fallback |
| No episodes parsed (empty catalog) | 200 `{seasons:[]}`; UI shows text inputs (nothing to constrain) |

## Testing

- LostFilm — fixture e2e: stub `/series/<slug>/seasons` with multi-season
  `data-code` markup; assert `SeasonCatalog` returns the seasons grouped
  with correct episode lists, sorted; assert a non-matching URL errors.
- registry/handler — table test with a fake `WithSeasonCatalog` plugin:
  `/trackers/seasons` returns the JSON shape; non-catalog tracker → 422;
  unknown URL → 404; `supports_season_catalog` set in `/trackers/match`.
- frontend — Vitest: when match advertises the catalog, the season
  dropdown lists released seasons, the episode dropdown depends on the
  chosen season, the summary hint renders, and a failed catalog fetch
  falls back to text inputs.

Success check: backend `go build ./... && go vet ./... && go test -race
./...` green; frontend `npm run typecheck && npm test && npm run build`
green; manual: paste a LostFilm series URL → see the summary + dependent
dropdowns constrained to released values.

## Documentation

CLAUDE.md (new `WithSeasonCatalog` capability + `/trackers/seasons`
endpoint) and `docs/plugin-development.md` (how a tracker exposes a
season catalog) in the implementation commit(s).
