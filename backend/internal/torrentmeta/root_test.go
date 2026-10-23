package torrentmeta_test

import (
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/torrentmeta"
	"github.com/artyomsv/marauder/backend/internal/torrentmeta/torrentmetatest"
)

func TestRootName(t *testing.T) {
	multi := torrentmetatest.Torrent("Show.S01", []domain.TorrentFile{{Path: "E01.mkv", Size: 10}})
	name, isMulti, err := torrentmeta.RootName(multi)
	if err != nil || name != "Show.S01" || !isMulti {
		t.Errorf("multi-file = (%q, %v, %v), want (Show.S01, true, nil)", name, isMulti, err)
	}

	single := torrentmetatest.SingleFile("Movie.mkv", 10)
	name, isMulti, err = torrentmeta.RootName(single)
	if err != nil || name != "Movie.mkv" || isMulti {
		t.Errorf("single-file = (%q, %v, %v), want (Movie.mkv, false, nil)", name, isMulti, err)
	}

	// name.utf-8 wins over a legacy-encoded name, as path.utf-8 does in Files.
	utf8 := []byte("d4:infod5:filesld6:lengthi1e4:pathl1:aeee4:name6:legacy10:name.utf-84:goodee")
	if name, _, err = torrentmeta.RootName(utf8); err != nil || name != "good" {
		t.Errorf("name.utf-8 = (%q, %v), want good", name, err)
	}

	for _, bad := range [][]byte{[]byte("garbage"), []byte("d4:infod5:filesleee"), []byte("de")} {
		if _, _, err := torrentmeta.RootName(bad); err == nil {
			t.Errorf("RootName(%q) = nil error, want an error", bad)
		}
	}
}

func TestClientRoot(t *testing.T) {
	manifest := []domain.TorrentFile{{Path: "E01.mkv", Size: 1}, {Path: "E02.mkv", Size: 2}}
	cf := func(paths ...string) []domain.ClientFile {
		out := make([]domain.ClientFile, len(paths))
		for i, p := range paths {
			size := int64(i + 1)
			out[i] = domain.ClientFile{Index: i, Path: p, Size: size}
		}
		return out
	}
	cases := []struct {
		name     string
		files    []domain.ClientFile
		manifest []domain.TorrentFile
		want     string
		wantOK   bool
	}{
		{"rooted", cf("Show/E01.mkv", "Show/E02.mkv"), manifest, "Show", true},
		// Without the torrent's file list the shape cannot tell a top folder
		// from an inner folder of a "no subfolder" layout (PR #226 review).
		{"rooted, no manifest", cf("Show/E01.mkv", "Show/E02.mkv"), nil, "", false},
		{"verbatim", cf("E01.mkv", "E02.mkv"), manifest, "", false},
		{"mixed roots", cf("A/E01.mkv", "B/E02.mkv"), manifest, "", false},
		{"one file at the top", cf("Show/E01.mkv", "E02.mkv"), manifest, "", false},
		{"empty", nil, manifest, "", false},
		// A client with "no subfolder" shows the torrent's own inner folder
		// first; it looks rooted, but the manifest says it is not.
		{"inner folder, verbatim", cf("Extras/E01.mkv", "Extras/E02.mkv"),
			[]domain.TorrentFile{{Path: "Extras/E01.mkv", Size: 1}, {Path: "Extras/E02.mkv", Size: 2}}, "", false},
		{"padding under the root", []domain.ClientFile{
			{Index: 0, Path: "Show/E01.mkv", Size: 1},
			{Index: 1, Path: "Show/.pad/0", Size: 7},
			{Index: 2, Path: "Show/E02.mkv", Size: 2},
		}, manifest, "Show", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := torrentmeta.ClientRoot(tc.files, tc.manifest)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("ClientRoot = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
