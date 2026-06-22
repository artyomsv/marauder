//go:build e2e

package e2e

import (
	"testing"
	"time"
)

// TestDelivery drives the full pipeline: create a qBittorrent client and a
// magnet topic with a category, wait for the scheduler to push it, then assert
// the delivery was recorded (via Marauder) and that qBittorrent set the native
// category and save path (issue #75).
func TestDelivery(t *testing.T) {
	env := readEnv(t) // skips if MARAUDER_BASE_URL unset

	m := newMarauder(env)
	m.Login(t)
	clientID := m.CreateQbitClient(t, "/downloads")

	q := newQbit(t, env)
	q.Login(t)

	cases := []struct {
		name             string
		category         string
		wantSavePath     string
		wantQbitCategory string
	}{
		{
			name:             "category sets qbit label and nests save path",
			category:         "sonarr-e2e",
			wantSavePath:     "/downloads/sonarr-e2e",
			wantQbitCategory: "sonarr-e2e",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			infohash := uniqueInfohash(t)
			topicID := m.CreateTopic(t, magnet(infohash), clientID, tc.category)

			// Delivery: poll Marauder's status API until the scheduler pushes
			// the torrent (real tick, bounded at 135s).
			if !pollUntil(10*time.Second, 135*time.Second, func() bool {
				return m.StatusHasInfohash(t, topicID, infohash)
			}) {
				t.Fatalf("torrent %s never appeared in topic %s status", infohash, topicID)
			}

			// Category + save path: read back from qBittorrent directly.
			var info qbitTorrent
			if !pollUntil(2*time.Second, 15*time.Second, func() bool {
				var ok bool
				info, ok = q.TorrentInfo(t, infohash)
				return ok
			}) {
				t.Fatalf("torrent %s not found in qBittorrent", infohash)
			}
			if info.Category != tc.wantQbitCategory {
				t.Errorf("qBit category = %q, want %q", info.Category, tc.wantQbitCategory)
			}
			if info.SavePath != tc.wantSavePath {
				t.Errorf("qBit save_path = %q, want %q", info.SavePath, tc.wantSavePath)
			}
		})
	}
}
