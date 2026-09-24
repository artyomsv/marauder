//go:build integration

package repo

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// dueIDs makes want due, runs DueForCheck and returns which of want came
// back. Other tests share the database, so the result is filtered to this
// test's own topics.
func dueIDs(t *testing.T, topics *Topics, exclude []uuid.UUID, want ...uuid.UUID) map[uuid.UUID]bool {
	t.Helper()
	for _, id := range want {
		if _, err := topics.pool.Exec(context.Background(),
			`UPDATE topics SET next_check_at = now() - interval '1 minute' WHERE id = $1`, id); err != nil {
			t.Fatalf("make topic due: %v", err)
		}
	}
	due, err := topics.DueForCheck(context.Background(), 10000, exclude)
	if err != nil {
		t.Fatalf("DueForCheck: %v", err)
	}
	out := map[uuid.UUID]bool{}
	for _, d := range due {
		for _, w := range want {
			if d.ID == w {
				out[w] = true
			}
		}
	}
	return out
}

// TestIntegration_DueForCheck_SkipsExcludedTopics pins the issue #198 fix: a
// topic the scheduler already holds must not take a row of the LIMIT window.
func TestIntegration_DueForCheck_SkipsExcludedTopics(t *testing.T) {
	pool := integrationPool(t)
	userID := seedUser(t, pool)
	held := seedTopic(t, pool, userID, domain.TopicStatusActive, nil)
	free := seedTopic(t, pool, userID, domain.TopicStatusActive, nil)
	topics := NewTopics(pool)

	got := dueIDs(t, topics, []uuid.UUID{held.ID}, held.ID, free.ID)
	if got[held.ID] {
		t.Error("excluded topic was returned")
	}
	if !got[free.ID] {
		t.Error("topic that was not excluded is missing")
	}
}

// A nil exclude list must mean "exclude nothing". Encoded naively it is SQL
// NULL, and `id = ANY(NULL)` would filter out every row.
func TestIntegration_DueForCheck_NilExclude_ReturnsEveryDueTopic(t *testing.T) {
	pool := integrationPool(t)
	userID := seedUser(t, pool)
	a := seedTopic(t, pool, userID, domain.TopicStatusActive, nil)
	b := seedTopic(t, pool, userID, domain.TopicStatusError, nil)

	got := dueIDs(t, NewTopics(pool), nil, a.ID, b.ID)
	if !got[a.ID] || !got[b.ID] {
		t.Errorf("due = %v, want both topics", got)
	}
}
