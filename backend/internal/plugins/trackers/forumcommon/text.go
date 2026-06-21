package forumcommon

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// DecodeWindows1251 converts a windows-1251 (Cyrillic) byte string into UTF-8.
// Several Russian forum trackers (RuTracker, Kinozal) serve cp1251, whose
// Cyrillic high bytes are invalid UTF-8 — so we only transcode when the input
// is NOT already valid UTF-8. That keeps ASCII and already-UTF-8 sources
// untouched while fixing real cp1251 titles. On a decode error the input is
// returned as-is rather than dropped. This matters because undecoded cp1251
// Cyrillic is invalid UTF-8 and Postgres rejects it (SQLSTATE 22021) on write.
func DecodeWindows1251(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	out, err := charmap.Windows1251.NewDecoder().String(s)
	if err != nil {
		return s
	}
	return out
}

// CleanTitle decodes a raw <title> match from windows-1251, trims it, and
// strips the given site suffix (e.g. " :: RuTracker.org"). Shared by the forum
// trackers' Check and ResolveMetadata so display names stay consistent.
func CleanTitle(raw, siteSuffix string) string {
	t := strings.TrimSpace(DecodeWindows1251(raw))
	return strings.TrimSuffix(t, siteSuffix)
}
