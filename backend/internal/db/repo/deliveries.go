package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// deliveriesPool is the minimal subset of *pgxpool.Pool used by
// Deliveries, defined as an interface so tests can substitute a mock.
type deliveriesPool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Deliveries is the repository for topic_deliveries — the record of every
// torrent Marauder has pushed to a client for a topic.
type Deliveries struct {
	pool deliveriesPool
}

// NewDeliveries constructs the repository.
func NewDeliveries(pool *pgxpool.Pool) *Deliveries {
	return &Deliveries{pool: pool}
}

// Record inserts a delivery, idempotently: re-detecting the same release
// (same topic + infohash) is a no-op rather than a duplicate row. Returns
// true when a new row was inserted, false when the delivery already
// existed. The label is only set on first insert.
func (r *Deliveries) Record(ctx context.Context, d *domain.TopicDelivery) (bool, error) {
	const q = `
INSERT INTO topic_deliveries (topic_id, infohash, label, client_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (topic_id, infohash) DO NOTHING`
	ct, err := r.pool.Exec(ctx, q, d.TopicID, d.Infohash, d.Label, d.ClientID)
	if err != nil {
		return false, fmt.Errorf("deliveries: record: %w", err)
	}
	return ct.RowsAffected() > 0, nil
}

// ListForTopic returns a topic's deliveries, newest first.
func (r *Deliveries) ListForTopic(ctx context.Context, topicID uuid.UUID) ([]*domain.TopicDelivery, error) {
	const q = `
SELECT id, topic_id, infohash, label, client_id, delivered_at
FROM topic_deliveries
WHERE topic_id = $1
ORDER BY delivered_at DESC`
	rows, err := r.pool.Query(ctx, q, topicID)
	if err != nil {
		return nil, fmt.Errorf("deliveries: list: %w", err)
	}
	defer rows.Close()
	var out []*domain.TopicDelivery
	for rows.Next() {
		var d domain.TopicDelivery
		var clientID *uuid.UUID
		var deliveredAt time.Time
		if err := rows.Scan(&d.ID, &d.TopicID, &d.Infohash, &d.Label, &clientID, &deliveredAt); err != nil {
			return nil, fmt.Errorf("deliveries: scan: %w", err)
		}
		d.ClientID = clientID
		d.DeliveredAt = deliveredAt
		out = append(out, &d)
	}
	return out, rows.Err()
}
