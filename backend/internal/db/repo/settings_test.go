package repo

import (
	"context"
	"regexp"
	"testing"

	pgxmock "github.com/pashagolub/pgxmock/v5"
)

var settingsColumns = []string{"follow_tracker_title_default", "replace_on_update_default", "replace_delete_data_default"}

func TestSettings_Get_ReadsTheSingleton(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()
	r := &Settings{pool: mock}
	mock.ExpectQuery("SELECT follow_tracker_title_default, replace_on_update_default, replace_delete_data_default FROM settings WHERE id = 1").
		WillReturnRows(pgxmock.NewRows(settingsColumns).AddRow(true, true, false))
	got, err := r.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if *got != (InstanceSettings{DefaultFollowTrackerTitle: true, DefaultReplaceOnUpdate: true}) {
		t.Errorf("Get = %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestSettings_Update_PatchesAndReturnsStoredRow(t *testing.T) {
	on, off := true, false
	for _, patch := range []SettingsPatch{
		{DefaultFollowTrackerTitle: &off},
		{DefaultReplaceOnUpdate: &on},
		{DefaultReplaceDeleteData: &off},
		{DefaultFollowTrackerTitle: &off, DefaultReplaceOnUpdate: &on, DefaultReplaceDeleteData: &off},
	} {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatalf("pgxmock.NewPool: %v", err)
		}
		t.Cleanup(mock.Close)
		r := &Settings{pool: mock}
		mock.ExpectQuery(regexp.QuoteMeta("UPDATE settings SET "+
			"follow_tracker_title_default = COALESCE($1, follow_tracker_title_default), "+
			"replace_on_update_default = COALESCE($2, replace_on_update_default), "+
			"replace_delete_data_default = COALESCE($3, replace_delete_data_default), "+
			"updated_at = now() WHERE id = 1 RETURNING follow_tracker_title_default, replace_on_update_default, replace_delete_data_default")).
			WithArgs(patch.DefaultFollowTrackerTitle, patch.DefaultReplaceOnUpdate, patch.DefaultReplaceDeleteData).
			WillReturnRows(pgxmock.NewRows(settingsColumns).AddRow(false, true, false))
		got, err := r.Update(context.Background(), patch)
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if *got != (InstanceSettings{DefaultReplaceOnUpdate: true}) {
			t.Errorf("Update = %+v, want the returned row", got)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	}
}
