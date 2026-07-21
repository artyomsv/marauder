# Configurable Tracker Domains + Mirror Fallback — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let admins pick/enter the domain each tracker plugin uses (known mirrors + validated custom hostnames), with existing topics following automatically, and rotate to the next mirror when checks fail with network errors (issue #126).

**Architecture:** A `tracker_settings` table + in-memory `domains.Store` feed a resolver hook in `plugins/registry`. Plugins gain a `WithDomains` capability and a uniform `effectiveDomain()` method (registry override → fallback to the existing `p.domain` field, which tests keep overriding). `CanParse` regexes become host-agnostic with an explicit host-allowlist check (known ∪ custom) so the SSRF barrier stays a strict allowlist. Phase 2 wires the scheduler's existing `timeout`/`unreachable` error classification to `Store.ReportFailure`, which rotates the active domain (ring, cooldown, persisted).

**Tech Stack:** Go 1.25 (chi, pgx/pgxmock, zerolog, prometheus), React 19 + TS (React Query, zustand, shadcn, Vitest), PostgreSQL.

**Spec:** `docs/superpowers/specs/2026-07-21-tracker-domains-design.md`

## Global Constraints

- Backend verify (run from repo root; never install Go locally):
  `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go vet ./... && gofmt -l . | tee /dev/stderr | wc -l | grep -q '^0$' && go test -race ./..."`
- Frontend verify:
  `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test -- --run && npm run build"`
- Commit messages: imperative, ≤72-char subject, Conventional Commits. **No AI/agent attribution of any kind** (no Co-Authored-By trailers, no model/vendor names).
- Commit directly on branch `126-allow-configurable-tracker-domains-and-automatic-mirror-fallback`; do not push unless the user says so.
- Go: tabs, gofmt-clean, table-driven tests named `TestFn_Scenario_Expected`, manual fakes (no mock libs), wrap errors with `%w`.
- Frontend: 2-space indent, `interface` over `type` for object shapes, components ≤250 lines, i18n via `useT()` with keys in BOTH `en` and `ru` dictionaries, React Query keys only via `QK` factory, icons from `lucide-react` only.
- Never hand-edit `frontend/src/components/ui/*` (shadcn-managed).
- Migration file: `backend/internal/db/migrations/0015_add_tracker_settings.sql` (next free number; check nothing else claimed 0015 before creating).
- The 12 fixed-domain plugins: anidub, anilibria, freetorrents, hdclub, kinozal, lostfilm, nnmclub, rutor, rutracker, tapochek, toloka, unionpeer. genericmagnet/generictorrentfile/torznab/newznab are out of scope (user-supplied URLs).
- Keep `CHANGELOG.md` `[Unreleased]` updated as part of the final task.

---

### Task 1: Migration 0015 + `TrackerSettings` repo

**Files:**
- Create: `backend/internal/db/migrations/0015_add_tracker_settings.sql`
- Create: `backend/internal/db/repo/tracker_settings.go`
- Test: `backend/internal/db/repo/tracker_settings_test.go`

**Interfaces:**
- Produces: `repo.TrackerSetting{TrackerName string; ActiveDomain string; CustomDomains []string}`, `repo.NewTrackerSettings(pool *pgxpool.Pool) *TrackerSettings`, methods `List(ctx) ([]TrackerSetting, error)`, `Upsert(ctx, trackerName, activeDomain string, customDomains []string) error`.

- [ ] **Step 1: Write the migration**

```sql
-- 0015_add_tracker_settings.sql
-- Per-tracker domain configuration (issue #126): which domain the plugin
-- uses ("" / NULL = plugin default) and admin-added custom mirror hostnames.
CREATE TABLE tracker_settings (
    tracker_name   text PRIMARY KEY,
    active_domain  text,
    custom_domains jsonb NOT NULL DEFAULT '[]',
    updated_at     timestamptz NOT NULL DEFAULT now()
);
```

Check how migrations are applied (embed/goose/etc. — look at `backend/internal/db` for the migration runner) and follow the same registration pattern as `0014_add_topic_replace_on_update.sql` (if files are auto-discovered by glob, no extra registration is needed).

- [ ] **Step 2: Write failing repo tests**

`tracker_settings_test.go` — same pgxmock pattern as `deliveries_test.go` (open with `pgxmock.NewPool()`, inject via the unexported pool interface). Tests:

```go
func TestTrackerSettings_List_ScansRows(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	r := &TrackerSettings{pool: mock}
	rows := pgxmock.NewRows([]string{"tracker_name", "active_domain", "custom_domains"}).
		AddRow("kinozal", "kinozal.me", []byte(`["kinozal.example"]`)).
		AddRow("rutracker", nil, []byte(`[]`))
	mock.ExpectQuery(`SELECT tracker_name, COALESCE\(active_domain,''\), custom_domains FROM tracker_settings`).
		WillReturnRows(rows)
	got, err := r.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 || got[0].ActiveDomain != "kinozal.me" || got[0].CustomDomains[0] != "kinozal.example" {
		t.Errorf("unexpected result: %+v", got)
	}
	if got[1].ActiveDomain != "" || len(got[1].CustomDomains) != 0 {
		t.Errorf("nil active/empty custom not normalised: %+v", got[1])
	}
}

func TestTrackerSettings_Upsert_ExecutesOnConflict(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	r := &TrackerSettings{pool: mock}
	mock.ExpectExec(`INSERT INTO tracker_settings .* ON CONFLICT \(tracker_name\) DO UPDATE`).
		WithArgs("kinozal", "kinozal.me", []byte(`["kinozal.example"]`)).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := r.Upsert(context.Background(), "kinozal", "kinozal.me", []string{"kinozal.example"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
}
```

Adjust `WithArgs` JSON encoding to whatever the implementation marshals (match the existing repo style for jsonb columns — see how `sonarr_instances.go` passes `allowed_trackers`; if it passes `[]string` directly via pgx array/jsonb codec, mirror that and fix the expectation).

- [ ] **Step 3: Run tests, verify they fail** (`TrackerSettings` undefined)

- [ ] **Step 4: Implement the repo**

