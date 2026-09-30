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

func TestMatchesClientFile(t *testing.T) {
	f := domain.TorrentFile{Path: "Subs/E01.srt", Size: 7}
	cases := []struct {
		name string
		c    domain.ClientFile
		want bool
	}{
		{"with top folder", domain.ClientFile{Path: "Show S01/Subs/E01.srt", Size: 7}, true},
		{"renamed root", domain.ClientFile{Path: "Show S01E01-06/Subs/E01.srt", Size: 7}, true},
		{"no subfolder layout", domain.ClientFile{Path: "Subs/E01.srt", Size: 7}, true},
		{"windows separators", domain.ClientFile{Path: `Show S01\Subs\E01.srt`, Size: 7}, true},
		{"size differs", domain.ClientFile{Path: "Show S01/Subs/E01.srt", Size: 8}, false},
		{"deeper path", domain.ClientFile{Path: "Show/Extra/Subs/E01.srt", Size: 7}, false},
		{"not at a folder boundary", domain.ClientFile{Path: "Show/XSubs/E01.srt", Size: 7}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchesClientFile(tc.c, f); got != tc.want {
				t.Errorf("MatchesClientFile = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatchSkip(t *testing.T) {
	client := []domain.ClientFile{
		{Index: 0, Path: "Show/E01.mkv", Size: 100},
		{Index: 2, Path: "Show/E02.mkv", Size: 200},
		{Index: 3, Path: "Show/E03.mkv", Size: 300},
	}
	skip := []domain.TorrentFile{{Path: "E02.mkv", Size: 200}, {Path: "E01.mkv", Size: 100}, {Path: "Gone.mkv", Size: 1}}
	indices, matched := MatchSkip(client, skip)
	if !reflect.DeepEqual(indices, []int{0, 2}) {
		t.Errorf("indices = %v, want [0 2] (client ids, sorted)", indices)
	}
	if matched != 2 {
		t.Errorf("matched = %d, want 2 (Gone.mkv has no client file)", matched)
	}
}
