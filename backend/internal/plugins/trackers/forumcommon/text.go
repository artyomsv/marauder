package forumcommon

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
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

// EncodeWindows1251Query percent-escapes s for use as a query parameter on a
// cp1251 site (RuTracker's tracker.php?nm=). Go's url.Values.Encode emits
// UTF-8 percent-encoding, which cp1251 backends read as mojibake — every
// Cyrillic search would return zero results. So: transcode to cp1251 first
// (unmappable runes become the encoder's substitute byte rather than failing
// the whole query), then percent-escape everything outside RFC-3986
// unreserved.
func EncodeWindows1251Query(s string) string {
	enc, err := encoding.ReplaceUnsupported(charmap.Windows1251.NewEncoder()).String(s)
	if err != nil {
		enc = s // fall back to UTF-8 bytes; a degraded search beats an error
	}
	var b strings.Builder
	for i := 0; i < len(enc); i++ {
		c := enc[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
