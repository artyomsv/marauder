# Changelog

All notable changes to Marauder will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed (Phase 7 — host port migration to 34xxx range)

Marauder previously exposed the gateway on host port `6688`, the dev
overlay used `8679` / `8680` / `55432` / `6611` / `9191`, and the SSO
overlay used `8643`. All of these violate
`~/.claude/rules/local-port-ranges.md`, which requires host-exposed
ports to live in the `30000-49999` range to avoid colliding with
gcloud emulators, IDE debuggers, framework defaults, and other dev
tools that compete for low-numbered ports.

This phase migrates every host-exposed port to the `34xxx` namespace
(Marauder = project number 4 in the `3Xxxx` mnemonic). **Container-
internal ports stay unchanged** per the rule's scope clarification —
only the host side of the `ports:` mapping moves.

### New host port allocation

| Service | Old | New | Container-internal |
|---|---|---|---|
| Gateway (prod) | `6688` | **`34080`** | 6688 |
| Vite dev server | `5174` | **`34000`** | n/a |
| Backend (dev overlay) | `8679` | **`34081`** | 8679 |
| Frontend container (dev overlay) | `8680` | **`34001`** | 8081 |
| Postgres (dev overlay) | `55432` | **`34432`** | 5432 |
| qBittorrent (dev overlay) | `6611` | **`34611`** | 6611 |
| Transmission (dev overlay) | `9191` | **`34091`** | 9091 |
| Keycloak (sso overlay) | `8643` | **`34643`** | 8643 |

`55432` was actually the worst offender — completely out of the
allowed range (above 49999). The old `6611` for qBittorrent and
`9191` for Transmission were in forbidden 6xxx/9xxx zones.

### Files changed

- **`deploy/docker-compose.yml`** — gateway port mapping now
  `${MARAUDER_HOST_PORT:-34080}:6688`. Defaults for
  `MARAUDER_PUBLIC_BASE_URL` and `MARAUDER_CORS_ORIGINS` updated to
  `http://localhost:34080`.
- **`deploy/docker-compose.dev.yml`** — every published port wrapped
  in env vars with `34xxx` defaults: `MARAUDER_DEV_DB_PORT`,
  `MARAUDER_DEV_BACKEND_PORT`, `MARAUDER_DEV_FRONTEND_PORT`,
  `MARAUDER_DEV_QBIT_PORT`, `MARAUDER_DEV_TRANSMISSION_PORT`.
- **`deploy/docker-compose.sso.yml`** — Keycloak host port → `34643`
  (still listens on 8643 internally so the docker-network DNS link
  `http://keycloak:8643/realms/marauder` from the backend
  `MARAUDER_OIDC_ISSUER` still works). `MARAUDER_OIDC_REDIRECT_URL`
  default is the browser-facing host port `http://localhost:34080/...`.
- **`deploy/.env.example`** — every `localhost:6688` reference and
  `MARAUDER_HOST_PORT=34080` documented at the top of the Server
  section.
- **`deploy/keycloak/realm-marauder.json`** — `redirectUris`,
  `webOrigins`, and `post.logout.redirect.uris` all updated to
  `localhost:34080`.
- **`backend/internal/config/config.go`** — `CORSOrigins` and
  `PublicBaseURL` envDefault tags now point at `34000` (Vite dev) and
  `34080` (gateway).
- **`frontend/vite.config.ts`** — dev server `port: 34000` (was
  5174). The `/api` proxy default target is now
  `http://localhost:34081` (the dev compose backend host port).
- **`.github/workflows/e2e.yml`** — every `curl` to `localhost:6688`
  → `localhost:34080`, and `localhost:6611` → `localhost:34611`.
- **`README.md`, `CONTRIBUTING.md`, `docs/PRD.md`, `docs/oidc.md`,
  `docs/test-e2e-magnet.md`** — all quick-start instructions, sample
  curl commands, and infrastructure tables updated.
- **`site/src/pages/{features,index,install}.astro`** — marketing
  copy and code blocks updated.
- **`CLAUDE.md`** — Ports section rewritten with the full new
  allocation table including the env var override knobs.

### Why this matters

The previous `6688` port worked fine in isolation but was a ticking
time bomb on a developer machine running multiple services
simultaneously. The user's `docker ps` showed:
- `projectr-x` already owns the entire `31xxx` band
- `test-me-ai` uses `45xxx`
- `gotenberg` and `clamav` squat on `3100` and `3310`
- `keycloak-shared-dev` sits on `9080`

Marauder's `6688` happened not to collide but `8679` / `8680` /
`6611` / `9191` from the dev overlay all could have. Pushing every
host port into the `34xxx` band gives Marauder its own isolated
namespace and aligns with the global rule.

### Fixed (Phase 6 — credential test false-positive hotfix)

A user reported entering an intentionally wrong username, clicking the
"Test login" button on the Credentials page, and seeing the UI report
success. Root cause analysis found **three layered bugs** across the
handler and all 8 forum-tracker plugins:

- **`credentials.go` handler discarded `Verify`'s bool return.** The
  `Test` handler did `if _, err := wc.Verify(...); err != nil` —
  `Verify` returns `(bool, error)` where the bool is "is the session
  still alive". When the user wasn't really logged in, Verify returned
  `(false, nil)` and the handler treated the missing error as success.
  The `Create` and `Update` handlers didn't call `Verify` at all,
  relying on `Login` returning nil as the success signal.
- **`rutracker.go` Login ALWAYS succeeded** due to an
  `|| resp.StatusCode == 200` escape hatch in its success check. The
  login page always returns 200 (with a "wrong password" form on
  failure), so Login never failed regardless of credentials.
- **`tapochek.go` Login had no check at all** — it just did the POST,
  closed the body, and set `sess.LoggedIn = true`. `Verify` returned
  `(true, nil)` unconditionally. Tapochek was effectively a "trust
  whatever the user typed" credential sink.
- **`lostfilm_session.go` Login was fragile** — only checked for the
  substring `"error"` in the response body, with no HTTP status check
  and no positive success indicator. `Verify` checked for the literal
  word `"logout"` which could false-positive on any page that happened
  to mention it (meta descriptions, cookie banners, etc).
- **Every other forum tracker** (`kinozal`, `nnmclub`, `anidub`,
  `toloka`, `unionpeer`, `hdclub`, `freetorrents`) did only
  negative-indicator detection: "does the response body contain the
  phrase for 'wrong password' in the target language?". Fragile,
  dependent on upstream wording staying stable.
- **11 `body, _ := io.ReadAll(...)` discards** across these 8 plugins
  violated `~/.claude/rules/go-conventions.md` (never assign to `_`
  without justification). I caught and fixed this pattern in
  `scheduler.go` during the Phase 5 refactor but missed the tracker
  plugins.

### What changed

- **`loginAndVerify` helper** in `credentials.go`: runs Login + Verify
  and fails if EITHER step fails, explicitly treating
  `Verify(...) == (false, nil)` as failure with a clear error
  message. Used uniformly by `Create`, `Update` (password-rotation
  branch), and `Test`. This is the primary fix — even if a plugin's
  Login has a false-positive, Verify's positive-indicator check
  (authenticated page contains a logged-in marker) catches it.
- **`rutracker.go`**: removed the `|| resp.StatusCode == 200` escape
  hatch. Login now requires the positive `id="logged-in-username"`
  marker in the response body.
- **`tapochek.go`**: `Verify` now hits `/index.php` and looks for a
  `logout.php?sid=` nav link (phpBB authenticated-only pattern). A
  permissive "always true" was the worst possible default.
- **`lostfilm_session.go`**:
  - `Login` now requires HTTP 200 and a non-empty body in addition to
    the negative-indicator check. Real LostFilm success returns a
    JSON user object; empty or non-200 is explicitly rejected.
  - `Verify` now requires BOTH a specific `href="/logout"` anchor AND
    the absence of the login form (`type="password"` / `name="mail"`).
    Belt-and-suspenders — a site redesign would have to flip both
    signals at once to silently break the check.
- **`credentials_test.go`** (new): table-driven regression test for
  `loginAndVerify`. The `verify returns (false, nil) — the
  regression the user reported` case is pinned closed forever.
- **All 8 forum plugin bodies** now propagate `io.ReadAll` errors
  with `%w` wrapping instead of discarding them with `_`.
