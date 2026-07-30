//go:build integration

// Package-level harness for the repo integration suite.
//
// The rest of this package's tests use pgxmock: they pin SQL text and bound
// arguments but never execute a statement, so they cannot say what the SQL
// actually *does*. Several behaviours the topic-reset feature rests on are
// pure SQL semantics and are invisible to a mock:
//
//   - `extra - 'downloaded_episodes'` dropping exactly one JSONB key,
//   - the `status` CASE leaving a paused row paused,
//   - `last_checked_at IS NOT DISTINCT FROM $n` matching a post-reset NULL,
//     where a plain `=` would silently make every post-reset check discard
//     its own result.
//
// These run against a real Postgres and are build-tagged so `go test ./...`
// is unaffected. Point MARAUDER_TEST_DB_URL at a throwaway database and run:
//
//	go test -tags=integration ./internal/db/repo/...
package repo

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/artyomsv/marauder/backend/internal/db"
	"github.com/artyomsv/marauder/backend/internal/domain"
)

// testDBURLEnv names the Postgres the suite runs against. Unset means "skip",
// so a developer without a database still gets a clean `go test` run.
const testDBURLEnv = "MARAUDER_TEST_DB_URL"

// migrateOnce applies the schema a single time per process, however many
// tests ask for a pool.
var (
	migrateOnce sync.Once
	migrateErr  error
)

// integrationPool returns a pool against the test database with the real
// migrations applied, or skips the test when no database is configured.
func integrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv(testDBURLEnv)
	if url == "" {
		t.Skipf("%s is not set; skipping repo integration tests", testDBURLEnv)
	}
	migrateOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		// Reuse the server's own migration runner rather than restating the
		// migration list: these tests are only worth anything if the schema
		// they assert against is the schema production boots on.
		migrateErr = db.Migrate(ctx, url)
	})
	if migrateErr != nil {
		t.Fatalf("apply migrations to %s: %v", testDBURLEnv, migrateErr)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("open test pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedUser inserts a user with a generated username and deletes it (cascading
// to its topics) on cleanup, so tests are isolated from each other and can run
// in any order or concurrently.
func seedUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	u, err := NewUsers(pool).Create(context.Background(), &domain.User{
		Username: "itest-" + uuid.NewString(),
		Role:     domain.RoleUser,
	})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID); err != nil {
			t.Errorf("cleanup user %s: %v", u.ID, err)
		}
	})
	return u.ID
}

// seedTopic inserts one topic for the user with the supplied extra blob and
// status. The URL is generated so the (user_id, url) unique index can never
// make two tests collide.
func seedTopic(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, status domain.TopicStatus, extra map[string]any) *domain.Topic {
	t.Helper()
	created, err := NewTopics(pool).Create(context.Background(), &domain.Topic{
		UserID:           userID,
		TrackerName:      "itest",
		URL:              "https://tracker.invalid/topic/" + uuid.NewString(),
		DisplayName:      "Integration Topic",
		Extra:            extra,
		CheckIntervalSec: 900,
		NextCheckAt:      time.Now().Add(time.Hour),
		Status:           status,
	})
	if err != nil {
		t.Fatalf("seed topic: %v", err)
	}
	return created
}

// reload fetches the topic's current row.
func reload(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) *domain.Topic {
	t.Helper()
	got, err := NewTopics(pool).GetByID(context.Background(), id, nil)
	if err != nil {
		t.Fatalf("reload topic %s: %v", id, err)
	}
	return got
}
