package torrentmeta

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
)

// The input is a tracker response, so the decoder is bounded: nesting depth
// (the same bound infohash uses, turning a crafted payload's unbounded
// recursion into an error), list length, and the total number of values.
//
// The value cap is about memory, not time. Every value consumes at least one
// input byte, but decodes into an interface, a slice element or a map entry
// costing tens of bytes of heap, so an 8 MiB torrent of tiny values would
// become hundreds of MB — on every .torrent delivery. A real pack needs about
// five values per file (the entry, its length, its path list and components),
// so a 5000-file torrent stays well under 100k.
const (
	maxDepth   = 32
	maxListLen = 100_000
	maxValues  = 250_000
)

var (
	errUnexpectedEnd = errors.New("bencode: unexpected end of data")
	errTooManyValues = errors.New("bencode: too many values")
)

type decoder struct {
	data   []byte
	pos    int
	values int
}

// value decodes the value at d.pos. Integers become int64, strings string,
// lists []any and dictionaries map[string]any.
func (d *decoder) value(depth int) (any, error) {
	if depth > maxDepth {
		return nil, errors.New("bencode: nested too deeply")
	}
	d.values++
	if d.values > maxValues {
		return nil, errTooManyValues
	}
	if d.pos >= len(d.data) {
		return nil, errUnexpectedEnd
	}
	switch c := d.data[d.pos]; {
	case c == 'i':
		end := bytes.IndexByte(d.data[d.pos:], 'e')
		if end < 0 {
			return nil, errUnexpectedEnd
		}
		n, err := strconv.ParseInt(string(d.data[d.pos+1:d.pos+end]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("bencode: integer: %w", err)
		}
		d.pos += end + 1
		return n, nil
	case c == 'l':
		d.pos++
		out := []any{}
		for {
			if d.pos >= len(d.data) {
				return nil, errUnexpectedEnd
			}
			if d.data[d.pos] == 'e' {
				d.pos++
				return out, nil
			}
			if len(out) >= maxListLen {
				return nil, errors.New("bencode: list too long")
			}
			v, err := d.value(depth + 1)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
	case c == 'd':
		d.pos++
		out := map[string]any{}
		for {
			if d.pos >= len(d.data) {
				return nil, errUnexpectedEnd
			}
			if d.data[d.pos] == 'e' {
				d.pos++
				return out, nil
			}
			k, err := d.str()
			if err != nil {
				return nil, err
			}
			v, err := d.value(depth + 1)
			if err != nil {
				return nil, err
			}
			out[k] = v
		}
	case c >= '0' && c <= '9':
		return d.str()
	default:
		return nil, fmt.Errorf("bencode: unexpected byte %q at offset %d", c, d.pos)
	}
}

func (d *decoder) str() (string, error) {
	colon := bytes.IndexByte(d.data[d.pos:], ':')
	if colon < 0 {
		return "", errUnexpectedEnd
	}
	n, err := strconv.Atoi(string(d.data[d.pos : d.pos+colon]))
	if err != nil || n < 0 {
		return "", fmt.Errorf("bencode: bad string length at offset %d", d.pos)
	}
	start := d.pos + colon + 1
	if n > len(d.data)-start {
		return "", errUnexpectedEnd
	}
	d.pos = start + n
	return string(d.data[start:d.pos]), nil
}
