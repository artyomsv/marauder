package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v3"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

func newMockUsers(t *testing.T) (*Users, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	t.Cleanup(func() { mock.Close() })
	return &Users{pool: mock}, mock
}

// usersColumns mirrors the SELECT list in users.scanOne.
var usersColumns = []string{
	"id", "username", "email", "password_hash", "role",
	"oidc_subject", "oidc_issuer", "is_disabled", "created_at", "updated_at", "last_login_at",
}

func TestUsers_GetInitialAdmin_Found(t *testing.T) {
	r, mock := newMockUsers(t)
	id := uuid.New()
	now := time.Now().UTC()

	rows := pgxmock.NewRows(usersColumns).AddRow(
		id, "admin", "", "", "admin", "", "", false, now, now, (*time.Time)(nil),
	)
	mock.ExpectQuery(`FROM users\s+WHERE role = 'admin' ORDER BY created_at ASC LIMIT 1`).
		WillReturnRows(rows)

	u, err := r.GetInitialAdmin(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.ID != id || u.Role != domain.RoleAdmin {
		t.Errorf("got id=%s role=%s, want id=%s role=admin", u.ID, u.Role, id)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestUsers_GetInitialAdmin_NotFound(t *testing.T) {
	r, mock := newMockUsers(t)
	mock.ExpectQuery(`FROM users\s+WHERE role = 'admin'`).WillReturnError(pgx.ErrNoRows)

	if _, err := r.GetInitialAdmin(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
