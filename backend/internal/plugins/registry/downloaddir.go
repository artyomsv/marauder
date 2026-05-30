package registry

import (
	"path"
	"strings"
)

// EffectiveDownloadDir resolves the save path a client plugin should use for
// a topic, unifying the per-topic override, the client's base download folder,
// and the topic's category across every client.
//
// Precedence:
//
//  1. override (the topic's explicit DownloadDir) wins outright when set;
//  2. otherwise the category nests under the client's base folder:
//     join(base, category);
//  3. with no override and no category, the client's base folder is used.
//
// Paths use forward slashes because they address remote torrent clients
// (qBittorrent savepath, Transmission download-dir, …); we deliberately use
// path.Join, not filepath.Join, so behaviour is identical regardless of the
// host OS Marauder runs on.
//
// The category is treated as a relative segment and cannot escape the base:
// it is cleaned as if rooted ("../../etc" -> "etc"), guarding against path
// traversal in user-supplied category values.
func EffectiveDownloadDir(base, override, category string) string {
	if override != "" {
		return override
	}
	category = sanitizeCategory(category)
	if category == "" {
		return base
	}
	return path.Join(base, category)
}

// sanitizeCategory normalises a user-supplied category into a safe relative
// path segment that cannot traverse above its base folder.
func sanitizeCategory(category string) string {
	category = strings.TrimSpace(category)
	if category == "" {
		return ""
	}
	// Root, clean, then strip the leading slash: this collapses any ".."
	// components so the result can never climb above the base it is joined to.
	cleaned := strings.TrimPrefix(path.Clean("/"+category), "/")
	if cleaned == "." {
		return ""
	}
	return cleaned
}
