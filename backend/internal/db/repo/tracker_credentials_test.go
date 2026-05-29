package repo

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v3"
)

func newMockCreds(t *testing.T) (*TrackerCredentials, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	t.Cleanup(func() { mock.Close() })
	return &TrackerCredentials{pool: mock}, mock
}

func TestTrackerCredentials_SetSession_UpdatesEncryptedColumns(t *testing.T) {
	repo, mock := newMockCreds(t)
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled expectations: %v", err)
		}
	})

	id, userID := uuid.New(), uuid.New()
	enc, nonce := []byte("ct"), []byte("nonce")

	mock.ExpectExec(`UPDATE tracker_credentials SET session_enc = \$3, session_nonce = \$4, updated_at = now\(\) WHERE id = \$1 AND user_id = \$2`).
		WithArgs(id, userID, enc, nonce).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := repo.SetSession(context.Background(), id, userID, enc, nonce); err != nil {
		t.Fatalf("SetSession: %v", err)
	}
}
