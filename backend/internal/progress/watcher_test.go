package progress

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/events"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

type fakeDeliveries struct {
	inflight  []*domain.InFlightDelivery
	completed []uuid.UUID
	markWon   bool
}

func (f *fakeDeliveries) ListInFlight(_ context.Context) ([]*domain.InFlightDelivery, error) {
	return f.inflight, nil
}
func (f *fakeDeliveries) MarkCompleted(_ context.Context, id uuid.UUID) (bool, error) {
	f.completed = append(f.completed, id)
	return f.markWon, nil
}

type fakeClients struct{ client *domain.Client }

func (f *fakeClients) GetByID(_ context.Context, _ uuid.UUID, _ uuid.UUID) (*domain.Client, error) {
	return f.client, nil
}

type fakeDecryptor struct{}

func (fakeDecryptor) Decrypt(ct, _ []byte) ([]byte, error) { return ct, nil }

type fakeEmitter struct{ events []events.Event }

func (f *fakeEmitter) Emit(_ context.Context, ev events.Event) { f.events = append(f.events, ev) }

type fakeStatus struct{ statuses []registry.TorrentStatus }

func (f fakeStatus) Name() string                       { return "fake" }
func (f fakeStatus) DisplayName() string                { return "Fake" }
func (f fakeStatus) ConfigSchema() map[string]any       { return nil }
func (f fakeStatus) Test(context.Context, []byte) error { return nil }
func (f fakeStatus) Add(context.Context, []byte, *domain.Payload, domain.AddOptions) error {
	return nil
}
func (f fakeStatus) Status(_ context.Context, _ []byte, _ []string) ([]registry.TorrentStatus, error) {
	return f.statuses, nil
}

func newTestWatcher(t *testing.T, del *fakeDeliveries, emit *fakeEmitter, st fakeStatus) *Watcher {
	t.Helper()
	cid := uuid.New()
	w := New(del, &fakeClients{client: &domain.Client{ID: cid, ClientName: "fake"}}, fakeDecryptor{}, emit,
		Config{PollInterval: 0, PublicBaseURL: "http://x"}, zerolog.Nop())
	w.statusLookup = func(string) (registry.WithStatus, bool) { return st, true }
	return w
}

func inflight(clientID uuid.UUID) *domain.InFlightDelivery {
	tid, uid, did := uuid.New(), uuid.New(), uuid.New()
	return &domain.InFlightDelivery{
		DeliveryID: did, TopicID: tid, UserID: uid, ClientID: &clientID,
		Infohash: "abc123", Label: "s01e01", DisplayName: "Show",
	}
}

func TestPoll_Seeding_MarksCompletedAndEmits(t *testing.T) {
	cid := uuid.New()
	d := inflight(cid)
	del := &fakeDeliveries{inflight: []*domain.InFlightDelivery{d}, markWon: true}
	emit := &fakeEmitter{}
	st := fakeStatus{statuses: []registry.TorrentStatus{{Hash: "ABC123", PercentDone: 1.0, State: registry.StateSeeding}}}
	w := newTestWatcher(t, del, emit, st)
	w.poll(context.Background())
	if len(del.completed) != 1 || del.completed[0] != d.DeliveryID {
		t.Fatalf("expected MarkCompleted for the delivery, got %v", del.completed)
	}
	if len(emit.events) != 1 || emit.events[0].Type != events.DownloadCompleted {
		t.Fatalf("expected one DownloadCompleted event, got %+v", emit.events)
	}
	if emit.events[0].TopicID == nil || *emit.events[0].TopicID != d.TopicID {
		t.Error("event should carry the delivery's topic id")
	}
}

func TestPoll_StillDownloading_EmitsProgressNotCompletion(t *testing.T) {
	cid := uuid.New()
	del := &fakeDeliveries{inflight: []*domain.InFlightDelivery{inflight(cid)}, markWon: true}
	emit := &fakeEmitter{}
	st := fakeStatus{statuses: []registry.TorrentStatus{{Hash: "abc123", PercentDone: 0.5, State: registry.StateDownloading}}}
	w := newTestWatcher(t, del, emit, st)
	w.poll(context.Background())
	if len(del.completed) != 0 {
		t.Fatalf("downloading torrent must not complete: %v", del.completed)
	}
	if countType(emit, events.DownloadProgress) != 1 || countType(emit, events.DownloadCompleted) != 0 {
		t.Fatalf("expected one progress, no completion: %+v", emit.events)
	}
}

func TestPoll_DownloadingEmitsProgressOnChange(t *testing.T) {
	cid := uuid.New()
	d := inflight(cid)
	del := &fakeDeliveries{inflight: []*domain.InFlightDelivery{d}, markWon: true}
	emit := &fakeEmitter{}
	st := fakeStatus{statuses: []registry.TorrentStatus{{Hash: "abc123", PercentDone: 0.5, State: registry.StateDownloading}}}
	w := newTestWatcher(t, del, emit, st)

	w.poll(context.Background()) // first sight → emit progress
	if got := countType(emit, events.DownloadProgress); got != 1 {
		t.Fatalf("first poll progress emits = %d, want 1", got)
	}
	d0 := lastProgress(emit)
	if d0["percent_done"] != 0.5 || d0["state"] != registry.StateDownloading || d0["infohash"] != "abc123" {
		t.Fatalf("progress data wrong: %+v", d0)
	}

	emit.events = nil
	w.poll(context.Background()) // unchanged → no re-emit
	if got := countType(emit, events.DownloadProgress); got != 0 {
		t.Fatalf("unchanged poll progress emits = %d, want 0", got)
	}
}

func countType(e *fakeEmitter, ty events.Type) int {
	n := 0
	for _, ev := range e.events {
		if ev.Type == ty {
			n++
		}
	}
	return n
}

func lastProgress(e *fakeEmitter) map[string]any {
	for i := len(e.events) - 1; i >= 0; i-- {
		if e.events[i].Type == events.DownloadProgress {
			return e.events[i].Data
		}
	}
	return nil
}

func TestPoll_LostTransition_NoEmit(t *testing.T) {
	cid := uuid.New()
	del := &fakeDeliveries{inflight: []*domain.InFlightDelivery{inflight(cid)}, markWon: false} // already completed by someone
	emit := &fakeEmitter{}
	st := fakeStatus{statuses: []registry.TorrentStatus{{Hash: "abc123", PercentDone: 1.0, State: registry.StateSeeding}}}
	w := newTestWatcher(t, del, emit, st)
	w.poll(context.Background())
	// MarkCompleted must still be attempted (we mark, then gate the emit on the
	// won bool) — assert it so a regression that skips the mark is caught.
	if len(del.completed) != 1 {
		t.Fatalf("expected MarkCompleted to be attempted once, got %v", del.completed)
	}
	if len(emit.events) != 0 {
		t.Fatalf("a lost NULL→now transition must not emit: %+v", emit.events)
	}
}

func TestPoll_ClientWithoutStatus_Skipped(t *testing.T) {
	cid := uuid.New()
	del := &fakeDeliveries{inflight: []*domain.InFlightDelivery{inflight(cid)}, markWon: true}
	emit := &fakeEmitter{}
	w := newTestWatcher(t, del, emit, fakeStatus{})
	w.statusLookup = func(string) (registry.WithStatus, bool) { return nil, false }
	w.poll(context.Background())
	if len(del.completed) != 0 || len(emit.events) != 0 {
		t.Fatalf("client without WithStatus must be skipped")
	}
}
