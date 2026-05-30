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

func newMetadataTestPlugin(t *testing.T, html string) *plugin {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/series/") {
			_, _ = w.Write([]byte(html))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	return &plugin{
		sessions:          forumcommon.New(),
		domain:            "www.lostfilm.tv",
		transport:         &e2etest.HostRewriteTransport{From: "www.lostfilm.tv", To: host, Inner: &allHostsToTest{To: host}},
		redirectValidator: permissiveRedirectValidator,
	}
}

func TestResolveMetadata_OgImageAndCleanTitle(t *testing.T) {
	// og:image present and absolute; title carries the " - LostFilm.TV" suffix.
	html := `<html><head>
<title>Монарх: Наследие монстров (Monarch) - LostFilm.TV</title>
<meta property="og:image" content="https://static.lostfilm.top/Images/791/Posters/poster.jpg">
</head><body>
<a href="/logout">logout</a>
<div class="main_poster"><img src="/Images/791/Posters/image.jpg"></div>
</body></html>`
	p := newMetadataTestPlugin(t, html)

	meta, err := p.ResolveMetadata(context.Background(), "https://www.lostfilm.tv/series/Monarch/", nil)
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	wantTitle := "Монарх: Наследие монстров (Monarch)"
	if meta.Title != wantTitle {
		t.Errorf("title = %q, want %q", meta.Title, wantTitle)
	}
	if meta.ImageURL != "https://static.lostfilm.top/Images/791/Posters/poster.jpg" {
		t.Errorf("image URL = %q, want the og:image", meta.ImageURL)
	}
}

func TestResolveMetadata_MainPosterFallbackMadeAbsolute(t *testing.T) {
	// No og:image — fall back to the main_poster <img src>, which is
	// root-relative and must be made absolute against the configured domain.
	html := `<html><head><title>The Boys :: LostFilm.tv</title></head><body>
<a href="/logout">logout</a>
<div class="main_poster">
  <img class="thumb" src="/Images/442/Posters/poster.jpg">
</div>
</body></html>`
	p := newMetadataTestPlugin(t, html)

	meta, err := p.ResolveMetadata(context.Background(), "https://www.lostfilm.tv/series/The_Boys/", nil)
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	if meta.Title != "The Boys" {
		t.Errorf("title = %q, want %q", meta.Title, "The Boys")
	}
	if meta.ImageURL != "https://www.lostfilm.tv/Images/442/Posters/poster.jpg" {
		t.Errorf("image URL = %q, want absolute main_poster src", meta.ImageURL)
	}
}

func TestResolveMetadata_PrefersOgTitleOverBloatedTitle(t *testing.T) {
	// Real LostFilm shape: a SEO-bloated <title> with an en-dash site suffix,
	// plus a clean og:title. og:title wins.
	html := `<html><head>
<title>Пацаны (The Boys). Сериал Пацаны (The Boys) Amazon Prime Video (США): гид по сериям – LostFilm.TV.</title>
<meta property="og:title" content="Пацаны (The Boys)">
<meta property="og:image" content="https://www.lostfilm.tv/Static/Images/442/Posters/poster.jpg">
</head><body><a href="/logout">logout</a></body></html>`
	p := newMetadataTestPlugin(t, html)

	meta, err := p.ResolveMetadata(context.Background(), "https://www.lostfilm.tv/series/The_Boys/", nil)
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	if meta.Title != "Пацаны (The Boys)" {
		t.Errorf("title = %q, want %q", meta.Title, "Пацаны (The Boys)")
	}
}

func TestCleanSeriesTitle_TrimsBloatedTitleAndEnDash(t *testing.T) {
	// Fallback path: no og:title, so the bloated <title> with an en-dash suffix
	// is trimmed to the leading show-name segment.
	raw := "Пацаны (The Boys). Сериал Пацаны (The Boys) Amazon Prime Video (США): гид – LostFilm.TV."
	if got := cleanSeriesTitle(raw); got != "Пацаны (The Boys)" {
		t.Errorf("cleanSeriesTitle = %q, want %q", got, "Пацаны (The Boys)")
	}
}

func TestResolveMetadata_NoImageReturnsEmpty(t *testing.T) {
	html := `<html><head><title>No Poster Show - LostFilm.TV</title></head><body>
<a href="/logout">logout</a></body></html>`
	p := newMetadataTestPlugin(t, html)

	meta, err := p.ResolveMetadata(context.Background(), "https://www.lostfilm.tv/series/No_Poster/", nil)
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	if meta.Title != "No Poster Show" {
		t.Errorf("title = %q, want %q", meta.Title, "No Poster Show")
	}
	if meta.ImageURL != "" {
		t.Errorf("image URL = %q, want empty (never fabricated)", meta.ImageURL)
	}
}
