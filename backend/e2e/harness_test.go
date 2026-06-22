//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"testing"
	"time"
)

type config struct {
	BaseURL      string
	AdminUser    string
	AdminPass    string
	QbitURL      string
	QbitUser     string
	QbitPassword string
}

// readEnv loads the e2e config from the environment. If MARAUDER_BASE_URL is
// unset the whole suite is skipped, so a stray local `go test -tags=e2e` is
// inert rather than a hard failure.
func readEnv(t *testing.T) config {
	t.Helper()
	base := os.Getenv("MARAUDER_BASE_URL")
	if base == "" {
		t.Skip("MARAUDER_BASE_URL unset; skipping full-stack e2e")
	}
	return config{
		BaseURL:      base,
		AdminUser:    envOr("MARAUDER_ADMIN_USER", "admin"),
		AdminPass:    envOr("MARAUDER_ADMIN_PASS", "pleasechangeme"),
		QbitURL:      envOr("QBIT_URL", "http://qbittorrent:6611"),
		QbitUser:     envOr("QBIT_USER", "admin"),
		QbitPassword: os.Getenv("QBIT_PASSWORD"),
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// uniqueInfohash returns an obviously-synthetic 40-hex-char infohash that is
// stable per test name, so multiple table rows do not collide in qBittorrent.
// The "deadbeef" prefix marks it as a test value; it is never a real torrent.
func uniqueInfohash(t *testing.T) string {
	t.Helper()
	var sum uint32
	for _, r := range t.Name() {
		sum = sum*31 + uint32(r)
	}
	return fmt.Sprintf("deadbeef%032x", sum)
}

func magnet(infohash string) string {
	return "magnet:?xt=urn:btih:" + infohash + "&dn=marauder-e2e"
}

// pollUntil calls fn every interval until it returns true or timeout elapses.
func pollUntil(interval, timeout time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if fn() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(interval)
	}
}
