//go:build integration

package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// capabilityExtra is the capability half of a topic's extra blob — the keys a
// reset must preserve. Returned fresh each call so a test can mutate its copy.
func capabilityExtra() map[string]any {
	return map[string]any{
		"quality":       "1080p",
		"start_season":  2,
		"start_episode": 5,
		"source":        "sonarr",
	}
}

// extraWithEpisodes is a populated topic blob: capability keys plus the
// per-episode progress a reset must drop.
func extraWithEpisodes() map[string]any {
	e := capabilityExtra()
	e["downloaded_episodes"] = []string{"s01e01", "s01e02"}
	return e
}

// assertCapabilityKeysSurvived checks that the targeted JSONB key delete
// removed downloaded_episodes and nothing else. Numbers come back from JSONB
// as float64, which is why start_season is not compared against an int.
func assertCapabilityKeysSurvived(t *testing.T, extra map[string]any) {
	t.Helper()
	if got := extra["quality"]; got != "1080p" {
		t.Errorf("quality: want %q, got %v", "1080p", got)
	}
	if got, ok := extra["start_season"].(float64); !ok || got != 2 {
		t.Errorf("start_season: want 2, got %v (%T)", extra["start_season"], extra["start_season"])
	}
	if got, ok := extra["start_episode"].(float64); !ok || got != 5 {
		t.Errorf("start_episode: want 5, got %v (%T)", extra["start_episode"], extra["start_episode"])
	}
	if got := extra["source"]; got != "sonarr" {
		t.Errorf("source: want %q, got %v", "sonarr", got)
	}
	if _, present := extra["downloaded_episodes"]; present {
		t.Errorf("downloaded_episodes survived the reset: %v", extra["downloaded_episodes"])
	}
}

// TestIntegration_ResetCheckState_DropsOnlyDownloadedEpisodes proves the
// `extra = COALESCE(extra,'{}'::jsonb) - 'downloaded_episodes'` key delete does
// what its name says. A mock can only confirm the string is in the statement;
// if it were ever "simplified" to an assignment of '{}'::jsonb, or the key were
// misspelled, every topic would lose its quality/season/source configuration on
// reset and no pgxmock test would notice.
func TestIntegration_ResetCheckState_DropsOnlyDownloadedEpisodes(t *testing.T) {
	pool := integrationPool(t)
	userID := seedUser(t, pool)
	topics := NewTopics(pool)
	ctx := context.Background()

	topic := seedTopic(t, pool, userID, domain.TopicStatusActive, extraWithEpisodes())
	if err := topics.ResetCheckState(ctx, topic.ID, userID); err != nil {
		t.Fatalf("ResetCheckState: %v", err)
	}

	got := reload(t, pool, topic.ID)
	assertCapabilityKeysSurvived(t, got.Extra)
}

// TestIntegration_ResetCheckState_EmptyExtraStaysEmpty covers the COALESCE
// half: a topic with no episode progress must survive the same statement
// rather than trip over a NULL/absent key.
func TestIntegration_ResetCheckState_EmptyExtraStaysEmpty(t *testing.T) {
	pool := integrationPool(t)
	userID := seedUser(t, pool)
	ctx := context.Background()

	topic := seedTopic(t, pool, userID, domain.TopicStatusActive, map[string]any{})
	if err := NewTopics(pool).ResetCheckState(ctx, topic.ID, userID); err != nil {
		t.Fatalf("ResetCheckState: %v", err)
	}
	if got := reload(t, pool, topic.ID); len(got.Extra) != 0 {
		t.Errorf("want an empty extra blob, got %v", got.Extra)
	}
}

