package repo

import (
	"context"
	"testing"

	"github.com/pashagolub/pgxmock/v3"
)

func newMockTrackerSettings(t *testing.T) (*TrackerSettings, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	t.Cleanup(func() { mock.Close() })
	return &TrackerSettings{pool: mock}, mock
}

func TestTrackerSettings_List_ScansRows(t *testing.T) {
	repo, mock := newMockTrackerSettings(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	rows := pgxmock.NewRows([]string{"tracker_name", "active_domain", "custom_domains"}).
		AddRow("kinozal", "kinozal.me", []byte(`["kinozal.example"]`)).
		AddRow("rutracker", nil, []byte(`[]`))
	mock.ExpectQuery(`SELECT tracker_name, COALESCE\(active_domain,''\), custom_domains FROM tracker_settings`).
		WillReturnRows(rows)

	got, err := repo.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 || got[0].ActiveDomain != "kinozal.me" || got[0].CustomDomains[0] != "kinozal.example" {
		t.Errorf("unexpected result: %+v", got)
	}
	if got[1].ActiveDomain != "" || len(got[1].CustomDomains) != 0 {
		t.Errorf("nil active/empty custom not normalised: %+v", got[1])
	}
}

func TestTrackerSettings_Upsert_ExecutesOnConflict(t *testing.T) {
	repo, mock := newMockTrackerSettings(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	mock.ExpectExec(`INSERT INTO tracker_settings .* ON CONFLICT \(tracker_name\) DO UPDATE`).
		WithArgs("kinozal", "kinozal.me", []byte(`["kinozal.example"]`)).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := repo.Upsert(context.Background(), "kinozal", "kinozal.me", []string{"kinozal.example"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
}
