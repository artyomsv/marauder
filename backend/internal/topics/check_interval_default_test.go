package topics

import "testing"

// Issue #250: an explicit interval wins, 0 takes the instance default, and a
// default outside the range (a bad settings read) falls back to the built-in.
func TestResolveCheckInterval(t *testing.T) {
	for _, tc := range []struct {
		name               string
		explicit, fallback int
		want               int
	}{
		{"explicit wins", 1800, 3600, 1800},
		{"zero takes default", 0, 3600, 3600},
		{"explicit out of range passes through", 10, 3600, 10},
		{"zero default uses built-in", 0, 0, DefaultCheckIntervalSec},
		{"default below range uses built-in", 0, MinCheckIntervalSec - 1, DefaultCheckIntervalSec},
		{"default above range uses built-in", 0, MaxCheckIntervalSec + 1, DefaultCheckIntervalSec},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveCheckInterval(tc.explicit, tc.fallback); got != tc.want {
				t.Errorf("ResolveCheckInterval(%d, %d) = %d, want %d", tc.explicit, tc.fallback, got, tc.want)
			}
		})
	}
}
