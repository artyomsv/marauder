package torrentmeta

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/torrentmeta/torrentmetatest"
)

func TestFiles_MultiFile(t *testing.T) {
	data := torrentmetatest.Torrent("Show S01", []domain.TorrentFile{
		{Path: "E01.mkv", Size: 100},
		{Path: "Subs/E01.srt", Size: 7},
	})
	got, err := Files(data)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	want := []domain.TorrentFile{{Path: "E01.mkv", Size: 100}, {Path: "Subs/E01.srt", Size: 7}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Files = %+v, want %+v", got, want)
	}
}

func TestFiles_SingleFile(t *testing.T) {
	got, err := Files(torrentmetatest.SingleFile("Movie.mkv", 4242))
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	want := []domain.TorrentFile{{Path: "Movie.mkv", Size: 4242}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Files = %+v, want %+v", got, want)
	}
}

// Hybrid (v1+v2) torrents list BEP 47 padding files in info.files. Clients
// hide them, so counting them would make every update look like it added files.
func TestFiles_DropsPaddingFiles(t *testing.T) {
	data := []byte("d4:infod5:filesl" +
		"d6:lengthi100e4:pathl7:E01.mkvee" +
		"d4:attr1:p6:lengthi28e4:pathl4:.pad2:28ee" +
		"d6:lengthi200e4:pathl4:.pad7:E02.mkvee" +
		"d6:lengthi300e4:pathl7:E03.mkvee" +
		"e4:name4:Show12:piece lengthi16384e6:pieces0:ee")
	got, err := Files(data)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	want := []domain.TorrentFile{{Path: "E01.mkv", Size: 100}, {Path: "E03.mkv", Size: 300}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Files = %+v, want %+v", got, want)
	}
}

func TestFiles_PrefersUTF8Path(t *testing.T) {
	data := []byte("d4:infod5:filesl" +
		"d6:lengthi5e4:pathl3:bade10:path.utf-8l4:goodee" +
		"e4:name1:x12:piece lengthi16384e6:pieces0:ee")
	got, err := Files(data)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(got) != 1 || got[0].Path != "good" {
		t.Errorf("Files = %+v, want path.utf-8 value", got)
	}
}

func TestFiles_V2OnlyIsReported(t *testing.T) {
	data := []byte("d4:infod9:file treed0:dee4:name1:x12:piece lengthi16384eee")
	if _, err := Files(data); !errors.Is(err, ErrV2Only) {
		t.Errorf("err = %v, want ErrV2Only", err)
	}
}

// Every decoded value costs far more heap than its byte in the input, so a
// crafted torrent of tiny values would otherwise turn a few MiB into hundreds
// of MB. Three lists of 90k integers stay under the list-length bound and
// exceed the value cap.
func TestFiles_TooManyValuesIsAnError(t *testing.T) {
	var b strings.Builder
	b.WriteString("l")
	for range 3 {
		b.WriteString("l")
		b.WriteString(strings.Repeat("i0e", 90_000))
		b.WriteString("e")
	}
	b.WriteString("e")

	if _, err := Files([]byte(b.String())); !errors.Is(err, errTooManyValues) {
		t.Errorf("Files error = %v, want errTooManyValues", err)
	}
}

// A large real-world pack stays well inside the value cap.
func TestFiles_ManyFilesWithinValueCap(t *testing.T) {
	files := make([]domain.TorrentFile, 5000)
	for i := range files {
		files[i] = domain.TorrentFile{Path: fmt.Sprintf("Season 1/E%04d.mkv", i), Size: int64(i + 1)}
	}

	got, err := Files(torrentmetatest.Torrent("Pack", files))
	if err != nil || len(got) != len(files) {
		t.Fatalf("Files = (%d files, %v), want %d files", len(got), err, len(files))
	}
}

