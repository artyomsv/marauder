package repo

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v3"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

func newMockDeliveries(t *testing.T) (*Deliveries, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	t.Cleanup(func() { mock.Close() })
	return &Deliveries{pool: mock}, mock
}

func TestDeliveries_Record_InsertsNew(t *testing.T) {
	repo, mock := newMockDeliveries(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	topicID := uuid.New()
	clientID := uuid.New()
	mock.ExpectExec(`INSERT INTO topic_deliveries`).
		WithArgs(topicID, "abc123", "s02e06", &clientID).
		WillReturnResult(pgconn.NewCommandTag("INSERT 0 1"))

	inserted, err := repo.Record(context.Background(), &domain.TopicDelivery{
		TopicID:  topicID,
		Infohash: "abc123",
		Label:    "s02e06",
		ClientID: &clientID,
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if !inserted {
		t.Error("expected inserted=true for a new delivery")
	}
}

func TestDeliveries_Record_DuplicateIsNoOp(t *testing.T) {
	repo, mock := newMockDeliveries(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	topicID := uuid.New()
	mock.ExpectExec(`INSERT INTO topic_deliveries`).
		WithArgs(topicID, "dup", "", (*uuid.UUID)(nil)).
		WillReturnResult(pgconn.NewCommandTag("INSERT 0 0")) // ON CONFLICT DO NOTHING

	inserted, err := repo.Record(context.Background(), &domain.TopicDelivery{
		TopicID:  topicID,
		Infohash: "dup",
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if inserted {
		t.Error("expected inserted=false when the delivery already exists")
	}
}

func TestDeliveries_ListForTopic(t *testing.T) {
	repo, mock := newMockDeliveries(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	topicID := uuid.New()
	id1 := uuid.New()
	clientID := uuid.New()
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	rows := pgxmock.NewRows([]string{"id", "topic_id", "infohash", "label", "client_id", "delivered_at"}).
		AddRow(id1, topicID, "hash1", "s01e01", &clientID, now)
	mock.ExpectQuery(`SELECT id, topic_id, infohash, label, client_id, delivered_at`).
		WithArgs(topicID).
		WillReturnRows(rows)

	got, err := repo.ListForTopic(context.Background(), topicID)
	if err != nil {
		t.Fatalf("ListForTopic: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d deliveries, want 1", len(got))
	}
	if got[0].Infohash != "hash1" || got[0].Label != "s01e01" {
		t.Errorf("unexpected delivery: %+v", got[0])
	}
	if got[0].ClientID == nil || *got[0].ClientID != clientID {
		t.Errorf("client id not scanned: %+v", got[0].ClientID)
	}
}
