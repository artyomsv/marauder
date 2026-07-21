package domains

import (
	"context"
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

func TestStore_ReportFailure_RingWrapsPastEnd(t *testing.T) {
	registry.RegisterTracker(&stubTracker{name: "wraptest", domains: []string{"a.example", "b.example"}})
	f := &fakeRepo{}
	s := New(f, zerolog.Nop())
	now := time.Unix(2000, 0)
	s.now = func() time.Time { return now }

	s.ReportFailure(context.Background(), "wraptest") // a -> b
	if got := s.Resolve("wraptest").Active; got != "b.example" {
		t.Fatalf("active after 1st rotation = %q, want b.example", got)
	}
	now = now.Add(RotateCooldown + time.Second)
	s.ReportFailure(context.Background(), "wraptest") // b -> a (wraps)
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

	s.ReportFailure(context.Background(), "customringtest") // b -> custom (known + custom order)
	if got := s.Resolve("customringtest").Active; got != "custom.example" {
		t.Errorf("active = %q, want custom.example", got)
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
