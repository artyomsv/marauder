package topics

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

func TestNormalizeFolderName(t *testing.T) {
	cases := []struct {
		name    string
		mode    domain.FolderNameMode
		in      string
		want    string
		wantErr bool
	}{
		{"plain", domain.FolderNameCustom, "My Show", "My Show", false},
		{"trimmed", domain.FolderNameCustom, "  Show  ", "Show", false},
		{"255 bytes", domain.FolderNameCustom, strings.Repeat("a", 255), strings.Repeat("a", 255), false},
		{"cyrillic", domain.FolderNameCustom, "Сериал", "Сериал", false},
		{"reserved word inside a longer name", domain.FolderNameCustom, "Console", "Console", false},
		{"inner dots", domain.FolderNameCustom, "Show.S01.1080p", "Show.S01.1080p", false},

		{"empty", domain.FolderNameCustom, "", "", true},
		{"only spaces", domain.FolderNameCustom, "   ", "", true},
		{"256 bytes", domain.FolderNameCustom, strings.Repeat("a", 256), "", true},
		{"128 two-byte runes", domain.FolderNameCustom, strings.Repeat("я", 128), "", true},
		{"dot", domain.FolderNameCustom, ".", "", true},
		{"dot dot", domain.FolderNameCustom, "..", "", true},
		{"padding folder", domain.FolderNameCustom, ".pad", "", true},
		{"slash", domain.FolderNameCustom, "a/b", "", true},
		{"backslash", domain.FolderNameCustom, `a\b`, "", true},
		{"colon", domain.FolderNameCustom, "a:b", "", true},
		{"star", domain.FolderNameCustom, "a*", "", true},
		{"question mark", domain.FolderNameCustom, "a?", "", true},
		{"angle brackets", domain.FolderNameCustom, "<a>", "", true},
		{"quote", domain.FolderNameCustom, `a"b`, "", true},
		{"pipe", domain.FolderNameCustom, "a|b", "", true},
		{"control character", domain.FolderNameCustom, "tab\there", "", true},
		{"trailing dot", domain.FolderNameCustom, "name.", "", true},
		{"reserved CON", domain.FolderNameCustom, "CON", "", true},
		{"reserved nul with extension", domain.FolderNameCustom, "nul.mkv", "", true},
		{"reserved Com3", domain.FolderNameCustom, "Com3", "", true},
		{"reserved LPT9 with extension", domain.FolderNameCustom, "LPT9.txt", "", true},

		{"default ignores name", domain.FolderNameDefault, "a/b", "", false},
		{"keep ignores name", domain.FolderNameKeep, "whatever", "", false},
		{"unknown mode", domain.FolderNameMode("bogus"), "x", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeFolderName(tc.mode, tc.in)
			if tc.wantErr {
				if !errors.Is(err, ErrFolderNameInvalid) {
					t.Fatalf("NormalizeFolderName(%q, %q) = (%q, %v), want ErrFolderNameInvalid", tc.mode, tc.in, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeFolderName(%q, %q): unexpected error %v", tc.mode, tc.in, err)
			}
			if got != tc.want {
				t.Errorf("NormalizeFolderName(%q, %q) = %q, want %q", tc.mode, tc.in, got, tc.want)
			}
		})
	}
}

// A folder name shares the folder between the old and the new torrent, so
// replacing the old one with "delete data" would delete the new one's files.
func TestBuildAndCreate_FolderName(t *testing.T) {
	yes := true
	t.Run("omitted delete-data defaults off", func(t *testing.T) {
		store := &fakeStore{}
		_, err := BuildAndCreate(context.Background(), store, CreateInput{
			UserID: uuid.New(), URL: goodURL,
			ReplaceOnUpdate: true,
			FolderNameMode:  domain.FolderNameCustom, FolderName: "  My Show ",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if store.created.ReplaceDeleteData {
			t.Error("ReplaceDeleteData = true, want false with a folder name")
		}
		if store.created.FolderNameMode != domain.FolderNameCustom || store.created.FolderName != "My Show" {
			t.Errorf("stored (%q, %q), want (custom, My Show)", store.created.FolderNameMode, store.created.FolderName)
		}
	})
	t.Run("explicit delete-data refused", func(t *testing.T) {
		_, err := BuildAndCreate(context.Background(), &fakeStore{}, CreateInput{
			UserID: uuid.New(), URL: goodURL,
			ReplaceOnUpdate: true, ReplaceDeleteData: &yes,
			FolderNameMode: domain.FolderNameKeep,
		})
		if !errors.Is(err, ErrFolderNameDeletesData) {
			t.Fatalf("want ErrFolderNameDeletesData, got %v", err)
		}
	})
	t.Run("invalid name refused", func(t *testing.T) {
		_, err := BuildAndCreate(context.Background(), &fakeStore{}, CreateInput{
			UserID: uuid.New(), URL: goodURL,
			FolderNameMode: domain.FolderNameCustom, FolderName: "a/b",
		})
		if !errors.Is(err, ErrFolderNameInvalid) {
			t.Fatalf("want ErrFolderNameInvalid, got %v", err)
		}
	})
	t.Run("omitted mode is default and keeps delete-data on", func(t *testing.T) {
		store := &fakeStore{}
		if _, err := BuildAndCreate(context.Background(), store, CreateInput{
			UserID: uuid.New(), URL: goodURL, ReplaceOnUpdate: true,
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if store.created.FolderNameMode != domain.FolderNameDefault || !store.created.ReplaceDeleteData {
			t.Errorf("stored (%q, deleteData %v), want (default, true)", store.created.FolderNameMode, store.created.ReplaceDeleteData)
		}
	})
}
