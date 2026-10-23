package torrentmeta

import (
	"errors"
	"strings"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// RootName returns a .torrent's info.name (name.utf-8 preferred) and whether
// it is a multi-file torrent. For a multi-file torrent the name is its top
// folder; for a single-file torrent it is the file (issue #225).
func RootName(data []byte) (name string, multiFile bool, err error) {
	d := &decoder{data: data}
	root, err := d.value(0)
	if err != nil {
		return "", false, err
	}
	top, ok := root.(map[string]any)
	if !ok {
		return "", false, errors.New("torrent is not a bencoded dictionary")
	}
	info, ok := top["info"].(map[string]any)
	if !ok {
		return "", false, errors.New("torrent has no info dictionary")
	}
	name = firstString(info, "name.utf-8", "name")
	if name == "" {
		return "", false, errors.New("torrent has no name")
	}
	_, multiFile = info["files"].([]any)
	return name, multiFile, nil
}

// ClientRoot returns the top folder a client shows a torrent's files under:
// the one first path component every client file shares, when each has more
// than one component. With a manifest (Files of the same torrent) the folder
// must also be the rooted layout MapClientFiles accepts — a client set to
// "no subfolder" shows a torrent whose files all sit in one inner folder in a
// way that looks rooted, and renaming that inner folder would be wrong.
// Without a manifest only the shape is checked.
func ClientRoot(files []domain.ClientFile, manifest []domain.TorrentFile) (string, bool) {
	if len(files) == 0 {
		return "", false
	}
	root, _, _ := strings.Cut(files[0].Path, "/")
	strip := rootStripper(files)
	for _, f := range files {
		if _, ok := strip(f.Path); !ok {
			return "", false
		}
	}
	if manifest != nil {
		if _, ok := bijection(files, manifest, strip); !ok {
			return "", false
		}
	}
	return root, true
}
