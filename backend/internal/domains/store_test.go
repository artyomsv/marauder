package domains

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
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
type stubTracker struct {
	name    string
	domains []string
}

func (s *stubTracker) Name() string         { return s.name }
func (s *stubTracker) DisplayName() string  { return s.name }
func (s *stubTracker) CanParse(string) bool { return false }
func (s *stubTracker) Parse(context.Context, string) (*domain.Topic, error) {
	return nil, nil
}
func (s *stubTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (s *stubTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}
func (s *stubTracker) Domains() []string { return s.domains }

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

// rotateOnce reports RotateFailureThreshold failures for a tracker so it
// crosses the gate and rotates exactly once (the caller controls the clock so
// all failures land inside one RotateFailureWindow).
func rotateOnce(s *Store, tracker string) {
	for i := 0; i < RotateFailureThreshold; i++ {
		s.ReportFailure(context.Background(), tracker)
	}
}

func TestStore_ReportFailure_SingleFailureBelowThreshold_NoRotation(t *testing.T) {
	registry.RegisterTracker(&stubTracker{name: "threshtest", domains: []string{"a.example", "b.example"}})
	f := &fakeRepo{}
	s := New(f, zerolog.Nop())
	s.ReportFailure(context.Background(), "threshtest") // 1 < threshold
	if got := s.Resolve("threshtest").Active; got != "" {
		t.Errorf("rotated on a single failure: active=%q", got)
	}
	if len(f.upserts) != 0 {
		t.Errorf("persisted on a single failure: %+v", f.upserts)
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

	rotateOnce(s, "rotatest")                         // a -> b (2 failures trip the gate)
	s.ReportFailure(context.Background(), "rotatest") // within cooldown: no-op
	if got := s.Resolve("rotatest").Active; got != "b.example" {
		t.Errorf("active = %q, want b.example", got)
	}
	if len(rotations) != 1 || len(f.upserts) != 1 {
		t.Errorf("rotations=%d upserts=%d, want 1/1", len(rotations), len(f.upserts))
	}
	now = now.Add(RotateCooldown + time.Second)
	rotateOnce(s, "rotatest") // b -> c
	if got := s.Resolve("rotatest").Active; got != "c.example" {
		t.Errorf("active after 2nd rotation = %q, want c.example", got)
	}
}

func TestStore_ReportFailure_SingleDomain_NoRotation(t *testing.T) {
	registry.RegisterTracker(&stubTracker{name: "singletest", domains: []string{"only.example"}})
	f := &fakeRepo{}
	s := New(f, zerolog.Nop())
	rotateOnce(s, "singletest")
	if len(f.upserts) != 0 {
		t.Errorf("single-domain tracker rotated: %+v", f.upserts)
	}
}

func TestStore_ReportFailure_RingWrapsPastEnd(t *testing.T) {
	registry.RegisterTracker(&stubTracker{name: "wraptest", domains: []string{"a.example", "b.example"}})
	f := &fakeRepo{}
	s := New(f, zerolog.Nop())
	now := time.Unix(2000, 0)
	s.now = func() time.Time { return now }

	rotateOnce(s, "wraptest") // a -> b
	if got := s.Resolve("wraptest").Active; got != "b.example" {
		t.Fatalf("active after 1st rotation = %q, want b.example", got)
	}
	now = now.Add(RotateCooldown + time.Second)
	rotateOnce(s, "wraptest") // b -> a (wraps)
	if got := s.Resolve("wraptest").Active; got != "a.example" {
		t.Errorf("active after wrap = %q, want a.example", got)
	}
	if len(f.upserts) != 2 {
		t.Errorf("upserts = %d, want 2", len(f.upserts))
	}
}

func TestStore_ReportFailure_CustomDomainsPartOfRing(t *testing.T) {
	registry.RegisterTracker(&stubTracker{name: "customringtest", domains: []string{"a.example", "b.example"}})
	f := &fakeRepo{}
	s := New(f, zerolog.Nop())
	if err := s.Set(context.Background(), "customringtest", "b.example", []string{"custom.example"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	rotateOnce(s, "customringtest") // b -> custom (known + custom order)
	if got := s.Resolve("customringtest").Active; got != "custom.example" {
		t.Errorf("active = %q, want custom.example", got)
	}
}

// syncRepo is a SettingsRepo that signals on calls after every Upsert
// append, giving the test a synchronization point instead of a sleep.
type syncRepo struct {
	mu      sync.Mutex
	upserts []repo.TrackerSetting
	calls   chan struct{}
}

func (r *syncRepo) List(context.Context) ([]repo.TrackerSetting, error) { return nil, nil }

func (r *syncRepo) Upsert(_ context.Context, name, active string, custom []string) error {
	r.mu.Lock()
	r.upserts = append(r.upserts, repo.TrackerSetting{
		TrackerName:   name,
		ActiveDomain:  active,
		CustomDomains: append([]string{}, custom...),
	})
	r.mu.Unlock()
	r.calls <- struct{}{}
	return nil
}

// TestStore_ReportFailure_ConcurrentSetKeepsFreshConfig reproduces the
// stale-snapshot race: a rotation in progress must not persist an active
// domain or custom-domain list that predates a concurrent admin Store.Set. It
// parks ReportFailure right after it has computed (and cached) the rotation but
// before it persists, via the beforePersist test seam — this is exactly the
// window where the pre-fix code persisted its stale local rotation target
// instead of re-reading the cache. A concurrent Set (changing the active
// domain and adding a
// custom domain) is then run to completion before ReportFailure is allowed
// to persist, and the test asserts the LAST Upsert the fake repo received
// carries the newly-added custom domain.
//
// This fails on the pre-fix implementation (verified via `git stash`): the
// rotation's Upsert lands last but with the custom list captured before Set
// ran, silently discarding the admin's added mirror on the next restart.
func TestStore_ReportFailure_ConcurrentSetKeepsFreshConfig(t *testing.T) {
	registry.RegisterTracker(&stubTracker{name: "concurrenttest", domains: []string{"a.example", "b.example"}})
	f := &syncRepo{calls: make(chan struct{}, 2)}
	s := New(f, zerolog.Nop())

	now := time.Unix(9000, 0)
	s.now = func() time.Time { return now }
	// Pre-arm the failure counter (within a fresh window) so a single
	// ReportFailure trips the threshold and rotates immediately.
	s.failWindow["concurrenttest"] = now
	s.failCount["concurrenttest"] = RotateFailureThreshold - 1

	reached := make(chan struct{})
	proceed := make(chan struct{})
	var once sync.Once
	// Park via the beforePersist seam: it fires after the rotation is cached
	// but before the persist phase — exactly the stale-snapshot window.
	s.beforePersist = func() {
		once.Do(func() { close(reached) })
		<-proceed
	}

	rfDone := make(chan struct{})
	go func() {
		s.ReportFailure(context.Background(), "concurrenttest") // a -> b
		close(rfDone)
	}()
	<-reached // rotation decided & cached; ReportFailure parked before persisting

	setDone := make(chan error, 1)
	go func() {
		// Admin picks a distinct active domain (not the rotation target
		// "b.example") plus a custom mirror — both must survive.
		setDone <- s.Set(context.Background(), "concurrenttest", "custom.example", []string{"custom.example"})
	}()
	<-f.calls // Set's Upsert has landed — nothing else can call Upsert yet

	close(proceed) // release ReportFailure to persist
	<-rfDone
	if err := <-setDone; err != nil {
		t.Fatalf("Set: %v", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.upserts) != 2 {
		t.Fatalf("upserts = %d, want 2: %+v", len(f.upserts), f.upserts)
	}
	last := f.upserts[len(f.upserts)-1]
	// The rotation's persist must reflect the admin's fresh active domain, not
	// its own stale rotation target ("b.example").
	if last.ActiveDomain != "custom.example" {
		t.Errorf("last persisted row reverted the admin's active domain: got %q, want custom.example", last.ActiveDomain)
	}
	found := false
	for _, d := range last.CustomDomains {
		if d == "custom.example" {
			found = true
		}
	}
	if !found {
		t.Errorf("last persisted row lost the concurrently-added custom domain: %+v", last)
	}
}

func TestStore_ReportFailure_UnknownTracker_NoOp(t *testing.T) {
	f := &fakeRepo{}
	s := New(f, zerolog.Nop())
	s.ReportFailure(context.Background(), "does-not-exist")
	if len(f.upserts) != 0 {
		t.Errorf("unknown tracker rotated: %+v", f.upserts)
	}
	if cfg := s.Resolve("does-not-exist"); cfg.Active != "" || len(cfg.Custom) != 0 {
		t.Errorf("unknown tracker cache mutated: %+v", cfg)
	}
}

// TestStore_Load_DropsActiveDomainNoLongerInTheRing is the self-heal for the
// 2026-07-30 RuTracker incident. Rotation moved the tracker onto
// rutracker.nl, which was later removed from the plugin's domain list for
// serving only a "Redirecting..." stub. Removing it from the code is not
// enough on its own: the dead host stays in tracker_settings, so an upgraded
// install keeps fetching it and cannot recover by itself — the stub's failure
// classifies as `parse`, and only timeout/unreachable rotate.
//
// Load therefore re-validates the persisted active domain and discards one
// the plugin no longer recognises, letting effectiveDomain fall back to the
// canonical host.
func TestStore_Load_DropsActiveDomainNoLongerInTheRing(t *testing.T) {
	registry.RegisterTracker(&stubTracker{name: "loadheal", domains: []string{"canonical.example", "mirror.example"}})
	f := &fakeRepo{rows: []repo.TrackerSetting{
		{TrackerName: "loadheal", ActiveDomain: "removed.example"},
	}}
	s := New(f, zerolog.Nop())

	if err := s.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := s.Resolve("loadheal").Active; got != "" {
		t.Errorf("active = %q, want it dropped so the plugin falls back to its canonical domain", got)
	}
}

// A still-valid active domain must survive Load untouched — the self-heal
// must not undo a deliberate admin choice.
func TestStore_Load_KeepsValidActiveDomain(t *testing.T) {
	registry.RegisterTracker(&stubTracker{name: "loadkeep", domains: []string{"canonical.example", "mirror.example"}})
	f := &fakeRepo{rows: []repo.TrackerSetting{
		{TrackerName: "loadkeep", ActiveDomain: "mirror.example"},
	}}
	s := New(f, zerolog.Nop())

	if err := s.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := s.Resolve("loadkeep").Active; got != "mirror.example" {
		t.Errorf("active = %q, want mirror.example preserved", got)
	}
}

// An admin-added custom mirror is legitimate even though the plugin does not
// ship it, so it must not be treated as stale.
func TestStore_Load_KeepsActiveCustomDomain(t *testing.T) {
	registry.RegisterTracker(&stubTracker{name: "loadcustom", domains: []string{"canonical.example"}})
	f := &fakeRepo{rows: []repo.TrackerSetting{
		{TrackerName: "loadcustom", ActiveDomain: "self.hosted.example", CustomDomains: []string{"self.hosted.example"}},
	}}
	s := New(f, zerolog.Nop())

	if err := s.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := s.Resolve("loadcustom").Active; got != "self.hosted.example" {
		t.Errorf("active = %q, want the custom domain preserved", got)
	}
}

// A tracker with no registered plugin (or one without WithDomains) must be
// left alone rather than having its configuration silently wiped.
func TestStore_Load_UnknownTrackerKeepsActive(t *testing.T) {
	f := &fakeRepo{rows: []repo.TrackerSetting{
		{TrackerName: "notregistered", ActiveDomain: "whatever.example"},
	}}
	s := New(f, zerolog.Nop())

	if err := s.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := s.Resolve("notregistered").Active; got != "whatever.example" {
		t.Errorf("active = %q, want it untouched for an unknown tracker", got)
	}
}
