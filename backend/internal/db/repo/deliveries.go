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

// MarkCompleted atomically stamps completed_at for a delivery, but only if it
// was still NULL. Returns true when this call won the NULL→now() transition —
// the caller that gets true is the one (and only one) that should fire the
// download.completed event. Survives restarts: a completed row never fires again.
func (r *Deliveries) MarkCompleted(ctx context.Context, deliveryID uuid.UUID) (bool, error) {
	const q = `UPDATE topic_deliveries SET completed_at = now() WHERE id = $1 AND completed_at IS NULL`
	ct, err := r.pool.Exec(ctx, q, deliveryID)
	if err != nil {
		return false, fmt.Errorf("deliveries: mark completed: %w", err)
	}
	return ct.RowsAffected() > 0, nil
}

// DeleteByInfohashes removes a topic's delivery rows whose infohash is in the
// supplied set. The scheduler's "replace previous version" policy (issue #101)
// calls this after the old torrents have been removed from the client, so the
// status view and progress watcher stop tracking torrents that no longer exist.
// A no-op for an empty set. Returns the number of rows removed.
func (r *Deliveries) DeleteByInfohashes(ctx context.Context, topicID uuid.UUID, hashes []string) (int64, error) {
	if len(hashes) == 0 {
		return 0, nil
	}
	const q = `DELETE FROM topic_deliveries WHERE topic_id = $1 AND infohash = ANY($2)`
	ct, err := r.pool.Exec(ctx, q, topicID, hashes)
	if err != nil {
		return 0, fmt.Errorf("deliveries: delete by infohashes: %w", err)
	}
	return ct.RowsAffected(), nil
}

// DeleteForTopic removes every delivery row for a topic. Used by the topic
// reset endpoint: the (topic_id, infohash) unique index plus Record's
// ON CONFLICT DO NOTHING means a surviving row would make the post-reset
// re-delivery record a silent no-op, leaving the status view permanently
// empty for that torrent. Returns the number of rows removed.
//
// The DELETE joins topics and keys on the owner, so ownership is enforced here
// rather than resting on caller discipline: passing a topic id belonging to
// another user deletes nothing. Callers still check ownership up front (the
// reset handler does, via a user-scoped Topics.GetByID) so they can return 404
// instead of a silent no-op — this is the second line of defence, not the first.
func (r *Deliveries) DeleteForTopic(ctx context.Context, topicID, userID uuid.UUID) (int64, error) {
	const q = `
DELETE FROM topic_deliveries d
USING topics t
WHERE d.topic_id = t.id AND t.id = $1 AND t.user_id = $2`
	ct, err := r.pool.Exec(ctx, q, topicID, userID)
	if err != nil {
		return 0, fmt.Errorf("deliveries: delete for topic: %w", err)
	}
	return ct.RowsAffected(), nil
}

// ListInFlight returns deliveries not yet marked complete, joined with their
// topic's owner, notifier override, display name and tracker URL. Bounded to
// the last 30 days and to deliveries with a known client, so rows that can
// never complete (client without WithStatus, removed torrents) age out of the
// working set instead of being polled forever.
func (r *Deliveries) ListInFlight(ctx context.Context) ([]*domain.InFlightDelivery, error) {
	const q = `
SELECT d.id, d.topic_id, t.user_id, t.notifier_id, d.client_id, d.infohash, d.label, t.display_name, t.url
FROM topic_deliveries d
JOIN topics t ON t.id = d.topic_id
WHERE d.completed_at IS NULL
  AND d.client_id IS NOT NULL
  AND d.delivered_at > now() - interval '30 days'`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("deliveries: list in-flight: %w", err)
	}
	defer rows.Close()
	var out []*domain.InFlightDelivery
	for rows.Next() {
		var d domain.InFlightDelivery
		if err := rows.Scan(&d.DeliveryID, &d.TopicID, &d.UserID, &d.NotifierID, &d.ClientID, &d.Infohash, &d.Label, &d.DisplayName, &d.URL); err != nil {
			return nil, fmt.Errorf("deliveries: scan in-flight: %w", err)
		}
		out = append(out, &d)
	}
	return out, rows.Err()
}
