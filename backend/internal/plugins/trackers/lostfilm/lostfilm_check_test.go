package lostfilm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/extra"
	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// TestCheck_ParsesSeasonsPageNotLanding is the regression guard for the bug
// where a mid-season "start from S05E01" floor silently skipped early
// episodes. The LostFilm series landing page only renders the most recent
// episodes (behind an "ВСЕ СЕРИИ (N)" expander); the full released catalog
// lives at /series/<slug>/seasons. Check must read the seasons page so the
// floor is applied against every released episode, not just the recent few.
func TestCheck_ParsesSeasonsPageNotLanding(t *testing.T) {
	// Landing page: only the 5 most recent episodes (S5 E4-E8) — what the
	// site actually serves at /series/<slug>/.
	landing := `<div data-code="442-5-4"></div><div data-code="442-5-5"></div>` +
		`<div data-code="442-5-6"></div><div data-code="442-5-7"></div>` +
		`<div data-code="442-5-8"></div>`
	// Seasons page: the FULL catalog (S5 E1-E8). If Check reads this, the
	// S05E01 floor yields 8 pending episodes; if it wrongly reads the
	// landing page it would yield only 5.
	seasons := `<div data-code="442-5-1"></div><div data-code="442-5-2"></div>` +
		`<div data-code="442-5-3"></div><div data-code="442-5-4"></div>` +
		`<div data-code="442-5-5"></div><div data-code="442-5-6"></div>` +
		`<div data-code="442-5-7"></div><div data-code="442-5-8"></div>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/seasons") {
			_, _ = w.Write([]byte(seasons))
			return
		}
		_, _ = w.Write([]byte(landing))
	}))
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(srv.URL, "http://")
	p := &plugin{
		sessions:          forumcommon.New(),
		domain:            "www.lostfilm.tv",
		transport:         &e2etest.HostRewriteTransport{From: "www.lostfilm.tv", To: host, Inner: &allHostsToTest{To: host}},
		redirectValidator: permissiveRedirectValidator,
	}

	topic := &domain.Topic{
		URL: "https://www.lostfilm.tv/series/The_Boys",
		Extra: map[string]any{
			"slug":                "The_Boys",
			"start_season":        5,
			"start_episode":       1,
			"downloaded_episodes": []string{},
		},
	}

	check, err := p.Check(context.Background(), topic, nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	pending := extra.StringSlice(check.Extra, "pending_episodes")
	if len(pending) != 8 {
		t.Fatalf("pending = %d (%v), want 8 — Check must read the full seasons page, not the landing page", len(pending), pending)
	}
	// The early episodes the landing page omits must be present and ordered first.
	if pending[0] != "442005001" || pending[2] != "442005003" {
		t.Errorf("pending head = %v, want S05E01..E03 first", pending[:3])
	}
}
