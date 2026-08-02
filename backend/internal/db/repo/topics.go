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

// ErrStaleCheckResult is returned by RecordCheckResult, MarkEpisodeDownloaded
// and VerifyCheckState when the topic's check state changed between the worker
// observing it and acting on it — in practice a reset landing mid-check, the
// topic being deleted, or a long check being re-dispatched and the second
// worker winning. For the two writes the write is discarded on purpose; for
// VerifyCheckState the caller has not acted yet and should stop. Either way it
// describes state that no longer exists: callers should log it and carry on,
// not treat it as a persistence failure.
var ErrStaleCheckResult = errors.New("stale check result")

// The check-state version token
// ─────────────────────────────
//
// RecordCheckResult and MarkEpisodeDownloaded both write back the result of a
// check that may have been running for tens of seconds, so both guard on the
// pair (last_checked_at, next_check_at) — the version token for "the check
// state as this worker saw it when the topic was dispatched". Exactly two
// statements write those columns: RecordCheckResult and ResetCheckState.
//
// Both columns are needed, because neither alone is a usable token:
//
//   - last_checked_at is not monotonic. ResetCheckState sets it to NULL, so two
//     consecutive resets look identical. A worker dispatched by reset #1 (which
//     sets next_check_at = now(), so a check is very likely already running)
//     observes NULL; reset #2 lands, removing the torrents from the client and
//     deleting their on-disk data; the worker then writes back and
//     NULL IS NOT DISTINCT FROM NULL still matches, restoring the pre-reset
//     hash. Reset #2 is silently undone after its irreversible half already ran.
//   - next_check_at is never NULL, but on its own it does not distinguish a
//     topic that was reset from one that simply came due again.
//
// Together they work: ResetCheckState writes next_check_at = now() at
// Postgres' microsecond resolution, so each reset stamps a distinct value
// while also clearing last_checked_at.
//
// The last_checked_at comparison is IS NOT DISTINCT FROM, not =, because a
// reset sets it to NULL and `= NULL` is never true — the fresh post-reset
// check, which observes NULL, could otherwise never persist its own result.
// next_check_at is NOT NULL, so a plain = suffices there.

// RecordCheckResult updates the state after a scheduler run.
//
// The write is guarded on the check-state version token described above: it
// lands only while the topic's (last_checked_at, next_check_at) pair still
// holds the values the worker observed at dispatch.
//
// Without the guard a reset that lands mid-check is silently undone: the
// worker finishes, writes back the pre-reset hash and a backoff next_check_at,
// and the topic looks like it was never reset — except its torrents are gone
// from the client (and its files off disk, if the user ticked delete_data).
// Nothing re-downloads until the tracker's own hash changes.
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
WHERE id = $1 AND last_checked_at IS NOT DISTINCT FROM $7 AND next_check_at = $8`
	ct, err := r.pool.Exec(ctx, q, t.ID, hash, updated, nextCheckAt, errMsg, errCode, t.LastCheckedAt, t.NextCheckAt)
	if err != nil {
		return fmt.Errorf("topics: record check result: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrStaleCheckResult
	}
	return nil
}

// VerifyCheckState reports whether the topic's check-state version token still
// holds the values the worker observed at dispatch. It is the read-only form of
// the guard RecordCheckResult and MarkEpisodeDownloaded carry in their WHERE
// clauses, for callers that need to know *before* doing something irreversible
// rather than after.
//
// Its caller is the scheduler, immediately before handing a payload to a
// torrent client. A topic reset takes a snapshot of the topic's deliveries and
// removes exactly those torrents, so a torrent added after that snapshot is
// invisible to it: with delete_data set, the reset reports success while that
// torrent's files stay on disk, and the re-delivered release then rechecks and
// resumes against them.
//
// This narrows that window; it does not close it. ResetCheckState is the last
// statement the reset runs, so the token does not change until the reset is
// essentially finished — a worker that reads the token while the reset is still
// removing torrents sees a valid one and proceeds. What it does remove is the
// unbounded case: a worker that reaches the submit point at any point *after*
// the reset completed. Closing the remainder needs a claim-on-dispatch marker.
//
// Returns nil when the token matches and ErrStaleCheckResult when it does not
// (including when the topic no longer exists), so callers handle "the topic
// moved on" in one shape regardless of which guard reported it.
func (r *Topics) VerifyCheckState(ctx context.Context, t *domain.Topic) error {
	const q = `SELECT 1 FROM topics
