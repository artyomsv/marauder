package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// fakeTopicEventsStore is a stub topicEventsStore for handler tests.
type fakeTopicEventsStore struct {
	rows []*domain.TopicEvent
	err  error
}

func (f *fakeTopicEventsStore) ListForTopic(_ context.Context, _, _ uuid.UUID, _ int, _ int64) ([]*domain.TopicEvent, error) {
	return f.rows, f.err
}

// fakeTopicOwner is a stub topicOwnerStore for handler tests.
type fakeTopicOwner struct {
	ok bool
}

func (f *fakeTopicOwner) GetByID(_ context.Context, _ uuid.UUID, _ *uuid.UUID) (*domain.Topic, error) {
	if f.ok {
		return &domain.Topic{ID: uuid.New()}, nil
	}
	return nil, errors.New("not found")
}

// topicEventsResp mirrors the JSON shape the handler returns.
type topicEventsResp struct {
	Events []struct {
		ID        int64          `json:"id"`
		EventType string         `json:"event_type"`
		Severity  string         `json:"severity"`
		Message   string         `json:"message"`
		Data      map[string]any `json:"data,omitempty"`
		CreatedAt string         `json:"created_at"`
	} `json:"events"`
}

// TestTopicEvents_List_ReturnsTopicHistory verifies that a GET on an owned
// topic returns 200 with events ordered as the store delivered them.
func TestTopicEvents_List_ReturnsTopicHistory(t *testing.T) {
	tid := uuid.New()
	store := &fakeTopicEventsStore{rows: []*domain.TopicEvent{
		{ID: 2, TopicID: tid, EventType: "release.found", Severity: "info", Message: "New release", CreatedAt: time.Now()},
		{ID: 1, TopicID: tid, EventType: "check.failed", Severity: "error", Message: "boom", CreatedAt: time.Now()},
	}}
	h := &TopicEvents{Events: store, Topics: &fakeTopicOwner{ok: true}, BaseURL: ""}

	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), nil), "id", tid.String())
	h.List(w, req)

	if w.Code != 200 {
		t.Fatalf("status %d, want 200; body %s", w.Code, w.Body.String())
	}
	var resp topicEventsResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Events) != 2 {
		t.Fatalf("got %d events, want 2", len(resp.Events))
	}
	if resp.Events[0].EventType != "release.found" {
		t.Errorf("events[0].event_type = %q, want release.found", resp.Events[0].EventType)
	}
	if resp.Events[1].EventType != "check.failed" {
		t.Errorf("events[1].event_type = %q, want check.failed", resp.Events[1].EventType)
	}
}

// TestTopicEvents_List_NotOwnedTopic verifies that when GetByID errors (topic
// not found or not owned), the handler returns 404.
func TestTopicEvents_List_NotOwnedTopic(t *testing.T) {
	store := &fakeTopicEventsStore{}
	h := &TopicEvents{Events: store, Topics: &fakeTopicOwner{ok: false}, BaseURL: ""}

	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), nil), "id", uuid.New().String())
	h.List(w, req)

	if w.Code != 404 {
		t.Fatalf("status %d, want 404; body %s", w.Code, w.Body.String())
	}
}