func TestFiles_RejectsMalformedInput(t *testing.T) {
	cases := map[string][]byte{
		"empty":         nil,
		"not a dict":    []byte("i3e"),
		"no info":       []byte("d3:foo3:bare"),
		"truncated":     []byte("d4:infod5:filesl"),
		"negative size": []byte("d4:infod5:filesld6:lengthi-1e4:pathl1:aeeee4:name1:xee"),
		"deep nesting":  []byte(strings.Repeat("l", 100) + strings.Repeat("e", 100)),
		"huge string":   []byte("d4:info99999999999:x"),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Files(data); err == nil {
				t.Error("Files returned no error")
			}
		})
	}
}

func TestSkipSet(t *testing.T) {
	prev := []domain.TorrentFile{{Path: "E01.mkv", Size: 100}, {Path: "E02.mkv", Size: 200}}
	next := []domain.TorrentFile{
		{Path: "E01.mkv", Size: 100}, // unchanged -> skip
		{Path: "E02.mkv", Size: 201}, // re-encoded -> new
		{Path: "E03.mkv", Size: 300}, // added -> new
	}
	got := SkipSet(prev, next)
	want := []domain.TorrentFile{{Path: "E01.mkv", Size: 100}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SkipSet = %+v, want %+v", got, want)
	}
	if got := SkipSet(nil, next); len(got) != 0 {
		t.Errorf("SkipSet(nil, next) = %+v, want empty", got)
	}
}

