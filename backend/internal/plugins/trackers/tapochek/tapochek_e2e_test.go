package tapochek

import (
	"context"
	"crypto/tls"
	"net"
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

// newE2EPlugin points a plugin at srv while the request URL keeps saying
// https://tapochek.net, so CanParse, the host guard and every built URL stay
// real.
//
// It redirects the DIAL rather than rewriting req.URL, unlike the shared
// e2etest.HostRewriteTransport. That matters here specifically: net/http
// reads and writes the cookie jar keyed on req.URL, so a transport that
// rewrites the URL in place files bb_data under 127.0.0.1 while the plugin
// looks it up under tapochek.net — and every session assertion would then
// fail for a reason that does not exist in production.
func newE2EPlugin(t *testing.T, h http.HandlerFunc) *plugin {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "https://")
	return &plugin{
		sessions: forumcommon.New(),
		domain:   defaultDomain,
		transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // httptest's self-signed cert
		},
	}
}

// e2eHandler serves the shape the live site serves: a login that answers 302
// with an empty body and a bb_data cookie, and a topic page carrying the
// torrent block.
func e2eHandler(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/login.php"):
		// A successful login sets bb_data and redirects to the index with
		// NO body — there is nothing to match on success, which is why the
		// cookie is the authority.
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: userCookie, Path: "/"})
		http.Redirect(w, r, "https://"+defaultDomain+"/index.php", http.StatusFound)
	case strings.HasPrefix(r.URL.Path, "/index.php"):
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: userCookie, Path: "/"})
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<html><body>ok</body></html>`))
	case strings.HasPrefix(r.URL.Path, "/viewtopic.php"):
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fixtureTopicHTML))
	case strings.HasPrefix(r.URL.Path, "/download.php"):
		w.Header().Set("Content-Type", "application/x-bittorrent")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(fixtureTorrentBytes)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestE2E(t *testing.T) {
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "tapochek/login-then-torrent-then-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			return newE2EPlugin(t, e2eHandler), "https://tapochek.net/viewtopic.php?t=289113"
		},
		Creds: &domain.TrackerCredential{
			UserID:    uuid.New(),
			Username:  "alice",
			SecretEnc: []byte("password"),
		},
		// The change token, not an infohash: Tapochek publishes none, so this
		// is the digest of the torrent block's stable fields.
		ExpectedHash:         pageFingerprint(fixtureTorrentBlock),
		ExpectedNameContains: "Lady Death Demonicron",
	})
}
