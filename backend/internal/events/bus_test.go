package events

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

type fakeRecorder struct {
	got    *domain.TopicEvent
	retID  int64
	called int
}

func (f *fakeRecorder) Record(_ context.Context, e *domain.TopicEvent) (int64, error) {
	f.called++
	f.got = e
	return f.retID, nil
}

type fakeNotifier struct {
	event      string
	notifierID *uuid.UUID
	called     int
}

func (f *fakeNotifier) SendVia(_ context.Context, _ uuid.UUID, nid *uuid.UUID, event string, _ domain.Message) int {
	f.called++
	f.event = event
	f.notifierID = nid
	return 1
}

type fakePublisher struct {
	called int
	id     int64
}

func (f *fakePublisher) Publish(_ uuid.UUID, _ Event, id int64) { f.called++; f.id = id }

func newBus(t *testing.T) (*Bus, *fakeRecorder, *fakeNotifier, *fakePublisher) {
	t.Helper()
	rec := &fakeRecorder{retID: 42}
	notif := &fakeNotifier{}
	pub := &fakePublisher{}
	return New(rec, notif, pub, zerolog.Nop()), rec, notif, pub
}

func TestEmit_ReleaseFound_PersistsNotifiesAndPublishes(t *testing.T) {
	bus, rec, notif, pub := newBus(t)
	tid := uuid.New()
	bus.Emit(context.Background(), Event{
		UserID: uuid.New(), TopicID: &tid, Type: ReleaseFound,
		Severity: "info", Title: "X", Body: "Y",
	})
	if rec.called != 1 {
		t.Errorf("recorder called %d, want 1", rec.called)
	}
	if rec.got.EventType != string(ReleaseFound) {
		t.Errorf("recorded type %s, want release.found", rec.got.EventType)
	}
	if notif.called != 1 || notif.event != string(ReleaseFound) {
		t.Errorf("notifier called %d event %q", notif.called, notif.event)
	}
	if pub.called != 1 || pub.id != 42 {
		t.Errorf("publisher called %d id %d, want 1/42", pub.called, pub.id)
	}
}

func TestEmit_CheckStarted_PublishesOnly(t *testing.T) {
	bus, rec, notif, pub := newBus(t)
	tid := uuid.New()
	bus.Emit(context.Background(), Event{UserID: uuid.New(), TopicID: &tid, Type: CheckStarted})
	if rec.called != 0 {
		t.Errorf("recorder called %d, want 0", rec.called)
	}
	if notif.called != 0 {
		t.Errorf("notifier called %d, want 0", notif.called)
	}
	if pub.called != 1 {
		t.Errorf("publisher called %d, want 1", pub.called)
	}
	if pub.id != 0 {
		t.Errorf("ephemeral publish id %d, want 0", pub.id)
	}
}

func TestEmit_NilSeams_NoPanic(t *testing.T) {
	bus := New(nil, nil, nil, zerolog.Nop())
	tid := uuid.New()
	bus.Emit(context.Background(), Event{UserID: uuid.New(), TopicID: &tid, Type: ReleaseFound})
	// no panic = pass
}
