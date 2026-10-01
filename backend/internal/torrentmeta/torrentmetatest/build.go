// Package torrentmetatest builds minimal .torrent files for tests and for the
// real-client check. The piece hashes are zero bytes of the right length:
// clients accept such a torrent, and nothing ever downloads, because no peer
// has the data.
package torrentmetatest

import (
	"fmt"
	"strings"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

const pieceLength = 16384

// Torrent returns a multi-file .torrent named name. Each file's Path is split
// on "/" into the path list.
func Torrent(name string, files []domain.TorrentFile) []byte {
	var b strings.Builder
	var total int64
	b.WriteString("d4:infod5:filesl")
	for _, f := range files {
		total += f.Size
		fmt.Fprintf(&b, "d6:lengthi%de4:pathl", f.Size)
		for _, part := range strings.Split(f.Path, "/") {
			fmt.Fprintf(&b, "%d:%s", len(part), part)
		}
		b.WriteString("ee")
	}
	b.WriteString("e")
	writeTail(&b, name, total)
	return []byte(b.String())
}

// SingleFile returns a single-file .torrent.
func SingleFile(name string, size int64) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "d4:infod6:lengthi%de", size)
	writeTail(&b, name, size)
	return []byte(b.String())
}

func writeTail(b *strings.Builder, name string, total int64) {
	pieces := int((total + pieceLength - 1) / pieceLength)
	if pieces == 0 {
		pieces = 1
	}
	fmt.Fprintf(b, "4:name%d:%s12:piece lengthi%de6:pieces%d:%see",
		len(name), name, pieceLength, pieces*20, strings.Repeat("\x00", pieces*20))
}
