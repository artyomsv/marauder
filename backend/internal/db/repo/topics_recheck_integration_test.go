//go:build integration

package repo

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// TestIntegration_QueueRecheck_BringsAnErroredTopicForward is the feature's
// reason to exist: a topic parked on a six-hour backoff becomes due now.
func TestIntegration_QueueRecheck_BringsAnErroredTopicForward(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	userID := seedUser(t, pool)
	topic := seedTopic(t, pool, userID, domain.TopicStatusError, nil)

	// Park it in the future, as a failing check would.
	if _, err := pool.Exec(ctx,
		`UPDATE topics SET next_check_at = now() + interval '6 hours' WHERE id = $1`, topic.ID); err != nil {
		t.Fatalf("park topic: %v", err)
	}

	topics := NewTopics(pool)
	out, err := topics.QueueRecheck(ctx, topic.ID, userID)
	if err != nil {
		t.Fatalf("QueueRecheck: %v", err)
	}
	if !out.Exists || !out.Queued {
		t.Fatalf("QueueRecheck = %+v, want {Exists:true Queued:true}", out)
	}

	got := reload(t, pool, topic.ID)
	if d := time.Since(got.NextCheckAt); d > time.Minute || d < -time.Minute {
		t.Errorf("next_check_at is %s from now, want ~now", d)
	}
}

// TestIntegration_QueueRecheck_LeavesEverythingElseAlone pins the narrowness of
// the statement. Clearing status or last_error here would claim the topic is
// healthy before anything has verified that.
func TestIntegration_QueueRecheck_LeavesEverythingElseAlone(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	userID := seedUser(t, pool)
	topic := seedTopic(t, pool, userID, domain.TopicStatusError, nil)

	if _, err := pool.Exec(ctx, `
UPDATE topics SET
    last_hash          = 'abc123',
    last_checked_at    = now() - interval '1 hour',
    consecutive_errors = 4,
    last_error         = 'boom',
    last_error_code    = 'auth'
WHERE id = $1`, topic.ID); err != nil {
		t.Fatalf("seed check state: %v", err)
	}
	before := reload(t, pool, topic.ID)

	if _, err := NewTopics(pool).QueueRecheck(ctx, topic.ID, userID); err != nil {
		t.Fatalf("QueueRecheck: %v", err)
	}

	got := reload(t, pool, topic.ID)
	if got.Status != before.Status {
		t.Errorf("status = %q, want %q unchanged", got.Status, before.Status)
	}
	if got.LastError != before.LastError {
		t.Errorf("last_error = %q, want %q unchanged", got.LastError, before.LastError)
	}
	if got.LastErrorCode != before.LastErrorCode {
		t.Errorf("last_error_code = %q, want %q unchanged", got.LastErrorCode, before.LastErrorCode)
	}
	if got.LastHash != before.LastHash {
		t.Errorf("last_hash = %q, want %q unchanged", got.LastHash, before.LastHash)
	}
	if got.ConsecutiveErrors != before.ConsecutiveErrors {
		t.Errorf("consecutive_errors = %d, want %d unchanged", got.ConsecutiveErrors, before.ConsecutiveErrors)
	}
	// last_checked_at is half of the check-state token; leaving it alone keeps
	// "when was this last checked?" truthful and minimises token disruption.
	if (got.LastCheckedAt == nil) != (before.LastCheckedAt == nil) {
		t.Fatalf("last_checked_at nullability changed")
	}
	if got.LastCheckedAt != nil && !got.LastCheckedAt.Equal(*before.LastCheckedAt) {
		t.Errorf("last_checked_at = %v, want %v unchanged", got.LastCheckedAt, before.LastCheckedAt)
	}
}

// TestIntegration_QueueRecheck_IgnoresPausedTopics — DueForCheck skips paused
// rows, so moving next_check_at would be a silent no-op. Reporting
// Exists=true, Queued=false lets the handler answer 409 instead of a
// misleading 204 — and distinguishes this case from an unknown topic, which
// is the entire point of the atomic outcome.
func TestIntegration_QueueRecheck_IgnoresPausedTopics(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	userID := seedUser(t, pool)
	topic := seedTopic(t, pool, userID, domain.TopicStatusPaused, nil)
	before := reload(t, pool, topic.ID)

	out, err := NewTopics(pool).QueueRecheck(ctx, topic.ID, userID)
	if err != nil {
		t.Fatalf("QueueRecheck: %v", err)
	}
	if !out.Exists {
		t.Fatal("QueueRecheck reported a paused topic as not existing")
	}
	if out.Queued {
		t.Fatal("QueueRecheck reported an update for a paused topic")
	}

	got := reload(t, pool, topic.ID)
	if !got.NextCheckAt.Equal(before.NextCheckAt) {
		t.Errorf("next_check_at moved for a paused topic: %v -> %v",
			before.NextCheckAt, got.NextCheckAt)
	}
}

// TestIntegration_QueueRecheck_IsScopedToTheOwner keeps ownership in the
// statement rather than relying on the handler to check first.
func TestIntegration_QueueRecheck_IsScopedToTheOwner(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	owner := seedUser(t, pool)
	stranger := seedUser(t, pool)
	topic := seedTopic(t, pool, owner, domain.TopicStatusActive, nil)
	before := reload(t, pool, topic.ID)

	out, err := NewTopics(pool).QueueRecheck(ctx, topic.ID, stranger)
	if err != nil {
		t.Fatalf("QueueRecheck: %v", err)
	}
	if out.Exists || out.Queued {
		t.Fatalf("QueueRecheck = %+v, want {Exists:false Queued:false} for another user's topic", out)
	}

	got := reload(t, pool, topic.ID)
	if !got.NextCheckAt.Equal(before.NextCheckAt) {
		t.Error("next_check_at moved for another user's topic")
	}
}

// TestIntegration_QueueRecheck_UnknownTopic reports Exists=false rather than
// erroring, so the handler can turn it into a 404 — and, crucially, it is
// distinguishable from a paused topic (Exists=true, Queued=false), which is
// the whole reason RecheckOutcome carries both fields.
func TestIntegration_QueueRecheck_UnknownTopic(t *testing.T) {
	pool := integrationPool(t)
	userID := seedUser(t, pool)
	out, err := NewTopics(pool).QueueRecheck(context.Background(), uuid.New(), userID)
	if err != nil {
		t.Fatalf("QueueRecheck: %v", err)
	}
	if out.Exists || out.Queued {
		t.Fatalf("QueueRecheck = %+v, want {Exists:false Queued:false} for an unknown topic", out)
	}
}
