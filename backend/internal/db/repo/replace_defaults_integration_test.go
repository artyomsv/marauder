//go:build integration

package repo

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

func TestReplaceDefaultsMigrationAndSettingsPatch(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	r := NewSettings(pool)
	before, err := r.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := r.Update(ctx, SettingsPatch{
			DefaultFollowTrackerTitle: &before.DefaultFollowTrackerTitle,
			DefaultReplaceOnUpdate:    &before.DefaultReplaceOnUpdate,
			DefaultReplaceDeleteData:  &before.DefaultReplaceDeleteData,
		}); err != nil {
			t.Errorf("restore settings: %v", err)
		}
	})
	if before.DefaultReplaceOnUpdate || !before.DefaultReplaceDeleteData {
		t.Fatalf("migration defaults = %+v, want replace=false/delete=true", before)
	}
	userID := seedUser(t, pool)
	existing := seedTopic(t, pool, userID, domain.TopicStatusActive, nil)
	on, off := true, false
	if _, err := r.Update(ctx, SettingsPatch{DefaultReplaceOnUpdate: &on}); err != nil {
		t.Fatal(err)
	}
	got, err := r.Update(ctx, SettingsPatch{DefaultReplaceDeleteData: &off})
	if err != nil {
		t.Fatal(err)
	}
	if !got.DefaultReplaceOnUpdate || got.DefaultReplaceDeleteData || got.DefaultFollowTrackerTitle != before.DefaultFollowTrackerTitle {
		t.Errorf("partial updates = %+v", got)
	}
	after := reload(t, pool, existing.ID)
	if after.ReplaceOnUpdate != existing.ReplaceOnUpdate || after.ReplaceDeleteData != existing.ReplaceDeleteData {
		t.Error("settings update changed an existing topic")
	}
}

func TestSonarrReplaceDefaultsMigrationAndNullUpdate(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	owner := seedUser(t, pool)
	r := NewSonarrInstances(pool)
	master := testMasterKey(t)
	// Omit the new columns in SQL to verify the migration's NULL defaults.
	var id uuid.UUID
	err := pool.QueryRow(ctx, `INSERT INTO sonarr_instances (name, url, owner_user_id) VALUES ('TEST-defaults', 'http://sonarr.invalid:8989', $1) RETURNING id`, owner).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM sonarr_instances WHERE id = $1`, id); err != nil {
			t.Errorf("cleanup instance: %v", err)
		}
	})
	inst, err := r.Get(ctx, master, id)
	if err != nil {
		t.Fatal(err)
	}
	if inst.DefaultReplaceOnUpdate != nil || inst.DefaultReplaceDeleteData != nil {
		t.Fatal("new instance does not inherit")
	}
	on, off := true, false
	inst.DefaultReplaceOnUpdate, inst.DefaultReplaceDeleteData = &on, &off
	got, err := r.Update(ctx, master, inst.ID, *inst)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultReplaceOnUpdate == nil || !*got.DefaultReplaceOnUpdate || got.DefaultReplaceDeleteData == nil || *got.DefaultReplaceDeleteData {
		t.Fatalf("overrides did not round-trip: %+v", got)
	}
	inst.DefaultReplaceOnUpdate, inst.DefaultReplaceDeleteData = nil, nil
	if _, err := r.Update(ctx, master, inst.ID, *inst); err != nil {
		t.Fatal(err)
	}
	got, err = r.Get(ctx, master, inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultReplaceOnUpdate != nil || got.DefaultReplaceDeleteData != nil {
		t.Fatal("nil update did not restore NULL")
	}
}
