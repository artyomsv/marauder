package registry

import "testing"

func TestEffectiveDownloadDir(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		override string
		category string
		want     string
	}{
		{"all empty", "", "", "", ""},
		{"base only", "/downloads", "", "", "/downloads"},
		{"category nests under base", "/downloads", "", "Movies", "/downloads/Movies"},
		{"nested category", "/downloads", "", "tv/hd", "/downloads/tv/hd"},
		{"override wins over base+category", "/downloads", "/explicit", "Movies", "/explicit"},
		{"override wins with no base", "", "/explicit", "", "/explicit"},
		{"category only, no base", "", "", "Movies", "Movies"},
		{"trailing slash on base is cleaned", "/downloads/", "", "Movies", "/downloads/Movies"},
		{"category traversal is confined", "/downloads", "", "../../etc", "/downloads/etc"},
		{"leading slash category is relative", "/downloads", "", "/abs/Movies", "/downloads/abs/Movies"},
		{"whitespace category is empty", "/downloads", "", "   ", "/downloads"},
		{"dot category is empty", "/downloads", "", ".", "/downloads"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EffectiveDownloadDir(tt.base, tt.override, tt.category)
			if got != tt.want {
				t.Errorf("EffectiveDownloadDir(%q, %q, %q) = %q, want %q",
					tt.base, tt.override, tt.category, got, tt.want)
			}
		})
	}
}
