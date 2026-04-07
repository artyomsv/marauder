package lostfilm

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

const e2eSeriesHTML = `<html>
<head><title>The Series :: LostFilm.tv</title></head>
<body>
<a href="logout.php">logout</a>
<div class="series-block" data-episode="42">Episode 42</div>
<div class="series-block" data-episode="41">Episode 41</div>
<div class="series-block" data-episode="40">Episode 40</div>
<a href="magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&amp;dn=The.Series.S01E42">magnet</a>
</body>
</html>`

func TestE2E(t *testing.T) {
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "lostfilm/login-then-check-then-magnet-then-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/ajaxik.php"):
					w.WriteHeader(200)
					_, _ = w.Write([]byte(`{"success":true}`))
				case strings.HasPrefix(r.URL.Path, "/series/"):
					w.WriteHeader(200)
					_, _ = w.Write([]byte(e2eSeriesHTML))
				case r.URL.Path == "/my":
					w.WriteHeader(200)
					_, _ = w.Write([]byte(`<a href="logout">logout</a>`))
				default:
					w.WriteHeader(404)
				}
			}))
			t.Cleanup(srv.Close)

			testHost := strings.TrimPrefix(srv.URL, "http://")
			p := &plugin{
				sessions: forumcommon.New(),
				domain:   "www.lostfilm.tv",
				transport: &e2etest.HostRewriteTransport{
					From: "www.lostfilm.tv",
					To:   testHost,
				},
			}
			return p, "https://www.lostfilm.tv/series/the-series/"
		},
		Creds: &domain.TrackerCredential{
			UserID:    uuid.New(),
			Username:  "alice",
			SecretEnc: []byte("password"),
		},
		ExpectedHash:         "ep-42",
		ExpectedNameContains: "The Series",
	})
}
