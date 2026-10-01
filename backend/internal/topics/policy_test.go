package topics

import (
	"errors"
	"testing"
)

func TestValidUpdatePolicy(t *testing.T) {
	cases := []struct {
		replace, deleteData, onlyNew bool
		wantErr                      bool
	}{
		{false, false, false, false},
		{true, true, false, false}, // replace + delete without only-new: fine
		{true, false, true, false}, // replace keeping data + only-new: fine
		{false, true, true, false}, // delete-data flag is inert while replace is off
		{true, true, true, true},   // the lossy mix
	}
	for _, tc := range cases {
		err := ValidUpdatePolicy(tc.replace, tc.deleteData, tc.onlyNew)
		if (err != nil) != tc.wantErr {
			t.Errorf("ValidUpdatePolicy(%v,%v,%v) = %v, wantErr %v", tc.replace, tc.deleteData, tc.onlyNew, err, tc.wantErr)
		}
		if err != nil && !errors.Is(err, ErrOnlyNewFilesDeletesData) {
			t.Errorf("err = %v, want ErrOnlyNewFilesDeletesData", err)
		}
	}
}
