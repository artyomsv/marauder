// Package torrentmeta reads the file list of a .torrent and decides which of
// its files an update already had (issue #205). It is separate from infohash,
// which only needs the raw span of the info dictionary: this package needs
// its decoded contents.
package torrentmeta

import (
	"errors"
	"fmt"
	"reflect"
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

// ErrLayoutMismatch reports a client file list that cannot be laid over the
// torrent's own list one to one. Nothing may be skipped or started then: a
// file matched to the wrong entry would either download an old file again or
// never download a new one.
var ErrLayoutMismatch = errors.New("the client's file list does not match the torrent")

// MapClientFiles pairs each file the client lists with the torrent's own
// entry for it, keyed by the client's file index. Clients list a torrent
// either without its top folder or under one — the torrent's own name, a
// renamed one, or a folder the client created for a single-file torrent
// (qBittorrent "Create subfolder") — and that choice is made once per
// torrent, not per file. So ONE layout is inferred for the whole list:
//
//   - verbatim: every client path is a manifest path;
//   - rooted: every client path has at least two components, all share the
//     same first one, and the rest is a manifest path.
//
// A layout counts only as a bijection: as many client files as manifest
// entries, each client file paired with an entry of equal path and size, and
// every entry used exactly once. Matching each file on its own, both ways at
// once, let Extras/E01.mkv pass for an old E01.mkv of the same size and be
// skipped as it (PR #210 review). Client entries that are BEP 47 padding are
// ignored, as Files ignores them in the manifest.
//
// Exactly one valid layout is the answer; two valid layouts are accepted only
// when they pair every file identically. Anything else is ErrLayoutMismatch.
func MapClientFiles(client []domain.ClientFile, manifest []domain.TorrentFile) (map[int]domain.TorrentFile, error) {
	files := make([]domain.ClientFile, 0, len(client))
	for _, c := range client {
		c.Path = strings.ReplaceAll(c.Path, `\`, "/")
		if isPaddingPath(c.Path) {
			continue
		}
		files = append(files, c)
	}
	verbatim, vok := bijection(files, manifest, func(p string) (string, bool) { return p, true })
	rooted, rok := bijection(files, manifest, rootStripper(files))
	return pickLayout(verbatim, vok, rooted, rok, len(files), len(manifest))
}

// pickLayout decides between the two layouts' results. Both being valid
// cannot happen for a non-empty list — the rooted reading of a path is
// strictly shorter than the verbatim one, so both cannot equal the same
// manifest — but an ambiguity must refuse rather than guess, so it is checked.
func pickLayout(verbatim map[int]domain.TorrentFile, vok bool, rooted map[int]domain.TorrentFile, rok bool, clientCount, manifestCount int) (map[int]domain.TorrentFile, error) {
	switch {
	case vok && rok:
		if reflect.DeepEqual(verbatim, rooted) {
			return verbatim, nil
		}
		return nil, fmt.Errorf("%w: %d client files fit two layouts differently", ErrLayoutMismatch, clientCount)
	case vok:
		return verbatim, nil
	case rok:
		return rooted, nil
	}
	return nil, fmt.Errorf("%w: %d client files cannot be paired one to one with the torrent's %d files",
		ErrLayoutMismatch, clientCount, manifestCount)
}

// rootStripper returns the rooted layout's path mapping: drop the first
// component, which must be present and the same for every client file.
func rootStripper(files []domain.ClientFile) func(string) (string, bool) {
	root := ""
	if len(files) > 0 {
		root, _, _ = strings.Cut(files[0].Path, "/")
	}
	return func(p string) (string, bool) {
		first, rest, ok := strings.Cut(p, "/")
		if !ok || first == "" || rest == "" || first != root {
			return "", false
		}
		return rest, true
	}
}

// bijection pairs files with manifest under one path mapping, or reports
// false when that mapping is not one to one.
func bijection(files []domain.ClientFile, manifest []domain.TorrentFile, mapPath func(string) (string, bool)) (map[int]domain.TorrentFile, bool) {
	if len(files) != len(manifest) {
		return nil, false
	}
	// Counted, not a set: a manifest could list the same path and size twice,
	// and each copy may pair with one client file only.
	remaining := make(map[domain.TorrentFile]int, len(manifest))
	for _, f := range manifest {
		remaining[f]++
	}
	out := make(map[int]domain.TorrentFile, len(files))
	for _, c := range files {
		p, ok := mapPath(c.Path)
		if !ok {
			return nil, false
		}
		key := domain.TorrentFile{Path: p, Size: c.Size}
		if remaining[key] == 0 {
			return nil, false
		}
		if _, dup := out[c.Index]; dup {
			return nil, false
		}
		remaining[key]--
		out[c.Index] = key
	}
	// Equal counts and every client file consuming one entry means every
	// entry was used exactly once.
	return out, true
}

// isPaddingPath mirrors isPadding for a client's path: any ".pad" component.
func isPaddingPath(p string) bool {
	for _, part := range strings.Split(p, "/") {
		if part == ".pad" {
			return true
		}
	}
	return false
}

// SkipIndices returns the sorted client indices whose torrent entry is in
// skip — the files to mark "do not download".
func SkipIndices(mapping map[int]domain.TorrentFile, skip []domain.TorrentFile) []int {
	old := make(map[domain.TorrentFile]struct{}, len(skip))
	for _, f := range skip {
		old[f] = struct{}{}
	}
	var indices []int
	for idx, f := range mapping {
		if _, ok := old[f]; ok {
			indices = append(indices, idx)
		}
	}
	sort.Ints(indices)
	return indices
}