- **`tapochek_e2e_test.go`** fake server updated to serve an
  `/index.php` page with the `logout.php?sid=` marker that the new
  Verify expects.

### Known limitations

The 7 forum plugins with only-negative-indicator Login checks
(`kinozal`, `nnmclub`, `anidub`, `toloka`, `unionpeer`, `hdclub`,
`freetorrents`) are still somewhat fragile in isolation — a wording
change to the login page would break them. But `loginAndVerify` in
the handler makes this defense-in-depth: the handler always calls
Verify after Login, and Verify uses positive indicators. A regression
would require BOTH the negative-indicator AND the Verify marker to
drift at the same time. If any plugin's Verify also turns out to be
too loose, the same helper makes it easy to audit — it's one call
site for all 8 plugins.

### Phase 5 — code-review remediation (parallel-agent refactor)

A four-agent code review (security-officer + code-reviewer + rules-
compliance + qa) of the Phase 4 commits surfaced ~25 findings ranging
from a real correctness bug (mid-loop submit failure marking
`updated=false`) to an SSRF in the LostFilm redirector chain to a
brittle stringly-typed cross-package error contract. This phase fixes
all of them. Work was split into 8 tracks across 7 parallel agents
plus an orchestrator, with file ownership designed to avoid edit
conflicts.

### Added (Phase 5a — typed sentinel + shared extra package)
- **`backend/internal/plugins/registry/errors.go`** (new) — exports
  `var ErrNoPendingEpisodes = errors.New("no pending episodes")`. Per-
  episode trackers (currently only LostFilm) wrap this via `%w` from
  `Download` when the pending list is empty; the scheduler matches it
  via `errors.Is`. Replaces the previous `strings.Contains` substring
  match that was the brittle inter-package contract flagged by every
  reviewer.
- **`backend/internal/extra/`** (new package) — exports
  `extra.Int(m, key)`, `extra.StringSlice(m, key)`,
  `extra.String(m, key, fallback)`. Reads values out of the untyped
  `map[string]any` blobs that `domain.Topic.Extra` and
  `domain.Check.Extra` carry, handling JSON-roundtrip shape drift
  (`[]string`→`[]any`, `int`→`float64`). Both `lostfilm.go` and
  `scheduler.go` had near-identical local copies of these helpers
  before; both now import the shared package and the local copies
  are deleted.

### Changed (Phase 5b — scheduler refactor + 10 new unit tests)
- **`runCheck` split** into a thin orchestrator + `loadCredentials` +
  `downloadAllPending` + `recordResult`. `runCheck` is now ~30 lines
  (was 125) and each piece is independently testable.
- **C-1 fix — mid-loop submit failure now records progress.** Previously
  a submit failure on iteration `i > 0` called `RecordCheckResult` with
  hardcoded `updated=false`, forgetting the timestamp of the last
  topic-updated event. Now passes `updated || anySubmitted`.
- **H-5 fix — per-iteration context.** The download loop previously
  shared a single `TrackerHTTPTimeout + 5s` deadline across up to 25
  Download+Check round-trips; a 12-episode series could trip the
  deadline mid-loop and lose `RecordCheckResult` for the successful
  hash. Each iteration now gets its own
  `context.WithTimeout(ctx, TrackerHTTPTimeout)`; persistence calls
  use the parent ctx so they survive iteration deadline expiry.
- **H-6 fix — dropped redundant `tr.Check`.** The loop previously
  refetched the entire LostFilm series page after every single episode
  download (12 episodes = 13 series-page fetches per tick). Remaining
  episodes are now derived locally from
  `check.Extra["pending_episodes"][1:]`.
- **H-7 fix — atomic persistence.** `markEpisodeDownloaded` no longer
  silently swallows persistence failures. Uses the new atomic
  `Topics.MarkEpisodeDownloaded(ctx, id, packed)` repo method (Phase
  5d) when the topics repo implements the optional interface; bubbles
  errors out so the next tick retries.
- **H-8 fix — `maxPerTick` is now configurable** via
  `cfg.SchedulerMaxEpisodesPerTick` (env
  `MARAUDER_SCHEDULER_MAX_EPISODES_PER_TICK`, default 25). Cap-hit logs
  a Warn and increments the new
  `marauder_scheduler_episodes_per_tick_capped_total{tracker_name}`
  counter so operators can see when a runaway tracker is losing
  progress every tick.
- **R-1 fix — discarded errors.** All seven `_ = s.topics.RecordCheckResult(...)`
  call sites in `runCheck` go through a new `recordResult` wrapper
  that logs persistence failures at Warn level. No more silent swallows
  in the hot path.
- **Testability seams**: introduced 5 small consumer-side interfaces
  (`topicsRepo`, `markEpisodeDownloader`, `clientsRepo`,
  `credentialsRepo`, `decryptor`) plus 2 lookup-fn fields
  (`trackerLookupFn`, `clientLookupFn`) on `*Scheduler`. The exported
  `New(...)` constructor signature is unchanged so `cmd/server/main.go`
  still compiles.
- **`backend/internal/scheduler/scheduler_test.go`** (new) — 10 test
  functions covering: hash unchanged, single-payload happy path,
  3-pending-episodes loop, first-iteration error, mid-loop error
  preserves progress, persistence failure mid-loop, max-per-tick cap
  enforcement, fmt-wrapped sentinel matching, table-driven backoff
  curve (7 cases across 0–20 consecutive errors with cap at 6h), and
  the typed sentinel matcher itself. The scheduler package previously
  reported `[no test files]`; this closes
  `techdebt/2-3-scheduler-no-unit-tests.md` (file removed in this
  phase).

### Changed (Phase 5c — LostFilm hardening + 4-file split)
- **HIGH-1 fix — SSRF allowlist.** `fetchTorrentByPacked` previously
  followed any `Location`/meta-refresh URL through external hosts with
  the user's authenticated session cookies attached, with no host
  allowlist. A compromised redirector could have pointed at internal
  addresses or exfiltrated cookies. New `validateRedirectURL`:
  - parses the URL and rejects non-`http(s)` schemes,
  - rejects any host not in `allowedRedirectHosts` (lostfilm.tv +
    its known redirector chain hosts),
  - resolves the host via `net.LookupIP` and rejects loopback,
    private (RFC 1918), link-local, and unspecified addresses.
  Applied to BOTH the v_search next-hop AND the final `.torrent` URL.
  A test seam (`plugin.redirectValidator` field) lets unit tests
  install a permissive validator since httptest uses 127.0.0.1.
- **H-9 fix — `userAgent` reverted to project convention.** Was
  `"Mozilla/5.0 (Marauder; +https://marauder.cc) AppleWebKit/537.36"`
  — the worst of both worlds (still trivially a bot, inconsistent
  with every other plugin). Now `"Marauder/1.1 (+https://marauder.cc)"`
  with an explanatory comment.
- **LOW-4 fix — body preview leakage gone.** The `v_search returned
  no redirect` error previously embedded up to 200 bytes of upstream
  HTML in its message, which the scheduler persisted into
  `topics.last_error` and the UI displayed. If the upstream login
  page contained a CSRF token, it ended up in the DB. Replaced with
  a stable string: `"likely not authenticated, please re-add credentials"`.
- **`Download` returns the typed sentinel** when `pending_episodes`
  is empty, via `fmt.Errorf("lostfilm Download: %w (...)", registry.ErrNoPendingEpisodes)`.
- **Adopted shared extra package** — local `extraInt`,
  `extraStringSlice`, `stringFromAny` deleted; all call sites use
  `extra.Int` / `extra.StringSlice` / `extra.String`.
- **4-file split** — `lostfilm.go` was ~685 lines (over the ~300
  ceiling). Now split across:
  - `lostfilm.go` (~245 lines) — package doc, plugin struct, registry
    registration, `CanParse`, `Parse`, `Check`, `Download`
  - `lostfilm_session.go` — constants, `urlPattern`, `Login`,
    `Verify`, `session`, `fetch`, `fetchURL`
  - `lostfilm_parse.go` — episode regexes, `episodeRef`,
    `parseEpisodes` (now using `sort.Slice`)
  - `lostfilm_redirector.go` — `allowedRedirectHosts`,
    `validateRedirectURL`, `fetchTorrentByPacked` orchestrator,
    `resolveVSearchRedirect`, `pickQualityLink`, `qualityMatches`,
    `sanitiseQuality`
