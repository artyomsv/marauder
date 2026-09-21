package pageredact

import (
	"bytes"
	"sort"
)

// span is a half-open byte range [start, end) in the ORIGINAL page.
type span struct{ start, end int }

// edit replaces one source span with Placeholder, or removes it outright
// when drop is set. Dropping is for the second and later pieces of one secret
// that inline markup split in two: the placeholder appears once, where the
// secret began, and the markup between the pieces is kept.
type edit struct {
	span
	drop bool
}

type editList []edit

func (l *editList) replace(s span) {
	if s.start < s.end {
		*l = append(*l, edit{span: s})
	}
}

// replaceAll records one secret that maps to one or more source pieces.
func (l *editList) replaceAll(pieces []span) {
	placed := false
	for _, p := range pieces {
		if p.start >= p.end {
			continue
		}
		*l = append(*l, edit{span: p, drop: placed})
		placed = true
	}
}

// apply writes page with every edit applied. Edits are computed against the
// original page, never against each other's output, so no detector can
// re-match text another one inserted.
func (l editList) apply(page []byte) []byte {
	if len(l) == 0 {
		return append([]byte(nil), page...)
	}
	es := absorbPlaceholders(page, l)
	sort.Slice(es, func(i, j int) bool { return es[i].start < es[j].start })
	merged := []edit{es[0]}
	for _, e := range es[1:] {
		cur := &merged[len(merged)-1]
		if e.start < cur.end {
			cur.end = max(cur.end, e.end)
			cur.drop = cur.drop && e.drop
			continue
		}
		merged = append(merged, e)
	}
	var out bytes.Buffer
	out.Grow(len(page))
	pos := 0
	for _, e := range merged {
		out.Write(page[pos:e.start])
		if !e.drop {
			out.WriteString(Placeholder)
		}
		pos = e.end
	}
	out.Write(page[pos:])
	return out.Bytes()
}

// absorbPlaceholders widens any edit that overlaps a placeholder already in
// the page to cover the whole placeholder. That is what makes a second pass
// change nothing: without it, a username of `REDACTED` matches inside
// `MARAUDER-REDACTED` and rewrites it to `MARAUDER-MARAUDER-REDACTED`.
func absorbPlaceholders(page []byte, l editList) []edit {
	out := append([]edit(nil), l...)
	ph := []byte(Placeholder)
	for off := 0; ; {
		i := bytes.Index(page[off:], ph)
		if i < 0 {
			return out
		}
		p := span{off + i, off + i + len(ph)}
		for k := range out {
			if out[k].start < p.end && p.start < out[k].end {
				out[k].start = min(out[k].start, p.start)
				out[k].end = max(out[k].end, p.end)
				out[k].drop = false
			}
		}
		off = p.end
	}
}
