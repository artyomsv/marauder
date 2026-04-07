package e2etest

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"

	// Force-register the qBittorrent plugin so the runner can fetch it
	// from the global registry. Tracker e2e tests blank-import this
	// package, which transitively pulls qBittorrent in.
	_ "github.com/artyomsv/marauder/backend/internal/plugins/clients/qbittorrent"
)

// Case is what a per-tracker e2e test passes to RunFullPipeline.
//
// Setup is called once at the start of the test, with the test's
// QBitFake already running. The test should:
//   - stand up a fake httptest.Server simulating the tracker
//   - construct the plugin pointed at that server
//   - return the plugin and the canonical topic URL the test will use
//
// The runner takes care of Parse -> Login -> Verify -> Check ->
// Download -> qbittorrent.Add -> assertions.
type Case struct {
	// Name is the human-readable test name. Defaults to the plugin's Name().
	Name string

	// Setup constructs the plugin + topic URL. The fake tracker server
	// should be created here and registered with t.Cleanup. The QBit
	// fake is provided so Setup can wire creds if needed (it usually
	// doesn't have to).
	Setup func(t *testing.T, qbit *QBitFake) (plugin registry.Tracker, topicURL string)

	// Creds, if non-nil, will be passed to Login/Verify/Check/Download.
	// Use this for any tracker that implements WithCredentials.
	Creds *domain.TrackerCredential

	// ExpectedHash is asserted equal to the Check result's Hash.
	// Empty string means "any non-empty hash".
	ExpectedHash string

	// ExpectedNameContains, if non-empty, must be a substring of the
	// Check result's DisplayName.
	ExpectedNameContains string

	// SkipQBitSubmit suppresses the final "submit to qBittorrent" step.
	// Useful for trackers whose Download path is intentionally a stub
	// (e.g., lostfilm in v1.0).
	SkipQBitSubmit bool
}