- **Read errors no longer silently dropped** — `Login`, `Verify`,
  `fetch`, and `fetchURL` previously did `body, _ := io.ReadAll(...)`.
  IO errors now propagate with `%w` wrapping.
- **`qualityMatches` unknown-quality fallback removed** — previously
  fell through to `strings.Contains` for unknown tiers, which would
  re-introduce the 1080p/1080p_mp4 trap for any future quality. Now
  returns `false` for unknown qualities, forcing callers to add new
  tiers explicitly.
- **`parseEpisodes`** now uses `sort.Slice` (clearer for typical
  100+-episode series) and the `_ = strconv.Atoi(...)` discards have
  comments noting the regex guarantees digit-only matches.
- **New tests**: `TestValidateRedirectURL` (table-driven, 7 cases
  including allowlist + scheme + loopback + private IP rejection),
  `TestDownloadEmptyPendingReturnsTypedSentinel` (3 subtests asserting
  `errors.Is(err, registry.ErrNoPendingEpisodes)` for nil-Extra,
  empty-slice, and missing-key cases), 2 new `TestQualityMatcher`
  cases for unknown qualities. Existing `TestE2E` and
  `TestRedirectorFlow` updated to install a permissive validator.

### Added (Phase 5d — atomic topics repo + first repo-package tests)
- **`Topics.MarkEpisodeDownloaded(ctx, id, packed) error`** — new
  atomic method that appends a packed episode ID to
  `extra["downloaded_episodes"]` via a single SQL `jsonb_set` +
  `||` expression. Unlike `UpdateExtra` (which read-modify-writes
  the entire blob) this:
  - cannot wipe other extras keys,
  - is safe under concurrent updates,
  - returns `repo.ErrNotFound` if the topic was deleted between
    Check and the loop iteration.
- **`UpdateExtra` no longer silently no-ops** when the topic is gone
  — captures `RowsAffected()` and returns `ErrNotFound`. Marked
  `// Deprecated:` in the doc comment in favor of the new atomic
  method.
- **`scanTopic` no longer swallows malformed JSON** in the `extra`
  blob. Previously `_ = json.Unmarshal(extraRaw, &t.Extra)` returned
  an empty map silently; now returns a wrapped error so the row is
  rejected and the caller can log it.
- **Testability refactor**: `Topics.pool` field changed from
  `*pgxpool.Pool` to a small unexported `topicsPool` interface
  (`Exec` / `Query` / `QueryRow`) so `pgxmock` can slot in. The
  `NewTopics` constructor still takes `*pgxpool.Pool` so callers
  don't change.
- **`backend/internal/db/repo/topics_test.go`** (new) — first tests
  in the `db/repo` package. 10 test functions covering happy
  path / not-found / DB error / nil-map serialization for both
  `UpdateExtra` and `MarkEpisodeDownloaded`, plus a regression
  test that drives `GetByID` through a row with a malformed
  `extra` blob and asserts the new error path. Uses `pgxmock/v3` —
  no Docker Postgres needed.

### Added (Phase 5e — frontend hooks + centralized query keys)
- **`frontend/src/lib/queryKeys.ts`** (new) — `QK` constant exporting
  every React Query key used in the codebase as `as const` tuples:
  `QK.clients`, `QK.client(id)`, `QK.topics`, `QK.credentials`,
  `QK.notifiers`, `QK.systemInfo`, `QK.trackerMatch(url)`,
  `QK.audit`, `QK.systemStatus`. Replaces 20+ inline string-literal
  keys across the page files. A typo in any `invalidateQueries` call
  is now a TypeScript error instead of a silent no-op.
- **`frontend/src/lib/hooks/useSystemInfo.ts`** (new) — wraps the
  `/system/info` query (5-min stale time, no auth) so the AppShell
  version chip and the Settings About card and the Clients,
  Credentials, Notifiers pages all share one cache entry instead of
  five duplicate `useQuery` blocks.
- **`frontend/src/lib/hooks/useLogout.ts`** (new) — extracts the
  refresh-token revoke + auth-store clear + navigate-to-login
  sequence that was duplicated between AppShell and Settings.
  Standardizes both surfaces on SPA navigation (`useNavigate`) —
  Settings previously did a hard `window.location.href = "/login"`
  reload.
- **`frontend/src/lib/hooks/useDebouncedValue.ts`** (new) — generic
  `useDebouncedValue<T>(value, delayMs): T` for feeding text-input
  state into React Query's `enabled` flag without firing a request
  on every keystroke. Used by Topics' `/trackers/match` lookup.
- **`AppShell.tsx` and `Settings.tsx`** refactored to use the new
  hooks, dropping the duplicated query/logout code.

### Added (Phase 5f — shared ResourceCard component)
- **`frontend/src/components/shared/ResourceCard.tsx`** (new) — slot-
  based card chrome for list pages. Props: `title`, `icon?`,
  `badges?`, `actions?`, `children?`, `glow?` (`primary` or
  `accent`), `onClick?`. Preserves the same `framer-motion`
  animation, `group-hover:opacity-100` actions reveal, and Tailwind
  blur/glow background that the three pages used inline.
- **`Clients.tsx`, `Credentials.tsx`, `Notifiers.tsx`** all migrated
  to render their list cards via `<ResourceCard>`. Adopted `QK.*`
  query keys and the `useSystemInfo()` hook from Phase 5e. Per-page
  Test connection / Edit / DeleteConfirm action rows preserved
  unchanged.

### Changed (Phase 5g — Topics page refactor + useArmedConfirm)
- **`frontend/src/lib/hooks/useArmedConfirm.ts`** (new) — extracts
  the idle⇄armed state machine that `DeleteConfirm` and the Topics
  `BulkActionBar` had implemented separately. Returns
  `{ armed, arm, disarm, confirmAndDisarm }` with an auto-disarm
  timer (default 4000 ms).
- **`DeleteConfirm.tsx`** internal `useState`+`useEffect`+`useRef`
  machinery replaced with `useArmedConfirm({ timeoutMs })`. External
  API unchanged. Hardcoded `aria-label="Delete?"` now derives from
  the `label` prop (`Delete topic?`, `Delete client?`, etc) for
  future i18n.
- **`Topics.tsx` `BulkActionBar`** rewritten to use `useArmedConfirm`
  instead of its inline `useState(false)` + `setTimeout` clone.
- **`Topics.tsx` AddTopicCard `/trackers/match` lookup** — the
  hand-rolled `useEffect` + `setTimeout` debounce (with an
  `eslint-disable-next-line react-hooks/exhaustive-deps` smell)
  replaced with `useDebouncedValue(url, 350)` + `useQuery({
  queryKey: QK.trackerMatch(debounced), enabled: debounced.length >= 8 })`.
  React Query owns the cache; the local `useState<TrackerMatch | null>`
  and `useState<string | null>(matchError)` are gone.
- All `["topics"]` literal query keys in Topics.tsx replaced with
  `QK.topics`.

### Added (Phase 5h — Vitest + RTL + first frontend test)
- **`frontend/vitest.config.ts`** (new) — Vitest config with
  `environment: 'jsdom'`, the `@vitejs/plugin-react` plugin, and
  the `@/*` path alias.
- **`frontend/src/test/setup.ts`** (new) — wires
  `@testing-library/jest-dom/vitest` matchers and runs RTL `cleanup()`
  after each test.
- **`frontend/src/components/shared/DeleteConfirm.test.tsx`** (new)
  — 7 tests against the `DeleteConfirm` public API: idle render,
  arming, confirm fires `onConfirm`, cancel returns to idle, default
  4 s auto-disarm via `vi.useFakeTimers({ shouldAdvanceTime: true })`,
  custom `timeoutSeconds`, `isPending` disables the button. Uses
  `userEvent` (not `fireEvent`) for higher-fidelity interactions.
- **`frontend/package.json`** — added `vitest@^2.1.8`,
  `@testing-library/react@^16.1.0`,
  `@testing-library/user-event@^14.5.2`,
  `@testing-library/jest-dom@^6.6.3`, `jsdom@^25.0.1` to
  devDependencies; added `test` and `test:watch` scripts. The
  frontend previously had no test runner at all.

