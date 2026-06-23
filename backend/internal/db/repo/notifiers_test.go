package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v3"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

func newMockNotifiers(t *testing.T) (*Notifiers, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	t.Cleanup(func() { mock.Close() })
	return &Notifiers{pool: mock}, mock
}

func TestNotifiers_Create_NonDefault_NoUnset(t *testing.T) {
	r, mock := newMockNotifiers(t)
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet: %v", err)
		}
	})
	uid := uuid.New()
	now := time.Now().UTC()
	mock.ExpectQuery(`INSERT INTO notifiers`).
		WithArgs(uid, "telegram", "T", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), false).
		WillReturnRows(pgxmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow(uuid.New(), now, now))

	_, err := r.Create(context.Background(), &domain.Notifier{
		UserID: uid, NotifierName: "telegram", DisplayName: "T",
		ConfigEnc: []byte("e"), ConfigNonce: []byte("n"), Events: []string{"updated"}, IsDefault: false,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestNotifiers_Create_Default_UnsetsSameType(t *testing.T) {
	r, mock := newMockNotifiers(t)
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet: %v", err)
		}
	})
	uid := uuid.New()
	now := time.Now().UTC()
	mock.ExpectBegin()
	// unset same-type defaults first
	mock.ExpectExec(`UPDATE notifiers SET is_default = false`).
		WithArgs(uid, "telegram").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectQuery(`INSERT INTO notifiers`).
		WithArgs(uid, "telegram", "T", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), true).
		WillReturnRows(pgxmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow(uuid.New(), now, now))
	mock.ExpectCommit()

	_, err := r.Create(context.Background(), &domain.Notifier{
		UserID: uid, NotifierName: "telegram", DisplayName: "T",
		ConfigEnc: []byte("e"), ConfigNonce: []byte("n"), Events: []string{"updated"}, IsDefault: true,
	})
	if err != nil {
		t.Fatalf("Create default: %v", err)
	}
}

func TestNotifiers_Update_Default_UnsetsSameTypeThenUpdates(t *testing.T) {
	r, mock := newMockNotifiers(t)
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet: %v", err)
		}
	})
	uid := uuid.New()
	id := uuid.New()
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE notifiers SET is_default = false`).
		WithArgs(uid, "telegram", id).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec(`UPDATE notifiers SET`).
		WithArgs(id, uid, "T2", pgxmock.AnyArg(), true, pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	err := r.Update(context.Background(), id, uid, "telegram", "T2", []string{"updated"}, true, []byte("e"), []byte("n"))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
}

func TestNotifiers_Update_NotFound(t *testing.T) {
	r, mock := newMockNotifiers(t)
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet: %v", err)
		}
	})
	uid := uuid.New()
	id := uuid.New()
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE notifiers SET`).
		WithArgs(id, uid, "T", pgxmock.AnyArg(), false, pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectRollback()

	err := r.Update(context.Background(), id, uid, "telegram", "T", []string{"updated"}, false, []byte("e"), []byte("n"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
