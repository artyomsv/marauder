package infohash

import (
	"crypto/sha1"
	"encoding/base32"
	"encoding/hex"
	"strings"
	"testing"
)

func TestFromMagnet_Hex(t *testing.T) {
	const h = "C12FE1C06BBA254A9DC9F519B335AA7C1367A88A"
	got, err := FromMagnet("magnet:?xt=urn:btih:" + h + "&dn=Some+Release")
	if err != nil {
		t.Fatalf("FromMagnet: %v", err)
	}
	if got != strings.ToLower(h) {
		t.Errorf("got %q, want %q", got, strings.ToLower(h))
	}
}

func TestFromMagnet_Base32(t *testing.T) {
	// 20 arbitrary bytes → base32 is the alternate magnet encoding. We
	// assert the helper decodes it back to the same hex those bytes spell.
	raw := []byte("0123456789abcdef0123") // 20 bytes
	b32 := base32.StdEncoding.EncodeToString(raw)
	if len(b32) != 32 {
		t.Fatalf("test setup: base32 len = %d, want 32", len(b32))
	}
	got, err := FromMagnet("magnet:?xt=urn:btih:" + b32)
	if err != nil {
		t.Fatalf("FromMagnet base32: %v", err)
	}
	if want := hex.EncodeToString(raw); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFromMagnet_NoBtih(t *testing.T) {
	if _, err := FromMagnet("magnet:?dn=name&tr=udp://x"); err == nil {
		t.Error("expected error for magnet without btih")
	}
}

// TestFromTorrent_ExtractsInfoSpan builds a torrent dict with sibling keys
// around the info dict and asserts FromTorrent hashes exactly the info
// bytes — the only nontrivial logic (the SHA-1 itself is stdlib). `want`
// is computed independently from how the locator finds the span, so this
// is a real test of extraction, not a tautology.
func TestFromTorrent_ExtractsInfoSpan(t *testing.T) {
	info := []byte("d6:lengthi3e4:name3:foo12:piece lengthi16384e6:pieces20:" +
		"AAAAAAAAAAAAAAAAAAAAe")
	full := append([]byte("d8:announce10:http://x/y4:info"), info...)
	full = append(full, 'e') // close the outer dict

	sum := sha1.Sum(info)
	want := hex.EncodeToString(sum[:])

	got, err := FromTorrent(full)
	if err != nil {
		t.Fatalf("FromTorrent: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestFromTorrent_BinaryPiecesNoDesync proves the length-based scan is
// immune to bencode control bytes ('e', 'd', ':') appearing inside the
// binary pieces field — a regex-based extractor would mis-terminate here.
func TestFromTorrent_BinaryPiecesNoDesync(t *testing.T) {
	pieces := []byte{'e', 'e', 'd', ':', 0x00, 'i', '5', 'e', 0xff, 0x10,
		0x20, 0x30, 0x40, 0x50, 0x60, 0x70, 0x80, 0x90, 0xa0, 0xb0} // 20 bytes
	info := append([]byte("d6:lengthi9e4:name1:x6:pieces20:"), pieces...)
	info = append(info, 'e')
	full := append([]byte("d4:info"), info...)
	full = append(full, 'e')

	sum := sha1.Sum(info)
	want := hex.EncodeToString(sum[:])

	got, err := FromTorrent(full)
	if err != nil {
		t.Fatalf("FromTorrent: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFromTorrent_NoInfo(t *testing.T) {
	if _, err := FromTorrent([]byte("d8:announce3:abce")); err == nil {
		t.Error("expected error when info dict is absent")
	}
}

func TestFromTorrent_NotBencode(t *testing.T) {
	if _, err := FromTorrent([]byte("not a torrent")); err == nil {
		t.Error("expected error for non-bencoded input")
	}
}

func TestFromPayload_PrefersMagnet(t *testing.T) {
	const h = "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	got, err := FromPayload("magnet:?xt=urn:btih:"+h, []byte("ignored"))
	if err != nil {
		t.Fatalf("FromPayload: %v", err)
	}
	if got != h {
		t.Errorf("got %q, want %q", got, h)
	}
}

func TestFromPayload_FallsBackToTorrent(t *testing.T) {
	info := []byte("d6:lengthi1e4:name1:a6:pieces20:AAAAAAAAAAAAAAAAAAAAe")
	full := append([]byte("d4:info"), info...)
	full = append(full, 'e')
	sum := sha1.Sum(info)
	want := hex.EncodeToString(sum[:])

	got, err := FromPayload("", full)
	if err != nil {
		t.Fatalf("FromPayload: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFromPayload_Empty(t *testing.T) {
	if _, err := FromPayload("", nil); err == nil {
		t.Error("expected error for empty payload")
	}
}