### Fixed (Phase 5i — doc drift + transitional cleanup)
- **`deploy/docker-compose.yml`** dev marker bumped from
  `0.4.0-alpha` (which was both stale post-v1.0.0 AND inconsistent
  with the previous commit's CHANGELOG/ROADMAP that already claimed
  `1.1.0-dev`) to `1.1.0-dev` for backend / frontend / cfsolver.
  Added the new `MARAUDER_SCHEDULER_MAX_EPISODES_PER_TICK` env var
  to the backend service block.
- **Scheduler `isNoPendingError` cleaned up** — the transitional
  `strings.Contains` fallback that Phase 5b left in place during the
  cross-track parallel refactor is now deleted, since LostFilm
  (Phase 5c) wraps the typed sentinel everywhere it returns the
  empty-pending error. The `strings` import is gone from
  `scheduler.go`. Test case renamed from `legacy_substring` to
  `untyped substring no longer matches` (asserting the inverse).
- **`techdebt/2-3-scheduler-no-unit-tests.md`** removed — the new
  `scheduler_test.go` (Phase 5b) closes this debt. The
  `TestRunCheck_NonAtomicFallback` test still exists to cover the
  graceful-degradation path on the `markEpisodeDownloader` optional
  interface, in case a future repo implementation lacks the atomic
  method.
- **`CLAUDE.md`** (new, project-level) — structural snapshot of the
  repo for future Claude sessions per
  `~/.claude/rules/documentation-maintenance.md`. Catalogs the
  backend package layout, the new `extra` package, the new shared
  frontend hooks and queryKeys, the scheduler design (post-Phase 5b),
  the per-episode tracker contract, and the conventional dev
  commands.

### Added (Phase 4f — LostFilm real packed-int v_search flow + per-episode state)
The previous draft of Phase 4f–h posted `c/s/e` form fields to
`/v_search.php` and "picked the latest episode per Check", which was
a plausible-but-wrong reverse-engineering of LostFilm. This entry
replaces that draft with the **actual** flow extracted from
`main.min.js`:

- **`lostfilm.go` rewritten end-to-end** around the real
  `PlayEpisode(a) { window.open("/v_search.php?a=" + a) }` JS shape:
  - **Series page parsing** now reads two redundant attributes per
    episode: `data-code="<show>-<season>-<episode>"` (canonical,
    hyphens not colons) and `data-episode="<show><sss><eee>"` (the
    packed integer used by `PlayEpisode`). Both forms are decoded;
    `data-episode` is the fallback when `data-code` is missing.
  - **`Check` is now stateful**: it parses every episode, applies
    the `start_season`/`start_episode` floor, subtracts the
    `topic.Extra["downloaded_episodes"]` set, and returns the
    pending list in `check.Extra["pending_episodes"]` (packed IDs)
    plus a deterministic hash of the form `eps:N/done:M/pending:K`.
    The hash flips both when new episodes appear AND when the user
    catches up, so the scheduler always re-evaluates.
  - **`Download` is now per-episode**: it pulls the **oldest**
    pending packed ID from `check.Extra`, GETs
    `/v_search.php?a=<packed>` (it is a GET, not a POST), captures
    the 302 `Location` (or meta-refresh body fallback), follows the
    redirect through external hosts (`retre.org` / `tracktor.in` /
    `lf-tracker.io`), parses the destination's per-quality `.torrent`
    buttons, picks the one matching `topic.Extra["quality"]`, and
    GETs the bencode bytes. Failure when the redirect lands on
    `/login` is surfaced explicitly as "session expired".
- **`qualityMatches` helper** locks down a sharp footgun: the naive
  `Contains(label, want)` test would have false-matched `1080p_mp4`
  for a user asking for plain `1080p`. The helper hard-codes the
  three known LostFilm tiers (`SD`, `1080p`, `1080p_mp4`/`mp4`) so
  each maps to a distinct, non-overlapping label substring. New
  `TestQualityMatcher` table-test pins all 11 cases.
- **Per-episode state tracking** lives in
  `topic.Extra["downloaded_episodes"]` (slice of packed IDs). The
  scheduler appends to this list after every successful submit and
  the next `Check` subtracts it from the pending set. JSON-roundtrip
  through the JSONB column produces `[]any` instead of `[]string`,
  so a new `extraStringSlice` helper handles both shapes.
- **`TestRedirectorFlow`** rewritten to drive the new flow end-to-end
  against an `httptest.Server`: 3 episodes parsed, all 3 pending →
  Download fetches the **oldest** first → asserts `?a=791001005`
  hits the server with method=GET → asserts the resulting torrent
  path contains `1080p` but not `1080p_mp4` → marks the first
  episode downloaded → reruns Check → asserts pending shrinks to
  2 → applies start_season=3 filter → asserts pending is empty.
- **`backend/internal/db/repo/topics.go`** gains
  `Topics.UpdateExtra(ctx, id, extra)` so the scheduler can persist
  the growing `downloaded_episodes` list without touching the rest
  of the topic row.

### Added (Phase 4g — scheduler multi-download loop with no-pending sentinel)
- **`scheduler.runCheck` per-episode loop.** The scheduler now drains
  every pending episode in one tick instead of one-per-tick. The
  inner loop calls `tr.Download(ctx, t, check, creds)` repeatedly
  until either:
  - Download returns the `"no pending episodes"` sentinel error
    (matched via `isNoPendingError`) — graceful exit, all submitted
    work counted as success.
  - A real download/submit error occurs mid-loop — record the
    progress made so far AND record the error+backoff so the next
    tick retries.
  - The `maxPerTick = 25` safety guard fires — stops a misbehaving
    plugin from burning unbounded download bandwidth per tick.
- **`Scheduler.markEpisodeDownloaded`** pops the first pending packed
  ID off `check.Extra["pending_episodes"]`, appends it to
  `topic.Extra["downloaded_episodes"]`, and persists via
  `Topics.UpdateExtra`. After persisting, the scheduler re-runs
  `tr.Check` so the next loop iteration sees the shrunken pending
  list (and a fresh hash).
- **Backwards-compatible**: every existing tracker plugin returns
  one payload from `Download` and then errors on the second call
  ("nothing to download"). The loop's `i > 0 + isNoPendingError`
  branch breaks cleanly after one iteration, so single-payload
  plugins keep their old semantics for free.
- **Tech debt logged**: `techdebt/2-3-scheduler-no-unit-tests.md`
  notes that the scheduler still has zero unit-test coverage and
  that the "no pending" contract is currently a stringly-typed
  `errors.Is`-incompatible sentinel.

### Added (Phase 4h — shared DeleteConfirm safety component)
- **`frontend/src/components/shared/DeleteConfirm.tsx`** (new) — a
  one-click trash icon that swaps in place to a `Delete? ✓ ✗` row
  on the first click and only fires `onConfirm` after the second.
  Auto-cancels after 4s of inactivity. No modal, no portal, no
  layout shift — the confirm row replaces the trash icon at the
  same position. Click events are stopped from bubbling so it can
  live inside row-level click handlers. Spinner state via
  `isPending`.
- **Wired into 4 pages** (`Topics.tsx`, `Clients.tsx`,
  `Credentials.tsx`, `Notifiers.tsx`), replacing the previous
  `<Button onClick={() => del.mutate(id)}>` patterns. Removes the
  accidental-deletion footgun without adding a modal dialog —
  consistent with the "no JS confirm() / no blocking modals" rule.

### Fixed (Phase 4i — real version from /system/info on AppShell + Settings)
- **`AppShell.tsx` and `Settings.tsx`** now render the live build
  version from `GET /api/v1/system/info` instead of hardcoded
  `v0.1` / `v0.4.0-alpha` strings. Settings additionally renders
  the build commit and date when present (suppressed if either is
  literally `"unknown"`). 5-minute React Query stale time.
- **`deploy/docker-compose.yml`** dev marker bumped from
  `0.1.0-dev` → `1.1.0-dev` for backend / frontend / cfsolver, so
  local images built off `main` post-1.0.0 no longer wear the
  pre-v0.1 label.

### Maintenance
- **`.gitignore`** now excludes `.claude/` (per-machine Claude Code
  settings). Stops `settings.local.json` from leaking into commits.