// TestIntegration_ResetCheckState_StatusCase proves the
// `CASE WHEN status = 'paused' THEN 'paused' ELSE 'active' END` against real
// rows in each state. Resetting must never silently resume a topic the user
// deliberately stopped — which matters most under a bulk reset over a mixed
// selection, where the user cannot see per-topic status while confirming.
func TestIntegration_ResetCheckState_StatusCase(t *testing.T) {
	pool := integrationPool(t)
	userID := seedUser(t, pool)
	topics := NewTopics(pool)
	ctx := context.Background()

	tests := []struct {
		name  string
		start domain.TopicStatus
		want  domain.TopicStatus
	}{
		{"paused stays paused", domain.TopicStatusPaused, domain.TopicStatusPaused},
		{"errored is normalised back to active", domain.TopicStatusError, domain.TopicStatusActive},
		{"active stays active", domain.TopicStatusActive, domain.TopicStatusActive},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			topic := seedTopic(t, pool, userID, tt.start, capabilityExtra())
			if err := topics.ResetCheckState(ctx, topic.ID, userID); err != nil {
				t.Fatalf("ResetCheckState: %v", err)
			}
			if got := reload(t, pool, topic.ID).Status; got != tt.want {
				t.Errorf("status: want %q, got %q", tt.want, got)
			}
		})
	}
}

// TestIntegration_ResetCheckState_ScopedToOwner proves the WHERE user_id
// clause: another user's reset must not touch the row, and must report
// ErrNotFound rather than a silent success.
func TestIntegration_ResetCheckState_ScopedToOwner(t *testing.T) {
	pool := integrationPool(t)
	owner, stranger := seedUser(t, pool), seedUser(t, pool)
	ctx := context.Background()

	topic := seedTopic(t, pool, owner, domain.TopicStatusActive, extraWithEpisodes())
	err := NewTopics(pool).ResetCheckState(ctx, topic.ID, stranger)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound for a non-owner, got %v", err)
	}
	if got := reload(t, pool, topic.ID); got.Extra["downloaded_episodes"] == nil {
		t.Error("a non-owner's reset cleared the episode progress")
	}
}

// TestIntegration_CheckStateToken_NullSemantics is the reason this suite
// exists. RecordCheckResult and MarkEpisodeDownloaded guard on
// `last_checked_at IS NOT DISTINCT FROM $n AND next_check_at = $m`.
//
// `IS NOT DISTINCT FROM` is load-bearing and not interchangeable with `=`:
// ResetCheckState sets last_checked_at to NULL, and `NULL = NULL` is never
// true, so under `=` the fresh post-reset check — the one the reset exists to
// trigger — would match zero rows and silently discard its own result. The
// topic would then never record a hash, and every subsequent check would
// re-deliver the same release forever. No pgxmock test can see this, because
// no SQL is executed.
func TestIntegration_CheckStateToken_NullSemantics(t *testing.T) {
	pool := integrationPool(t)
	userID := seedUser(t, pool)
	topics := NewTopics(pool)
	ctx := context.Background()

	topic := seedTopic(t, pool, userID, domain.TopicStatusActive, extraWithEpisodes())

	// A worker dispatched at seed time observes (NULL, next_check_at) and
	// persists its result. NULL on the left of the guard already — this alone
	// fails under `=`.
	observed := *topic
	if err := topics.RecordCheckResult(ctx, &observed, "hash-1", true,
		time.Now().Add(time.Hour), "", ""); err != nil {
		t.Fatalf("first check must persist against a NULL last_checked_at: %v", err)
	}
	if got := reload(t, pool, topic.ID).LastHash; got != "hash-1" {
		t.Fatalf("last_hash: want %q, got %q", "hash-1", got)
	}

	// The same worker's token is now spent: replaying it must be discarded,
	// not applied on top of the newer state.
	err := topics.RecordCheckResult(ctx, &observed, "hash-stale", true,
		time.Now().Add(2*time.Hour), "", "")
	if !errors.Is(err, ErrStaleCheckResult) {
		t.Fatalf("want ErrStaleCheckResult replaying a spent token, got %v", err)
	}
	if got := reload(t, pool, topic.ID).LastHash; got != "hash-1" {
		t.Errorf("a stale write reached the row: last_hash is now %q", got)
	}

	// A reset lands, restoring last_checked_at to NULL and stamping a fresh
	// next_check_at.
	if err := topics.ResetCheckState(ctx, topic.ID, userID); err != nil {
		t.Fatalf("ResetCheckState: %v", err)
	}
	fresh := reload(t, pool, topic.ID)
	if fresh.LastCheckedAt != nil {
		t.Fatalf("reset must NULL last_checked_at, got %v", fresh.LastCheckedAt)
	}

	// The check the reset queued observes that NULL. Its episode mark and its
	// result must both land — in this order, because marking an episode does
	// not consume the token but recording the result does.
	if err := topics.MarkEpisodeDownloaded(ctx, fresh, "s02e01"); err != nil {
		t.Fatalf("post-reset episode mark must persist against a NULL last_checked_at: %v", err)
	}
	if err := topics.RecordCheckResult(ctx, fresh, "hash-after-reset", true,
		time.Now().Add(time.Hour), "", ""); err != nil {
		t.Fatalf("post-reset check must persist against a NULL last_checked_at: %v", err)
	}

	after := reload(t, pool, topic.ID)
	if after.LastHash != "hash-after-reset" {
		t.Errorf("last_hash: want %q, got %q", "hash-after-reset", after.LastHash)
	}
	eps, _ := after.Extra["downloaded_episodes"].([]any)
	if len(eps) != 1 || eps[0] != "s02e01" {
		t.Errorf("downloaded_episodes: want exactly [s02e01] after the reset, got %v",
			after.Extra["downloaded_episodes"])
	}
}

