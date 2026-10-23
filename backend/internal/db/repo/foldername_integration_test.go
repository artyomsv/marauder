//go:build integration

package repo

import (
	"context"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// Folder name settings (issue #225) through the real migrations.
func TestTopicsFolderNameRoundTrip(t *testing.T) {
	pool := integrationPool(t)
	topicsRepo := NewTopics(pool)
	userID := seedUser(t, pool)
	ctx := context.Background()

	topic := seedTopic(t, pool, userID, domain.TopicStatusActive, map[string]any{})
	if topic.FolderNameMode != domain.FolderNameDefault || topic.FolderName != "" || topic.FolderNameSeen != "" {
		t.Fatalf("defaults = (%q, %q, %q), want (default, \"\", \"\")", topic.FolderNameMode, topic.FolderName, topic.FolderNameSeen)
	}

	update := func(p *FolderNamePolicy) *domain.Topic {
		t.Helper()
		if _, err := topicsRepo.Update(ctx, topic.ID, userID, topic.DisplayName, nil, nil, "", "", nil, TopicFlags{}, p, map[string]any{}); err != nil {
			t.Fatalf("Update(%+v): %v", p, err)
		}
		return reload(t, pool, topic.ID)
	}

	got := update(&FolderNamePolicy{Mode: domain.FolderNameCustom, Name: "My Show"})
	if got.FolderNameMode != domain.FolderNameCustom || got.FolderName != "My Show" {
		t.Errorf("after custom = (%q, %q), want (custom, My Show)", got.FolderNameMode, got.FolderName)
	}

	// A writer that is not changing the setting (an edit without the fields,
	// the Sonarr poller) passes nil and must leave it alone.
	got = update(nil)
	if got.FolderNameMode != domain.FolderNameCustom || got.FolderName != "My Show" {
		t.Errorf("after nil policy = (%q, %q), want unchanged (custom, My Show)", got.FolderNameMode, got.FolderName)
	}

	// A name is stored only in custom mode.
	got = update(&FolderNamePolicy{Mode: domain.FolderNameKeep, Name: "ignored"})
	if got.FolderNameMode != domain.FolderNameKeep || got.FolderName != "" {
		t.Errorf("after keep = (%q, %q), want (keep, \"\")", got.FolderNameMode, got.FolderName)
	}

	if err := topicsRepo.SetFolderNameSeen(ctx, topic.ID, "Renamed"); err != nil {
		t.Fatalf("SetFolderNameSeen: %v", err)
	}
	if got = reload(t, pool, topic.ID); got.FolderNameSeen != "Renamed" {
		t.Errorf("FolderNameSeen = %q, want Renamed", got.FolderNameSeen)
	}
	// Re-saving the same mode keeps the memory.
	if got = update(&FolderNamePolicy{Mode: domain.FolderNameKeep}); got.FolderNameSeen != "Renamed" {
		t.Errorf("same-mode update cleared FolderNameSeen to %q", got.FolderNameSeen)
	}
	// Leaving keep mode forgets it, so a later keep does not reuse a stale name.
	if got = update(&FolderNamePolicy{Mode: domain.FolderNameDefault}); got.FolderNameSeen != "" {
		t.Errorf("mode change left FolderNameSeen = %q, want empty", got.FolderNameSeen)
	}

	update(&FolderNamePolicy{Mode: domain.FolderNameKeep})
	if err := topicsRepo.SetFolderNameSeen(ctx, topic.ID, "Renamed again"); err != nil {
		t.Fatalf("SetFolderNameSeen: %v", err)
	}
	if err := topicsRepo.ResetCheckState(ctx, topic.ID, userID); err != nil {
		t.Fatalf("ResetCheckState: %v", err)
	}
	if got = reload(t, pool, topic.ID); got.FolderNameSeen != "" || got.FolderNameMode != domain.FolderNameKeep {
		t.Errorf("after reset = (%q, seen %q), want (keep, seen empty)", got.FolderNameMode, got.FolderNameSeen)
	}
}

func TestDeliveriesLatestOther(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	userID := seedUser(t, pool)
	topic := seedTopic(t, pool, userID, domain.TopicStatusActive, map[string]any{})
	d := NewDeliveries(pool)

	if got, err := d.LatestOther(ctx, topic.ID, "zzzz"); err != nil || got != nil {
		t.Fatalf("LatestOther without deliveries = (%+v, %v), want (nil, nil)", got, err)
	}
	for _, rec := range []struct{ hash, age string }{{"aaaa", "2 hours"}, {"bbbb", "1 hour"}} {
		if _, err := d.Record(ctx, &domain.TopicDelivery{TopicID: topic.ID, Infohash: rec.hash, Label: rec.hash}); err != nil {
			t.Fatalf("Record %s: %v", rec.hash, err)
		}
		if _, err := pool.Exec(ctx,
			`UPDATE topic_deliveries SET delivered_at = now() - $2::interval WHERE topic_id = $1 AND infohash = $3`,
			topic.ID, rec.age, rec.hash); err != nil {
			t.Fatalf("age %s: %v", rec.hash, err)
		}
	}
	latest := func(exclude string) string {
		t.Helper()
		got, err := d.LatestOther(ctx, topic.ID, exclude)
		if err != nil || got == nil {
			t.Fatalf("LatestOther(exclude %s) = (%+v, %v)", exclude, got, err)
		}
		return got.Infohash
	}
	if got := latest("zzzz"); got != "bbbb" {
		t.Errorf("LatestOther = %s, want the newest, bbbb", got)
	}
	// A retry finds its own row already recorded and must pass over it.
	if got := latest("bbbb"); got != "aaaa" {
		t.Errorf("LatestOther(exclude bbbb) = %s, want aaaa", got)
	}
}
