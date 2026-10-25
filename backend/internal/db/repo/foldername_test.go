package repo

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v5"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// Folder name settings (issue #225). The integration tests execute the SQL;
// these pin the text, so a dropped clause fails at unit level too.

func TestTopics_Update_FolderPolicy_WritesAllThreeColumns(t *testing.T) {
	r, mock := newMockTopics(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	id, userID := uuid.New(), uuid.New()
	row := topicRow(id, userID, time.Now().UTC())
	row[29] = "custom"
	row[30] = "My Show"

	mode, name := "custom", "My Show"
	mock.ExpectQuery(`UPDATE topics SET[\s\S]*`+
		`folder_name_mode = COALESCE\(\$16::text, folder_name_mode\),\s+`+
		`folder_name = CASE WHEN \$16::text IS NULL THEN folder_name WHEN \$16::text = 'custom' THEN NULLIF\(\$17::text, ''\) ELSE NULL END,\s+`+
		`folder_name_seen = CASE WHEN \$16::text IS NOT NULL AND \$16::text <> folder_name_mode THEN NULL ELSE folder_name_seen END`).
		WithArgs(id, userID, pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
			false, false, false, false, (*int)(nil), false, false, &mode, &name, false).
		WillReturnRows(pgxmock.NewRows(topicColumnsAll).AddRow(row...))

	got, err := r.Update(context.Background(), id, userID, "X", nil, nil, "", "", nil, TopicFlags{},
		&FolderNamePolicy{Mode: domain.FolderNameCustom, Name: "My Show"}, map[string]any{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.FolderNameMode != domain.FolderNameCustom || got.FolderName != "My Show" {
		t.Errorf("scanned (%q, %q), want (custom, My Show)", got.FolderNameMode, got.FolderName)
	}
}

func TestTopics_ResetCheckState_ClearsFolderNameSeen(t *testing.T) {
	r, mock := newMockTopics(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	id, userID := uuid.New(), uuid.New()
	mock.ExpectExec(`UPDATE topics SET[\s\S]*folder_name_seen\s+= NULL,[\s\S]*WHERE id = \$1 AND user_id = \$2`).
		WithArgs(id, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := r.ResetCheckState(context.Background(), id, userID); err != nil {
		t.Fatalf("ResetCheckState: %v", err)
	}
}

func TestTopics_SetFolderNameSeen(t *testing.T) {
	r, mock := newMockTopics(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	// Guarded by the worker's check-state token, like RecordCheckResult, so a
	// reset that lands mid-check is not undone by a stale name (PR #226).
	observed := time.Now().Add(-time.Minute)
	next := time.Now().Add(time.Hour)
	topic := &domain.Topic{ID: uuid.New(), LastCheckedAt: &observed, NextCheckAt: next}
	mock.ExpectExec(`UPDATE topics SET folder_name_seen = \$2\s+WHERE id = \$1 AND folder_name_mode = 'keep'\s+AND last_checked_at IS NOT DISTINCT FROM \$3 AND next_check_at = \$4`).
		WithArgs(topic.ID, "Renamed", &observed, next).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := r.SetFolderNameSeen(context.Background(), topic, "Renamed"); err != nil {
		t.Fatalf("SetFolderNameSeen: %v", err)
	}
}

func TestDeliveries_LatestOther(t *testing.T) {
	d, mock := newMockDeliveries(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	topicID, rowID, clientID := uuid.New(), uuid.New(), uuid.New()
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT id, topic_id, infohash, label, client_id, delivered_at, files\s+FROM topic_deliveries\s+WHERE topic_id = \$1 AND infohash <> \$2\s+ORDER BY delivered_at DESC\s+LIMIT 1`).
		WithArgs(topicID, "own").
		WillReturnRows(pgxmock.NewRows([]string{"id", "topic_id", "infohash", "label", "client_id", "delivered_at", "files"}).
			AddRow(rowID, topicID, "prev", "v1", &clientID, now, []byte(`[{"path":"E01.mkv","size":100}]`)))

	got, err := d.LatestOther(context.Background(), topicID, "own")
	if err != nil {
		t.Fatalf("LatestOther: %v", err)
	}
	if got == nil || got.Infohash != "prev" || got.ClientID == nil || *got.ClientID != clientID {
		t.Errorf("LatestOther = %+v, want the prev row on the client", got)
	}
	if got != nil && (len(got.Files) != 1 || got.Files[0] != (domain.TorrentFile{Path: "E01.mkv", Size: 100})) {
		t.Errorf("LatestOther files = %+v, want the stored list", got.Files)
	}
}

func TestDeliveries_LatestOther_NoRows(t *testing.T) {
	d, mock := newMockDeliveries(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	topicID := uuid.New()
	mock.ExpectQuery(`FROM topic_deliveries`).
		WithArgs(topicID, "own").
		WillReturnError(pgx.ErrNoRows)

	got, err := d.LatestOther(context.Background(), topicID, "own")
	if err != nil || got != nil {
		t.Errorf("LatestOther with no rows = (%+v, %v), want (nil, nil)", got, err)
	}
}
