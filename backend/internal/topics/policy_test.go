package topics

import (
	"errors"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

func TestValidUpdatePolicy(t *testing.T) {
	cases := []struct {
		replace, deleteData, onlyNew bool
		folder                       domain.FolderNameMode
		want                         error
	}{
		{false, false, false, domain.FolderNameDefault, nil},
		{true, true, false, domain.FolderNameDefault, nil}, // replace + delete without only-new: fine
		{true, false, true, domain.FolderNameDefault, nil}, // replace keeping data + only-new: fine
		{false, true, true, domain.FolderNameDefault, nil}, // delete-data flag is inert while replace is off
		{true, true, true, domain.FolderNameDefault, ErrOnlyNewFilesDeletesData},
		// A folder name shares the folder between versions (issue #225).
		{true, true, false, domain.FolderNameCustom, ErrFolderNameDeletesData},
		{true, true, false, domain.FolderNameKeep, ErrFolderNameDeletesData},
		{true, false, false, domain.FolderNameCustom, nil},
		{false, true, false, domain.FolderNameKeep, nil},
		{true, true, false, "", nil}, // zero value is the default mode
	}
	for _, tc := range cases {
		err := ValidUpdatePolicy(tc.replace, tc.deleteData, tc.onlyNew, tc.folder)
		if tc.want == nil && err != nil {
			t.Errorf("ValidUpdatePolicy(%v,%v,%v,%q) = %v, want nil", tc.replace, tc.deleteData, tc.onlyNew, tc.folder, err)
		}
		if tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("ValidUpdatePolicy(%v,%v,%v,%q) = %v, want %v", tc.replace, tc.deleteData, tc.onlyNew, tc.folder, err, tc.want)
		}
	}
}
