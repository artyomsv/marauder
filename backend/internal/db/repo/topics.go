package repo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// Topics repository.
type Topics struct {
	pool *pgxpool.Pool
}

// NewTopics constructs the repository.
func NewTopics(pool *pgxpool.Pool) *Topics {
	return &Topics{pool: pool}
}

const topicColumns = `id, user_id, tracker_name, url, display_name, client_id,
		COALESCE(download_dir,''), extra, COALESCE(last_hash,''),
		last_checked_at, last_updated_at, next_check_at,
		check_interval_sec, consecutive_errors, status,
		COALESCE(last_error,''), created_at, updated_at`

func scanTopic(row pgx.Row) (*domain.Topic, error) {
	var t domain.Topic
	var extraRaw []byte
	var lastChecked, lastUpdated *time.Time
	var status string
	var clientID *uuid.UUID
	err := row.Scan(
		&t.ID, &t.UserID, &t.TrackerName, &t.URL, &t.DisplayName,
		&clientID, &t.DownloadDir, &extraRaw, &t.LastHash,
		&lastChecked, &lastUpdated, &t.NextCheckAt,
		&t.CheckIntervalSec, &t.ConsecutiveErrors, &status,
		&t.LastError, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	t.ClientID = clientID
	t.LastCheckedAt = lastChecked
	t.LastUpdatedAt = lastUpdated
	t.Status = domain.TopicStatus(status)
	if len(extraRaw) > 0 {
		_ = json.Unmarshal(extraRaw, &t.Extra)
	}
	if t.Extra == nil {
		t.Extra = map[string]any{}
	}
	return &t, nil
}

// Create inserts a new topic.
func (r *Topics) Create(ctx context.Context, t *domain.Topic) (*domain.Topic, error) {
	extra, _ := json.Marshal(t.Extra)
	q := `
INSERT INTO topics (user_id, tracker_name, url, display_name, client_id,
                    download_dir, extra, check_interval_sec, next_check_at, status)
VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8,$9,$10)
RETURNING ` + topicColumns
	row := r.pool.QueryRow(ctx, q,
		t.UserID, t.TrackerName, t.URL, t.DisplayName, t.ClientID,
		t.DownloadDir, extra, t.CheckIntervalSec, t.NextCheckAt, string(t.Status),
	)
	return scanTopic(row)
}

// GetByID fetches a topic, optionally scoped to a user.
func (r *Topics) GetByID(ctx context.Context, id uuid.UUID, userID *uuid.UUID) (*domain.Topic, error) {
	q := `SELECT ` + topicColumns + ` FROM topics WHERE id = $1`
	args := []any{id}
	if userID != nil {
		q += ` AND user_id = $2`
		args = append(args, *userID)
	}
	row := r.pool.QueryRow(ctx, q, args...)
	t, err := scanTopic(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// ListForUser returns all topics for a user, newest first.
func (r *Topics) ListForUser(ctx context.Context, userID uuid.UUID) ([]*domain.Topic, error) {
	q := `SELECT ` + topicColumns + ` FROM topics WHERE user_id = $1 ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Topic
	for rows.Next() {
		t, err := scanTopic(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Delete removes a topic (cascade deletes events).
func (r *Topics) Delete(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `DELETE FROM topics WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateStatus is used by handlers to pause/resume a topic.
func (r *Topics) UpdateStatus(ctx context.Context, id uuid.UUID, userID uuid.UUID, status domain.TopicStatus) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE topics SET status = $3, updated_at = now() WHERE id = $1 AND user_id = $2`,
		id, userID, string(status),
	)
	return err
}

// RecordCheckResult updates the state after a scheduler run.
func (r *Topics) RecordCheckResult(
	ctx context.Context, id uuid.UUID, hash string, updated bool,
	nextCheckAt time.Time, errMsg string,
) error {
	q := `
UPDATE topics SET
    last_checked_at   = now(),
    last_hash         = CASE WHEN $2 = '' THEN last_hash ELSE $2 END,
    last_updated_at   = CASE WHEN $3 THEN now() ELSE last_updated_at END,
    next_check_at     = $4,
    last_error        = NULLIF($5,''),
    consecutive_errors = CASE WHEN $5 = '' THEN 0 ELSE consecutive_errors + 1 END,
    status            = CASE WHEN $5 = '' THEN 'active' ELSE 'error' END,
    updated_at        = now()
WHERE id = $1`
	_, err := r.pool.Exec(ctx, q, id, hash, updated, nextCheckAt, errMsg)
	return err
}

// UpdateExtra overwrites the topic.extra JSONB blob with the supplied
// map. Used by the scheduler when a plugin reports per-episode
// download progress (e.g. LostFilm tracks the list of already-downloaded
// packed episode IDs in extra["downloaded_episodes"] so the next check
// only fetches what's missing).
func (r *Topics) UpdateExtra(ctx context.Context, id uuid.UUID, extra map[string]any) error {
	raw, err := json.Marshal(extra)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	_, err = r.pool.Exec(ctx,
		`UPDATE topics SET extra = $2, updated_at = now() WHERE id = $1`,
		id, raw,
	)
	return err
}

// DueForCheck returns up to `limit` topics whose next_check_at is in the past
// and status is active. Used by the scheduler.
func (r *Topics) DueForCheck(ctx context.Context, limit int) ([]*domain.Topic, error) {
	q := `SELECT ` + topicColumns + `
FROM topics
WHERE status = 'active' AND next_check_at <= now()
ORDER BY next_check_at ASC
LIMIT $1`
	rows, err := r.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Topic
	for rows.Next() {
		t, err := scanTopic(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