### Added (Phase 4e — tracker credentials surface end-to-end)
The `tracker_credentials` table existed in the schema since v0.1
but had no REST handler and no frontend UI — it was unreachable.
This phase wires the entire surface end-to-end so users can finally
add LostFilm / RuTracker / Kinozal accounts.

- **`backend/internal/db/repo/tracker_credentials.go`** (new) —
  Create / GetByID / GetForTracker / ListForUser / Update / Delete
  methods. Mirrors the existing Clients repo pattern with a unique
  `(user_id, tracker_name)` constraint enforced at the DB layer.
- **`backend/internal/api/handlers/credentials.go`** (new) — REST
  handler exposing five endpoints under `/api/v1/credentials`:
  `GET /` (list, no secrets), `POST /` (validates by calling the
  plugin's `Login` before saving), `PUT /{id}` (rotate username and
  optionally password), `DELETE /{id}`, `POST /{id}/test` (decrypts
  the secret and re-runs `Login` + `Verify`).
- **`Credentials` handler struct** wired into `router.go` Deps and
  constructed in `cmd/server/main.go` with the existing master key
  + audit logger. Every create / update / delete is audit-logged.
- **Scheduler now passes credentials into `Check`/`Download`.** The
  scheduler at `backend/internal/scheduler/scheduler.go` gains a
  `creds *repo.TrackerCredentials` field. Before each topic check,
  if the tracker implements `WithCredentials` and the user has a
  stored credential, the scheduler decrypts the secret, calls
  `Login`, and passes the in-memory credential into both `Check`
  and `Download`. Login failures are recorded as `auth_error` in
  the metric and as a backoff-retry on the topic.
- **`frontend/src/pages/Credentials.tsx`** (new) — `/accounts` page.
  Lists existing accounts grouped by tracker (display name, username,
  test/edit/delete buttons). Add form filters out trackers that
  already have a credential to honour the unique constraint.
  Validates by attempting Login on submit; if Login fails the
  credential is not stored.
- **New nav entry** "Accounts" in `AppShell.tsx` and corresponding
  i18n keys in `en.ts` / `ru.ts`.
- **Plugin contract clarified**: `WithCredentials.Login` receives
  a `*TrackerCredential` whose `SecretEnc` field carries the
  **plaintext** password in-memory after decryption. The persisted
  blob is the AES-256-GCM ciphertext.

### Added (Phase 4a–d — tracker capability discovery + quality / episode in AddTopic)
- **`WithEpisodeFilter` capability interface** in
  `backend/internal/plugins/registry/registry.go`. Tracker plugins
  that implement `SupportsEpisodeFilter() bool` promise to honour
  `topic.Extra["start_season"]` / `topic.Extra["start_episode"]`
  in their `Check`/`Download` methods.
- **`GET /api/v1/trackers/match?url=<encoded>`** — new endpoint in
  `backend/internal/api/handlers/trackers.go`. Looks up the tracker
  plugin that claims a URL via `registry.FindTrackerForURL`, then
  type-asserts every optional capability and returns the snapshot:
  `tracker_name`, `display_name`, `qualities`, `default_quality`,
  `supports_episode_filter`, `requires_credentials`,
  `uses_cloudflare`. 404 if no plugin matches. Used by the
  AddTopic form.
- **`POST /api/v1/topics`** now accepts three optional fields:
  `quality`, `start_season`, `start_episode`. They are validated
  against the plugin's `WithQuality.Qualities()` list (where
  applicable) and overlaid onto the Extra map the plugin's
  `Parse()` returned, then persisted in the existing `topics.extra`
  JSONB column. No DB schema change.
- **AddTopic form is now capability-driven**
  (`frontend/src/pages/Topics.tsx`). After the user pastes a URL the
  form debounces 350 ms then calls `/trackers/match`. If the
  response includes `qualities`, a quality `<select>` appears
  (defaulting to `default_quality`). If `supports_episode_filter`
  is true, two number inputs ("Start season", "Start episode") are
  rendered. If `requires_credentials` is true, a yellow notice
  invites the user to add a tracker account. The detected tracker
  display name is shown inline as a green confirmation.

### Added (Phase 3 — edit torrent clients + per-plugin URL guidance)
- **`GET /api/v1/clients/{id}`** in
  `backend/internal/api/handlers/clients.go` — returns the client row
  with the **decrypted** config blob, scoped to the calling user.
  Audit-logged on every read. Used by the frontend Edit Client form
  so the user can see (and rotate) what they previously saved.
- **`PUT /api/v1/clients/{id}`** — overwrites the mutable fields
  (`display_name`, `is_default`, `config`) on an existing client.
  Calls `plugin.Test()` before persistence, so a bad config never
  overwrites a good one. Plugin name (`client_name`) cannot be
  swapped via PUT — delete and re-add to switch from Transmission to
  qBittorrent. Audit-logged.
- **`Clients.Update(ctx, id, userID, displayName, isDefault, configEnc, configNonce)`**
  in `backend/internal/db/repo/clients.go`.
- **Frontend Edit button** on every client card in
  `frontend/src/pages/Clients.tsx`. Opens a new `EditClientCard`
  component that fetches the decrypted config via the new GET, hydrates
  every field (URL, username, password), and PUTs the result on save.
- **Inline help text** under every URL field — `Field` type gains an
  optional `helpText`. Transmission's URL field now reads
  *"Use the full RPC URL ending in /transmission/rpc. Default
  Transmission Web UI port is 9091; some packages (e.g.
  transmission-daemon) use 8083 or 9091. Example:
  http://192.168.2.65:8083/transmission/rpc"*. Same treatment for
  qBittorrent, Deluge, µTorrent, and the download-folder plugin.
- **`docs/clients.md`** — new per-client setup guide. One section per
  supported client showing the exact URL format, default port,
  required fields, and the most common gotchas. The Add Client form
  now links to this doc inline.
- **`api.put<T>(path, body)`** added to `frontend/src/lib/api.ts` —
  the wrapper previously only had `get / post / patch / del`.

### Added (Phase 2 — real Settings page + change-password endpoint)
- **`frontend/src/pages/Settings.tsx`** replaces the v0.4 placeholder
  with a real Settings page. Three sections, single column:
  - **Appearance** — segmented controls for theme (light/dark),
    language (English/Русский), and table density
    (comfortable/compact). All three are persisted in
    `marauder-prefs` localStorage via the existing `usePrefs` Zustand
    store. Server-side persistence is deferred.
  - **Account** — username + email read-only, plus a three-field
    change-password form (current / new / confirm) wired to the new
    backend endpoint. Sign-out button revokes the refresh token and
    clears the auth store.
  - **About** — version (`v0.4.0-alpha`), license, links to
    marauder.cc, GitHub, CHANGELOG, ROADMAP.
- **`POST /api/v1/auth/me/password`** in
  `backend/internal/api/handlers/auth.go` — change-password handler
  for local accounts. Verifies the current password with Argon2id,
  enforces an 8-char minimum on the new password, hashes with
  Argon2id, persists via the new
  `Users.UpdatePasswordHash(ctx, id, hash)` repo method, audit-logs
  every attempt (success and failure). OIDC-only accounts are
  rejected with 400 because they have no local password to change.
- The route registration in `frontend/src/App.tsx` now points
  `/settings` at `<SettingsPage>` instead of the generic placeholder.
- New i18n keys under `settings.*` in both `en.ts` and `ru.ts`.

### Changed (Phase 1 — visual & interaction polish across app + site)
- **Brand palette switched from violet/cyan to blue/amber/slate.** Only
  CSS tokens were touched; every component reads
  `hsl(var(--primary))` so no JSX changes were needed.
  - `frontend/src/index.css`: `--primary` 265→217 (Tailwind blue),
    `--accent` 192→38 (Tailwind amber), `--ring` mirrors primary,
    body radial gradients + glass-card shadow rebalanced.
  - `site/src/styles/global.css`: same tokens swapped to keep the
    marketing site brand-consistent with the app.
- **Dark/light mode toggle now actually works.** Previously
  `frontend/index.html` hardcoded `class="dark"` on `<html>` and the
  header showed a static Moon icon labelled "dark" with no handler.
  - Added `theme: "light" | "dark"` + `setTheme` to the existing
    `usePrefs` Zustand store at `frontend/src/lib/prefs.ts`. The
    setter toggles `.dark` on `document.documentElement` and
    `onRehydrateStorage` re-applies the persisted theme on store
    rehydrate.
  - Removed the hardcoded `class="dark"` from `frontend/index.html`
    and added an inline boot script that reads
    `localStorage["marauder-prefs"]` synchronously and applies the
    `.dark` class before React mounts — no FOUC flash.
  - `AppShell.tsx` header now renders a real Sun/Moon toggle button
    next to the locale switcher.
