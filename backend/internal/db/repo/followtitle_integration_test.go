//go:build integration

package repo

import (
	"context"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// Follow-the-tracker-title (the setting floated in issue #86 and scoped out
// of the #90 self-heal gate) through the real migrations. The unit tests pin
// the UPDATE text; these execute the CASE that clears the flag on a rename.
func TestTopicsFollowTrackerTitle(t *testing.T) {
	pool := integrationPool(t)
	topicsRepo := NewTopics(pool)
	userID := seedUser(t, pool)
	ctx := context.Background()

	topic := seedTopic(t, pool, userID, domain.TopicStatusActive, map[string]any{})
	if topic.FollowTrackerTitle {
		t.Fatal("default FollowTrackerTitle = true, want false")
	}

	update := func(name string, flags TopicFlags) *domain.Topic {
		t.Helper()
		if _, err := topicsRepo.Update(ctx, topic.ID, userID, name, nil, nil, "", "", nil, flags, nil, map[string]any{}); err != nil {
			t.Fatalf("Update(%q, %+v): %v", name, flags, err)
		}
		return reload(t, pool, topic.ID)
	}

	// An unchanged name writes the requested flag.
	got := update(topic.DisplayName, TopicFlags{FollowTrackerTitle: true})
	if !got.FollowTrackerTitle {
		t.Error("same-name update with the flag on left it off")
	}
	got = update(topic.DisplayName, TopicFlags{FollowTrackerTitle: false})
	if got.FollowTrackerTitle {
		t.Error("same-name update with the flag off left it on")
	}

	// The rule this feature turns on: a submitted name change takes
	// ownership of the name and turns the flag off, whatever the caller
	// asked for. Mirrors display_name_is_placeholder's clear-on-rename.
	got = update("Renamed By Hand", TopicFlags{FollowTrackerTitle: true})
	if got.FollowTrackerTitle {
		t.Error("a rename left FollowTrackerTitle on; typing a name must clear it")
	}
	if got.DisplayName != "Renamed By Hand" {
		t.Errorf("DisplayName = %q, want Renamed By Hand", got.DisplayName)
	}

	// The scheduler's own title write (UpdateDisplayName) is NOT a user
	// rename: following must survive the updates it triggers.
	got = update("Renamed By Hand", TopicFlags{FollowTrackerTitle: true})
	if !got.FollowTrackerTitle {
		t.Fatal("same-name update with the flag on left it off")
	}
	written, err := topicsRepo.UpdateDisplayName(ctx, topic.ID, "Tracker Title v2")
	if err != nil {
		t.Fatalf("UpdateDisplayName: %v", err)
	}
	if !written {
		t.Error("UpdateDisplayName reported no write while the topic follows")
	}
	got = reload(t, pool, topic.ID)
	if !got.FollowTrackerTitle {
		t.Error("UpdateDisplayName cleared FollowTrackerTitle; only a user rename may clear it")
	}
	if got.DisplayName != "Tracker Title v2" {
		t.Errorf("DisplayName = %q, want Tracker Title v2", got.DisplayName)
	}

	// The race the WHERE clause of UpdateDisplayName exists for: a user rename
	// lands while a check is running. The check's snapshot still says the topic
	// may follow, but the rename has already locked the name — the in-flight
	// tracker title must not overwrite it (issue #90 holds inside a running
	// check too).
	got = update("Typed Mid Check", TopicFlags{FollowTrackerTitle: true})
	if got.FollowTrackerTitle {
		t.Fatal("a rename must clear FollowTrackerTitle")
	}
	written, err = topicsRepo.UpdateDisplayName(ctx, topic.ID, "Tracker Title v3")
	if err != nil {
		t.Fatalf("UpdateDisplayName: %v", err)
	}
	if written {
		t.Error("UpdateDisplayName reported a write to a locked name")
	}
	got = reload(t, pool, topic.ID)
	if got.DisplayName != "Typed Mid Check" {
		t.Errorf("DisplayName = %q, want Typed Mid Check — an in-flight check overwrote a user rename", got.DisplayName)
	}
}

// The instance-wide default new topics are seeded with when their creator
// sends no explicit choice.
func TestSettingsFollowTrackerTitleDefault(t *testing.T) {
	pool := integrationPool(t)
	settingsRepo := NewSettings(pool)
	ctx := context.Background()

	// The singleton is shared state: leave it as it was found so test order
	// never matters.
	before, err := settingsRepo.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	t.Cleanup(func() {
		if err := settingsRepo.SetDefaultFollowTrackerTitle(ctx, before.DefaultFollowTrackerTitle); err != nil {
			t.Errorf("restore default: %v", err)
		}
	})

	want := !before.DefaultFollowTrackerTitle
	if err := settingsRepo.SetDefaultFollowTrackerTitle(ctx, want); err != nil {
		t.Fatalf("SetDefaultFollowTrackerTitle: %v", err)
	}
	got, err := settingsRepo.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DefaultFollowTrackerTitle != want {
		t.Errorf("default = %v, want %v", got.DefaultFollowTrackerTitle, want)
	}
}