// RunFullPipeline drives a tracker plugin from URL parse all the way
// to a torrent landing in a fake qBittorrent. Use it from each
// tracker's `<name>_e2e_test.go` like:
//
//	func TestE2E(t *testing.T) {
//	    e2etest.RunFullPipeline(t, e2etest.Case{
//	        Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
//	            srv := httptest.NewServer(...)
//	            t.Cleanup(srv.Close)
//	            p := newTestPlugin(srv)
//	            return p, srv.URL + "/topic/123"
//	        },
//	        ExpectedHash: "0123456789abcdef...",
//	    })
//	}
func RunFullPipeline(t *testing.T, c Case) {
	t.Helper()
	name := c.Name
	if name == "" {
		name = "tracker_e2e"
	}

	t.Run(name, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		qbit := NewQBitFake(t)

		plugin, topicURL := c.Setup(t, qbit)
		if plugin == nil {
			t.Fatal("Setup returned nil plugin")
		}
		if topicURL == "" {
			t.Fatal("Setup returned empty topicURL")
		}

		// 1. CanParse
		if !plugin.CanParse(topicURL) {
			t.Fatalf("CanParse(%q) returned false", topicURL)
		}

		// 2. Parse
		topic, err := plugin.Parse(ctx, topicURL)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if topic == nil {
			t.Fatal("Parse returned nil topic")
		}
		if topic.URL != topicURL {
			t.Errorf("topic.URL = %q, want %q", topic.URL, topicURL)
		}
		if topic.TrackerName != plugin.Name() {
			t.Errorf("topic.TrackerName = %q, want %q", topic.TrackerName, plugin.Name())
		}

		// 3. Login + Verify (only if WithCredentials)
		if wc, ok := plugin.(registry.WithCredentials); ok && c.Creds != nil {
			if err := wc.Login(ctx, c.Creds); err != nil {
				t.Fatalf("Login: %v", err)
			}
			ok, err := wc.Verify(ctx, c.Creds)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if !ok {
				t.Errorf("Verify returned false after a successful Login")
			}
		}

		// 4. Check
		check, err := plugin.Check(ctx, topic, c.Creds)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if check.Hash == "" {
			t.Error("Check returned empty Hash")
		}
		if c.ExpectedHash != "" && check.Hash != c.ExpectedHash {
			t.Errorf("Check Hash = %q, want %q", check.Hash, c.ExpectedHash)
		}
		if c.ExpectedNameContains != "" && !strings.Contains(check.DisplayName, c.ExpectedNameContains) {
			t.Errorf("Check DisplayName = %q, want substring %q", check.DisplayName, c.ExpectedNameContains)
		}

		// 5. Download
		payload, err := plugin.Download(ctx, topic, check, c.Creds)
		if err != nil {
			t.Fatalf("Download: %v", err)
		}
		if payload == nil {
			t.Fatal("Download returned nil payload")
		}
		if payload.MagnetURI == "" && len(payload.TorrentFile) == 0 {
			t.Error("Download returned empty payload (no magnet, no torrent bytes)")
		}

		if c.SkipQBitSubmit {
			return
		}

		// 6. Submit to fake qBittorrent
		client := registry.GetClient("qbittorrent")
		if client == nil {
			t.Fatal("qbittorrent client plugin not registered")
		}
		cfg, _ := json.Marshal(map[string]string{
			"url":      qbit.URL,
			"username": qbit.Username,
			"password": qbit.Password,
		})
		if err := client.Add(ctx, cfg, payload, domain.AddOptions{
			DownloadDir: "/downloads",
		}); err != nil {
			t.Fatalf("qbittorrent.Add: %v", err)
		}

		// 7. Assert the qBit fake received the right thing
		if qbit.AddCalls() != 1 {
			t.Errorf("qBit add calls = %d, want 1", qbit.AddCalls())
		}
		last := qbit.Last()
		if payload.MagnetURI != "" && last.Magnet != payload.MagnetURI {
			t.Errorf("qBit received magnet = %q, want %q", last.Magnet, payload.MagnetURI)
		}
		if len(payload.TorrentFile) > 0 && len(last.TorrentFile) != len(payload.TorrentFile) {
			t.Errorf("qBit received %d torrent bytes, want %d", len(last.TorrentFile), len(payload.TorrentFile))
		}
		if last.SavePath != "/downloads" {
			t.Errorf("qBit savepath = %q, want %q", last.SavePath, "/downloads")
		}
	})
}

// HostRewriteTransport rewrites outgoing request URLs from a production
// host to a test server host, and forces https->http. Tracker plugins
// hard-code the production hostname (e.g. "rutracker.org"); E2E tests
// install a HostRewriteTransport so the same plugin code transparently
// reaches a local httptest.Server while CanParse and the regex URL
// patterns continue to see the canonical hostname.
//
// If StripSubdomain is true, "X.From" hosts are also rewritten to To
// (so a Kinozal-style "dl.kinozal.tv" mirror is captured).
type HostRewriteTransport struct {
	From           string // production hostname (e.g. "rutracker.org")
	To             string // test server host:port (e.g. "127.0.0.1:34065")
	StripSubdomain bool
	Inner          http.RoundTripper // nil = http.DefaultTransport
}

// RoundTrip implements http.RoundTripper.
func (h *HostRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme == "https" {
		req.URL.Scheme = "http"
	}
	host := req.URL.Host
	if host == h.From {
		req.URL.Host = h.To
	} else if h.StripSubdomain && strings.HasSuffix(host, "."+h.From) {
		req.URL.Host = h.To
	}
	inner := h.Inner
	if inner == nil {
		inner = http.DefaultTransport
	}
	return inner.RoundTrip(req)
}

// SchemeRewrite is a no-frills https->http transport for tests where
// the plugin is configured with the test server's host directly (no
// host rewriting needed).
type SchemeRewrite struct{ StripDLSubdomain bool }

// RoundTrip implements http.RoundTripper.
func (s *SchemeRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme == "https" {
		req.URL.Scheme = "http"
	}
	if s.StripDLSubdomain && strings.HasPrefix(req.URL.Host, "dl.") {
		req.URL.Host = strings.TrimPrefix(req.URL.Host, "dl.")
	}
	return http.DefaultTransport.RoundTrip(req)
}
