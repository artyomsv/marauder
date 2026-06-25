package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// topicEventsPool is the minimal pgx surface used by TopicEvents.
type topicEventsPool interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// TopicEvents is the repository for topic_events — a topic's history feed.
type TopicEvents struct {
	pool topicEventsPool
}

// NewTopicEvents constructs the repository.
func NewTopicEvents(pool *pgxpool.Pool) *TopicEvents {
	return &TopicEvents{pool: pool}
}

// Record inserts a history row and returns its serial id. Data is marshalled
// to JSON for the jsonb column (nil Data → SQL NULL).
func (r *TopicEvents) Record(ctx context.Context, e *domain.TopicEvent) (int64, error) {
	var data []byte
	if e.Data != nil {
		b, err := json.Marshal(e.Data)
		if err != nil {
			return 0, fmt.Errorf("topic_events: marshal data: %w", err)
		}
		data = b
	}
	const q = `
INSERT INTO topic_events (topic_id, user_id, event_type, severity, message, data)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id`
	var id int64
	if err := r.pool.QueryRow(ctx, q, e.TopicID, e.UserID, e.EventType, e.Severity, e.Message, data).Scan(&id); err != nil {
		return 0, fmt.Errorf("topic_events: record: %w", err)
	}
	return id, nil
}

// ListForTopic returns a topic's events, newest first, capped at limit.
// beforeID==0 returns from the newest; a positive beforeID pages older.
func (r *TopicEvents) ListForTopic(ctx context.Context, topicID, userID uuid.UUID, limit int, beforeID int64) ([]*domain.TopicEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const q = `
SELECT id, topic_id, user_id, event_type, severity, message, data, created_at
FROM topic_events
WHERE topic_id = $1 AND user_id = $2 AND ($3 = 0 OR id < $3)
ORDER BY id DESC
LIMIT $4`
	rows, err := r.pool.Query(ctx, q, topicID, userID, beforeID, limit)
	if err != nil {
		return nil, fmt.Errorf("topic_events: list: %w", err)
	}
	defer rows.Close()
	return scanTopicEvents(rows)
}

// ListForUserSince returns a user's events with id > sinceID, oldest first.
// Used by the Phase 3 SSE reconnect replay.
func (r *TopicEvents) ListForUserSince(ctx context.Context, userID uuid.UUID, sinceID int64) ([]*domain.TopicEvent, error) {
	const q = `
SELECT id, topic_id, user_id, event_type, severity, message, data, created_at
FROM topic_events
WHERE user_id = $1 AND id > $2
ORDER BY id ASC
LIMIT 200`
	rows, err := r.pool.Query(ctx, q, userID, sinceID)
	if err != nil {
		return nil, fmt.Errorf("topic_events: list since: %w", err)
	}
	defer rows.Close()
	return scanTopicEvents(rows)
}

func scanTopicEvents(rows pgx.Rows) ([]*domain.TopicEvent, error) {
	var out []*domain.TopicEvent
	for rows.Next() {
		var e domain.TopicEvent
		var data []byte
		var createdAt time.Time
		if err := rows.Scan(&e.ID, &e.TopicID, &e.UserID, &e.EventType, &e.Severity, &e.Message, &data, &createdAt); err != nil {
			return nil, fmt.Errorf("topic_events: scan: %w", err)
		}
		if len(data) > 0 {
			_ = json.Unmarshal(data, &e.Data)
		}
		e.CreatedAt = createdAt
		out = append(out, &e)
	}
	return out, rows.Err()
}
