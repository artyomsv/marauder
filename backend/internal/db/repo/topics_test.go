package repo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v3"

	"github.com/artyomsv/marauder/backend/internal/domain"
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

// topicRow returns a pgxmock row slice that matches topicColumns exactly
// (19 columns as of migration 0004). Callers override individual fields as
// needed. The helper centralises column-order so tests don't drift.
func topicRow(id, userID uuid.UUID, now time.Time) []any {
	return []any{
		id, userID, "faketracker", "https://example.invalid/t/1",
		"My Topic", (*uuid.UUID)(nil),
		"",                            // download_dir
		"",                            // category
		[]byte(`{"quality":"1080p"}`), // extra
		"",                            // last_hash
		(*time.Time)(nil), (*time.Time)(nil), now,
		3600, 0, "active",
		"", now, now,
	}
}

// topicColumns19 mirrors the header slice for pgxmock.NewRows (19 cols).
var topicColumns19 = []string{
	"id", "user_id", "tracker_name", "url", "display_name", "client_id",
	"download_dir", "category", "extra", "last_hash",
	"last_checked_at", "last_updated_at", "next_check_at",
	"check_interval_sec", "consecutive_errors", "status",
	"last_error", "created_at", "updated_at",
}

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

	// Build a row that matches topicColumns exactly (19 columns).
	rows := pgxmock.NewRows(topicColumns19).AddRow(
		id, userID, "faketracker", "https://example.invalid/t/1",
		"My Topic", (*uuid.UUID)(nil),
		"", "",
		[]byte("{not valid json"), "",
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

	rows := pgxmock.NewRows(topicColumns19).AddRow(topicRow(id, userID, now)...)

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

// ---------- DueForCheck ----------

// TestTopics_DueForCheck_IncludesErrorStatus verifies that the query
// uses status IN ('active', 'error') so errored topics are retried on
// their backoff schedule.
func TestTopics_DueForCheck_IncludesErrorStatus(t *testing.T) {
	repo, mock := newMockTopics(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	id := uuid.New()
	userID := uuid.New()
	now := time.Now().UTC()

	rows := pgxmock.NewRows(topicColumns19).AddRow(topicRow(id, userID, now)...)

	// The regex asserts the IN clause is present.
	mock.ExpectQuery(`status IN \('active', 'error'\)`).
		WithArgs(10).
		WillReturnRows(rows)

	topics, err := repo.DueForCheck(context.Background(), 10)
	if err != nil {
		t.Fatalf("DueForCheck: unexpected error: %v", err)
	}
	if len(topics) != 1 {
		t.Fatalf("DueForCheck: want 1 topic, got %d", len(topics))
	}
	if topics[0].ID != id {
		t.Errorf("DueForCheck: want id=%s, got %s", id, topics[0].ID)
	}
}

// ---------- Create ----------

// TestTopics_Create_RoundTripsCategory verifies that Create includes
// the category column in the INSERT and that scanTopic reads it back.
func TestTopics_Create_RoundTripsCategory(t *testing.T) {
	repo, mock := newMockTopics(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	id := uuid.New()
	userID := uuid.New()
	now := time.Now().UTC()

	// Build the RETURNING row with category="movies".
	row := topicRow(id, userID, now)
	// column index 7 is category (0-based: id,user_id,tracker_name,url,display_name,client_id,download_dir,category,...)
	row[7] = "movies"

	rows := pgxmock.NewRows(topicColumns19).AddRow(row...)

	// Match INSERT containing the category column.
	mock.ExpectQuery(`INSERT INTO topics.*category.*RETURNING`).
		WithArgs(
			userID, "faketracker", "https://example.invalid/t/1",
			"My Topic", (*uuid.UUID)(nil),
			"",               // download_dir
			"movies",         // category
			pgxmock.AnyArg(), // extra (JSON)
			3600, pgxmock.AnyArg(), "active",
		).
		WillReturnRows(rows)

	t.Logf("now=%v", now)

	in := &domain.Topic{
		UserID:           userID,
		TrackerName:      "faketracker",
		URL:              "https://example.invalid/t/1",
		DisplayName:      "My Topic",
		ClientID:         nil,
		DownloadDir:      "",
		Category:         "movies",
		Extra:            map[string]any{"quality": "1080p"},
		CheckIntervalSec: 3600,
		NextCheckAt:      now,
		Status:           domain.TopicStatusActive,
	}

	got, err := repo.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}
	if got.Category != "movies" {
		t.Errorf("Create: want Category=%q, got %q", "movies", got.Category)
	}
}

// ---------- Update ----------

// TestTopics_Update_HappyPath verifies that Update issues the expected
// UPDATE … RETURNING query and returns the scanned topic.
func TestTopics_Update_HappyPath(t *testing.T) {
	r, mock := newMockTopics(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	id := uuid.New()
	userID := uuid.New()
	now := time.Now().UTC()

	// Build the RETURNING row reflecting the updated values.
	row := topicRow(id, userID, now)
	row[4] = "Updated Name"                                // display_name
	row[7] = "series"                                      // category
	row[8] = []byte(`{"quality":"720p","start_season":2}`) // extra

	rows := pgxmock.NewRows(topicColumns19).AddRow(row...)

	mock.ExpectQuery(`UPDATE topics SET`).
		WithArgs(
			id, userID,
			"Updated Name",    // $3 display_name
			(*uuid.UUID)(nil), // $4 client_id
			"",                // $5 download_dir
			"series",          // $6 category
			pgxmock.AnyArg(),  // $7 extra (JSON)
		).
		WillReturnRows(rows)

	extra := map[string]any{"quality": "720p", "start_season": 2}
	got, err := r.Update(context.Background(), id, userID, "Updated Name", nil, "", "series", extra)
	if err != nil {
		t.Fatalf("Update: unexpected error: %v", err)
	}
	if got.DisplayName != "Updated Name" {
		t.Errorf("Update: want DisplayName=%q, got %q", "Updated Name", got.DisplayName)
	}
	if got.Category != "series" {
		t.Errorf("Update: want Category=%q, got %q", "series", got.Category)
	}
}

// TestTopics_Update_NotFound verifies that pgx.ErrNoRows is translated
// to repo.ErrNotFound.
func TestTopics_Update_NotFound(t *testing.T) {
	r, mock := newMockTopics(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	id := uuid.New()
	userID := uuid.New()

	mock.ExpectQuery(`UPDATE topics SET`).
		WithArgs(id, userID, pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnError(pgx.ErrNoRows)

	_, err := r.Update(context.Background(), id, userID, "X", nil, "", "", map[string]any{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update: want ErrNotFound, got %v", err)
	}
}

// TestTopics_Update_DBError verifies that an arbitrary DB error is
// propagated unchanged (not wrapped as ErrNotFound).
func TestTopics_Update_DBError(t *testing.T) {
	r, mock := newMockTopics(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	id := uuid.New()
	userID := uuid.New()
	dbErr := errors.New("connection reset")

	mock.ExpectQuery(`UPDATE topics SET`).
		WithArgs(id, userID, pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnError(dbErr)

	_, err := r.Update(context.Background(), id, userID, "X", nil, "", "", map[string]any{})
	if err == nil {
		t.Fatal("Update: want error, got nil")
	}
	if !errors.Is(err, dbErr) {
		t.Fatalf("Update: want wrapped %v, got %v", dbErr, err)
	}
}
