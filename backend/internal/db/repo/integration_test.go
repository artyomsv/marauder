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
	"errors"
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

func TestTopicsNotifyOnlyRoundTrip(t *testing.T) {
	pool := integrationPool(t)
	topicsRepo := NewTopics(pool)
	userID := seedUser(t, pool)
	ctx := context.Background()

	// The two flags are deliberately DIFFERENT. With {true,true} the INSERT
	// argument list can be transposed and this test still passes, which is
	// exactly the bug it has to catch, so each one is also asserted on its own
	// rather than with a combined || — a joint check names neither field.
	created, err := topicsRepo.Create(ctx, &domain.Topic{
		UserID:                    userID,
		TrackerName:               "faketracker",
		URL:                       "https://example.com/notify-only-roundtrip",
		DisplayName:               "Notify Only Round Trip",
		NotifyOnly:                true,
		NotifyOnlyAnnounceCurrent: false,
		CheckIntervalSec:          900,
		NextCheckAt:               time.Now().UTC(),
		Status:                    domain.TopicStatusActive,
		Extra:                     map[string]any{},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !created.NotifyOnly {
		t.Errorf("Create returned NotifyOnly = false, want true")
	}
	if created.NotifyOnlyAnnounceCurrent {
		t.Errorf("Create returned NotifyOnlyAnnounceCurrent = true, want false")
	}
	// Re-read the stored row: Create's return value is built from RETURNING,
	// so only a fresh SELECT proves the columns themselves hold the pair.
	reloaded, err := topicsRepo.GetByID(ctx, created.ID, &userID)
	if err != nil {
		t.Fatalf("GetByID created: %v", err)
	}
	if !reloaded.NotifyOnly {
		t.Errorf("stored NotifyOnly = false, want true")
	}
	if reloaded.NotifyOnlyAnnounceCurrent {
		t.Errorf("stored NotifyOnlyAnnounceCurrent = true, want false")
	}

	// A plain topic must default to the historical behaviour.
	plain, err := topicsRepo.Create(ctx, &domain.Topic{
		UserID:           userID,
		TrackerName:      "faketracker",
		URL:              "https://example.com/notify-only-default",
		DisplayName:      "Default",
		CheckIntervalSec: 900,
		NextCheckAt:      time.Now().UTC(),
		Status:           domain.TopicStatusActive,
		Extra:            map[string]any{},
	})
	if err != nil {
		t.Fatalf("Create plain: %v", err)
	}
	if plain.NotifyOnly {
		t.Errorf("default NotifyOnly = true, want false")
	}
	if plain.NotifyOnlyAnnounceCurrent {
		t.Errorf("default NotifyOnlyAnnounceCurrent = true, want false")
	}

	// Update must persist both, and GetByID must read them back.
	if _, err := topicsRepo.Update(ctx, plain.ID, userID, plain.DisplayName, nil, nil, "", "",
		TopicFlags{NotifyOnly: true, NotifyOnlyAnnounceCurrent: false}, map[string]any{}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := topicsRepo.GetByID(ctx, plain.ID, &userID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !got.NotifyOnly {
		t.Errorf("Update did not persist NotifyOnly = true")
	}
	if got.NotifyOnlyAnnounceCurrent {
		t.Errorf("Update wrote NotifyOnlyAnnounceCurrent = true, want false")
	}
}

// TestMarkEpisodesDownloadedBulk exercises the SQL a mock cannot: to_jsonb over
// a text[] must CONCATENATE onto the existing downloaded_episodes array, in
// order, rather than nesting an array inside it. The notify-only watch mode
// (issue #184) depends on one statement for a tracker-chosen list length, and
// the whole point of that statement is that it is all-or-nothing.
func TestMarkEpisodesDownloadedBulk(t *testing.T) {
	pool := integrationPool(t)
	topicsRepo := NewTopics(pool)
	userID := seedUser(t, pool)
	ctx := context.Background()

	// Seed with one episode already marked, so the append is proven to extend
	// the existing array instead of replacing it.
	topic := seedTopic(t, pool, userID, domain.TopicStatusActive, map[string]any{
		"downloaded_episodes": []string{"1-1"},
		"quality":             "1080p",
	})

	if err := topicsRepo.MarkEpisodesDownloaded(ctx, topic, []string{"1-2", "1-3", "1-4"}); err != nil {
		t.Fatalf("MarkEpisodesDownloaded: %v", err)
	}
	got := reload(t, pool, topic.ID)
	want := []string{"1-1", "1-2", "1-3", "1-4"}
	marked, ok := got.Extra["downloaded_episodes"].([]any)
	if !ok {
		t.Fatalf("downloaded_episodes is %T, want a JSON array: %#v",
			got.Extra["downloaded_episodes"], got.Extra["downloaded_episodes"])
	}
	if len(marked) != len(want) {
		t.Fatalf("downloaded_episodes = %v, want %v", marked, want)
	}
	for i := range want {
		if marked[i] != want[i] {
			t.Errorf("downloaded_episodes[%d] = %v, want %q", i, marked[i], want[i])
		}
	}
	// Sibling keys must survive — this is a targeted append, not a blob write.
	if got.Extra["quality"] != "1080p" {
		t.Errorf("quality = %v, want it untouched", got.Extra["quality"])
	}

	// A stale token must write nothing at all: the whole list is discarded,
	// leaving no partial state for the next tick to re-announce.
	stale := *got
	staleNext := got.NextCheckAt.Add(-time.Hour)
	stale.NextCheckAt = staleNext
	if err := topicsRepo.MarkEpisodesDownloaded(ctx, &stale, []string{"2-1", "2-2"}); !errors.Is(err, ErrStaleCheckResult) {
		t.Fatalf("stale bulk mark: want ErrStaleCheckResult, got %v", err)
	}
	after := reload(t, pool, topic.ID)
	if n := len(after.Extra["downloaded_episodes"].([]any)); n != len(want) {
		t.Errorf("a rejected bulk mark wrote %d episodes, want the original %d", n, len(want))
	}
}
