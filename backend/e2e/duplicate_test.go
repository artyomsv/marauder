//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// topicState reads GET /topics/{id} and returns the topic's status and last
// error. domain.Topic has no JSON tags, so the fields marshal as "Status" /
// "LastError".
func (m *marauderClient) topicState(t *testing.T, id string) (status, lastError string) {
	t.Helper()
	data := m.do(t, http.MethodGet, "/api/v1/topics/"+id, nil)
	var out struct {
		Status    string `json:"Status"`
		LastError string `json:"LastError"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode topic %s: %v (%s)", id, err, string(data))
	}
	return out.Status, out.LastError
}

// TestDuplicateDelivery is the e2e regression for issue #76: when a torrent
// with a given infohash is already present in qBittorrent, delivering the SAME
// infohash again must be an idempotent success — not a topic error.
//
// It forces a real duplicate add through the scheduler without DB surgery by
// creating two topics whose magnets share one infohash (differing only by the
// display-name `dn`, so they are distinct topic URLs). The second topic's
// delivery hits qBittorrent's duplicate signal (409, or 200 "Fails." depending
// on version); the fix must treat it as an idempotent success so the second
// topic records its delivery and stays active.
func TestDuplicateDelivery(t *testing.T) {
	env := readEnv(t) // skips if MARAUDER_BASE_URL unset

	m := newMarauder(env)
	m.Login(t)
	clientID := m.CreateQbitClient(t, "/downloads")

	q := newQbit(t, env)
	q.Login(t)

	ih := uniqueInfohash(t) // shared infohash for both topics

	// Topic A delivers the infohash first.
	topicA := m.CreateTopic(t, "magnet:?xt=urn:btih:"+ih+"&dn=dup-alpha", clientID, "dup-e2e")
	if !pollUntil(10*time.Second, 135*time.Second, func() bool {
		return m.StatusHasInfohash(t, topicA, ih)
	}) {
		t.Fatalf("topic A never delivered infohash %s", ih)
	}
	// Confirm it is actually present in qBittorrent before forcing the duplicate.
	if !pollUntil(2*time.Second, 15*time.Second, func() bool {
		_, ok := q.TorrentInfo(t, ih)
		return ok
	}) {
		t.Fatalf("infohash %s not present in qBittorrent after topic A delivered", ih)
	}

	// Topic B: a different magnet URL (dn) but the SAME infohash, so its delivery
	// is a duplicate add at the qBittorrent layer.
	topicB := m.CreateTopic(t, "magnet:?xt=urn:btih:"+ih+"&dn=dup-beta", clientID, "dup-e2e")

	// With the fix, the duplicate add is an idempotent success → topic B records
	// its delivery. Pre-fix, the duplicate errored and B recorded nothing.
	if !pollUntil(10*time.Second, 135*time.Second, func() bool {
		return m.StatusHasInfohash(t, topicB, ih)
	}) {
		status, lastErr := m.topicState(t, topicB)
		t.Fatalf("topic B (duplicate infohash) never recorded a delivery; status=%q last_error=%q", status, lastErr)
	}

	// And it must not have been parked in the error state by the duplicate.
	if status, lastErr := m.topicState(t, topicB); status == "error" {
		t.Errorf("topic B is in error state after a duplicate delivery: status=%q last_error=%q", status, lastErr)
	}
}