// TestIntegration_VerifyCheckState_TracksTheSameToken proves the read-only
// pre-submit guard agrees with the writes it protects, against real rows. It is
// consulted immediately before a payload reaches a torrent client, so a drift
// between its WHERE clause and RecordCheckResult's would either wave through
// submissions the write then refuses to record, or block valid ones — and it
// carries the same `IS NOT DISTINCT FROM` NULL semantics, so `=` breaks it in
// the same way.
func TestIntegration_VerifyCheckState_TracksTheSameToken(t *testing.T) {
	pool := integrationPool(t)
	userID := seedUser(t, pool)
	topics := NewTopics(pool)
	ctx := context.Background()

	topic := seedTopic(t, pool, userID, domain.TopicStatusActive, extraWithEpisodes())

	// Freshly seeded: last_checked_at is NULL, so this is the `=` trap.
	if err := topics.VerifyCheckState(ctx, topic); err != nil {
		t.Fatalf("a NULL last_checked_at must verify: %v", err)
	}

	// A check lands, moving the token on.
	observed := *topic
	if err := topics.RecordCheckResult(ctx, &observed, "hash-1", true,
		time.Now().Add(time.Hour), "", ""); err != nil {
		t.Fatalf("RecordCheckResult: %v", err)
	}
	if err := topics.VerifyCheckState(ctx, &observed); !errors.Is(err, ErrStaleCheckResult) {
		t.Fatalf("want ErrStaleCheckResult for a spent token, got %v", err)
	}
	// ...and the current token verifies again.
	if err := topics.VerifyCheckState(ctx, reload(t, pool, topic.ID)); err != nil {
		t.Fatalf("the current token must verify: %v", err)
	}

	// A reset moves it on again, and the check the reset queued verifies.
	if err := topics.ResetCheckState(ctx, topic.ID, userID); err != nil {
		t.Fatalf("ResetCheckState: %v", err)
	}
	if err := topics.VerifyCheckState(ctx, reload(t, pool, topic.ID)); err != nil {
		t.Fatalf("the post-reset token must verify: %v", err)
	}

	// A deleted topic reads as stale rather than as an error, so the scheduler
	// treats "gone" the same as "moved on".
	fresh := reload(t, pool, topic.ID)
	if err := topics.Delete(ctx, topic.ID, userID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := topics.VerifyCheckState(ctx, fresh); !errors.Is(err, ErrStaleCheckResult) {
		t.Fatalf("want ErrStaleCheckResult for a deleted topic, got %v", err)
	}
}

// TestIntegration_ResetFlow_Composed exercises the whole reset as the handler
// drives it, over a mixed selection: seed populated topics, run a check on one,
// reset all three, then assert every property the feature depends on at once.
func TestIntegration_ResetFlow_Composed(t *testing.T) {
	pool := integrationPool(t)
	userID := seedUser(t, pool)
	topics := NewTopics(pool)
	ctx := context.Background()

	active := seedTopic(t, pool, userID, domain.TopicStatusActive, extraWithEpisodes())
	paused := seedTopic(t, pool, userID, domain.TopicStatusPaused, extraWithEpisodes())
	errored := seedTopic(t, pool, userID, domain.TopicStatusError, extraWithEpisodes())

	// A check runs against the active topic and records a failure, so the row
	// carries a hash, a last_checked_at, an error and a non-zero error count
	// for the reset to clear.
	observed := *active
	if err := topics.RecordCheckResult(ctx, &observed, "hash-before-reset", true,
		time.Now().Add(time.Hour), "tracker unreachable", "unreachable"); err != nil {
		t.Fatalf("RecordCheckResult: %v", err)
	}
	before := reload(t, pool, active.ID)
	if before.LastHash != "hash-before-reset" || before.ConsecutiveErrors != 1 {
		t.Fatalf("check result did not land: hash=%q errors=%d",
			before.LastHash, before.ConsecutiveErrors)
	}

	resetAt := time.Now()
	for _, topic := range []*domain.Topic{active, paused, errored} {
		if err := topics.ResetCheckState(ctx, topic.ID, userID); err != nil {
			t.Fatalf("ResetCheckState(%s): %v", topic.ID, err)
		}
	}

	// The reset topic is wiped back to "never checked" but keeps its config.
	got := reload(t, pool, active.ID)
	assertCapabilityKeysSurvived(t, got.Extra)
	if got.LastHash != "" {
		t.Errorf("last_hash: want cleared, got %q", got.LastHash)
	}
	if got.LastCheckedAt != nil || got.LastUpdatedAt != nil {
		t.Errorf("check timestamps not cleared: checked=%v updated=%v",
			got.LastCheckedAt, got.LastUpdatedAt)
	}
	if got.ConsecutiveErrors != 0 || got.LastError != "" || got.LastErrorCode != "" {
		t.Errorf("error state not cleared: n=%d err=%q code=%q",
			got.ConsecutiveErrors, got.LastError, got.LastErrorCode)
	}
	if got.CheckIntervalSec != active.CheckIntervalSec || got.DisplayName != active.DisplayName {
		t.Errorf("reset touched configuration: interval=%d name=%q",
			got.CheckIntervalSec, got.DisplayName)
	}
	// next_check_at = now() is what queues the immediate re-check DueForCheck
	// selects on; a surviving backoff would leave the topic idle for hours.
	if got.NextCheckAt.After(time.Now().Add(time.Minute)) || got.NextCheckAt.Before(resetAt.Add(-time.Minute)) {
		t.Errorf("next_check_at should be ~now, got %v", got.NextCheckAt)
	}

	// A mixed selection keeps each topic's intent.
	if s := reload(t, pool, paused.ID).Status; s != domain.TopicStatusPaused {
		t.Errorf("paused topic became %q", s)
	}
	if s := reload(t, pool, errored.ID).Status; s != domain.TopicStatusActive {
		t.Errorf("errored topic should be active, got %q", s)
	}
	if s := reload(t, pool, active.ID).Status; s != domain.TopicStatusActive {
		t.Errorf("active topic became %q", s)
	}

	// The check that was in flight when the reset landed is discarded...
	staleErr := topics.RecordCheckResult(ctx, &observed, "hash-from-stale-check", true,
		time.Now().Add(time.Hour), "", "")
	if !errors.Is(staleErr, ErrStaleCheckResult) {
		t.Fatalf("want ErrStaleCheckResult from the mid-check worker, got %v", staleErr)
	}
	if h := reload(t, pool, active.ID).LastHash; h != "" {
		t.Errorf("the stale check undid the reset: last_hash is %q", h)
	}

	// ...while the check the reset queued persists normally.
	if err := topics.RecordCheckResult(ctx, reload(t, pool, active.ID), "hash-after-reset", true,
		time.Now().Add(time.Hour), "", ""); err != nil {
		t.Fatalf("post-reset check must persist: %v", err)
	}
	if h := reload(t, pool, active.ID).LastHash; h != "hash-after-reset" {
		t.Errorf("last_hash: want %q, got %q", "hash-after-reset", h)
	}
}
