// Package infohash derives the BitTorrent v1 infohash from a download
// payload so Marauder can correlate what it pushed with what a torrent
// client reports.
//
// The infohash is the universal key linking a delivery to live client
// status: qBittorrent queries by `hashes=`, Transmission's torrent-get
// accepts `hashString`, and both speak the same 40-char lowercase hex.
//
// Two sources cover every payload Marauder produces:
//
//   - Magnet URIs (RuTracker, torznab, genericmagnet): the hash is the
//     `xt=urn:btih:` parameter, in 40-char hex or 32-char base32.
//   - .torrent files (LostFilm, generictorrentfile): the hash is the
//     SHA-1 of the bencoded `info` dictionary's raw bytes.
//
// The bencode walk is length-based, so binary bytes inside the `pieces`
// field (which routinely contain 'e'/'d'/':' bytes) never desync the
// parser — that correctness property is why this is a hand-rolled scanner
// rather than a regex.
package infohash

import (
	"crypto/sha1"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// FromPayload returns the lowercase-hex BitTorrent v1 infohash for a
// download payload. The magnet URI is preferred when present (cheaper, no
// bencode parse); otherwise the .torrent bytes are hashed. Returns an
// error when neither source yields a usable hash — callers treat that as
// "skip delivery tracking for this push", never as a download failure.
func FromPayload(magnetURI string, torrentFile []byte) (string, error) {
	if strings.TrimSpace(magnetURI) != "" {
		return FromMagnet(magnetURI)
	}
	if len(torrentFile) > 0 {
		return FromTorrent(torrentFile)
	}
	return "", errors.New("payload has neither magnet uri nor torrent file")
}

// FromMagnet extracts the v1 infohash from a magnet URI's
// `xt=urn:btih:<hash>` parameter, normalised to lowercase hex. Both the
// 40-char hex and 32-char base32 encodings are valid in magnet links and
// both return hex.
func FromMagnet(magnet string) (string, error) {
	q := magnet
	if i := strings.IndexByte(q, '?'); i >= 0 {
		q = q[i+1:]
	}
	vals, err := url.ParseQuery(q)
	if err != nil {
		return "", fmt.Errorf("parse magnet query: %w", err)
	}
	const prefix = "urn:btih:"
	for _, xt := range vals["xt"] {
		if !strings.HasPrefix(xt, prefix) {
			continue
		}
		return normalizeHash(strings.TrimPrefix(xt, prefix))
	}
	return "", errors.New("magnet has no urn:btih infohash")
}

// FromTorrent computes the v1 infohash (SHA-1 of the bencoded `info`
// dictionary) from a raw .torrent file, returned as lowercase hex.
func FromTorrent(data []byte) (string, error) {
	info, err := infoDictBytes(data)
	if err != nil {
		return "", err
	}
	sum := sha1.Sum(info)
	return hex.EncodeToString(sum[:]), nil
}

// normalizeHash converts a btih payload (hex or base32) into lowercase hex.
func normalizeHash(h string) (string, error) {
	h = strings.TrimSpace(h)
	switch len(h) {
	case 40:
		if _, err := hex.DecodeString(h); err != nil {
			return "", fmt.Errorf("invalid hex infohash: %w", err)
		}
		return strings.ToLower(h), nil
	case 32:
		raw, err := base32.StdEncoding.DecodeString(strings.ToUpper(h))
		if err != nil {
			return "", fmt.Errorf("invalid base32 infohash: %w", err)
		}
		return hex.EncodeToString(raw), nil
	default:
		return "", fmt.Errorf("infohash has unexpected length %d", len(h))
	}
}

// infoDictBytes locates the raw byte span of the top-level `info` value in
// a bencoded torrent. The span is returned verbatim (not re-encoded) so
// the SHA-1 matches what every BitTorrent client computes.
func infoDictBytes(data []byte) ([]byte, error) {
	if len(data) == 0 || data[0] != 'd' {
		return nil, errors.New("not a bencoded dictionary")
	}
	pos := 1
	for pos < len(data) && data[pos] != 'e' {
		key, next, err := scanString(data, pos)
		if err != nil {
			return nil, err
		}
		valStart := next
		valEnd, err := scanValue(data, valStart)
		if err != nil {
			return nil, err
		}
		if string(key) == "info" {
			return data[valStart:valEnd], nil
		}
		pos = valEnd
	}
	return nil, errors.New("no info dictionary in torrent")
}

// scanValue returns the index immediately past the bencoded value at pos.
func scanValue(data []byte, pos int) (int, error) {
	if pos >= len(data) {
		return 0, errors.New("unexpected end of bencode")
	}
	c := data[pos]
	switch {
	case c == 'i':
		end := indexByteFrom(data, pos+1, 'e')
		if end < 0 {
			return 0, errors.New("unterminated integer")
		}
		return end + 1, nil
	case c == 'l' || c == 'd':
		pos++
		for pos < len(data) && data[pos] != 'e' {
			if c == 'd' {
				// Dict entries are key/value pairs; the key is a string.
				_, next, err := scanString(data, pos)
				if err != nil {
					return 0, err
				}
				pos = next
			}
			next, err := scanValue(data, pos)
			if err != nil {
				return 0, err
			}
			pos = next
		}
		if pos >= len(data) {
			return 0, errors.New("unterminated container")
		}
		return pos + 1, nil // step past the closing 'e'
	case c >= '0' && c <= '9':
		_, next, err := scanString(data, pos)
		if err != nil {
			return 0, err
		}
		return next, nil
	default:
		return 0, fmt.Errorf("unexpected bencode byte %q", c)
	}
}

// scanString reads a `<len>:<bytes>` token and returns the bytes plus the
// index just past them.
func scanString(data []byte, pos int) ([]byte, int, error) {
	colon := indexByteFrom(data, pos, ':')
	if colon < 0 {
		return nil, 0, errors.New("bencode string missing colon")
	}
	n, err := strconv.Atoi(string(data[pos:colon]))
	if err != nil || n < 0 {
		return nil, 0, errors.New("invalid bencode string length")
	}
	start := colon + 1
	end := start + n
	if end > len(data) {
		return nil, 0, errors.New("bencode string length exceeds data")
	}
	return data[start:end], end, nil
}

// indexByteFrom is bytes.IndexByte starting at an offset, returning an
// absolute index or -1.
func indexByteFrom(data []byte, from int, b byte) int {
	for i := from; i < len(data); i++ {
		if data[i] == b {
			return i
		}
	}
	return -1
}
