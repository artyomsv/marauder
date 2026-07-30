package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// topicsPool is the minimal subset of *pgxpool.Pool used by Topics.
// Defined as an unexported interface so tests can substitute a mock
// (e.g. pgxmock) without changing the public constructor signature.
// The concrete *pgxpool.Pool type still satisfies this interface.
type topicsPool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Topics repository.
type Topics struct {
	pool topicsPool
}

// NewTopics constructs the repository.
func NewTopics(pool *pgxpool.Pool) *Topics {
	return &Topics{pool: pool}
}

const topicColumns = `id, user_id, tracker_name, url, display_name,
		COALESCE(image_url,''), client_id, notifier_id,
		COALESCE(download_dir,''), COALESCE(category,''), extra, COALESCE(last_hash,''),
		last_checked_at, last_updated_at, next_check_at,
		check_interval_sec, consecutive_errors, status,
		COALESCE(last_error,''), COALESCE(last_error_code,''), created_at, updated_at, display_name_is_placeholder,
		replace_on_update, replace_delete_data`

func scanTopic(row pgx.Row) (*domain.Topic, error) {
	var t domain.Topic
	var extraRaw []byte
	var lastChecked, lastUpdated *time.Time
	var status string
	var clientID, notifierID *uuid.UUID
	err := row.Scan(
		&t.ID, &t.UserID, &t.TrackerName, &t.URL, &t.DisplayName,
		&t.ImageURL, &clientID, &notifierID, &t.DownloadDir, &t.Category, &extraRaw, &t.LastHash,
		&lastChecked, &lastUpdated, &t.NextCheckAt,
		&t.CheckIntervalSec, &t.ConsecutiveErrors, &status,
		&t.LastError, &t.LastErrorCode, &t.CreatedAt, &t.UpdatedAt, &t.DisplayNameIsPlaceholder,
		&t.ReplaceOnUpdate, &t.ReplaceDeleteData,
	)
	if err != nil {
		return nil, err
	}
	t.ClientID = clientID
	t.NotifierID = notifierID
	t.LastCheckedAt = lastChecked
	t.LastUpdatedAt = lastUpdated
	t.Status = domain.TopicStatus(status)
	if len(extraRaw) > 0 {
		// Surface malformed JSON rather than silently treating a
		// corrupted blob as an empty map. The caller logs and skips
		// the row; a corrupt extra column is a data-integrity issue
		// that must not masquerade as "no extras".
		if err := json.Unmarshal(extraRaw, &t.Extra); err != nil {
			return nil, fmt.Errorf("topics: scan extra blob (id=%s): %w", t.ID, err)
		}
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
INSERT INTO topics (user_id, tracker_name, url, display_name, image_url, client_id, notifier_id,
                    download_dir, category, extra, check_interval_sec, next_check_at, status,
                    display_name_is_placeholder, replace_on_update, replace_delete_data)
VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,$7,NULLIF($8,''),NULLIF($9,''),$10,$11,$12,$13,$14,$15,$16)
RETURNING ` + topicColumns
	row := r.pool.QueryRow(ctx, q,
		t.UserID, t.TrackerName, t.URL, t.DisplayName, t.ImageURL, t.ClientID, t.NotifierID,
		t.DownloadDir, t.Category, extra, t.CheckIntervalSec, t.NextCheckAt, string(t.Status),
		t.DisplayNameIsPlaceholder, t.ReplaceOnUpdate, t.ReplaceDeleteData,
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

// GetByURL fetches a user's topic by its URL. Used by the Sonarr poller as
// a cheap pre-check before attempting to create an auto-imported topic.
// Returns ErrNotFound when the user has no topic with that URL.
func (r *Topics) GetByURL(ctx context.Context, userID uuid.UUID, url string) (*domain.Topic, error) {
	q := `SELECT ` + topicColumns + ` FROM topics WHERE user_id = $1 AND url = $2`
	row := r.pool.QueryRow(ctx, q, userID, url)
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

// ErrStaleCheckResult is returned by RecordCheckResult when the topic's check
// state changed between the worker observing it and writing its result back —
// in practice a reset landing mid-check, or the topic being deleted. The write
// is discarded on purpose; it describes state that no longer exists. Callers
// should log it and carry on, not treat it as a persistence failure.
var ErrStaleCheckResult = errors.New("stale check result")

// RecordCheckResult updates the state after a scheduler run.
//
// The write is guarded by optimistic concurrency on last_checked_at ($7): it
// lands only while the column still holds the value the worker observed when
// the topic was dispatched. last_checked_at is written by exactly two
// statements — this one and ResetCheckState — which makes it a version token
// for "the check state as this worker saw it".
//
// Without the guard a reset that lands mid-check is silently undone: the
// worker finishes, writes back the pre-reset hash and a backoff next_check_at,
// and the topic looks like it was never reset — except its torrents are gone
// from the client (and its files off disk, if the user ticked delete_data).
// Nothing re-downloads until the tracker's own hash changes.
//
// The comparison is IS NOT DISTINCT FROM, not =: ResetCheckState sets the
// column to NULL, and `= NULL` is never true, so the fresh post-reset check —
// which observes NULL — could otherwise never persist its own result.
//
// Returns ErrStaleCheckResult when the guard rejected the write.
func (r *Topics) RecordCheckResult(
	ctx context.Context, t *domain.Topic, hash string, updated bool,
	nextCheckAt time.Time, errMsg, errCode string,
) error {
	q := `
UPDATE topics SET
    last_checked_at   = now(),
    last_hash         = CASE WHEN $2 = '' THEN last_hash ELSE $2 END,
    last_updated_at   = CASE WHEN $3 THEN now() ELSE last_updated_at END,
    next_check_at     = $4,
    last_error        = NULLIF($5,''),
    last_error_code   = CASE WHEN $5 = '' THEN '' ELSE $6 END,
    consecutive_errors = CASE WHEN $5 = '' THEN 0 ELSE consecutive_errors + 1 END,
    status            = CASE WHEN $5 = '' THEN 'active' ELSE 'error' END,
    updated_at        = now()
WHERE id = $1 AND last_checked_at IS NOT DISTINCT FROM $7`
	ct, err := r.pool.Exec(ctx, q, t.ID, hash, updated, nextCheckAt, errMsg, errCode, t.LastCheckedAt)
	if err != nil {
		return fmt.Errorf("topics: record check result: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrStaleCheckResult
	}
	return nil
}

// UpdateExtra overwrites the topic.extra JSONB blob with the supplied
// map. Used by the scheduler's fallback path when a plugin reports
// per-episode download progress (e.g. LostFilm tracks the list of
// already-downloaded packed episode IDs in extra["downloaded_episodes"]
// so the next check only fetches what's missing).
//
// Deprecated: this method overwrites the entire JSONB blob and is
// unsafe under concurrent updates — a partially populated map will
// wipe server-side fields (quality, start_season, etc.). Prefer
// MarkEpisodeDownloaded for the episode-tracking hot path. Kept in
// place for backward compatibility with the scheduler's
// non-atomic fallback branch.
func (r *Topics) UpdateExtra(ctx context.Context, id uuid.UUID, extra map[string]any) error {
	raw, err := json.Marshal(extra)
	if err != nil {
		return fmt.Errorf("topics: marshal extra: %w", err)
	}
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	ct, err := r.pool.Exec(ctx,
		`UPDATE topics SET extra = $2, updated_at = now() WHERE id = $1`,
		id, raw,
	)
	if err != nil {
		return fmt.Errorf("topics: update extra: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkEpisodeDownloaded atomically appends the supplied packed episode
// ID to the topic's extra["downloaded_episodes"] array using a Postgres
// JSONB SET expression. Unlike UpdateExtra (which overwrites the whole
// blob), this:
//
//   - cannot wipe other extras keys,
//   - is safe under concurrent updates because the SQL is a single
//     atomic statement,
//   - returns ErrNotFound if the topic was deleted.
//
// The packed ID is appended exactly once per call; the scheduler is
// responsible for de-duplication on its side (it works from a pending
// list that's already filtered).
func (r *Topics) MarkEpisodeDownloaded(ctx context.Context, id uuid.UUID, packed string) error {
	// Atomic JSONB array append. jsonb_set requires the target path
	// to exist so we COALESCE both the column (NULL -> '{}') and the
	// inner downloaded_episodes key (missing -> '[]') before appending.
	const query = `
UPDATE topics
SET    extra = jsonb_set(
           COALESCE(extra, '{}'::jsonb),
           '{downloaded_episodes}',
           (COALESCE(extra->'downloaded_episodes', '[]'::jsonb) || to_jsonb($2::text)),
           true
       ),
       updated_at = now()
WHERE  id = $1`
	ct, err := r.pool.Exec(ctx, query, id, packed)
	if err != nil {
		return fmt.Errorf("topics: mark episode downloaded: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetCheckState discards a topic's check/download state so the next check
// re-detects the current release as new and re-delivers it. It is the inverse
// of RecordCheckResult, plus the per-episode progress MarkEpisodeDownloaded
// accumulates in extra.
//
// Configuration is deliberately untouched: client, notifier, category,
// download dir, interval, replace policy, display name, and the capability
// keys in extra (quality, start_season, start_episode, source) all survive.
// Only downloaded_episodes is dropped, via a targeted JSONB key delete rather
// than the whole-blob overwrite UpdateExtra would do.
//
// A paused topic stays paused — only 'error' is normalised back to 'active'.
// Resetting must not silently resume topics the user deliberately stopped,
// which matters most under a bulk reset over a mixed selection.
//
// next_check_at = now() is what "check now" means here: DueForCheck selects on
// it, and there is no separate manual-trigger path in the scheduler.
//
// Clearing last_checked_at also invalidates any check already in flight:
// RecordCheckResult only writes while that column still holds the value its
// worker observed, so a check that started before this reset can no longer
// undo it. See RecordCheckResult for the full reasoning.
//
// Returns ErrNotFound when the topic does not exist or belongs to another user.
func (r *Topics) ResetCheckState(ctx context.Context, id, userID uuid.UUID) error {
	const q = `
UPDATE topics SET
    last_hash          = NULL,
    last_checked_at    = NULL,
    last_updated_at    = NULL,
    consecutive_errors = 0,
    last_error         = NULL,
    last_error_code    = '',
    next_check_at      = now(),
    status             = CASE WHEN status = 'paused' THEN 'paused' ELSE 'active' END,
    extra              = COALESCE(extra, '{}'::jsonb) - 'downloaded_episodes',
    updated_at         = now()
WHERE id = $1 AND user_id = $2`
	ct, err := r.pool.Exec(ctx, q, id, userID)
	if err != nil {
		return fmt.Errorf("topics: reset check state: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Update edits a topic's user-editable fields (display name, client, notifier,
// download dir, category, and the capability Extra map). It does NOT
// touch url/tracker/status/hash/scheduling. Returns ErrNotFound when the
// topic doesn't belong to the user.
func (r *Topics) Update(ctx context.Context, id, userID uuid.UUID, displayName string, clientID, notifierID *uuid.UUID, downloadDir, category string, replaceOnUpdate, replaceDeleteData bool, extra map[string]any) (*domain.Topic, error) {
	raw, err := json.Marshal(extra)
	if err != nil {
		return nil, fmt.Errorf("topics: marshal extra: %w", err)
	}
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	row := r.pool.QueryRow(ctx, `UPDATE topics SET
		display_name = $3, client_id = $4, notifier_id = $5, download_dir = $6, category = $7,
		extra = $8, replace_on_update = $9, replace_delete_data = $10,
		display_name_is_placeholder = CASE WHEN display_name <> $3 THEN false ELSE display_name_is_placeholder END,
		updated_at = now()
	WHERE id = $1 AND user_id = $2
	RETURNING `+topicColumns, id, userID, displayName, clientID, notifierID, downloadDir, category, raw, replaceOnUpdate, replaceDeleteData)
	t, err := scanTopic(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// UpdateDisplayName persists a freshly-resolved title for a topic. The
// scheduler calls this when a tracker's Check reports a DisplayName that
// differs from what's stored — upgrading placeholder names (e.g. "RuTracker
// topic 123") to the real title without the user editing anything. A blank
// name is ignored so a tracker that didn't resolve a title can't wipe a good
// one.
func (r *Topics) UpdateDisplayName(ctx context.Context, id uuid.UUID, displayName string) error {
	if displayName == "" {
		return nil
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE topics SET display_name = $2, display_name_is_placeholder = false, updated_at = now() WHERE id = $1`,
		id, displayName,
	)
	return err
}

// DueForCheck returns up to `limit` topics whose next_check_at is in the past
// and status is active or error. Errored topics are retried on their
// exponential-backoff schedule; paused topics remain excluded.
func (r *Topics) DueForCheck(ctx context.Context, limit int) ([]*domain.Topic, error) {
	q := `SELECT ` + topicColumns + `
FROM topics
WHERE status IN ('active', 'error') AND next_check_at <= now()
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
