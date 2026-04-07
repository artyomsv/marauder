package repo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v3"
)

// jsonMarshalForTest is a tiny indirection so the stdlib contract test
// reads as documentation rather than calling encoding/json directly.
func jsonMarshalForTest(m map[string]any) ([]byte, error) {
	return json.Marshal(m)
}

// newMockTopics wires a Topics repo around a pgxmock pool. The Topics
// struct holds the pool through the unexported topicsPool interface, so
// we can substitute pgxmock directly since it satisfies that interface.
func newMockTopics(t *testing.T) (*Topics, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	t.Cleanup(func() {
		mock.Close()
	})
	return &Topics{pool: mock}, mock
}

// assertExpectationsMet fails the test if any expected DB call was not
// consumed. Called via t.Cleanup to ensure it runs regardless of path.
func assertExpectationsMet(t *testing.T, mock pgxmock.PgxPoolIface) {
	t.Helper()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// ---------- UpdateExtra ----------

func TestTopics_UpdateExtra_HappyPath(t *testing.T) {
	repo, mock := newMockTopics(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	id := uuid.New()
	extra := map[string]any{"quality": "1080p", "downloaded_episodes": []string{"S01E01"}}

	// The method marshals extra to JSON and passes it as $2 (as []byte).
	// We use a regex-style match on the SQL and pgxmock.AnyArg for the
	// marshalled JSON since map iteration order is not deterministic.
	mock.ExpectExec(`UPDATE topics SET extra = \$2, updated_at = now\(\) WHERE id = \$1`).
		WithArgs(id, pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := repo.UpdateExtra(context.Background(), id, extra); err != nil {
		t.Fatalf("UpdateExtra: unexpected error: %v", err)
	}
}

func TestTopics_UpdateExtra_NotFound(t *testing.T) {
	repo, mock := newMockTopics(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	id := uuid.New()
	mock.ExpectExec(`UPDATE topics SET extra`).
		WithArgs(id, pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	err := repo.UpdateExtra(context.Background(), id, map[string]any{"k": "v"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateExtra: want ErrNotFound, got %v", err)
	}
}

func TestTopics_UpdateExtra_DBError(t *testing.T) {
	repo, mock := newMockTopics(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	id := uuid.New()
	dbErr := errors.New("connection refused")
	mock.ExpectExec(`UPDATE topics SET extra`).
		WithArgs(id, pgxmock.AnyArg()).
		WillReturnError(dbErr)

	err := repo.UpdateExtra(context.Background(), id, map[string]any{"k": "v"})
	if err == nil {
		t.Fatalf("UpdateExtra: want error, got nil")
	}
	if !errors.Is(err, dbErr) {
		t.Fatalf("UpdateExtra: want wrapped %v, got %v", dbErr, err)
	}
	if !strings.Contains(err.Error(), "topics: update extra") {
		t.Errorf("UpdateExtra: error should include wrap context, got %q", err.Error())
	}
}

// TestTopics_UpdateExtra_NilMap verifies that a nil map still produces
// a single UPDATE and does not short-circuit with an error. The exact
// serialized payload is not pgxmock-asserted (pgxmock v3 does not
// expose captured arguments), so we cross-check the serialization
// contract separately in TestTopics_UpdateExtra_NilMap_Serialization.
func TestTopics_UpdateExtra_NilMap(t *testing.T) {
	repo, mock := newMockTopics(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	id := uuid.New()
	mock.ExpectExec(`UPDATE topics SET extra = \$2, updated_at = now\(\) WHERE id = \$1`).
		WithArgs(id, pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := repo.UpdateExtra(context.Background(), id, nil); err != nil {
		t.Fatalf("UpdateExtra(nil): unexpected error: %v", err)
	}
}

// TestTopics_UpdateExtra_NilMap_Serialization documents the marshalling
// contract that UpdateExtra relies on: encoding/json turns a nil map
// into the JSON literal "null" (4 bytes, non-empty), so the empty-raw
// fallback to `{}` is only taken when Marshal itself returns an empty
// slice — which it does not. This guards against a future Go stdlib
// change silently altering the behaviour.
func TestTopics_UpdateExtra_NilMap_Serialization(t *testing.T) {
	raw, err := jsonMarshalForTest(nil)
	if err != nil {
		t.Fatalf("marshal nil map: %v", err)
	}
	if string(raw) != "null" {
		t.Errorf("encoding/json contract changed: marshal(nil map) = %q, want %q", raw, "null")
	}
}

// ---------- MarkEpisodeDownloaded ----------

func TestTopics_MarkEpisodeDownloaded_HappyPath(t *testing.T) {
	repo, mock := newMockTopics(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	id := uuid.New()
	packed := "S01E05"

	// Regex-match the jsonb_set expression. Escape parens/dollar signs.
	mock.ExpectExec(`UPDATE topics\s+SET\s+extra = jsonb_set\(`).
		WithArgs(id, packed).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := repo.MarkEpisodeDownloaded(context.Background(), id, packed); err != nil {
		t.Fatalf("MarkEpisodeDownloaded: unexpected error: %v", err)
	}
}

func TestTopics_MarkEpisodeDownloaded_NotFound(t *testing.T) {
	repo, mock := newMockTopics(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	id := uuid.New()
	mock.ExpectExec(`UPDATE topics\s+SET\s+extra = jsonb_set`).
		WithArgs(id, "S02E03").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	err := repo.MarkEpisodeDownloaded(context.Background(), id, "S02E03")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("MarkEpisodeDownloaded: want ErrNotFound, got %v", err)
	}
}

func TestTopics_MarkEpisodeDownloaded_DBError(t *testing.T) {
	repo, mock := newMockTopics(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	id := uuid.New()
	dbErr := errors.New("deadlock detected")
	mock.ExpectExec(`UPDATE topics\s+SET\s+extra = jsonb_set`).
		WithArgs(id, "S03E01").
		WillReturnError(dbErr)

	err := repo.MarkEpisodeDownloaded(context.Background(), id, "S03E01")
	if err == nil {
		t.Fatalf("MarkEpisodeDownloaded: want error, got nil")
	}
	if !errors.Is(err, dbErr) {
		t.Fatalf("MarkEpisodeDownloaded: want wrapped %v, got %v", dbErr, err)
	}
	if !strings.Contains(err.Error(), "topics: mark episode downloaded") {
		t.Errorf("MarkEpisodeDownloaded: missing wrap context: %q", err.Error())
	}
}

// ---------- scanTopic malformed extra ----------

// TestTopics_ScanTopic_MalformedExtra drives GetByID through a mocked
// pool that returns a row whose extra column holds invalid JSON. Before
// the fix, this silently produced an empty Extra map; after the fix, it
// must surface a scan error.
func TestTopics_ScanTopic_MalformedExtra(t *testing.T) {
	repo, mock := newMockTopics(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	id := uuid.New()
	userID := uuid.New()
	now := time.Now().UTC()

	// Build a row that matches topicColumns exactly (18 columns).
	rows := pgxmock.NewRows([]string{
		"id", "user_id", "tracker_name", "url", "display_name", "client_id",
		"download_dir", "extra", "last_hash",
		"last_checked_at", "last_updated_at", "next_check_at",
		"check_interval_sec", "consecutive_errors", "status",
		"last_error", "created_at", "updated_at",
	}).AddRow(
		id, userID, "faketracker", "https://example.invalid/t/1",
		"My Topic", (*uuid.UUID)(nil),
		"", []byte("{not valid json"), "",
		(*time.Time)(nil), (*time.Time)(nil), now,
		3600, 0, "active",
		"", now, now,
	)

	mock.ExpectQuery(`SELECT .* FROM topics WHERE id = \$1`).
		WithArgs(id).
		WillReturnRows(rows)

	got, err := repo.GetByID(context.Background(), id, nil)
	if err == nil {
		t.Fatalf("GetByID: expected scan error from malformed extra, got topic=%+v", got)
	}
	if !strings.Contains(err.Error(), "scan extra blob") {
		t.Errorf("GetByID: error should mention scan extra blob, got %q", err.Error())
	}
}

// TestTopics_ScanTopic_ValidExtra sanity-checks the happy path so we
// know the malformed-extra test is exercising the error branch and not
// some unrelated scan failure.
func TestTopics_ScanTopic_ValidExtra(t *testing.T) {
	repo, mock := newMockTopics(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	id := uuid.New()
	userID := uuid.New()
	now := time.Now().UTC()

	rows := pgxmock.NewRows([]string{
		"id", "user_id", "tracker_name", "url", "display_name", "client_id",
		"download_dir", "extra", "last_hash",
		"last_checked_at", "last_updated_at", "next_check_at",
		"check_interval_sec", "consecutive_errors", "status",
		"last_error", "created_at", "updated_at",
	}).AddRow(
		id, userID, "faketracker", "https://example.invalid/t/1",
		"My Topic", (*uuid.UUID)(nil),
		"", []byte(`{"quality":"1080p"}`), "",
		(*time.Time)(nil), (*time.Time)(nil), now,
		3600, 0, "active",
		"", now, now,
	)

	mock.ExpectQuery(`SELECT .* FROM topics WHERE id = \$1`).
		WithArgs(id).
		WillReturnRows(rows)

	got, err := repo.GetByID(context.Background(), id, nil)
	if err != nil {
		t.Fatalf("GetByID: unexpected error: %v", err)
	}
	if got.Extra["quality"] != "1080p" {
		t.Errorf("GetByID: want Extra[quality]=1080p, got %v", got.Extra["quality"])
	}
}
