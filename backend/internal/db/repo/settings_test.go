package repo

import (
	"context"
	"regexp"
	"testing"

	pgxmock "github.com/pashagolub/pgxmock/v5"
)

var settingsColumnNames = []string{"follow_tracker_title_default", "replace_on_update_default", "replace_delete_data_default", "check_interval_sec_default"}

func TestSettings_Get_ReadsTheSingleton(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()
	r := &Settings{pool: mock}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT follow_tracker_title_default, replace_on_update_default, replace_delete_data_default, check_interval_sec_default FROM settings WHERE id = 1")).
		WillReturnRows(pgxmock.NewRows(settingsColumnNames).AddRow(true, true, false, 3600))
	got, err := r.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if *got != (InstanceSettings{DefaultFollowTrackerTitle: true, DefaultReplaceOnUpdate: true, DefaultCheckIntervalSec: 3600}) {
		t.Errorf("Get = %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestSettings_Update_PatchesAndReturnsStoredRow(t *testing.T) {
	on, off := true, false
	hour := 3600
	for _, patch := range []SettingsPatch{
		{DefaultFollowTrackerTitle: &off},
		{DefaultReplaceOnUpdate: &on},
		{DefaultReplaceDeleteData: &off},
		{DefaultCheckIntervalSec: &hour},
		{DefaultFollowTrackerTitle: &off, DefaultReplaceOnUpdate: &on, DefaultReplaceDeleteData: &off, DefaultCheckIntervalSec: &hour},
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
			"check_interval_sec_default = COALESCE($4, check_interval_sec_default), "+
			"updated_at = now() WHERE id = 1 RETURNING follow_tracker_title_default, replace_on_update_default, replace_delete_data_default, check_interval_sec_default")).
			WithArgs(patch.DefaultFollowTrackerTitle, patch.DefaultReplaceOnUpdate, patch.DefaultReplaceDeleteData, patch.DefaultCheckIntervalSec).
			WillReturnRows(pgxmock.NewRows(settingsColumnNames).AddRow(false, true, false, 3600))
		got, err := r.Update(context.Background(), patch)
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if *got != (InstanceSettings{DefaultReplaceOnUpdate: true, DefaultCheckIntervalSec: 3600}) {
			t.Errorf("Update = %+v, want the returned row", got)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	}
}