WHERE id = $1 AND last_checked_at IS NOT DISTINCT FROM $2 AND next_check_at = $3`
	var one int
	err := r.pool.QueryRow(ctx, q, t.ID, t.LastCheckedAt, t.NextCheckAt).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleCheckResult
	}
	if err != nil {
		return fmt.Errorf("topics: verify check state: %w", err)
	}
	return nil
}

// UpdateExtra overwrites the topic.extra JSONB blob with the supplied map.
//
// Deprecated: unused. Its only caller was the scheduler's non-atomic
// episode-tracking fallback, which has been removed. It overwrites the entire
// JSONB blob and is unsafe under concurrent updates — a partially populated
// map wipes server-side fields (quality, start_season, downloaded_episodes) —
// and it carries no check-state guard, so a write built from a worker's
// in-memory copy would undo a reset wholesale. Use MarkEpisodeDownloaded for
// episode progress and Update for user edits; do not add new callers.
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
//     atomic statement.
//
// The packed ID is appended exactly once per call; the scheduler is
// responsible for de-duplication on its side (it works from a pending
// list that's already filtered).
//
// It carries the same check-state version-token guard as RecordCheckResult
// (see above), because it fires earlier in the same check and appends to a
// key ResetCheckState deletes. Unguarded, the losing interleaving strands an
// episode permanently: the worker delivers episode E and records its delivery
// row, a reset then removes E from the client, deletes its data and its
// delivery row, and clears downloaded_episodes — and only then does the
// worker's append land, re-marking E as downloaded. E is now absent from the
// client, absent from disk, and skipped by every future check.
//
// Returns ErrStaleCheckResult when the guard rejected the write — the topic
// was reset (or deleted) mid-check. Dropping the mark is the correct outcome:
// the episode is simply re-downloaded on the next tick, which is exactly what
// the reset asked for.
func (r *Topics) MarkEpisodeDownloaded(ctx context.Context, t *domain.Topic, packed string) error {
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
WHERE  id = $1 AND last_checked_at IS NOT DISTINCT FROM $3 AND next_check_at = $4`
	ct, err := r.pool.Exec(ctx, query, t.ID, packed, t.LastCheckedAt, t.NextCheckAt)
	if err != nil {
		return fmt.Errorf("topics: mark episode downloaded: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrStaleCheckResult
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
// Clearing last_checked_at and stamping a fresh next_check_at also invalidates
// any check already in flight: RecordCheckResult and MarkEpisodeDownloaded
// only write while BOTH columns still hold the values their worker observed,
// so a check that started before this reset can no longer undo it. Writing
// next_check_at = now() at microsecond resolution is what makes consecutive
// resets distinguishable — clearing last_checked_at alone would leave reset #2
// with the same NULL token reset #1 produced, and a worker dispatched by reset
// #1 would happily overwrite reset #2. See the version-token comment above
// RecordCheckResult for the full reasoning.
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

// RecheckOutcome is the atomic result of QueueRecheck: whether the topic
// exists for this user, and whether its next check was actually brought
// forward. Both facts come from ONE statement, deliberately.
//
// Classifying by combining separate statements does not work here: the handler
// needs to tell "paused" from "not found", and any interleaving of a pause or
// resume between two queries makes that inference wrong. A single snapshot
// cannot disagree with itself.
type RecheckOutcome struct {
	Exists bool
	Queued bool
}

// QueueRecheck brings a topic's next check forward to now, so the scheduler
// picks it up on its next tick. It is the non-destructive counterpart to
// ResetCheckState: a topic parked on a backoff after a tracker outage can be
// retried the moment the cause is fixed, without removing anything from the
// download client.
//
// It writes next_check_at and NOTHING else. status, last_error and
// last_error_code belong to the scheduler, which clears them on the next
// successful check — reporting the topic healthy before anything has verified
// that would be a lie. last_checked_at is left alone too: it has not checked
// anything yet, and it is half of the (last_checked_at, next_check_at)
// optimistic-concurrency token, so disturbing it needlessly would widen the
// window in which an in-flight check discards its own result.
//
// Paused topics are not queued because DueForCheck ignores them; moving their
// next_check_at would be a silent no-op the API would have to report as
// success. Ownership is enforced by the statement, matching ResetCheckState.
//
// Exists and Queued are read from the same CTE snapshot, so there is no gap
// between "does it exist" and "was it updated" for a concurrent pause/resume
// to fall into — see the RecheckOutcome doc comment for why that matters.
func (r *Topics) QueueRecheck(ctx context.Context, id, userID uuid.UUID) (RecheckOutcome, error) {
	// The eligibility predicate (status <> 'paused') MUST live in the UPDATE's
	// own WHERE clause, not in the target CTE. Under READ COMMITTED, Postgres
	// re-evaluates an UPDATE's own WHERE against the newest committed row
	// version after locking it, so a pause committed between the statement
	// snapshot and the update is still honoured. A status tested inside a CTE
	// is frozen at the snapshot, which would let a concurrent pause slip
	// through and return 204 — promising a check DueForCheck then ignores.
	//
	// target therefore tests only existence and ownership, which is what the
	// caller needs to tell "paused" (409) from "not found" (404).
	const q = `
WITH target AS (
    SELECT 1 FROM topics WHERE id = $1 AND user_id = $2
), updated AS (
    UPDATE topics SET
        next_check_at = now(),
        updated_at    = now()
    WHERE id = $1 AND user_id = $2 AND status <> 'paused'
    RETURNING 1
)
SELECT EXISTS (SELECT 1 FROM target), EXISTS (SELECT 1 FROM updated)`
	var out RecheckOutcome
	if err := r.pool.QueryRow(ctx, q, id, userID).Scan(&out.Exists, &out.Queued); err != nil {
		return RecheckOutcome{}, fmt.Errorf("topics: queue recheck: %w", err)
	}
	return out, nil
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