- **Language switcher dropdown rewritten** at
  `frontend/src/components/layout/LocaleSwitcher.tsx`. The bare native
  `<select>` (whose `<option>` styling is browser-controlled and
  ignores Tailwind) is replaced with a small custom popover: trigger
  button + click-outside handler + Escape-to-close + glass-card panel
  + Check icon on the active locale. ~85 LOC, no new dependency.
- **Sitewide alpha disclaimer banner on marauder.cc.** Inserted a
  warning-tinted banner immediately after `<Header />` in
  `site/src/layouts/Page.astro` so every page shows it. New
  `.alpha-banner` rule in `site/src/styles/global.css`. Banner text:
  *"Alpha release. Marauder is in early alpha. Most plugins are
  structurally complete but have not been validated against live
  services yet — expect rough edges. See plugin status →"*
- **Version label dropped from `1.0.0` to `0.4.0-alpha`** in
  `site/src/data/seo.ts` (the home hero pill picks this up
  automatically). Hero pill recoloured from green-pulse to
  warning-pulse to match the alpha framing. README badges updated
  from `violet.svg` to `blue.svg`. PRD §9.1 design language paragraph
  rewritten to describe the new palette.

### Changed (marauder.cc visual & content polish)
- **Replaced emoji icons with inline lucide SVG icons** via a new
  `site/src/components/Icon.astro` component. Six feature-card icons
  on the home page (radio-tower, globe, send, shield-check, activity,
  blocks), the install warning callout (triangle-alert), and inline
  arrow-right / github icons all render as zero-JS inline `<svg>` —
  no `astro-icon` or `@iconify-json/lucide` dependency added.
- **Dialled back the violet color usage** across the site. The
  brand violet remains on the primary CTA buttons and the Marauder
  logo gradient, but is no longer used for section header labels,
  hover borders, link underlines, step number circles, or
  background radial glows. Section labels now use
  `text-muted-foreground`, hover borders use `foreground/30`, and
  link underlines use `foreground/40`. The body background is a
  single subtle violet ellipse instead of two stacked violet/cyan
  glows.
- **Removed all `monitorrent` mentions from the marketing site and
  internal documentation** except for one credits line in the
  README. Deleted `site/src/pages/vs/monitorrent.astro` and
  `docs/migrating-from-monitorrent.md`. Reworded copy in
  `docs/VISION.md`, `docs/COMPETITORS.md`, `docs/PRD.md`,
  `docs/ROADMAP.md`, and `CONTRIBUTING.md` to describe the
  forum-tracker monitoring niche on its own terms. Cleaned up the
  same comments in `backend/internal/plugins/trackers/lostfilm/lostfilm.go`
  and `backend/internal/plugins/registry/registry.go`. The single
  remaining mention is in `README.md` under "License & credits".

