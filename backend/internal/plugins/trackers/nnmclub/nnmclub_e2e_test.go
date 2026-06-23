package nnmclub

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

func TestE2E(t *testing.T) {
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "nnmclub/anonymous-magnet-then-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/forum/viewtopic.php"):
					w.WriteHeader(200)
					_, _ = w.Write([]byte(fixtureViewtopicHTML))
				case strings.HasPrefix(r.URL.Path, "/forum/login.php"):
					w.WriteHeader(200)
					_, _ = w.Write([]byte(`<a href="logout.php">logout</a>`))
				case r.URL.Path == "/forum/index.php":
					w.WriteHeader(200)
					_, _ = w.Write([]byte(`<a href="logout.php">logout</a>`))
				default:
					w.WriteHeader(404)
				}
			}))
			t.Cleanup(srv.Close)

			testHost := strings.TrimPrefix(srv.URL, "http://")
			p := &plugin{
				sessions: forumcommon.New(),
				domain:   "nnmclub.to",
				transport: &e2etest.HostRewriteTransport{
					From: "nnmclub.to",
					To:   testHost,
				},
			}
			return p, "https://nnmclub.to/forum/viewtopic.php?t=420880"
		},
		// Anonymous Phase 1: no creds, magnet payload expected.
		ExpectedHash:      "094ec3052ed759240e4dfd89f3f7ca5c5b428ff4",
		ExpectTorrentFile: false,
	})
}
