//go:build integration

package repo

import (
	"context"
	"reflect"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// The two flags are DIFFERENT in each write so a transposed column or
// argument cannot pass unnoticed.
func TestTopicsUpdatePolicyRoundTrip(t *testing.T) {
	pool := integrationPool(t)
	topicsRepo := NewTopics(pool)
	userID := seedUser(t, pool)
	ctx := context.Background()

	topic := seedTopic(t, pool, userID, domain.TopicStatusActive, map[string]any{})
	if topic.AddPausedOnUpdate || topic.OnlyNewFiles {
		t.Fatalf("defaults = (%v, %v), want both false", topic.AddPausedOnUpdate, topic.OnlyNewFiles)
	}

	if _, err := topicsRepo.Update(ctx, topic.ID, userID, topic.DisplayName, nil, nil, "", "", nil,
		TopicFlags{AddPausedOnUpdate: true, OnlyNewFiles: false}, map[string]any{}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got := reload(t, pool, topic.ID)
	if !got.AddPausedOnUpdate {
		t.Error("stored AddPausedOnUpdate = false, want true")
	}
	if got.OnlyNewFiles {
		t.Error("stored OnlyNewFiles = true, want false")
	}

	if _, err := topicsRepo.Update(ctx, topic.ID, userID, topic.DisplayName, nil, nil, "", "", nil,
		TopicFlags{AddPausedOnUpdate: false, OnlyNewFiles: true}, map[string]any{}); err != nil {
		t.Fatalf("Update 2: %v", err)
	}
	got = reload(t, pool, topic.ID)
	if got.AddPausedOnUpdate || !got.OnlyNewFiles {
		t.Errorf("stored = (%v, %v), want (false, true)", got.AddPausedOnUpdate, got.OnlyNewFiles)
	}
}

// LatestFiles is the only-new-files baseline: the newest delivery of ANOTHER
// infohash, and no baseline at all when that row has no file list.
func TestDeliveriesLatestFiles(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	userID := seedUser(t, pool)
	topic := seedTopic(t, pool, userID, domain.TopicStatusActive, map[string]any{})
	d := NewDeliveries(pool)

	got, err := d.LatestFiles(ctx, topic.ID, "zzzz")
	if err != nil || got != nil {
		t.Fatalf("LatestFiles on a topic without deliveries = (%v, %v), want (nil, nil)", got, err)
	}

	older := []domain.TorrentFile{{Path: "E01.mkv", Size: 100}}
	newer := []domain.TorrentFile{{Path: "E01.mkv", Size: 100}, {Path: "E02.mkv", Size: 200}}
	record := func(rec *domain.TopicDelivery, age string) {
		t.Helper()
		if _, err := d.Record(ctx, rec); err != nil {
			t.Fatalf("Record %s: %v", rec.Label, err)
		}
		// Pin the order explicitly: rows written in one test can share a timestamp.
		if _, err := pool.Exec(ctx,
			`UPDATE topic_deliveries SET delivered_at = now() - $2::interval WHERE topic_id = $1 AND infohash = $3`,
			topic.ID, age, rec.Infohash); err != nil {
			t.Fatalf("age %s: %v", rec.Infohash, err)
		}
	}
	record(&domain.TopicDelivery{TopicID: topic.ID, Infohash: "aaaa", Label: "v1", Files: older}, "3 hours")
	record(&domain.TopicDelivery{TopicID: topic.ID, Infohash: "bbbb", Label: "v2", Files: newer}, "2 hours")

	latest := func(exclude string) []domain.TorrentFile {
		t.Helper()
		files, err := d.LatestFiles(ctx, topic.ID, exclude)
		if err != nil {
			t.Fatalf("LatestFiles(exclude %s): %v", exclude, err)
		}
		return files
	}
	if got := latest("zzzz"); !reflect.DeepEqual(got, newer) {
		t.Errorf("LatestFiles = %+v, want the newest list %+v", got, newer)
	}
	// A retried delivery of v2 finds its own row already recorded; comparing
	// v2 with itself would skip every file, so that row must be passed over.
	if got := latest("bbbb"); !reflect.DeepEqual(got, older) {
		t.Errorf("LatestFiles(exclude bbbb) = %+v, want the older list %+v", got, older)
	}

	// A later magnet delivery has no file list: the previous version's files
	// are unknown, so there is no baseline — not a fallback to v2's list.
	record(&domain.TopicDelivery{TopicID: topic.ID, Infohash: "cccc", Label: "v3"}, "1 hour")
	if got := latest("zzzz"); got != nil {
		t.Errorf("LatestFiles after a magnet delivery = %+v, want nil (no baseline)", got)
	}
	if got := latest("cccc"); !reflect.DeepEqual(got, newer) {
		t.Errorf("LatestFiles(exclude cccc) = %+v, want %+v", got, newer)
	}

	var files []byte
	if err := pool.QueryRow(ctx,
		`SELECT files FROM topic_deliveries WHERE topic_id = $1 AND infohash = 'cccc'`, topic.ID).Scan(&files); err != nil {
		t.Fatalf("read magnet row: %v", err)
	}
	if files != nil {
		t.Errorf("magnet delivery files = %s, want NULL", files)
	}
}

// SetFiles stores the list of exactly one delivery — the topic and infohash
// named — and a stored list becomes the next update's baseline.
func TestDeliveriesSetFiles(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	userID := seedUser(t, pool)
	topic := seedTopic(t, pool, userID, domain.TopicStatusActive, map[string]any{})
	other := seedTopic(t, pool, userID, domain.TopicStatusActive, map[string]any{})
	d := NewDeliveries(pool)

	// Both topics have a NULL row under the same infohash, and the first topic
	// has a second NULL row: only one of the three may change.
	for _, rec := range []*domain.TopicDelivery{
		{TopicID: topic.ID, Infohash: "aaaa", Label: "v1"},
		{TopicID: topic.ID, Infohash: "bbbb", Label: "v2"},
		{TopicID: other.ID, Infohash: "bbbb", Label: "other v2"},
	} {
		if _, err := d.Record(ctx, rec); err != nil {
			t.Fatalf("Record %s: %v", rec.Label, err)
		}
	}
	// Pin the order: aaaa is older, so bbbb is the topic's newest row.
	if _, err := pool.Exec(ctx,
		`UPDATE topic_deliveries SET delivered_at = now() - interval '1 hour' WHERE topic_id = $1 AND infohash = 'aaaa'`,
		topic.ID); err != nil {
		t.Fatalf("age aaaa: %v", err)
	}

	files := []domain.TorrentFile{{Path: "E01.mkv", Size: 100}, {Path: "E02.mkv", Size: 200}}
	if err := d.SetFiles(ctx, topic.ID, "bbbb", files); err != nil {
		t.Fatalf("SetFiles: %v", err)
	}

	stored := func(topicID any, hash string) []byte {
		t.Helper()
		var raw []byte
		if err := pool.QueryRow(ctx,
			`SELECT files FROM topic_deliveries WHERE topic_id = $1 AND infohash = $2`, topicID, hash).Scan(&raw); err != nil {
			t.Fatalf("read %s: %v", hash, err)
		}
		return raw
	}
	if raw := stored(topic.ID, "aaaa"); raw != nil {
		t.Errorf("other infohash files = %s, want NULL", raw)
	}
	if raw := stored(other.ID, "bbbb"); raw != nil {
		t.Errorf("other topic's row files = %s, want NULL", raw)
	}
	if got, err := d.LatestFiles(ctx, topic.ID, "zzzz"); err != nil || !reflect.DeepEqual(got, files) {
		// bbbb is the topic's newest row, so its stored list is the baseline.
		t.Errorf("LatestFiles = (%+v, %v), want (%+v, nil)", got, err, files)
	}
}