### Added (marauder.cc marketing site)
- **New `site/` directory** containing the Astro 5 + Tailwind 4 +
  Shiki marketing site for `https://marauder.cc`. Designed for
  **100% Lighthouse SEO** with zero React/JS hydration:
  - 8 routes: home (`/`), `/install`, `/features`, `/trackers`,
    `/integrations`, `/docs`, `/vs/sonarr`, `/legal`, plus a friendly 404
  - Per-page **unique title, meta description, canonical URL**
  - **Open Graph + Twitter Card** on every page (8 OG tags + 4
    Twitter Card tags) generated centrally by `BaseHead.astro`
  - **JSON-LD structured data** on every page via `JsonLd.astro` with
    XSS-safe `</script>` escape:
    - sitewide: `Organization` + `WebSite`
    - home: `SoftwareApplication` (with version/license/category) +
      `FAQPage` (8 Q&A pairs)
    - `/install`: `HowTo` with 5 numbered steps (triggers Google's
      "How to" rich result)
    - inner pages: `BreadcrumbList`
  - **Sitemap** auto-generated at `/sitemap-index.xml` via
    `@astrojs/sitemap`, excluding the 404 page
  - **`robots.txt`** allowing all crawlers and pointing to the sitemap
  - **`CNAME` file** with `marauder.cc` for GitHub Pages custom domain
  - **Favicon SVG** + Apple touch icon SVG matching the app's
    violet/cyan brand
  - **OG image** at `/og/default.svg` (1200×630) with brand text
  - One long-form **comparison page** for SEO long-tail:
    `/vs/sonarr` (Sonarr-Radarr-Prowlarr feature matrix +
    explanation of why the *arr stack can't see forum trackers)
  - **Performance budget:** 0 JS frameworks shipped (Astro outputs
    pure HTML by default), only 2.25 KB of Astro's prefetch helper.
    Total HTML max 40 KB per page, single CSS bundle 35 KB
  - **Visual identity** matching the app: dark-first slate base,
    deep-violet primary, electric-cyan accent, glass cards, Inter
    + JetBrains Mono fonts, generous spacing
- **`.github/workflows/site.yml`** — Pages deploy workflow:
  - Triggers on push to main when `site/**` or the workflow itself
    changes, plus `workflow_dispatch`
  - Runs `npm ci && npm run build` in `site/` with the Node 22 cache
  - Asserts `dist/index.html`, `dist/sitemap-index.xml`,
    `dist/robots.txt`, `dist/CNAME`, the `<title>` tag, and the
    JSON-LD block are all present before deploying
  - Uploads the `dist/` directory as a Pages artifact and deploys
    via `actions/deploy-pages@v4`
  - `concurrency: pages` ensures only one deploy in flight at a time
  - Validated with `actionlint` (clean)
- **`docs/site-deploy.md`** — full guide for the one-time setup
  (Pages source toggle + DNS records at the registrar, with the
  exact 4 A records and CNAME GitHub Pages requires) plus the
  ongoing edit workflow, troubleshooting matrix, and Lighthouse
  validation steps.

### Added (CI / GitHub Actions)
- **Five GitHub Actions workflows** under `.github/workflows/`:
  - **`ci.yml`** — fast-feedback PR pipeline (under 3 min budget):
    `go vet`, race-detector tests, `golangci-lint`, `govulncheck`,
    cfsolver build/vet, frontend `tsc --noEmit` and `npm run build`,
    bundle-size summary. Cancels in-flight runs on the same ref.
  - **`docker.yml`** — builds backend, frontend, and cfsolver images
    on every push to main and on every tag. Trivy scan with HIGH/
    CRITICAL fail-on, SARIF uploaded to the GitHub Code Scanning view.
    Does NOT push images.
  - **`e2e.yml`** — heavyweight nightly + on-tag end-to-end test that
    brings up the full compose stack (db + backend + frontend +
    gateway + qBittorrent), then runs the magnet → qBittorrent
    walkthrough from `docs/test-e2e-magnet.md` end-to-end. Includes
    backend log capture on failure and a clean teardown step.
  - **`release.yml`** — tag-pushed release pipeline. Multi-arch
    (amd64 + arm64) build via QEMU + buildx, push to `ghcr.io/
    artyomsv/marauder-{backend,frontend,cfsolver}` with semver tags,
    cosign keyless signing via OIDC, CycloneDX SBOM per image, GitHub
    Release with the auto-extracted CHANGELOG section. Pre-release
    detection from `-rc`/`-alpha`/`-beta` tag suffixes.
  - **`codeql.yml`** — GitHub CodeQL SAST for Go and TypeScript with
    the `security-extended` query pack. Runs on PR + push + weekly.
- **`.github/dependabot.yml`** — automated dependency updates across
  Go modules (backend + cfsolver), npm (frontend), GitHub Actions,
  and Docker base images. Weekly Monday cadence, minor/patch updates
  grouped per ecosystem to reduce PR noise. React 19 / Vite 8 /
  Tailwind 4 majors are pinned per the v1.0 tech-stack lock.
- **PR + Issue templates**:
  - `.github/PULL_REQUEST_TEMPLATE.md` — checklist mirroring CONTRIBUTING.md
  - `.github/ISSUE_TEMPLATE/bug.yml` — structured bug report
  - `.github/ISSUE_TEMPLATE/feature.yml` — structured feature request
  - `.github/ISSUE_TEMPLATE/tracker_breakage.yml` — special-case
    template for forum-tracker plugin breakage with HTML excerpt
    upload, scrubbing checkboxes, and a tracker dropdown
- **`backend/.golangci.yml`** — golangci-lint v2 config covering 12
  linters (errcheck, govet, ineffassign, staticcheck, unused,
  bodyclose, rowserrcheck, sqlclosecheck, errorlint, gosec, misspell,
  unconvert) plus gofmt + goimports formatters. Includes principled
  exclusions for test files, init-based plugin registration,
  `defer .Body.Close()` and `defer tx.Rollback()` patterns, and
  SHA-1 used as a content hash (G401/G505) which is the same hash
  BitTorrent uses internally.
- **`docs/ci.md`** — full CI/CD documentation: per-workflow
  description, when each runs, what to do when it fails, how to
  cut a release, how to validate locally with the same Docker
  commands the workflows use.

### Fixed (lint pass over the existing codebase)
- `internal/crypto/crypto_test.go`: replace tautological
  `HashToken("x") != HashToken("x")` comparison with two assigned
  variables so staticcheck SA4000 stops (correctly) flagging it.
- `internal/plugins/trackers/kinozal/kinozal_test.go`: replace
  `if HasPrefix { TrimPrefix }` with the unconditional
  `TrimPrefix` (S1017).
- `internal/plugins/clients/transmission/transmission_test.go`:
  remove the unused `mu sync.Mutex` field on `fakeServer`.
- `internal/crypto/crypto.go`: bound-check `len(want)` before the
  uint32 conversion in `VerifyPassword`, with a `#nosec G115`
  annotation explaining the bound is enforced.
- `internal/plugins/clients/downloadfolder/downloadfolder.go`: file
  permissions tightened from `0o640` to `0o600` per gosec G306, with
  a comment explaining the trade-off for shared-group setups.
- `internal/plugins/e2etest/qbitfake.go`: bound the test server's
  form-parsing body size with `http.MaxBytesReader` to satisfy gosec
  G120 even on a fake server.
- `gofmt -w` applied across the backend.

### Verified
- `golangci-lint run --timeout=5m`: **0 issues**.
- `go build ./...` and `go vet ./...`: clean.
- `go test ./...`: 29 packages, 0 failures.
- `actionlint` over all 5 workflow files: clean.

### Added (Torznab + Newznab support)
- **Torznab and Newznab indexer plugins** — opens Marauder up to
  several hundred indexers without writing scrapers. Sonarr, Radarr,
  Prowlarr, Jackett, and NZBHydra2 collectively cover 500+ indexers
  via these two protocols, and Marauder now speaks both.
  - `torznab` — for any Torznab indexer (Jackett, Prowlarr,
    NZBHydra2 in torrent mode, or a direct Torznab feed). Uses the
    explicit `torznab+https://...` URL prefix so CanParse never
    collides with forum-tracker plugins. The hash is the newest
    item's `infohash` (or `guid` fallback). New releases at the top
    of the feed trigger a Marauder "update" the same way a forum-
    thread re-upload does. Enclosure magnet URIs route directly to
    the user's torrent client.
  - `newznab` — for any Usenet indexer (NZBGeek, NZBPlanet,
    DOGnzb, NZBHydra2). Uses `newznab+https://...` prefix. Marauder
    downloads the `.nzb` and hands the bytes to a `downloadfolder`
    client pointed at a SABnzbd / NZBGet watch directory — the
    Usenet handoff is unchanged from the *arr stack workflow.
  - Shared `torznabcommon` parser package handles the common
    RSS+attr XML shape (both protocols share it). 4 parser unit
    tests cover the Torznab feed, the Newznab feed, empty input,
    and malformed XML.
- **Per-plugin tests** for both new plugins:
  - `torznab`: 7 tests (CanParse, Parse, Check happy path with
    infohash, Check fallback to GUID when no infohash, Check on
    empty feed, Check on HTTP 500, safeFilename helper) plus an
    E2E test that runs the full pipeline against a fake indexer
    and submits to a fake qBittorrent.
  - `newznab`: 4 tests (CanParse, Parse, Parse rejects bad scheme)
    plus an E2E test that runs the full pipeline through a fake
    NZB indexer that serves both the RSS feed and the .nzb bytes.
- **Bundled tracker count: 16** (was 14).
- **`docs/torznab-newznab.md`** — full integration guide explaining
  the model fit, the URL prefix scheme, step-by-step Prowlarr and
  NZBGeek walkthroughs, category numbers, and the validation
  procedure.

### Added (previous push — full tracker E2E coverage)
- **Two new tracker plugins** completing the original monitorrent
  catalog:
  - `freetorrents` — phpBB-derived Free-Torrents.org. Login form,
    `viewtopic.php` scrape, magnet + dl.php fallback. Alpha (needs
    live-account validation).
  - `hdclub` — HD-Club.org TBDev/Gazelle-style private tracker.
    `details.php` scrape, `download.php` torrent fetch. Alpha.
  - **Bundled tracker count: 14** (was 12 in v1.0.0).
- **`internal/plugins/e2etest` package** — shared E2E test harness:
  - `QBitFake` — httptest-backed stand-in for the qBittorrent WebUI v2
    API that captures every torrent submission for assertions
  - `RunFullPipeline(t, Case)` — generic runner that drives a tracker
    plugin through CanParse → Parse → Login → Verify → Check →
    Download → submit-to-fake-qbit → assertions
  - `HostRewriteTransport` — `http.RoundTripper` that rewrites a
    production hostname to a local httptest.Server host. Lets the
    plugin's regex URL patterns and CanParse keep matching against
    canonical hostnames while HTTP traffic transparently routes to
    the test server. **Production code is unmodified between unit
    tests and E2E.**
- **End-to-end tests for all 14 trackers** (one `<name>_e2e_test.go`
  per package, in-package so it can construct the plugin with private
  fields). Every test exercises the complete pipeline including the
  fake-qBit submission step:
  - `genericmagnet`, `generictorrentfile`
  - `rutracker`, `kinozal`, `nnmclub`
  - `lostfilm`, `anilibria`, `anidub`
  - `rutor`, `toloka`, `unionpeer`, `tapochek`
  - `freetorrents`, `hdclub`
- **`lostfilm` Download** is now wired to extract a magnet URI from
  the series page if one is present, instead of returning a stub
  error. The redirector flow for paid users is still pending live
  validation, but the magnet path is real and exercised in E2E.

### Changed
- `freetorrents` and `hdclub` are wired into `cmd/server/main.go`
  via blank imports.

### Verified
- `go build ./...` and `go vet ./...` clean.
- `go test ./...`: **26 test packages, all green**, including
  14 fresh tracker E2E tests.

## [1.0.0] — 2026-04-07

The initial production release. The full feature set landed across the
v0.1 → v0.4 development branches and is collected here.

### Architecture

- **Backend:** Go 1.23, `chi` HTTP router, `pgx` v5 connection pool,
  `goose`-managed embedded migrations, `zerolog` structured JSON logging,
  RFC 7807 problem-details error responses, security-headers middleware,
  request-id middleware, recovery middleware that turns panics into
  500s with trace IDs.
- **Frontend:** React 19.2 + Vite 8 + Tailwind CSS 4.2 + shadcn/ui 4.1.2,
  TanStack Query for server state, zustand for local UI state,
  framer-motion for entry animations, lucide-react for icons. Dark-first
  design language with deep-violet primary, electric-cyan accent, glass
  cards, and radial gradients.
- **Database:** PostgreSQL 18 (currently 18.3 alpine; rolls forward
  automatically when 18.4 publishes).
- **Deployment:** Docker + docker-compose, four-service production stack
  (postgres + backend + frontend + nginx gateway), `cfsolver` profile
  for the optional Cloudflare-bypass sidecar, `sso` profile for the
  optional Keycloak realm, `dev` overlay for end-to-end testing with
  real qBittorrent and Transmission containers.

### Auth

- **Local accounts:** Argon2id password hashing
  (`time=3, memory=64 MiB, parallelism=4`), ES256-signed JWT access
  tokens, opaque refresh tokens stored as SHA-256 hashes server-side,
  refresh-token rotation with reuse detection that revokes the entire
  token family on misuse.
- **OIDC:** auth-code flow via `coreos/go-oidc/v3`. Provisions new
  users on first sign-in. Pre-built `docker-compose.sso.yml` overlay
  brings up Keycloak 26.0 with a `marauder` realm and an
  `alice/marauder` test user. Documented in `docs/oidc.md`.
- **Master key:** AES-256-GCM at-rest encryption for tracker
  credentials, client configs, notifier configs, and JWT signing
  keys, all keyed by `MARAUDER_MASTER_KEY` (32-byte base64).
- **Audit log:** async logger (256-buffered channel + background
  drainer) that records login success/failure/logout to a
  Postgres-backed audit_log table. Admin-only `GET /api/v1/system/audit`
  + frontend page exposes recent entries.

### Plugin architecture

A plugin is one Go file plus its tests. `init()` self-registers with
the global `registry` package on process start. Three kinds of plugin:

| Kind | Interface | Optional capabilities |
|---|---|---|
| Tracker | `Tracker` | `WithCredentials`, `WithQuality`, `WithCloudflare` |
| Client  | `Client`  | — |
| Notifier | `Notifier` | — |

See [`docs/plugin-development.md`](docs/plugin-development.md) for the
full guide.

**Total bundled in v1.0:** 11 trackers, 5 clients, 4 notifiers.

#### Trackers (11)

| Plugin | Site | Status |
|---|---|---|
| `genericmagnet` | any magnet URI | ✅ E2E validated |
| `generictorrentfile` | any HTTP(S) `.torrent` URL | ✅ unit-tested |
| `rutracker` | RuTracker.org | 🟡 alpha (fixture-tested, needs live validation) |
| `kinozal` | Kinozal.tv | 🟡 alpha |
| `nnmclub` | NNM-Club.to (with `WithCloudflare`) | 🟡 alpha |
| `lostfilm` | LostFilm.tv (with `WithQuality`) | 🟡 alpha |
| `anilibria` | Anilibria.tv (uses public v3 API) | 🟡 alpha |
| `anidub` | tr.anidub.com (with `WithQuality`) | 🟡 alpha |
| `rutor` | Rutor.org | 🟡 alpha |
| `toloka` | Toloka.to | 🟡 alpha |
| `unionpeer` | Unionpeer.org | 🟡 alpha |
| `tapochek` | Tapochek.net | 🟡 alpha |

> **Alpha** means the plugin is structurally complete with fixture-based
> unit tests and follows the same patterns as the validated plugins, but
> has not been validated against a live site by the maintainer because
> doing so requires a real account on each site. The next release moves
> any plugin that a community member validates to "stable".

#### Clients (5)

| Plugin | Status |
|---|---|
| `downloadfolder` | ✅ unit-tested |
| `qbittorrent` (WebUI v2) | ✅ E2E validated against real qBittorrent docker container |
| `transmission` (RPC) | ✅ unit-tested with mocked-server |
| `deluge` (Web JSON-RPC) | ✅ unit-tested with mocked-server |
| `utorrent` (token-based WebUI) | 🟡 unit-tested with mocked-server, no live µTorrent docker image to validate against |

#### Notifiers (4)

| Plugin | Status |
|---|---|
| `telegram` (Bot API) | ✅ unit-tested via custom RoundTripper |
| `email` (SMTP, PLAIN auth) | ✅ unit-tested with injected sender |
| `webhook` (POST JSON) | ✅ unit-tested with httptest |
| `pushover` (form POST) | ✅ unit-tested with httptest |

### Cloudflare bypass

A separate `cfsolver/` Go service uses `chromedp` + Debian-slim
chromium to drive a target URL through any Cloudflare interstitial
and return the resulting cookies + user-agent. Runs as its own Docker
image and is gated behind the `cfsolver` compose profile so it doesn't
start unless the user opts in. Tracker plugins that opt into the
`WithCloudflare` capability automatically route through it via the
`internal/cfsolver` client package.

### Scheduler

- Single dispatch goroutine on a configurable tick (default 60s)
- Bounded worker pool (default 8) draining a buffered job channel
- Per-topic check pipeline: load → call tracker `Check` → compare hash
  → if changed, call `Download` → decrypt client config with master
  key → call client `Add`
- Exponential backoff on errors, capped at 6 hours
- Falls back to the user's default client if a topic has no explicit
  `client_id`
- In-memory ring buffer of the last 50 run summaries, exposed via
  `GET /api/v1/system/status` for the live System page
- Records detailed Prometheus metrics for every check, update, and
  client submit

### Observability

- **`/health`** — always 200 if the process is up
- **`/ready`** — 200 only when the database is reachable
- **`/metrics`** — Prometheus exposition, gated by a static bearer
  token (`MARAUDER_METRICS_TOKEN`). Includes:
  - `marauder_http_requests_total{method,route,status}`
  - `marauder_http_request_duration_seconds{method,route}`
  - `marauder_scheduler_runs_total{result}`
  - `marauder_scheduler_topic_checks_total{tracker,result}`
  - `marauder_scheduler_topic_check_duration_seconds{tracker}`
  - `marauder_tracker_updates_total{tracker}`
  - `marauder_client_submit_total{client,result}`
  - default `go_*` and `process_*` collectors
- **System status page** in the frontend showing the scheduler state,
  last-run summary, run history, and a Go runtime snapshot, all
  auto-refreshing every 5 seconds

### Frontend pages

- **Login** — animated card with local form + "Sign in with Keycloak"
  button (if OIDC is configured)
- **Dashboard** — four live status tiles + recent activity feed
- **Topics** — full CRUD with checkboxes, bulk pause/resume/delete,
  comfortable/compact density toggle, inline add card with auto-detect
  preview
- **Clients** — full CRUD with per-plugin field hints, Test-connection
  button per row, default-client toggle
- **Notifiers** — full CRUD with per-plugin field hints, Send-test
  button per row
- **System** (any user) — live scheduler + runtime status, run history
- **Audit log** (admin only) — append-only event table with action,
  actor, target, IP, user-agent, result
- **OIDC callback** — picks up tokens from the URL fragment and lands
  the user on the dashboard

### i18n

Tiny zustand-backed module with English and Russian dictionaries plus
a `useT()` hook. Locale is persisted in `localStorage` and switchable
from a header dropdown.

### Testing

- **18 unit-test packages** covering crypto, auth, plugin registry,
  every bundled tracker (where fixtures are available), every bundled
  client, and every bundled notifier
- **End-to-end magnet → qBittorrent walkthrough** documented and
  validated in [`docs/test-e2e-magnet.md`](docs/test-e2e-magnet.md)
- `go build ./... && go vet ./...` clean
- `npm run build` produces ~470 KB / ~146 KB gzipped frontend bundle

### Deployment

- Multi-stage Dockerfiles for backend and frontend, both running as
  non-root users with healthchecks
- `deploy/docker-compose.yml` — production stack
- `deploy/docker-compose.dev.yml` — overlay that exposes ports and
  starts real qBittorrent + Transmission containers
- `deploy/docker-compose.sso.yml` — overlay that adds Keycloak with a
  pre-imported realm
- All host ports are non-standard to avoid colliding with other
  services on the developer machine: gateway 6688, backend 8679,
  frontend dev 8680, Vite HMR 5174, Postgres dev 55432, Keycloak 8643

### Documentation

- `README.md` — top-level project overview
- `docs/VISION.md` — what we're building and why
- `docs/COMPETITORS.md` — how Marauder relates to Sonarr/Radarr/Prowlarr/
  Jackett/FlexGet/monitorrent
- `docs/PRD.md` — full product requirements document
- `docs/ROADMAP.md` — phased plan with v1.0 status
- `docs/plugin-development.md` — guide to writing tracker / client /
  notifier plugins
- `docs/oidc.md` — Keycloak OIDC walkthrough
- `docs/test-e2e-magnet.md` — reproducible end-to-end smoke test
- `docs/migrating-from-monitorrent.md` — migration guide
- `CONTRIBUTING.md` — local dev, test running, PR checklist
- `CHANGELOG.md` — this file

[Unreleased]: https://github.com/artyomsv/marauder/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/artyomsv/marauder/releases/tag/v1.0.0
