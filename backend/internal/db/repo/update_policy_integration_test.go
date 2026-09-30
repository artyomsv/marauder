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

func TestDeliveriesLatestFiles(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	userID := seedUser(t, pool)
	topic := seedTopic(t, pool, userID, domain.TopicStatusActive, map[string]any{})
	d := NewDeliveries(pool)

	got, err := d.LatestFiles(ctx, topic.ID)
	if err != nil || got != nil {
		t.Fatalf("LatestFiles on a topic without deliveries = (%v, %v), want (nil, nil)", got, err)
	}

	older := []domain.TorrentFile{{Path: "E01.mkv", Size: 100}}
	newer := []domain.TorrentFile{{Path: "E01.mkv", Size: 100}, {Path: "E02.mkv", Size: 200}}
	for _, rec := range []*domain.TopicDelivery{
		{TopicID: topic.ID, Infohash: "aaaa", Label: "v1", Files: older},
		{TopicID: topic.ID, Infohash: "bbbb", Label: "v2", Files: newer},
		// A later magnet delivery has no file list and must not hide v2's.
		{TopicID: topic.ID, Infohash: "cccc", Label: "v3"},
	} {
		if _, err := d.Record(ctx, rec); err != nil {
			t.Fatalf("Record %s: %v", rec.Label, err)
		}
	}
	// Pin the order explicitly: rows written in one test can share a timestamp.
	for hash, age := range map[string]string{"aaaa": "3 hours", "bbbb": "2 hours", "cccc": "1 hour"} {
		if _, err := pool.Exec(ctx,
			`UPDATE topic_deliveries SET delivered_at = now() - $2::interval WHERE topic_id = $1 AND infohash = $3`,
			topic.ID, age, hash); err != nil {
			t.Fatalf("age %s: %v", hash, err)
		}
	}

	got, err = d.LatestFiles(ctx, topic.ID)
	if err != nil {
		t.Fatalf("LatestFiles: %v", err)
	}
	if !reflect.DeepEqual(got, newer) {
		t.Errorf("LatestFiles = %+v, want the newest non-NULL list %+v", got, newer)
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
