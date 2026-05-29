package lostfilm

import (
	"strings"
	"testing"
)

// destBlock builds one inner-box--item block for the destination-page
// markup LostFilm currently serves.
func destBlock(label, href string) string {
	return `<div class="inner-box--item">` +
		`<div class="inner-box--label">` + label + `</div>` +
		`<div class="inner-box--link main"><a href="` + href + `">dl</a></div>` +
		`<div class="inner-box--link sub"><a href="` + href + `">` + href + `</a></div>` +
		`</div>`
}

func TestPickQualityLink_PicksTierByLabel(t *testing.T) {
	dest := []byte(destBlock("SD", "https://n.tracktor.site/td.php?s=sd") +
		destBlock("MP4", "https://n.tracktor.site/td.php?s=mp4") +
		destBlock("1080", "https://n.tracktor.site/td.php?s=q1080"))

	cases := map[string]string{
		"1080p": "s=q1080", // must pick the "1080" tier, not "MP4"
		"sd":    "s=sd",
		"mp4":   "s=mp4",
	}
	for want, wantInURL := range cases {
		url, _, err := pickQualityLink(dest, want)
		if err != nil {
			t.Fatalf("pickQualityLink(%q): %v", want, err)
		}
		if !strings.Contains(url, wantInURL) {
			t.Errorf("want %q → url %q, expected to contain %q", want, url, wantInURL)
		}
	}
}

func TestPickQualityLink_NoLinksErrors(t *testing.T) {
	if _, _, err := pickQualityLink([]byte(`<html><body>no download blocks here</body></html>`), "1080p"); err == nil {
		t.Error("want error when the destination page has no quality links")
	}
}

func TestPickQualityLink_FallsBackToFirstWhenNoQualityMatch(t *testing.T) {
	// Only an SD block exists but the user wants 1080p → fall back to the
	// first (only) link rather than failing the episode.
	dest := []byte(destBlock("SD", "https://n.tracktor.site/td.php?s=onlysd"))
	url, label, err := pickQualityLink(dest, "1080p")
	if err != nil {
		t.Fatalf("pickQualityLink: %v", err)
	}
	if !strings.Contains(url, "onlysd") || label != "SD" {
		t.Errorf("fallback url=%q label=%q, want the SD link", url, label)
	}
}
