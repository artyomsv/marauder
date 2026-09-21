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
	es := mergeEdits(append([]edit(nil), l...))
	es = mergeEdits(absorbPlaceholders(page, es))
	var out bytes.Buffer
	out.Grow(len(page))
	pos := 0
	for _, e := range es {
		out.Write(page[pos:e.start])
		if !e.drop {
			out.WriteString(Placeholder)
		}
		pos = e.end
	}
	out.Write(page[pos:])
	return out.Bytes()
}

// mergeEdits sorts es by start and merges overlapping edits. Adjacent edits
// stay separate: two secrets side by side are two placeholders. A merged edit
// is dropped only if every part of it was a drop.
func mergeEdits(es []edit) []edit {
	sort.Slice(es, func(i, j int) bool { return es[i].start < es[j].start })
	out := es[:1]
	for _, e := range es[1:] {
		cur := &out[len(out)-1]
		if e.start < cur.end {
			cur.end = max(cur.end, e.end)
			cur.drop = cur.drop && e.drop
			continue
		}
		out = append(out, e)
	}
	return out
}

// absorbPlaceholders widens any edit that overlaps a placeholder already in
// the page to cover the whole placeholder. That is what makes a second pass
// change nothing: without it, a username of `REDACTED` matches inside
// `MARAUDER-REDACTED` and rewrites it to `MARAUDER-MARAUDER-REDACTED`.
//
// es must be sorted and disjoint (mergeEdits), which is what lets this be one
// forward sweep. Placeholders and edits are both ordered, so the first edit
// that can still overlap only ever moves forward; comparing every placeholder
// with every edit took 17.5 s on a 3.8 MB second pass.
func absorbPlaceholders(page []byte, es []edit) []edit {
	ph := []byte(Placeholder)
	k := 0
	for off := 0; ; {
		i := bytes.Index(page[off:], ph)
		if i < 0 {
			return es
		}
		p := span{off + i, off + i + len(ph)}
		for k < len(es) && es[k].end <= p.start {
			k++
		}
		for j := k; j < len(es) && es[j].start < p.end; j++ {
			es[j].start = min(es[j].start, p.start)
			es[j].end = max(es[j].end, p.end)
			es[j].drop = false
		}
		off = p.end
	}
}
