package retention

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// fakeStore records every prune call.
type fakeStore struct {
	calls   []time.Time
	deleted int64
	err     error
}

func (f *fakeStore) DeleteOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	f.calls = append(f.calls, cutoff)
	return f.deleted, f.err
}

func newPruner(t *testing.T, store Store, cfg Config) *Pruner {
	t.Helper()
	p := New(store, cfg, zerolog.New(io.Discard))
	// Pin the clock so the expected cutoff is exact.
	p.now = func() time.Time { return time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC) }
	return p
}

func TestPruner_PruneOnce_DeletesEverythingOlderThanMaxAge(t *testing.T) {
	store := &fakeStore{deleted: 30}
	p := newPruner(t, store, Config{MaxAge: 90 * 24 * time.Hour, Interval: time.Hour})

	p.pruneOnce(context.Background())

	if len(store.calls) != 1 {
		t.Fatalf("DeleteOlderThan calls = %d, want 1", len(store.calls))
	}
	want := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	if !store.calls[0].Equal(want) {
		t.Errorf("cutoff = %v, want %v (now - 90d)", store.calls[0], want)
	}
}

// A zero MaxAge is the documented "keep history forever" setting, so it must
// never issue a DELETE — a cutoff of now would wipe the whole table.
func TestPruner_PruneOnce_MaxAgeZero_KeepsEverything(t *testing.T) {
	store := &fakeStore{}
	p := newPruner(t, store, Config{MaxAge: 0, Interval: time.Hour})

	p.pruneOnce(context.Background())

	if len(store.calls) != 0 {
		t.Errorf("DeleteOlderThan calls = %d, want 0 when retention is disabled", len(store.calls))
	}
}

// Pruning is housekeeping: a DB error must be logged and swallowed so the loop
// survives to try again rather than taking the process down.
func TestPruner_PruneOnce_StoreError_IsSwallowed(t *testing.T) {
	store := &fakeStore{err: errors.New("connection reset")}
	p := newPruner(t, store, Config{MaxAge: time.Hour, Interval: time.Hour})

	p.pruneOnce(context.Background())

	if len(store.calls) != 1 {
		t.Errorf("DeleteOlderThan calls = %d, want 1", len(store.calls))
	}
}

// Start must return promptly and its loop must exit on ctx cancellation.
func TestPruner_Start_StopsOnContextCancel(t *testing.T) {
	store := &fakeStore{}
	p := newPruner(t, store, Config{MaxAge: time.Hour, Interval: time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cancel()
	<-p.done
}
