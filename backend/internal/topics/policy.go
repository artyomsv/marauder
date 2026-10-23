package topics

import (
	"errors"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// ErrOnlyNewFilesDeletesData rejects a topic that both skips the previous
// version's files on an update and deletes those files when it replaces the
// previous version: the old episodes would be deleted and never downloaded
// again (issue #205).
var ErrOnlyNewFilesDeletesData = errors.New(
	"download only new files cannot be combined with deleting the previous version's files: the old files would be lost")

// ErrFolderNameDeletesData rejects a topic that keeps one folder across
// versions and deletes the previous version's files when it replaces it: the
// two versions share that folder, so the new version's files would be
// deleted too (issue #225).
var ErrFolderNameDeletesData = errors.New(
	"a folder name cannot be combined with deleting the previous version's files: both versions share the folder, so the new files would be deleted")

// ValidUpdatePolicy checks the combination of the replace-on-update (#101),
// only-new-files (#205) and folder name (#225) settings a topic would store.
// replaceDeleteData only acts while replaceOnUpdate is on, so it is only a
// conflict then.
func ValidUpdatePolicy(replaceOnUpdate, replaceDeleteData, onlyNewFiles bool, folderMode domain.FolderNameMode) error {
	if !replaceOnUpdate || !replaceDeleteData {
		return nil
	}
	if onlyNewFiles {
		return ErrOnlyNewFilesDeletesData
	}
	if usesFolderName(folderMode) {
		return ErrFolderNameDeletesData
	}
	return nil
}

// usesFolderName reports whether a mode names the folder itself rather than
// keeping the tracker's name. The zero value is the default mode.
func usesFolderName(mode domain.FolderNameMode) bool {
	return mode != "" && mode != domain.FolderNameDefault
}
