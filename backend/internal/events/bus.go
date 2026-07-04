package events

import (
	"context"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// Event is one thing that happened, tagged with its canonical Type.
type Event struct {
	UserID     uuid.UUID
	TopicID    *uuid.UUID // nil for non-topic events (e.g. credential session.expired)
	NotifierID *uuid.UUID // per-topic notifier override; nil => the user's default notifiers
	Type       Type
	Severity   string // info | warn | error (defaults to "info" when empty)
	Title      string
	Body       string
	Link       string
	SourceURL  string // original tracker/topic page; empty for non-topic events
	Data       map[string]any
}

// Recorder persists an event to the topic_events history table.
type Recorder interface {
	Record(ctx context.Context, e *domain.TopicEvent) (int64, error)
}

// Notifier fans an event out to a user's notifier plugins. Implemented by
// *notify.Dispatcher. The scheduler already proved this seam shape.
type Notifier interface {
	SendVia(ctx context.Context, userID uuid.UUID, notifierID *uuid.UUID, event string, msg domain.Message) int
}

// Publisher pushes an event onto the live SSE feed. Phase 1 wires nil; the
// Phase 3 hub implements it. id is the persisted history id (0 if ephemeral).
type Publisher interface {
	Publish(userID uuid.UUID, ev Event, id int64)
}

// Bus is the single event->sinks fan-out point. Every sink is optional
// (nil-safe) so the bus is cheap to construct in tests and across phases.
type Bus struct {
	rec   Recorder
	notif Notifier
	pub   Publisher
	log   zerolog.Logger
}

// New constructs a Bus. Any of rec/notif/pub may be nil.
func New(rec Recorder, notif Notifier, pub Publisher, log zerolog.Logger) *Bus {
	return &Bus{rec: rec, notif: notif, pub: pub, log: log.With().Str("component", "events").Logger()}
}

// Emit routes ev to its policy-selected sinks. Best-effort: every sink
// failure is logged and never propagated — emitting an event must never
// break the caller's flow.
func (b *Bus) Emit(ctx context.Context, ev Event) {
	if ev.Severity == "" {
		ev.Severity = "info"
	}
	p := PolicyFor(ev.Type)

	var id int64
	if p.Persist && b.rec != nil && ev.TopicID != nil {
		rec := &domain.TopicEvent{
			TopicID:   *ev.TopicID,
			UserID:    ev.UserID,
			EventType: string(ev.Type),
			Severity:  ev.Severity,
			Message:   ev.Title,
			Data:      ev.Data,
		}
		got, err := b.rec.Record(ctx, rec)
		if err != nil {
			b.log.Warn().Err(err).Str("type", string(ev.Type)).Msg("events: record failed")
		} else {
			id = got
		}
	}

	if p.Notifiable && b.notif != nil {
		b.notif.SendVia(ctx, ev.UserID, ev.NotifierID, string(ev.Type), domain.Message{
			Title: ev.Title, Body: ev.Body, Link: ev.Link, SourceURL: ev.SourceURL,
		})
	}

	if p.SSE && b.pub != nil {
		b.pub.Publish(ev.UserID, ev, id)
	}
}
