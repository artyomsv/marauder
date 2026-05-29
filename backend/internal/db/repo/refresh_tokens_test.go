package repo

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v3"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// newMockRefreshTokens wires a RefreshTokens repo around a pgxmock pool.
func newMockRefreshTokens(t *testing.T) (*RefreshTokens, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	t.Cleanup(func() { mock.Close() })
	return &RefreshTokens{pool: mock}, mock
}

// TestRefreshTokens_Rotate_InsertsBeforeUpdate guards the ordering bug
// where the old row's replaced_by FK was set before the new row existed,
// failing the non-deferrable refresh_tokens_replaced_by_fkey constraint
// (SQLSTATE 23503). pgxmock matches expectations in order, so declaring
// INSERT before UPDATE fails if the repo ever reverts the order.
func TestRefreshTokens_Rotate_InsertsBeforeUpdate(t *testing.T) {
	repo, mock := newMockRefreshTokens(t)
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled expectations: %v", err)
		}
	})

	oldID := uuid.New()
	newTok := &domain.RefreshToken{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		TokenHash: "newhash",
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}

	mock.ExpectBegin()
	// New token must be inserted FIRST so its id exists for the FK.
	mock.ExpectExec(`INSERT INTO refresh_tokens`).
		WithArgs(newTok.ID, newTok.UserID, newTok.TokenHash,
			newTok.IssuedAt, newTok.ExpiresAt, newTok.UserAgent, newTok.IP).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	// Then the old row is revoked and pointed at the new one.
	mock.ExpectExec(`UPDATE refresh_tokens SET revoked_at = now\(\), replaced_by = \$2 WHERE id = \$1`).
		WithArgs(oldID, newTok.ID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	if err := repo.Rotate(context.Background(), oldID, newTok); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
}