```go
package repo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// trackerSettingsPool is the minimal pgxpool subset used by TrackerSettings.
type trackerSettingsPool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// TrackerSetting is one tracker's domain configuration row.
type TrackerSetting struct {
	TrackerName   string
	ActiveDomain  string // "" = plugin default
	CustomDomains []string
}

// TrackerSettings is the repository for the tracker_settings table (issue #126).
type TrackerSettings struct {
	pool trackerSettingsPool
}

func NewTrackerSettings(pool *pgxpool.Pool) *TrackerSettings {
	return &TrackerSettings{pool: pool}
}

// List returns every configured tracker row.
func (r *TrackerSettings) List(ctx context.Context) ([]TrackerSetting, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT tracker_name, COALESCE(active_domain,''), custom_domains FROM tracker_settings ORDER BY tracker_name`)
	if err != nil {
		return nil, fmt.Errorf("tracker_settings: list: %w", err)
	}
	defer rows.Close()
	out := []TrackerSetting{}
	for rows.Next() {
		var s TrackerSetting
		var raw []byte
		if err := rows.Scan(&s.TrackerName, &s.ActiveDomain, &raw); err != nil {
			return nil, fmt.Errorf("tracker_settings: scan: %w", err)
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &s.CustomDomains); err != nil {
				return nil, fmt.Errorf("tracker_settings: custom_domains: %w", err)
			}
		}
		if s.CustomDomains == nil {
			s.CustomDomains = []string{}
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tracker_settings: rows: %w", err)
	}
	return out, nil
}

