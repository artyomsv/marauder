package generictorrentfile

import (
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

func TestE2E(t *testing.T) {
	body := []byte("d8:announce26:http://tracker.example.com/4:infod6:lengthi42e4:name8:test.txte")
	sum := sha1.Sum(body)
	expectedHash := hex.EncodeToString(sum[:])

	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "generictorrentfile/sha1-roundtrips-to-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/x-bittorrent")
				_, _ = w.Write(body)
			}))
			t.Cleanup(srv.Close)

			p := &plugin{httpClient: &http.Client{Timeout: 5 * time.Second}}
			return p, srv.URL + "/marauder-e2e.torrent"
		},
		ExpectedHash:         expectedHash,
		ExpectedNameContains: "marauder-e2e",
	})
}
