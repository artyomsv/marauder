// Package events defines the canonical event taxonomy emitted across the
// backend and the per-type policy that decides, for each event, whether it
// is persisted to history, eligible for notifier fan-out, and pushed over
// the live SSE feed. It is the single source of truth for "what happens to
// an event".
package events

// Type is a canonical event-type identifier. These strings are a one-way
// door: notifier subscriptions persist them, so they must not be renamed.
type Type string

const (
	TopicAdded        Type = "topic.added"
	CheckStarted      Type = "check.started"
	CheckCompleted    Type = "check.completed"
	ReleaseFound      Type = "release.found"
	DownloadSubmitted Type = "download.submitted"
	DownloadProgress  Type = "download.progress"
	DownloadCompleted Type = "download.completed"
	CheckFailed       Type = "check.failed"
	SessionExpired    Type = "session.expired"
)

// Policy describes the routing for one event type.
type Policy struct {
	Persist    bool // write a topic_events history row
	Notifiable bool // eligible for notifier fan-out (subject to subscription)
	SSE        bool // push over the live feed
}

var policies = map[Type]Policy{
	TopicAdded:        {Persist: true, Notifiable: false, SSE: true},
	CheckStarted:      {Persist: false, Notifiable: false, SSE: true},
	CheckCompleted:    {Persist: false, Notifiable: false, SSE: true},
	ReleaseFound:      {Persist: true, Notifiable: true, SSE: true},
	DownloadSubmitted: {Persist: true, Notifiable: true, SSE: true},
	DownloadProgress:  {Persist: false, Notifiable: false, SSE: true},
	DownloadCompleted: {Persist: true, Notifiable: true, SSE: true},
	CheckFailed:       {Persist: true, Notifiable: true, SSE: true},
	SessionExpired:    {Persist: true, Notifiable: true, SSE: true},
}

// PolicyFor returns the routing policy for t. An unknown type is inert
// (no persist, no notify, no SSE) — a defensive default.
func PolicyFor(t Type) Policy { return policies[t] }

// NotifiableTypes returns the event types a notifier may subscribe to.
func NotifiableTypes() []Type {
	var out []Type
	for t, p := range policies {
		if p.Notifiable {
			out = append(out, t)
		}
	}
	return out
}
