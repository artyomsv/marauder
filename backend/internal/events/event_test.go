package events

import "testing"

func TestPolicyFor(t *testing.T) {
	tests := []struct {
		typ                  Type
		persist, notify, sse bool
	}{
		{TopicAdded, true, false, true},
		{CheckStarted, false, false, true},
		{CheckCompleted, false, false, true},
		{ReleaseFound, true, true, true},
		{DownloadSubmitted, true, true, true},
		{DownloadProgress, false, false, true},
		{DownloadCompleted, true, true, true},
		{CheckFailed, true, true, true},
		{SessionExpired, true, true, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.typ), func(t *testing.T) {
			p := PolicyFor(tt.typ)
			if p.Persist != tt.persist || p.Notifiable != tt.notify || p.SSE != tt.sse {
				t.Errorf("PolicyFor(%s) = %+v, want persist=%v notify=%v sse=%v",
					tt.typ, p, tt.persist, tt.notify, tt.sse)
			}
		})
	}
}

func TestPolicyFor_Unknown_DefaultsToInert(t *testing.T) {
	p := PolicyFor(Type("nope.nope"))
	if p.Persist || p.Notifiable || p.SSE {
		t.Errorf("unknown type should be inert, got %+v", p)
	}
}

func TestNotifiableTypes(t *testing.T) {
	got := NotifiableTypes()
	want := map[Type]bool{ReleaseFound: true, DownloadSubmitted: true, DownloadCompleted: true, CheckFailed: true, SessionExpired: true}
	if len(got) != len(want) {
		t.Fatalf("got %d notifiable types, want %d", len(got), len(want))
	}
	for _, ty := range got {
		if !want[ty] {
			t.Errorf("unexpected notifiable type %s", ty)
		}
	}
}

func TestPolicyFor_TopicReset(t *testing.T) {
	p := PolicyFor(TopicReset)
	if !p.Persist {
		t.Error("topic.reset must be persisted so the timeline explains the emptied delivery list")
	}
	if p.Notifiable {
		t.Error("topic.reset must not be notifiable — the user performed the action themselves")
	}
	if !p.SSE {
		t.Error("topic.reset must be pushed over SSE so open tabs refresh")
	}
}

func TestNotifiableTypes_ExcludesTopicReset(t *testing.T) {
	for _, ty := range NotifiableTypes() {
		if ty == TopicReset {
			t.Fatal("topic.reset leaked into the notifier subscription list")
		}
	}
}
