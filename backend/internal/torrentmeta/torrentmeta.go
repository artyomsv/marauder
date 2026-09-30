// Package torrentmeta reads the file list of a .torrent and decides which of
// its files an update already had (issue #205). It is separate from infohash,
// which only needs the raw span of the info dictionary: this package needs
// its decoded contents.
package torrentmeta

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// ErrV2Only reports a BitTorrent v2-only torrent: its files live in a
// "file tree" this package does not read. Callers treat it as "unknown".
var ErrV2Only = errors.New("torrent is v2-only (file tree without a v1 file list)")

// Files returns the content files of a .torrent. For a multi-file torrent the
// paths are relative to its top folder (info.name is left out, so a renamed
// top folder does not make every file look new); a single-file torrent is one
// entry named info.name. BEP 47 padding files are dropped because clients hide
// them.
func Files(data []byte) ([]domain.TorrentFile, error) {
	d := &decoder{data: data}
	root, err := d.value(0)
	if err != nil {
		return nil, err
	}
	top, ok := root.(map[string]any)
	if !ok {
		return nil, errors.New("torrent is not a bencoded dictionary")
	}
	info, ok := top["info"].(map[string]any)
	if !ok {
		return nil, errors.New("torrent has no info dictionary")
	}
	if list, ok := info["files"].([]any); ok {
		out := make([]domain.TorrentFile, 0, len(list))
		for i, raw := range list {
			entry, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("files[%d] is not a dictionary", i)
			}
			parts := pathOf(entry)
			if len(parts) == 0 {
				return nil, fmt.Errorf("files[%d] has no path", i)
			}
			if isPadding(entry, parts) {
				continue
			}
			size, ok := entry["length"].(int64)
			if !ok || size < 0 {
				return nil, fmt.Errorf("files[%d] has no valid length", i)
			}
			out = append(out, domain.TorrentFile{Path: strings.Join(parts, "/"), Size: size})
		}
		return out, nil
	}
	if size, ok := info["length"].(int64); ok {
		name := firstString(info, "name.utf-8", "name")
		if name == "" || size < 0 {
			return nil, errors.New("single-file torrent has no name or a bad length")
		}
		return []domain.TorrentFile{{Path: name, Size: size}}, nil
	}
	if _, ok := info["file tree"]; ok {
		return nil, ErrV2Only
	}
	return nil, errors.New("torrent info has neither files nor length")
}

// pathOf prefers path.utf-8 (set by clients that also write a legacy-encoded
// path) over path.
func pathOf(entry map[string]any) []string {
	for _, key := range []string{"path.utf-8", "path"} {
		raw, ok := entry[key].([]any)
		if !ok || len(raw) == 0 {
			continue
		}
		parts := make([]string, 0, len(raw))
		for _, p := range raw {
			s, ok := p.(string)
			if !ok {
				parts = nil
				break
			}
			parts = append(parts, s)
		}
		if len(parts) > 0 {
			return parts
		}
	}
	return nil
}

func isPadding(entry map[string]any, parts []string) bool {
	if attr, ok := entry["attr"].(string); ok && strings.Contains(attr, "p") {
		return true
	}
	return parts[0] == ".pad"
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// SkipSet returns the entries of next that prev already had — same path and
// same size. A re-encoded episode keeps its name but changes size, so it is
// not in the set and downloads again.
func SkipSet(prev, next []domain.TorrentFile) []domain.TorrentFile {
	known := make(map[domain.TorrentFile]struct{}, len(prev))
	for _, f := range prev {
		known[f] = struct{}{}
	}
	var skip []domain.TorrentFile
	for _, f := range next {
		if _, ok := known[f]; ok {
			skip = append(skip, f)
		}
	}
	return skip
}

// MatchesClientFile reports whether a client's file is f. Clients prefix the
// path with the torrent's top folder, possibly renamed (qBittorrent content
// layouts), or leave it out; so the client path must equal f.Path either as a
// whole or after dropping exactly its first component. Sizes must be equal.
func MatchesClientFile(c domain.ClientFile, f domain.TorrentFile) bool {
	if c.Size != f.Size {
		return false
	}
	p := strings.ReplaceAll(c.Path, `\`, "/")
	if p == f.Path {
		return true
	}
	_, rest, ok := strings.Cut(p, "/")
	return ok && rest == f.Path
}

// MatchSkip maps skip onto the client's file list. It returns the sorted,
// de-duplicated client indices to mark "do not download", and how many skip
// entries matched at least one client file. A caller that gets matched <
// len(skip) must not start the torrent: an old file the client did not show
// would download again.
func MatchSkip(client []domain.ClientFile, skip []domain.TorrentFile) (indices []int, matched int) {
	bySize := make(map[int64][]domain.ClientFile, len(client))
	for _, c := range client {
		bySize[c.Size] = append(bySize[c.Size], c)
	}
	seen := make(map[int]bool, len(skip))
	for _, f := range skip {
		hit := false
		for _, c := range bySize[f.Size] {
			if !MatchesClientFile(c, f) {
				continue
			}
			hit = true
			if !seen[c.Index] {
				seen[c.Index] = true
				indices = append(indices, c.Index)
			}
		}
		if hit {
			matched++
		}
	}
	sort.Ints(indices)
	return indices, matched
}
