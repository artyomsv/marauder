package repo

import (
	"context"
	"testing"

	pgxmock "github.com/pashagolub/pgxmock/v5"
)

// The singleton settings row (id = 1) that seeds new topics. Two statements,
// pinned so a rework cannot silently start writing per-user rows.
func TestSettings_Get_ReadsTheSingleton(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()
	r := &Settings{pool: mock}

	mock.ExpectQuery(`SELECT follow_tracker_title_default FROM settings WHERE id = 1`).
		WillReturnRows(pgxmock.NewRows([]string{"follow_tracker_title_default"}).AddRow(true))

	got, err := r.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.DefaultFollowTrackerTitle {
		t.Error("DefaultFollowTrackerTitle = false, want true")
	}
}

func TestSettings_SetDefaultFollowTrackerTitle_WritesTheSingleton(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()
	r := &Settings{pool: mock}

	mock.ExpectExec(`UPDATE settings SET follow_tracker_title_default = \$1, updated_at = now\(\) WHERE id = 1`).
		WithArgs(true).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := r.SetDefaultFollowTrackerTitle(context.Background(), true); err != nil {
		t.Fatalf("SetDefaultFollowTrackerTitle: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
