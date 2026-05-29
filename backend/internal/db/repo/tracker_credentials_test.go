package repo

import (
	"context"
	"errors"
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

	mock.ExpectExec(`UPDATE tracker_credentials SET session_enc = \$3, session_nonce = \$4, session_expired_at = NULL, updated_at = now\(\) WHERE id = \$1 AND user_id = \$2`).
		WithArgs(id, userID, enc, nonce).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := repo.SetSession(context.Background(), id, userID, enc, nonce); err != nil {
		t.Fatalf("SetSession: %v", err)
	}
}

func TestTrackerCredentials_SetSession_NoRows_ReturnsErrNotFound(t *testing.T) {
	repo, mock := newMockCreds(t)
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled expectations: %v", err)
		}
	})

	id, userID := uuid.New(), uuid.New()
	enc, nonce := []byte("ct"), []byte("nonce")

	mock.ExpectExec(`UPDATE tracker_credentials SET session_enc = \$3, session_nonce = \$4, session_expired_at = NULL, updated_at = now\(\) WHERE id = \$1 AND user_id = \$2`).
		WithArgs(id, userID, enc, nonce).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	err := repo.SetSession(context.Background(), id, userID, enc, nonce)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetSession: got %v, want ErrNotFound", err)
	}
}

func TestTrackerCredentials_MarkSessionExpired_Transitions(t *testing.T) {
	repo, mock := newMockCreds(t)
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled: %v", err)
		}
	})
	id, userID := uuid.New(), uuid.New()
	mock.ExpectExec(`UPDATE tracker_credentials SET session_expired_at = now\(\) WHERE id = \$1 AND user_id = \$2 AND session_expired_at IS NULL`).
		WithArgs(id, userID).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	won, err := repo.MarkSessionExpired(context.Background(), id, userID)
	if err != nil {
		t.Fatalf("MarkSessionExpired: %v", err)
	}
	if !won {
		t.Errorf("expected won=true when RowsAffected=1, got false")
	}
}

func TestTrackerCredentials_MarkSessionExpired_AlreadyMarked(t *testing.T) {
	repo, mock := newMockCreds(t)
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled: %v", err)
		}
	})
	id, userID := uuid.New(), uuid.New()
	// RowsAffected=0: row already had session_expired_at set (or not found).
	// No error — the caller treats false as "someone else won the race".
	mock.ExpectExec(`UPDATE tracker_credentials SET session_expired_at = now\(\) WHERE id = \$1 AND user_id = \$2 AND session_expired_at IS NULL`).
		WithArgs(id, userID).WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	won, err := repo.MarkSessionExpired(context.Background(), id, userID)
	if err != nil {
		t.Fatalf("MarkSessionExpired: %v", err)
	}
	if won {
		t.Errorf("expected won=false when RowsAffected=0, got true")
	}
}

func TestTrackerCredentials_SetSession_ClearsExpiredMarker(t *testing.T) {
	repo, mock := newMockCreds(t)
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled: %v", err)
		}
	})
	id, userID := uuid.New(), uuid.New()
	mock.ExpectExec(`UPDATE tracker_credentials SET session_enc = \$3, session_nonce = \$4, session_expired_at = NULL, updated_at = now\(\) WHERE id = \$1 AND user_id = \$2`).
		WithArgs(id, userID, []byte("ct"), []byte("n")).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := repo.SetSession(context.Background(), id, userID, []byte("ct"), []byte("n")); err != nil {
		t.Fatalf("SetSession: %v", err)
	}
}
