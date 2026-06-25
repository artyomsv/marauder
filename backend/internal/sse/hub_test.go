package sse

import (
	"bufio"
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/events"
)

func TestPublish_DeliversToSubscriberOfSameUser(t *testing.T) {
	h := NewHub(zerolog.Nop())
	uid := uuid.New()
	ch, unsub := h.Subscribe(uid)
	defer unsub()

	tid := uuid.New()
	h.Publish(uid, events.Event{UserID: uid, TopicID: &tid, Type: events.DownloadProgress, Title: "X",
		Data: map[string]any{"percent_done": 0.5}}, 0)

	select {
	case frame := <-ch:
		if !bytes.Contains(frame, []byte("download.progress")) {
			t.Errorf("frame missing type: %s", frame)
		}
		if !bytes.Contains(frame, []byte("data: ")) {
			t.Errorf("frame missing data line: %s", frame)
		}
		if bytes.Contains(frame, []byte("id:")) {
			t.Errorf("ephemeral event (id=0) must not carry an id line: %s", frame)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for frame")
	}
}

func TestPublish_PersistedEventCarriesIDLine(t *testing.T) {
	h := NewHub(zerolog.Nop())
	uid := uuid.New()
	ch, unsub := h.Subscribe(uid)
	defer unsub()
	tid := uuid.New()
	h.Publish(uid, events.Event{UserID: uid, TopicID: &tid, Type: events.DownloadCompleted, Title: "Done"}, 42)
	frame := <-ch
	sc := bufio.NewScanner(bytes.NewReader(frame))
	var hasID bool
	for sc.Scan() {
		if sc.Text() == "id: 42" {
			hasID = true
		}
	}
	if !hasID {
		t.Errorf("persisted event must carry 'id: 42': %s", frame)
	}
}

func TestPublish_OnlyToOwningUser(t *testing.T) {
	h := NewHub(zerolog.Nop())
	me, other := uuid.New(), uuid.New()
	mine, unsub1 := h.Subscribe(me)
	defer unsub1()
	theirs, unsub2 := h.Subscribe(other)
	defer unsub2()
	h.Publish(other, events.Event{UserID: other, Type: events.CheckStarted}, 0)
	select {
	case <-mine:
		t.Fatal("received another user's event")
	case <-theirs:
		// correct
	case <-time.After(500 * time.Millisecond):
		t.Fatal("owner did not receive event")
	}
}

func TestPublish_DropsOnFullBuffer_NoBlock(t *testing.T) {
	h := NewHub(zerolog.Nop())
	uid := uuid.New()
	_, unsub := h.Subscribe(uid) // never drained
	defer unsub()
	done := make(chan struct{})
	go func() {
		for i := 0; i < subscriberBuffer+50; i++ {
			h.Publish(uid, events.Event{UserID: uid, Type: events.CheckStarted}, 0)
		}
		close(done)
	}()
	select {
	case <-done: // Publish never blocked despite a full, undrained buffer
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a full subscriber buffer")
	}
}

func TestUnsubscribe_StopsDelivery(t *testing.T) {
	h := NewHub(zerolog.Nop())
	uid := uuid.New()
	ch, unsub := h.Subscribe(uid)
	unsub()
	h.Publish(uid, events.Event{UserID: uid, Type: events.CheckStarted}, 0)
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("received a frame after unsubscribe")
		}
	case <-time.After(200 * time.Millisecond):
		// also acceptable: nothing delivered
	}
	_ = context.Background()
}