// Upsert writes one tracker's domain configuration. activeDomain "" is
// stored as NULL (plugin default).
func (r *TrackerSettings) Upsert(ctx context.Context, trackerName, activeDomain string, customDomains []string) error {
	if customDomains == nil {
		customDomains = []string{}
	}
	raw, err := json.Marshal(customDomains)
	if err != nil {
		return fmt.Errorf("tracker_settings: marshal custom_domains: %w", err)
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO tracker_settings (tracker_name, active_domain, custom_domains, updated_at)
		 VALUES ($1, NULLIF($2,''), $3, now())
		 ON CONFLICT (tracker_name) DO UPDATE
		 SET active_domain = EXCLUDED.active_domain,
		     custom_domains = EXCLUDED.custom_domains,
		     updated_at = now()`,
		trackerName, activeDomain, raw)
	if err != nil {
		return fmt.Errorf("tracker_settings: upsert: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: Run backend verify (Global Constraints command); expect PASS**
- [ ] **Step 6: Commit** — `feat(db): add tracker_settings table and repo (#126)`

---

### Task 2: Registry domain resolver + `WithDomains` capability

**Files:**
- Create: `backend/internal/plugins/registry/domains.go`
- Test: `backend/internal/plugins/registry/domains_test.go`
- Modify: `backend/internal/plugins/registry/registry.go` (extend `Reset()` only)

**Interfaces:**
- Produces:
  - `registry.WithDomains` — `interface { Tracker; Domains() []string }` (first entry = canonical/default domain)
  - `registry.DomainConfig{Active string; Custom []string}`
  - `registry.SetDomainResolver(func(trackerName string) DomainConfig)` — nil-safe, set once at boot
  - `registry.ActiveDomain(trackerName string) string` — "" when unconfigured
  - `registry.DomainAllowed(trackerName, host string, known []string) bool` — case-insensitive, `www.`-stripped membership in known ∪ custom

- [ ] **Step 1: Write failing tests**

```go
package registry

import "testing"

type domTracker struct{ Tracker }

func TestActiveDomain_NoResolver_ReturnsEmpty(t *testing.T) {
	SetDomainResolver(nil)
	if got := ActiveDomain("kinozal"); got != "" {
		t.Errorf("ActiveDomain = %q, want empty", got)
	}
}

func TestActiveDomain_WithResolver_ReturnsOverride(t *testing.T) {
	SetDomainResolver(func(name string) DomainConfig {
		if name == "kinozal" {
			return DomainConfig{Active: "kinozal.me"}
		}
		return DomainConfig{}
	})
	t.Cleanup(func() { SetDomainResolver(nil) })
	if got := ActiveDomain("kinozal"); got != "kinozal.me" {
		t.Errorf("ActiveDomain = %q, want kinozal.me", got)
	}
	if got := ActiveDomain("rutracker"); got != "" {
		t.Errorf("unconfigured tracker ActiveDomain = %q, want empty", got)
	}
}

func TestDomainAllowed_Table(t *testing.T) {
	SetDomainResolver(func(string) DomainConfig { return DomainConfig{Custom: []string{"kinozal.example"}} })
	t.Cleanup(func() { SetDomainResolver(nil) })
	known := []string{"kinozal.tv", "kinozal.me"}
	tests := []struct {
		name string
		host string
		want bool
	}{
		{"known host", "kinozal.tv", true},
		{"known host uppercase", "KINOZAL.TV", true},
		{"known host www", "www.kinozal.me", true},
		{"custom host", "kinozal.example", true},
		{"unknown host", "evil.example", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DomainAllowed("kinozal", tt.host, known); got != tt.want {
				t.Errorf("DomainAllowed(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests, verify failure** (undefined symbols)

- [ ] **Step 3: Implement `domains.go`**

```go
package registry

import (
	"strings"
	"sync"
)

// WithDomains declares the domains a tracker is known to operate on.
// The first entry is the canonical/default domain; the rest are known
// mirrors. Combined with admin-configured custom domains (via the
// resolver) it forms the runtime host allowlist for the plugin.
type WithDomains interface {
	Tracker
	Domains() []string
}

// DomainConfig is the admin-configured domain state for one tracker.
type DomainConfig struct {
	Active string   // "" = plugin default
	Custom []string // admin-added mirror hostnames
}

// DomainResolver returns the configured DomainConfig for a tracker name.
type DomainResolver func(trackerName string) DomainConfig

var (
	domainMu       sync.RWMutex
	domainResolver DomainResolver
)

// SetDomainResolver installs the process-wide domain resolver. Called once
// at boot (and from tests). nil disables overrides.
func SetDomainResolver(r DomainResolver) {
	domainMu.Lock()
	defer domainMu.Unlock()
	domainResolver = r
}

func resolveDomain(trackerName string) DomainConfig {
	domainMu.RLock()
	r := domainResolver
	domainMu.RUnlock()
	if r == nil {
		return DomainConfig{}
	}
	return r(trackerName)
}

// ActiveDomain returns the admin-selected domain for the tracker, or ""
// when none is configured — callers fall back to their compiled default.
func ActiveDomain(trackerName string) string {
	return resolveDomain(trackerName).Active
}

// DomainAllowed reports whether host is one of the tracker's known domains
// or an admin-configured custom domain. Comparison is case-insensitive and
// tolerates a leading "www.".
func DomainAllowed(trackerName, host string, known []string) bool {
	host = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(host)), "www.")
	if host == "" {
		return false
	}
	for _, d := range known {
		if host == strings.TrimPrefix(strings.ToLower(d), "www.") {
			return true
		}
	}
	for _, d := range resolveDomain(trackerName).Custom {
		if host == strings.TrimPrefix(strings.ToLower(d), "www.") {
			return true
		}
	}
	return false
}
```

In `registry.go`, extend `Reset()` with `domainResolver = nil` (inside its lock is fine — `domainMu` is separate, so set it via `SetDomainResolver(nil)` at the end of `Reset`).

- [ ] **Step 4: Run tests; expect PASS. Run backend verify.**
- [ ] **Step 5: Commit** — `feat(registry): domain resolver hook and WithDomains capability (#126)`

---

### Task 3: `domains.Store` (config cache + rotation ring)

**Files:**
- Create: `backend/internal/domains/store.go`
- Test: `backend/internal/domains/store_test.go`
- Modify: `backend/internal/metrics/` — add `marauder_tracker_domain_rotations_total{tracker}` CounterVec next to the existing scheduler collectors (find the file defining `marauder_scheduler_*` counters and mirror the pattern, including registration).

**Interfaces:**
- Consumes: `repo.TrackerSetting` / `Upsert` (Task 1), `registry.DomainConfig`, `registry.GetTracker`, `registry.WithDomains` (Task 2).
- Produces: `domains.New(settings SettingsRepo, log zerolog.Logger) *Store`, methods:
  - `Load(ctx) error` — read all rows into memory
  - `Resolve(trackerName string) registry.DomainConfig` — the resolver fn wired into the registry
  - `Get(trackerName string) (active string, custom []string)`
  - `Set(ctx, trackerName, active string, custom []string) error` — persist + update cache
  - `ReportFailure(ctx, trackerName string)` — Phase 2 rotation
  - `SetOnRotate(fn func(tracker, from, to string))` — notification hook
  - exported `const RotateCooldown = 10 * time.Minute`
  - `SettingsRepo` consumer interface: `List(ctx) ([]repo.TrackerSetting, error)`, `Upsert(ctx, name, active string, custom []string) error`

- [ ] **Step 1: Write failing tests** (fake `SettingsRepo` recording upserts; fake registry state via `registry.RegisterTracker` of a stub implementing `WithDomains` — use unique names per test to avoid duplicate-registration panics, or call `registry.Reset()` with `t.Cleanup` if other tests in the package tolerate it; prefer unique names):

```go
package domains

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

type fakeRepo struct {
	rows    []repo.TrackerSetting
	upserts []repo.TrackerSetting
}

func (f *fakeRepo) List(context.Context) ([]repo.TrackerSetting, error) { return f.rows, nil }
func (f *fakeRepo) Upsert(_ context.Context, name, active string, custom []string) error {
	f.upserts = append(f.upserts, repo.TrackerSetting{TrackerName: name, ActiveDomain: active, CustomDomains: custom})
	return nil
}

// stubTracker satisfies registry.Tracker minimally + WithDomains.
// (Fill the remaining Tracker methods with panic("unused") bodies.)

func TestStore_LoadAndResolve(t *testing.T) {
	f := &fakeRepo{rows: []repo.TrackerSetting{{TrackerName: "kinozal", ActiveDomain: "kinozal.me", CustomDomains: []string{"kinozal.example"}}}}
	s := New(f, zerolog.Nop())
	if err := s.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := s.Resolve("kinozal")
	if cfg.Active != "kinozal.me" || len(cfg.Custom) != 1 {
		t.Errorf("Resolve = %+v", cfg)
	}
	if got := s.Resolve("unknown"); got.Active != "" || len(got.Custom) != 0 {
		t.Errorf("unknown tracker Resolve = %+v", got)
	}
}

func TestStore_Set_PersistsAndUpdatesCache(t *testing.T) {
	f := &fakeRepo{}
	s := New(f, zerolog.Nop())
	if err := s.Set(context.Background(), "kinozal", "kinozal.guru", []string{"kinozal.example"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if len(f.upserts) != 1 || f.upserts[0].ActiveDomain != "kinozal.guru" {
		t.Errorf("upserts = %+v", f.upserts)
	}
	if cfg := s.Resolve("kinozal"); cfg.Active != "kinozal.guru" {
		t.Errorf("cache not updated: %+v", cfg)
	}
}

func TestStore_ReportFailure_RotatesOncePerCooldown(t *testing.T) {
	registry.RegisterTracker(&stubTracker{name: "rotatest", domains: []string{"a.example", "b.example", "c.example"}})
	f := &fakeRepo{}
	s := New(f, zerolog.Nop())
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }
	var rotations [][2]string
	s.SetOnRotate(func(_, from, to string) { rotations = append(rotations, [2]string{from, to}) })

	s.ReportFailure(context.Background(), "rotatest") // a -> b
	s.ReportFailure(context.Background(), "rotatest") // within cooldown: no-op
	if got := s.Resolve("rotatest").Active; got != "b.example" {
		t.Errorf("active = %q, want b.example", got)
	}
	if len(rotations) != 1 || len(f.upserts) != 1 {
		t.Errorf("rotations=%d upserts=%d, want 1/1", len(rotations), len(f.upserts))
	}
	now = now.Add(RotateCooldown + time.Second)
	s.ReportFailure(context.Background(), "rotatest") // b -> c
	if got := s.Resolve("rotatest").Active; got != "c.example" {
		t.Errorf("active after 2nd rotation = %q, want c.example", got)
	}
}

func TestStore_ReportFailure_SingleDomain_NoRotation(t *testing.T) {
	registry.RegisterTracker(&stubTracker{name: "singletest", domains: []string{"only.example"}})
	f := &fakeRepo{}
	s := New(f, zerolog.Nop())
	s.ReportFailure(context.Background(), "singletest")
	if len(f.upserts) != 0 {
		t.Errorf("single-domain tracker rotated: %+v", f.upserts)
	}
}
```

Also cover: ring wraps past the end back to the canonical domain; custom domains are part of the ring (known + custom order); unknown tracker name is a no-op.

- [ ] **Step 2: Run tests, verify failure**

- [ ] **Step 3: Implement the store**

```go
// Package domains holds the runtime per-tracker domain configuration
// (issue #126): which domain each plugin should use and the admin-added
// custom mirrors. It is the registry's DomainResolver backing store and
// the Phase-2 rotation engine. Single-process by design (same assumption
// as sse.Hub); a Redis-backed store is the multi-replica escape hatch.
package domains

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/metrics"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// RotateCooldown is the minimum interval between two automatic rotations
// of the same tracker — a burst of failing topics must not spin the ring
// past the working mirror before it gets a chance to serve a check.
const RotateCooldown = 10 * time.Minute

// SettingsRepo is the persistence seam (implemented by repo.TrackerSettings).
type SettingsRepo interface {
	List(ctx context.Context) ([]repo.TrackerSetting, error)
	Upsert(ctx context.Context, trackerName, activeDomain string, customDomains []string) error
}

// Store caches tracker domain configuration in memory.
type Store struct {
	mu         sync.RWMutex
	cfg        map[string]registry.DomainConfig
	lastRotate map[string]time.Time
	settings   SettingsRepo
	log        zerolog.Logger
	onRotate   func(tracker, from, to string)
	now        func() time.Time
}

func New(settings SettingsRepo, log zerolog.Logger) *Store {
	return &Store{
		cfg:        map[string]registry.DomainConfig{},
		lastRotate: map[string]time.Time{},
		settings:   settings,
		log:        log,
		now:        time.Now,
	}
}

// SetOnRotate installs the rotation notification hook (wired in main.go).
func (s *Store) SetOnRotate(fn func(tracker, from, to string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onRotate = fn
}

// Load reads all persisted rows into the cache. Called once at boot.
func (s *Store) Load(ctx context.Context) error {
	rows, err := s.settings.List(ctx)
	if err != nil {
		return fmt.Errorf("domains: load: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range rows {
		s.cfg[r.TrackerName] = registry.DomainConfig{Active: r.ActiveDomain, Custom: r.CustomDomains}
	}
	return nil
}

// Resolve implements registry.DomainResolver.
func (s *Store) Resolve(trackerName string) registry.DomainConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg[trackerName]
}

// Get returns the current configuration for the handler layer.
func (s *Store) Get(trackerName string) (active string, custom []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.cfg[trackerName]
	return c.Active, append([]string{}, c.Custom...)
}

// Set persists and caches one tracker's configuration.
func (s *Store) Set(ctx context.Context, trackerName, active string, custom []string) error {
	if err := s.settings.Upsert(ctx, trackerName, active, custom); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg[trackerName] = registry.DomainConfig{Active: active, Custom: custom}
	return nil
}

// ReportFailure rotates the tracker's active domain to the next candidate
// in the ring (known domains + custom domains), at most once per
// RotateCooldown. Persistence failure is logged, the in-memory rotation
// still applies (fail-open: next boot reloads the old value, worst case
// one extra rotation). No-op for unknown trackers, trackers without the
// WithDomains capability, and rings of length < 2.
func (s *Store) ReportFailure(ctx context.Context, trackerName string) {
	tr := registry.GetTracker(trackerName)
	wd, ok := tr.(registry.WithDomains)
	if !ok {
		return
	}
	s.mu.Lock()
	cur := s.cfg[trackerName]
	ring := append(append([]string{}, wd.Domains()...), cur.Custom...)
	if len(ring) < 2 {
		s.mu.Unlock()
		return
	}
	if last, ok := s.lastRotate[trackerName]; ok && s.now().Sub(last) < RotateCooldown {
		s.mu.Unlock()
		return
	}
	from := cur.Active
	if from == "" {
		from = ring[0]
	}
	idx := 0
	for i, d := range ring {
		if d == from {
			idx = i
			break
		}
	}
	to := ring[(idx+1)%len(ring)]
	cur.Active = to
	s.cfg[trackerName] = cur
	s.lastRotate[trackerName] = s.now()
	hook := s.onRotate
	custom := cur.Custom
	s.mu.Unlock()

	metrics.TrackerDomainRotations.WithLabelValues(trackerName).Inc()
	s.log.Warn().Str("tracker", trackerName).Str("from", from).Str("to", to).
		Msg("tracker domain rotated after network failures")
	if err := s.settings.Upsert(ctx, trackerName, to, custom); err != nil {
		s.log.Warn().Err(err).Str("tracker", trackerName).Msg("persist rotated domain failed")
	}
	if hook != nil {
		hook(trackerName, from, to)
	}
}
```

Fill in the elided `Load`/`Resolve`/`Get` bodies (straightforward map reads/writes under the mutex). Add the metrics CounterVec `TrackerDomainRotations` (name `marauder_tracker_domain_rotations_total`, label `tracker`) in the metrics package, mirroring the existing `marauder_scheduler_episodes_per_tick_capped_total{tracker_name}` collector's definition + registration.

- [ ] **Step 4: Run tests; PASS. Run backend verify.**
- [ ] **Step 5: Commit** — `feat(domains): in-memory domain store with failure rotation (#126)`

---

### Task 4: Kinozal adopts the seam (reference plugin)

**Files:**
- Modify: `backend/internal/plugins/trackers/kinozal/kinozal.go`
- Test: `backend/internal/plugins/trackers/kinozal/kinozal_test.go`

**Interfaces:**
- Consumes: `registry.ActiveDomain`, `registry.DomainAllowed`, `registry.WithDomains` (Task 2).
- Produces: the adoption pattern replicated in Tasks 5-7.

The uniform pattern (applies to every plugin task):
1. `knownDomains` package var — first entry = canonical.
2. `Domains() []string` method + `var _ registry.WithDomains = (*plugin)(nil)`.
3. `effectiveDomain()` — registry override, else the existing `p.domain` field (tests keep overriding `p.domain`; when `p.domain` differs from `defaultDomain` — i.e. a test override — the override wins over the registry so tests are immune to global state).
4. `urlPattern` host part becomes `([^/]+)` capture; `CanParse` checks `registry.DomainAllowed`; `Parse` submatch indexes shift by one.
5. Every production URL built from `p.domain` switches to `p.effectiveDomain()`.

- [ ] **Step 1: Write failing tests** (add to `kinozal_test.go`)

```go
func TestCanParse_CustomDomain_AllowedViaResolver(t *testing.T) {
	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "kinozal" {
			return registry.DomainConfig{Custom: []string{"kinozal.example"}}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{sessions: forumcommon.New(), domain: defaultDomain}
	if !p.CanParse("https://kinozal.example/details.php?id=123") {
		t.Error("custom domain should parse")
	}
	if p.CanParse("https://evil.example/details.php?id=123") {
		t.Error("unlisted domain must not parse")
	}
}

func TestEffectiveDomain_ResolverOverride(t *testing.T) {
	registry.SetDomainResolver(func(string) registry.DomainConfig {
		return registry.DomainConfig{Active: "kinozal.me"}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{sessions: forumcommon.New(), domain: defaultDomain}
	if got := p.effectiveDomain(); got != "kinozal.me" {
		t.Errorf("effectiveDomain = %q, want kinozal.me", got)
	}
	// A test-injected p.domain (≠ defaultDomain) must win over the resolver —
	// this is what keeps every httptest-based e2e test working.
	p.domain = "127.0.0.1:9999"
	if got := p.effectiveDomain(); got != "127.0.0.1:9999" {
		t.Errorf("effectiveDomain with test override = %q, want 127.0.0.1:9999", got)
	}
}

func TestDomains_CanonicalFirst(t *testing.T) {
	p := &plugin{}
	want := []string{"kinozal.tv", "kinozal.me", "kinozal.guru"}
	if !reflect.DeepEqual(p.Domains(), want) {
		t.Errorf("Domains() = %v, want %v", p.Domains(), want)
	}
}
```

- [ ] **Step 2: Run kinozal package tests, verify the new ones fail**

- [ ] **Step 3: Implement**

```go
var knownDomains = []string{"kinozal.tv", "kinozal.me", "kinozal.guru"}

// urlPattern is host-agnostic; CanParse gates the captured host against the
// known + admin-configured domain allowlist (the SSRF barrier — see
// registry.DomainAllowed).
var urlPattern = regexp.MustCompile(`^https?://(?:www\.)?([^/]+)/details\.php\?id=(\d+)`)

var _ registry.WithDomains = (*plugin)(nil)

// Domains implements registry.WithDomains; first entry is canonical.
func (p *plugin) Domains() []string { return knownDomains }

// effectiveDomain resolves the domain every request is built against:
// a test-injected p.domain wins (httptest servers), then the admin-
// configured active domain, then the compiled default.
func (p *plugin) effectiveDomain() string {
	if p.domain != defaultDomain {
		return p.domain
	}
	if d := registry.ActiveDomain(pluginName); d != "" {
		return d
	}
	return p.domain
}

func (p *plugin) CanParse(rawURL string) bool {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	return m != nil && registry.DomainAllowed(pluginName, m[1], knownDomains)
}
```

In `Parse`, the topic-id submatch moves from `m[1]` to `m[2]`. Replace every production read of `p.domain` with `p.effectiveDomain()` — Login (`takelogin.php`), Verify, `canonicalDetailsURL`, `fetchInfohash`, `ResolveMetadata` (`extractImageURL(body, p.effectiveDomain())`), `Download` (both the `dl.` and bare-domain fallbacks). Do NOT touch test files' `p.domain =` assignments.

- [ ] **Step 4: Run kinozal package tests (all, incl. existing e2e); expect PASS**
- [ ] **Step 5: Commit** — `feat(kinozal): configurable domain via registry resolver (#126)`

---

### Task 5: RuTracker adopts the seam

**Files:**
- Modify: `backend/internal/plugins/trackers/rutracker/rutracker.go` (and `authorcomment.go:55` — same `p.domain` swap)
- Test: `backend/internal/plugins/trackers/rutracker/rutracker_test.go`

Apply the exact Task-4 pattern with these substitutions:

- `knownDomains = []string{"rutracker.org", "rutracker.net", "rutracker.nl", "rutracker.cr"}`
- `urlPattern` becomes `^https?://(?:www\.)?([^/]+)/forum/viewtopic\.php\?t=(\d+)` (topic id shifts to `m[2]` in `Parse`)
- `p.domain` production reads to swap: login endpoint (`rutracker.go:115`), verify (`:146`), canonical check URL (`:248`), download URL (`:287`), author-comment canonical URL (`authorcomment.go:55`).

- [ ] **Step 1: Write the three failing tests from Task 4 adapted to rutracker (`CanParse` custom domain, `effectiveDomain` precedence incl. test-override-wins, `Domains()` order)**
- [ ] **Step 2: Verify failure, implement, run the full rutracker package (existing tests must stay green)**
- [ ] **Step 3: Run backend verify**
- [ ] **Step 4: Commit** — `feat(rutracker): configurable domain via registry resolver (#126)`

---

### Task 6: NNM-Club + LostFilm (runtime host allowlists)

**Files:**
- Modify: `backend/internal/plugins/trackers/nnmclub/nnmclub.go`
- Modify: `backend/internal/plugins/trackers/lostfilm/lostfilm_session.go`, `lostfilm_redirector.go`, `lostfilm_metadata.go`, `lostfilm.go` (mechanical `p.domain` → `p.effectiveDomain()` swaps)
- Test: `nnmclub_test.go`, lostfilm test files (existing must stay green; add the Task-4 trio per plugin)

**NNM-Club** (no `domain` field today — uses stored topic URL):
- `knownDomains = []string{"nnmclub.to", "nnmclub.me"}`, `Domains()`, `var _ registry.WithDomains`.
- `urlPattern` → `^https?://(?:www\.)?([^/]+)/forum/viewtopic\.php\?t=(\d+)`; `CanParse` via `registry.DomainAllowed(pluginName, m[1], knownDomains)`.
- The `fetch` SSRF guard (`nnmclub.go:171-176`): replace the literal `switch u.Hostname()` with

```go
	if !registry.DomainAllowed(pluginName, u.Hostname(), knownDomains) {
		return nil, fmt.Errorf("nnm-club: refusing to fetch off-site host %q", u.Hostname())
	}
```

  Keep the surrounding comment, updated to say the allowlist is now known domains + admin-configured custom domains (the CodeQL alert is already dismissed won't-fix; the barrier stays a strict allowlist).
- Since NNM-Club fetches the **stored** topic URL, an active-domain override must actually redirect fetches: add a small `canonicalURL(topic.URL)` step in `Check` — parse the URL and, when `registry.ActiveDomain(pluginName)` is non-empty and differs from the URL host, swap `u.Host` to the active domain before fetching. Add a unit test: resolver active=`nnmclub.me`, topic URL on `nnmclub.to` → the fetched URL host is `nnmclub.me` (use the plugin's `transport` test seam the same way existing nnmclub tests inject responses).

**LostFilm**:
- `knownDomains = []string{"www.lostfilm.tv", "lostfilm.tv", "lostfilm.win", "lostfilm.run"}`, `Domains()`, capability assert.
- `effectiveDomain()` as in Task 4 (it has `domain` field + `defaultDomain = "www.lostfilm.tv"`).
- Swap `p.domain` production reads: `lostfilm_session.go:48,146,184,273,306`, `lostfilm_redirector.go:131`, `lostfilm_metadata.go:65,67,85`.
- `urlPattern` → host-agnostic capture + `DomainAllowed` in `CanParse` (slug shifts to `m[2]`).
- `validateRedirectURL` (`lostfilm_redirector.go:84`): after the map lookup fails, also accept `registry.DomainAllowed(pluginName, host, nil)` (custom domains only — the map already covers known hosts + redirector hosts). Keep the private-IP check running for BOTH acceptance paths.

- [ ] **Step 1: Failing tests per plugin (Task-4 trio + the nnmclub active-domain fetch-rewrite test + a lostfilm `validateRedirectURL` custom-domain test)**
- [ ] **Step 2: Implement; full packages green; backend verify**
- [ ] **Step 3: Commit** — `feat(nnmclub,lostfilm): runtime domain allowlists (#126)`

---

### Task 7: Remaining fixed-domain plugins (anidub, freetorrents, hdclub, tapochek, toloka, unionpeer, rutor, anilibria)

**Files (one commit per plugin is fine, or one commit for all eight):**
- Modify: `anidub/anidub.go`, `freetorrents/freetorrents.go`, `hdclub/hdclub.go`, `tapochek/tapochek.go`, `toloka/toloka.go`, `unionpeer/unionpeer.go`, `rutor/rutor.go`, `anilibria/anilibria.go`
- Test: each plugin's existing `*_test.go` + the Task-4 trio per plugin

Apply the Task-4 pattern per plugin. Known domains + regex substitutions (path shapes copied from the current patterns — reuse them exactly):

| Plugin | knownDomains (canonical first) | New host-agnostic urlPattern |
|---|---|---|
| anidub | `["tr.anidub.com"]` | `^https?://([^/]+)/(?:[a-z0-9_-]+/)+([a-z0-9_-]+)\.html` — slug moves to `m[2]` |
| freetorrents | `["free-torrents.org"]` | `^https?://(?:www\.)?([^/]+)/forum/viewtopic\.php\?t=(\d+)` |
| hdclub | `["hdclub.org"]` | `^https?://(?:www\.)?([^/]+)/details\.php\?id=(\d+)` |
| tapochek | `["tapochek.net"]` | `^https?://(?:www\.)?([^/]+)/viewtopic\.php\?t=(\d+)` |
| toloka | `["toloka.to"]` | `^https?://(?:www\.)?([^/]+)/t(\d+)` |
| unionpeer | `["unionpeer.org", "unionpeer.net", "unionpeer.com"]` | `^https?://(?:www\.)?([^/]+)/forum/viewtopic\.php\?t=(\d+)` |
| rutor | `["rutor.org", "rutor.info"]` | `^https?://(?:www\.)?([^/]+)/torrent/(\d+)` |
| anilibria | `["anilibria.tv"]` | `^https?://(?:www\.)?([^/]+)/release/([^/]+?)\.html` |

Per-plugin notes:
- **anidub, freetorrents, hdclub, tapochek, toloka, unionpeer** have the `domain` field — identical treatment to Task 4 (swap the `p.domain` production reads listed in the grep: e.g. anidub `:84,:141`, unionpeer `:77,:134`, toloka `:75,:132`, tapochek `:79,:110,:160`).
- **anidub's** pattern is anchored to a single host with NO `www.` optional today; the host-agnostic form above is fine because `DomainAllowed` gates it. Its CanParse must keep matching `https://tr.anidub.com/...` (add that as a regression assert).
- **rutor** has NO `domain` field: add `domain string` + `defaultDomain = "rutor.org"` + set in `init()` (mirroring kinozal), give it `effectiveDomain()`, and in `Check`/`Download`/`fetch` rebuild the request URL host from `effectiveDomain()` when the stored URL host differs (same canonicalURL approach as nnmclub in Task 6 — this also closes rutor's "no host guard" gap: refuse hosts failing `DomainAllowed`).
- **anilibria** uses `apiBase` (`https://api.anilibria.tv/v3`) + page-host regex. Add `effectiveAPIBase()`: if `registry.ActiveDomain(pluginName)` != "" and `p.apiBase == apiBase` (i.e. not a test override) → `"https://api." + active + "/v3"`, else `p.apiBase`. Swap the two `p.apiBase` reads (`:119,:152`).
- If any plugin's `Parse` builds the placeholder display name from the regex, keep behavior identical after the index shift (assert in existing tests).

- [ ] **Step 1: For each plugin: failing Task-4-trio tests → implement → package tests green**
- [ ] **Step 2: Backend verify across the whole tree**
- [ ] **Step 3: Commit** — `feat(trackers): configurable domains for remaining fixed-domain plugins (#126)`

---

### Task 8: Admin API — list/update/test tracker domains

**Files:**
- Create: `backend/internal/api/handlers/tracker_domains.go`
- Test: `backend/internal/api/handlers/tracker_domains_test.go`
- Modify: `backend/internal/api/router.go` (Deps + admin route group)

**Interfaces:**
- Consumes: `domains.Store.Get/Set` (Task 3), `registry.ListTrackers`, `registry.WithDomains`.
- Produces routes (admin group, after the sonarr block):
  - `GET  /api/v1/system/trackers/domains` → `[]trackerDomainsView{name, display_name, default_domain, known_domains, custom_domains, active_domain}`
  - `PUT  /api/v1/system/trackers/{name}/domains` ← `{active_domain string, custom_domains []string}` → updated view
  - `POST /api/v1/system/trackers/{name}/domains/test` ← `{domain string}` → `{ok bool, detail string}`

- [ ] **Step 1: Write failing handler tests** (follow `sonarr_test.go` harness style — chi router with the handler mounted, fake store):

Cases:
1. `GET` returns one entry per `WithDomains` tracker with correct fields (register a stub `WithDomains` tracker; generic adapters absent).
2. `PUT` valid body → 200, fake store received `Set(name, active, custom)` with lowercased hostnames.
3. `PUT` active domain not in known ∪ custom → 422 (`problem.ErrUnprocessable` — check the problem package's actual 422 constructor name and use it).
4. `PUT` custom domain with scheme (`https://x.y`), port (`x.y:8080`), path (`x.y/z`), IP literal (`1.2.3.4`), invalid label (`-bad-.example`) → 422 each (table-driven).
5. `PUT` unknown tracker name → 404.
6. `PUT` empty active_domain ("") → 200, reverts to default (store receives `""`).
7. `POST test` with a domain that fails validation → 422 (don't exercise real network in the success case; inject the prober — see below).

- [ ] **Step 2: Verify failure, implement**

```go
// hostnameRe: RFC-1123 labels, at least two labels, no scheme/port/path.
var hostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

func validateHostname(h string) (string, error) {
	h = strings.ToLower(strings.TrimSpace(h))
	if h == "" {
		return "", errors.New("hostname is empty")
	}
	if net.ParseIP(h) != nil {
		return "", fmt.Errorf("hostname %q must not be an IP literal", h)
	}
	if !hostnameRe.MatchString(h) {
		return "", fmt.Errorf("hostname %q is not a valid domain name (no scheme, port or path)", h)
	}
	return h, nil
}
```

Handler struct: `TrackerDomains{Store domainsStore; Probe func(ctx, host) error; BaseURL string; Audit *audit.Logger}` with consumer seam `domainsStore interface{ Get(name string) (string, []string); Set(ctx context.Context, name, active string, custom []string) error }`. The default `Probe` (wired in router) does `GET https://<host>/` with a 10s-timeout `http.Client` and, before dialing, rejects hosts whose DNS resolves only to loopback/private/link-local IPs (reuse the shape of lostfilm's `validateRedirectURL` IP check — copy the ~10-line IP-range check into this handler file; do NOT import the plugin package). Validation order in PUT: tracker exists & implements `WithDomains` → each custom hostname validated & lowercased → active ∈ {""} ∪ known ∪ custom → `Store.Set` → audit entry `tracker.domains.update` (mirror `sonarr.instance.create`'s audit call shape).

Router: add `TrackerSettings *domains.Store` to `Deps`, construct `trackerDomainsH`, register the three routes inside the existing `RequireAdmin` group.

- [ ] **Step 3: Tests green; backend verify**
- [ ] **Step 4: Commit** — `feat(api): admin endpoints for tracker domain configuration (#126)`

---

### Task 9: Boot wiring + topic-create URL canonicalization

**Files:**
- Modify: `backend/cmd/server/main.go`
- Modify: `backend/internal/topics/create.go`
- Test: `backend/internal/topics/create_test.go` (or the package's existing test file)

**Interfaces:**
- Consumes: everything above.
- Produces: running wiring; `canonicalTopicURL(tr registry.Tracker, rawURL string) string` in package `topics`.

- [ ] **Step 1: main.go wiring** (after the repos block at `main.go:103-112`, before the router deps):

```go
	trackerSettingsRepo := repo.NewTrackerSettings(pool)
	domainStore := domains.New(trackerSettingsRepo, logger)
	if err := domainStore.Load(rootCtx); err != nil {
		logger.Warn().Err(err).Msg("tracker domain settings load failed; using plugin defaults")
	}
	registry.SetDomainResolver(domainStore.Resolve)
```

Wire the rotation notification hook (uses the notify dispatcher `disp` and `users` repo already constructed nearby — move this block AFTER `disp := notify.New(...)` at `main.go:147`):

```go
	domainStore.SetOnRotate(func(tracker, from, to string) {
		admin, aerr := users.GetInitialAdmin(context.Background())
		if aerr != nil || admin == nil {
			logger.Warn().Err(aerr).Msg("domain rotation: no admin to notify")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		disp.Send(ctx, admin.ID, string(events.CheckFailed), domain.Message{
			Title: fmt.Sprintf("Tracker %s switched to mirror %s", tracker, to),
			Body:  fmt.Sprintf("Checks against %s were failing; Marauder now uses %s. Revert or adjust under Settings → Tracker domains.", from, to),
		})
	})
```

Check `users.GetInitialAdmin`'s actual signature/return and `domain.Message` field names (see how the scheduler builds session-expiry messages around `scheduler.go:612`) and adapt; the event string `events.CheckFailed` routes to notifiers subscribed to error events, which is the right audience. Pass `domainStore` into router `Deps.TrackerSettings`.

- [ ] **Step 2: Failing test for URL canonicalization** (in package `topics`; a stub tracker implementing `WithDomains` with `Domains() = ["kinozal.tv", ...]`):

```go
func TestCanonicalTopicURL_RewritesMirrorHost(t *testing.T) {
	tr := &stubDomainsTracker{domains: []string{"kinozal.tv", "kinozal.me"}}
	got := canonicalTopicURL(tr, "https://kinozal.me/details.php?id=42")
	if got != "https://kinozal.tv/details.php?id=42" {
		t.Errorf("canonicalTopicURL = %q", got)
	}
	// Same host (modulo www.) → unchanged input returned verbatim.
	if got := canonicalTopicURL(tr, "https://www.kinozal.tv/details.php?id=42"); got != "https://www.kinozal.tv/details.php?id=42" {
		t.Errorf("same-host URL rewritten: %q", got)
	}
	// Non-WithDomains tracker → unchanged.
	if got := canonicalTopicURL(&stubPlainTracker{}, "https://x/y"); got != "https://x/y" {
		t.Errorf("plain tracker URL rewritten: %q", got)
	}
}
```

- [ ] **Step 3: Implement** in `create.go` — right after `registry.FindTrackerForURL(in.URL)` succeeds, do `in.URL = canonicalTopicURL(tr, in.URL)` so Parse stores the canonical host (stable dedup identity under `UNIQUE(user_id,url)`):

```go
// canonicalTopicURL rewrites a mirror-host topic URL onto the tracker's
// canonical (default) domain so the same topic added via different mirrors
// dedups to one row. The active mirror is a fetch-time concern
// (effectiveDomain) — stored identity stays stable across rotations.
func canonicalTopicURL(tr registry.Tracker, rawURL string) string {
	wd, ok := tr.(registry.WithDomains)
	if !ok || len(wd.Domains()) == 0 {
		return rawURL
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return rawURL
	}
	canonical := wd.Domains()[0]
	if strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.") ==
		strings.TrimPrefix(strings.ToLower(canonical), "www.") {
		return rawURL
	}
	u.Scheme = "https"
	u.Host = canonical
	return u.String()
}
```

- [ ] **Step 4: Backend verify (full tree)**
- [ ] **Step 5: Commit** — `feat(topics): wire domain store; canonicalize topic URLs on create (#126)`

---

### Task 10: Frontend — Settings "Tracker domains" section

**Files:**
- Modify: `frontend/src/lib/api.ts` (types + endpoint helpers, near the Sonarr section at `api.ts:197`)
- Modify: `frontend/src/lib/queryKeys.ts` (add `trackerDomains`)
- Create: `frontend/src/components/settings/TrackerDomainsCard.tsx` (+ `TrackerDomainRow.tsx` if the card nears 250 lines)
- Modify: `frontend/src/pages/Settings.tsx` (render for admins)
- Modify: `frontend/src/i18n/` en + ru dictionaries
- Test: `frontend/src/components/settings/TrackerDomainsCard.test.tsx`

**Interfaces:**
- Consumes: Task 8 endpoints.
- Produces:

```ts
export interface TrackerDomains {
  name: string;
  display_name: string;
  default_domain: string;
  known_domains: string[];
  custom_domains: string[];
  active_domain: string; // "" = default
}
```

- [ ] **Step 1: Write failing component tests** (mock `api` module like `Integrations.test.tsx` mocks it; mock `useAuthStore` for role):

1. renders a row per tracker with the active domain selected (default marked, e.g. `kinozal.tv (default)`);
2. changing the select fires `PUT /system/trackers/kinozal/domains` with `{active_domain: "kinozal.me", custom_domains: [...]}` and invalidates `QK.trackerDomains`;
3. adding a custom domain `kinozal.example` calls PUT with it appended; input rejects `https://x.y` client-side (error text shown, no request);
4. non-admin: `SettingsPage` does not render the section (assert by heading absence).

- [ ] **Step 2: Verify failure (`npm test -- --run` in docker)**

- [ ] **Step 3: Implement**

- `queryKeys.ts`: `trackerDomains: ["tracker-domains"] as const` (match the existing factory style).
- `api.ts`: `TrackerDomains` interface + functions following the existing Sonarr helpers' style (`listTrackerDomains(): Promise<TrackerDomains[]>`, `updateTrackerDomains(name, body)`, `testTrackerDomain(name, domain)`).
- Card: shadcn `Card` (same chrome as Settings' other cards), one row per tracker: display name, a domain `<select>`/shadcn Select of `[default_domain + " (default)", ...known, ...custom]`, an inline "add mirror" `Input` + Button (client-side hostname regex `^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$` on the lowercased trim), a per-custom-domain remove button, and a Test button per row calling the test endpoint and toasting/inline-labelling the result. React Query `useQuery({queryKey: QK.trackerDomains, ...})` + `useMutation` with invalidation. All copy through `useT()`; add `settings.domains.*` keys to BOTH en and ru (title, blurb, defaultSuffix, addPlaceholder, addButton, invalidHostname, test, testOk, testFail, instanceWideNote).
- `Settings.tsx`: `const user = useAuthStore((s) => s.user);` already exists — append `{user?.role === "admin" && <TrackerDomainsCard />}` between `AccountCard` and `AboutCard`, with the "instance-wide, admin-only" note inside the card description.

- [ ] **Step 4: Frontend verify (Global Constraints command); expect PASS**
- [ ] **Step 5: Commit** — `feat(ui): tracker domain settings section (#126)`

---

### Task 11: Phase 2 — scheduler reports network failures

**Files:**
- Modify: `backend/internal/scheduler/scheduler.go`
- Test: `backend/internal/scheduler/scheduler_test.go`

**Interfaces:**
- Consumes: `domains.Store.ReportFailure` (Task 3).
- Produces: scheduler seam `domainRotator interface{ ReportFailure(ctx context.Context, trackerName string) }`, new `Scheduler` field `domains domainRotator` (nil-safe), setter or constructor param — follow how the other optional deps get injected (check `scheduler.New`'s signature at `main.go:151` and add the store as a new param; update all `New` call sites including tests with `nil`).

- [ ] **Step 1: Failing test** (mirror the existing fake-based scheduler tests):

```go
type fakeRotator struct{ calls []string }

func (f *fakeRotator) ReportFailure(_ context.Context, name string) { f.calls = append(f.calls, name) }

func TestRecordResult_NetworkError_ReportsDomainFailure(t *testing.T) {
	tests := []struct {
		name      string
		errMsg    string
		wantCalls int
	}{
		{"unreachable rotates", "kinozal GET: dial tcp: connection refused", 1},
		{"timeout rotates", "context deadline exceeded", 1},
		{"auth error does not rotate", "kinozal login failed: invalid credentials", 0},
		{"success does not rotate", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rot := &fakeRotator{}
			s := newTestScheduler(t) // reuse the package's existing scheduler-builder helper
			s.domains = rot
			s.recordResult(context.Background(), zerolog.Nop(), uuid.New(), "kinozal",
				"", false, time.Now(), tt.errMsg)
			if len(rot.calls) != tt.wantCalls {
				t.Errorf("ReportFailure calls = %d, want %d", len(rot.calls), tt.wantCalls)
			}
			if tt.wantCalls == 1 && rot.calls[0] != "kinozal" {
				t.Errorf("ReportFailure tracker = %q, want kinozal", rot.calls[0])
			}
		})
	}
}
```

The exact `newTestScheduler` helper name and `recordResult` parameter order must match the package (see `TestRecordResult_ClassifiesAndPersistsErrorCode` at `scheduler_test.go:1823` for how a scheduler is built there and where `trackerName` fits after Step 2's signature change) — adjust the call accordingly; the two assertion messages are non-negotiable, so verify BOTH classifications route and BOTH negatives don't.

- [ ] **Step 2: Implement** — extend `recordResult` (`scheduler.go:314`) with a `trackerName string` param (update its call sites in `runCheck`); after `errCode = classifyError(errMsg)` add:

```go
	if s.domains != nil && (errCode == errCodeTimeout || errCode == errCodeUnreachable) {
		s.domains.ReportFailure(ctx, trackerName)
	}
```

- [ ] **Step 3: Scheduler package tests green; backend verify. Wire the real store in `main.go` (`scheduler.New(..., domainStore)`).**
- [ ] **Step 4: Commit** — `feat(scheduler): rotate tracker domain on network-classified failures (#126)`

---

### Task 12: Docs, changelog, full verification

**Files:**
- Modify: `CHANGELOG.md` (`[Unreleased]`), `CLAUDE.md` (backend package table: add `domains`; repo list: add `TrackerSettings`; plugin capability lists: add `WithDomains`; Settings page description), `docs/superpowers/specs/2026-07-21-tracker-domains-design.md` (only if implementation deviated — record deviations)

- [ ] **Step 1: CHANGELOG `[Unreleased]`** — Added: "Per-tracker domain configuration with known mirrors and validated custom domains (Settings → Tracker domains, admin-only), automatic mirror rotation on network failures, and mirror-stable topic dedup (#126)."
- [ ] **Step 2: CLAUDE.md updates** (same commit as the docs, per the documentation-maintenance rule — the structural additions already shipped in earlier commits, so this is the consolidated doc pass).
- [ ] **Step 3: Full backend + frontend verify commands; both must PASS. Fix anything red before committing.**
- [ ] **Step 4: Commit** — `docs: document tracker domain configuration (#126)`
- [ ] **Step 5: Optional live smoke** (needs the dev stack): set kinozal active domain to `kinozal.me` via the UI, confirm an existing kinozal topic's next check fetches `kinozal.me` (backend logs) — matches spec success criterion 1.

---

## Self-review notes (already applied)

- Spec §"all 16 plugins" was corrected to the 12 fixed-domain plugins (generic adapters have no fixed domain) — spec updated in the same branch.
- `effectiveDomain()` precedence (test-override field > resolver > default) is deliberate and asserted in Task 4's test — it keeps every httptest-based plugin test independent of global resolver state.
- Rotation persists via `Upsert` and survives restart; cooldown state (`lastRotate`) is intentionally in-memory only (worst case: one extra rotation right after boot).
- The notification event type reuses `events.CheckFailed`'s subscription channel rather than adding a new event type — admins subscribed to error events get rotation alerts; a dedicated event type would ripple through events policy, i18n, and the frontend for marginal value (YAGNI).
