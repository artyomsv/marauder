package registry

import (
	"context"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

type fakeTracker struct {
	name string
}

func (f *fakeTracker) Name() string        { return f.name }
func (f *fakeTracker) DisplayName() string { return f.name }
func (f *fakeTracker) CanParse(raw string) bool {
	return raw == "fake://"+f.name
}
func (f *fakeTracker) Parse(_ context.Context, raw string) (*domain.Topic, error) {
	return &domain.Topic{TrackerName: f.name, URL: raw, DisplayName: f.name}, nil
}
func (f *fakeTracker) Check(_ context.Context, _ *domain.Topic, _ *domain.TrackerCredential) (*domain.Check, error) {
	return &domain.Check{Hash: "stable"}, nil
}
func (f *fakeTracker) Download(_ context.Context, _ *domain.Topic, _ *domain.Check, _ *domain.TrackerCredential) (*domain.Payload, error) {
	return &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:abc"}, nil
}

func TestRegisterAndList(t *testing.T) {
	Reset()
	defer Reset()

	RegisterTracker(&fakeTracker{name: "alpha"})
	RegisterTracker(&fakeTracker{name: "beta"})

	list := ListTrackers()
	if len(list) != 2 {
		t.Fatalf("want 2 trackers, got %d", len(list))
	}
	// Sorted alphabetically
	if list[0].Name() != "alpha" || list[1].Name() != "beta" {
		t.Fatalf("not sorted: %v, %v", list[0].Name(), list[1].Name())
	}
}

func TestFindTrackerForURL(t *testing.T) {
	Reset()
	defer Reset()

	RegisterTracker(&fakeTracker{name: "alpha"})
	RegisterTracker(&fakeTracker{name: "beta"})

	if got := FindTrackerForURL("fake://alpha"); got == nil || got.Name() != "alpha" {
		t.Fatalf("want alpha, got %v", got)
	}
	if got := FindTrackerForURL("fake://unknown"); got != nil {
		t.Fatalf("want nil, got %v", got)
	}
}

func TestDuplicateRegistrationPanics(t *testing.T) {
	Reset()
	defer Reset()

	RegisterTracker(&fakeTracker{name: "dup"})
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate tracker name")
		}
	}()
	RegisterTracker(&fakeTracker{name: "dup"})
}

func TestGetTrackerNotFound(t *testing.T) {
	Reset()
	defer Reset()
	if got := GetTracker("nonexistent"); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}
