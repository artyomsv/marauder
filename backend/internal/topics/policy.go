package topics

import "errors"

// ErrOnlyNewFilesDeletesData rejects a topic that both skips the previous
// version's files on an update and deletes those files when it replaces the
// previous version: the old episodes would be deleted and never downloaded
// again (issue #205).
var ErrOnlyNewFilesDeletesData = errors.New(
	"download only new files cannot be combined with deleting the previous version's files: the old files would be lost")

// ValidUpdatePolicy checks the combination of the replace-on-update (#101) and
// only-new-files (#205) settings a topic would store. replaceDeleteData only
// acts while replaceOnUpdate is on, so it is only a conflict then.
func ValidUpdatePolicy(replaceOnUpdate, replaceDeleteData, onlyNewFiles bool) error {
	if onlyNewFiles && replaceOnUpdate && replaceDeleteData {
		return ErrOnlyNewFilesDeletesData
	}
	return nil
}
