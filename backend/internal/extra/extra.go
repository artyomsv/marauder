// Package extra contains shared helpers for reading values out of the
// untyped map[string]any blob that domain.Topic.Extra and
// domain.Check.Extra carry. JSON round-tripping through pgxpool turns
// []string into []any and int into float64, so these helpers handle
// both shapes uniformly.
package extra

import "strconv"

// Int reads an integer value from an extras map. Returns 0 if the key
// is missing, the value is nil, or the value is not coercible to int.
// Handles int / int64 / float64 / string forms (since JSON encoding
// produces float64 for numbers and the API may pass strings).
func Int(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(t) // best-effort: malformed values yield 0
		return n
	}
	return 0
}

// StringSlice reads a slice-of-string value from an extras map. Returns
// nil if the key is missing or the value is not a slice. JSON round-
// tripping turns []string into []any, so we handle both shapes.
func StringSlice(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// String reads a string value from an extras map, returning the
// fallback if the key is missing, nil, or not a string.
func String(m map[string]any, key, fallback string) string {
	if m == nil {
		return fallback
	}
	v, ok := m[key]
	if !ok || v == nil {
		return fallback
	}
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return fallback
}
