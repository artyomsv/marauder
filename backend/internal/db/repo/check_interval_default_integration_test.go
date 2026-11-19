//go:build integration

package repo

import (
	"context"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// Issue #250: the migration seeds the historical 900 s, a patch of only the
// interval leaves the other defaults alone, the CHECK constraint refuses an
// out-of-range value, and no existing topic changes.
func TestCheckIntervalDefaultMigrationAndSettingsPatch(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	r := NewSettings(pool)
	before, err := r.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := r.Update(ctx, SettingsPatch{DefaultCheckIntervalSec: &before.DefaultCheckIntervalSec}); err != nil {
			t.Errorf("restore settings: %v", err)
		}
	})
	if before.DefaultCheckIntervalSec != 900 {
		t.Fatalf("migration default = %d, want 900", before.DefaultCheckIntervalSec)
	}
	existing := seedTopic(t, pool, seedUser(t, pool), domain.TopicStatusActive, nil)

	hour := 3600
	got, err := r.Update(ctx, SettingsPatch{DefaultCheckIntervalSec: &hour})
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultCheckIntervalSec != hour {
		t.Errorf("interval = %d, want %d", got.DefaultCheckIntervalSec, hour)
	}
	if got.DefaultFollowTrackerTitle != before.DefaultFollowTrackerTitle ||
		got.DefaultReplaceOnUpdate != before.DefaultReplaceOnUpdate ||
		got.DefaultReplaceDeleteData != before.DefaultReplaceDeleteData {
		t.Errorf("interval patch changed other defaults: %+v, was %+v", got, before)
	}

	for _, bad := range []int{299, 604_801} {
		if _, err := r.Update(ctx, SettingsPatch{DefaultCheckIntervalSec: &bad}); err == nil {
			t.Errorf("interval %d accepted, want the CHECK constraint to refuse it", bad)
		}
	}

	if after := reload(t, pool, existing.ID); after.CheckIntervalSec != existing.CheckIntervalSec {
		t.Errorf("existing topic interval = %d, want %d", after.CheckIntervalSec, existing.CheckIntervalSec)
	}
}
