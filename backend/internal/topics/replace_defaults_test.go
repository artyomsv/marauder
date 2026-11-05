package topics

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

func TestBuildAndCreate_ReplaceDeleteDataDefaults(t *testing.T) {
	on, off := true, false
	choices := []struct {
		name  string
		value *bool
	}{{"nil", nil}, {"true", &on}, {"false", &off}}
	for _, fallback := range choices {
		for _, explicit := range choices {
			for _, onlyNew := range []bool{false, true} {
				for _, mode := range []domain.FolderNameMode{"", domain.FolderNameDefault, domain.FolderNameCustom, domain.FolderNameKeep} {
					t.Run(fmt.Sprintf("default=%s/explicit=%s/new=%v/folder=%s", fallback.name, explicit.name, onlyNew, mode), func(t *testing.T) {
						in := CreateInput{
							URL: goodURL, ReplaceOnUpdate: true,
							DefaultReplaceDeleteData: fallback.value, ReplaceDeleteData: explicit.value,
							OnlyNewFiles: onlyNew, FolderNameMode: mode,
						}
						if mode == domain.FolderNameCustom {
							in.FolderName = "TEST-folder"
						}
						store := &fakeStore{}
						res, err := BuildAndCreate(context.Background(), store, in)
						var wantErr error
						folder := mode == domain.FolderNameCustom || mode == domain.FolderNameKeep
						if explicit.value != nil && *explicit.value {
							if onlyNew {
								wantErr = ErrOnlyNewFilesDeletesData
							} else if folder {
								wantErr = ErrFolderNameDeletesData
							}
						}
						if !errors.Is(err, wantErr) {
							t.Fatalf("BuildAndCreate error = %v, want %v", err, wantErr)
						}
						if wantErr != nil {
							return
						}
						want := (fallback.value == nil || *fallback.value) && !onlyNew && !folder
						if explicit.value != nil {
							want = *explicit.value
						}
						if !res.Created || res.Topic.ReplaceDeleteData != want {
							t.Errorf("stored delete data = %v, want %v", res.Topic.ReplaceDeleteData, want)
						}
					})
				}
			}
		}
	}
}