func TestMapClientFiles_InfersOneLayout(t *testing.T) {
	tf := func(p string, s int64) domain.TorrentFile { return domain.TorrentFile{Path: p, Size: s} }
	cf := func(i int, p string, s int64) domain.ClientFile { return domain.ClientFile{Index: i, Path: p, Size: s} }
	cases := []struct {
		name     string
		client   []domain.ClientFile
		manifest []domain.TorrentFile
		want     map[int]domain.TorrentFile
	}{
		{
			// PR #210 review: Extras/E01.mkv must not pair with E01.mkv.
			name:     "rootless lookalike",
			client:   []domain.ClientFile{cf(0, "E01.mkv", 100), cf(1, "Extras/E01.mkv", 100)},
			manifest: []domain.TorrentFile{tf("E01.mkv", 100), tf("Extras/E01.mkv", 100)},
			want:     map[int]domain.TorrentFile{0: tf("E01.mkv", 100), 1: tf("Extras/E01.mkv", 100)},
		},
		{
			// The rooted twin: Show/E01.mkv is a manifest path in its own right.
			name:     "rooted lookalike",
			client:   []domain.ClientFile{cf(0, "Show/Show/E01.mkv", 100), cf(1, "Show/E01.mkv", 100)},
			manifest: []domain.TorrentFile{tf("Show/E01.mkv", 100), tf("E01.mkv", 100)},
			want:     map[int]domain.TorrentFile{0: tf("Show/E01.mkv", 100), 1: tf("E01.mkv", 100)},
		},
		{
			name:     "renamed root",
			client:   []domain.ClientFile{cf(0, "Show S01E01-06/E01.mkv", 100), cf(1, "Show S01E01-06/Subs/E01.srt", 7)},
			manifest: []domain.TorrentFile{tf("E01.mkv", 100), tf("Subs/E01.srt", 7)},
			want:     map[int]domain.TorrentFile{0: tf("E01.mkv", 100), 1: tf("Subs/E01.srt", 7)},
		},
		{
			name:     "qBittorrent create-subfolder single file",
			client:   []domain.ClientFile{cf(0, "Movie/Movie.mkv", 500)},
			manifest: []domain.TorrentFile{tf("Movie.mkv", 500)},
			want:     map[int]domain.TorrentFile{0: tf("Movie.mkv", 500)},
		},
		{
			name:     "windows separators",
			client:   []domain.ClientFile{cf(3, `Show\Subs\E01.srt`, 7)},
			manifest: []domain.TorrentFile{tf("Subs/E01.srt", 7)},
			want:     map[int]domain.TorrentFile{3: tf("Subs/E01.srt", 7)},
		},
		{
			name: "padding entries ignored",
			client: []domain.ClientFile{
				cf(0, "Show/E01.mkv", 100), cf(1, "Show/.pad/16384", 16384), cf(2, "Show/E02.mkv", 200),
			},
			manifest: []domain.TorrentFile{tf("E01.mkv", 100), tf("E02.mkv", 200)},
			want:     map[int]domain.TorrentFile{0: tf("E01.mkv", 100), 2: tf("E02.mkv", 200)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MapClientFiles(tc.client, tc.manifest)
			if err != nil {
				t.Fatalf("MapClientFiles: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("MapClientFiles = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestMapClientFiles_RefusesWhatIsNotOneToOne(t *testing.T) {
	manifest := []domain.TorrentFile{{Path: "E01.mkv", Size: 100}, {Path: "E02.mkv", Size: 200}}
	cases := map[string][]domain.ClientFile{
		"count mismatch": {{Index: 0, Path: "Show/E01.mkv", Size: 100}},
		"size mismatch": {
			{Index: 0, Path: "Show/E01.mkv", Size: 100}, {Index: 1, Path: "Show/E02.mkv", Size: 999},
		},
		"mixed roots": {
			{Index: 0, Path: "Show/E01.mkv", Size: 100}, {Index: 1, Path: "Other/E02.mkv", Size: 200},
		},
		"mixed layouts": {
			{Index: 0, Path: "E01.mkv", Size: 100}, {Index: 1, Path: "Show/E02.mkv", Size: 200},
		},
		"one entry used twice": {
			{Index: 0, Path: "Show/E01.mkv", Size: 100}, {Index: 1, Path: "Show/E01.mkv", Size: 100},
		},
		"duplicate client index": {
			{Index: 0, Path: "Show/E01.mkv", Size: 100}, {Index: 0, Path: "Show/E02.mkv", Size: 200},
		},
	}
	for name, client := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := MapClientFiles(client, manifest)
			if !errors.Is(err, ErrLayoutMismatch) {
				t.Errorf("MapClientFiles = (%+v, %v), want ErrLayoutMismatch", got, err)
			}
		})
	}
}

// Both layouts valid cannot arise from MapClientFiles itself (the rooted
// reading of every path is shorter than the verbatim one), so the refusal of
// an ambiguity is pinned on the decision directly.
func TestPickLayout_AmbiguityRefusesUnlessIdentical(t *testing.T) {
	a := map[int]domain.TorrentFile{0: {Path: "E01.mkv", Size: 1}}
	b := map[int]domain.TorrentFile{0: {Path: "E02.mkv", Size: 1}}
	if _, err := pickLayout(a, true, b, true, 1, 1); !errors.Is(err, ErrLayoutMismatch) {
		t.Errorf("different mappings: err = %v, want ErrLayoutMismatch", err)
	}
	got, err := pickLayout(a, true, map[int]domain.TorrentFile{0: {Path: "E01.mkv", Size: 1}}, true, 1, 1)
	if err != nil || !reflect.DeepEqual(got, a) {
		t.Errorf("identical mappings = (%+v, %v), want (%+v, nil)", got, err, a)
	}
	if _, err := pickLayout(nil, false, nil, false, 2, 3); err == nil || !strings.Contains(err.Error(), "2 client files") || !strings.Contains(err.Error(), "3 files") {
		t.Errorf("no layout: err = %v, want the counts named", err)
	}
}

func TestSkipIndices(t *testing.T) {
	mapping := map[int]domain.TorrentFile{
		4: {Path: "E03.mkv", Size: 300},
		0: {Path: "E01.mkv", Size: 100},
		2: {Path: "E02.mkv", Size: 200},
	}
	skip := []domain.TorrentFile{{Path: "E02.mkv", Size: 200}, {Path: "E01.mkv", Size: 100}}
	if got := SkipIndices(mapping, skip); !reflect.DeepEqual(got, []int{0, 2}) {
		t.Errorf("SkipIndices = %v, want [0 2] (client ids, sorted)", got)
	}
	if got := SkipIndices(mapping, nil); len(got) != 0 {
		t.Errorf("SkipIndices(nil skip) = %v, want empty", got)
	}
}
