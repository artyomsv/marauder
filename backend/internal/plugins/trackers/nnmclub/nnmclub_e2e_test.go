package nnmclub

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

const e2eViewtopicHTML = `<html><head><title>Big Anime Series :: NNM-Club</title></head>
<body>
<a href="logout.php">logout</a>
<div>Info-Hash: 0123456789ABCDEF0123456789ABCDEF01234567</div>
<a href="download.php?id=12345">скачать</a>
</body></html>`

func TestE2E(t *testing.T) {
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "nnmclub/login-then-torrent-then-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/forum/login.php"):
					http.SetCookie(w, &http.Cookie{Name: "phpbb2mysql_4_data", Value: "abc"})
					w.WriteHeader(200)
					_, _ = w.Write([]byte(`<a href="logout.php">logout</a>`))
				case strings.HasPrefix(r.URL.Path, "/forum/viewtopic.php"):
					w.WriteHeader(200)
					_, _ = w.Write([]byte(e2eViewtopicHTML))
				case strings.HasPrefix(r.URL.Path, "/forum/download.php"):
					w.Header().Set("Content-Type", "application/x-bittorrent")
					w.WriteHeader(200)
					_, _ = w.Write([]byte("d8:announce15:http://x/announcee"))
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
			return p, "https://nnmclub.to/forum/viewtopic.php?t=12345"
		},
		Creds: &domain.TrackerCredential{
			UserID:    uuid.New(),
			Username:  "alice",
			SecretEnc: []byte("password123"),
		},
		ExpectedHash:         "0123456789abcdef0123456789abcdef01234567",
		ExpectedNameContains: "Big Anime Series",
	})
}
