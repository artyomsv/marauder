package lostfilm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

func TestSeasonCatalog_GroupsBySeason(t *testing.T) {
	html := `<div data-code="442-1-1"></div><div data-code="442-1-2"></div>` +
		`<div data-code="442-2-1"></div><div data-code="442-2-2"></div><div data-code="442-2-3"></div>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/seasons") {
			_, _ = w.Write([]byte(html))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	p := &plugin{
		sessions:          forumcommon.New(),
		domain:            "www.lostfilm.tv",
		transport:         &e2etest.HostRewriteTransport{From: "www.lostfilm.tv", To: host, Inner: &allHostsToTest{To: host}},
		redirectValidator: permissiveRedirectValidator,
	}
	seasons, err := p.SeasonCatalog(context.Background(), "https://www.lostfilm.tv/series/The_Boys")
	if err != nil {
		t.Fatalf("SeasonCatalog: %v", err)
	}
	if len(seasons) != 2 {
		t.Fatalf("seasons = %d, want 2", len(seasons))
	}
	if seasons[0].Number != 1 || len(seasons[0].Episodes) != 2 {
		t.Errorf("season1 = %+v, want {1,[1 2]}", seasons[0])
	}
	if seasons[1].Number != 2 || len(seasons[1].Episodes) != 3 {
		t.Errorf("season2 = %+v, want {2,[1 2 3]}", seasons[1])
	}
}

func TestSeasonCatalog_RejectsNonSeriesURL(t *testing.T) {
	p := &plugin{sessions: forumcommon.New(), domain: "www.lostfilm.tv"}
	if _, err := p.SeasonCatalog(context.Background(), "https://example.com/x"); err == nil {
		t.Error("want error for non-series URL")
	}
}
