package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// Plugin sends to https://api.telegram.org. The Send method builds the
// URL by concatenating the base + "/bot<token>/sendMessage". To test
// without monkey-patching, we override the http.Client's Transport with
// one that redirects requests to a httptest server.

type rewriteRT struct {
	target string
}

func (r *rewriteRT) RoundTrip(req *http.Request) (*http.Response, error) {
	// Replace https://api.telegram.org with the test server URL.
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(r.target, "http://")
	return http.DefaultTransport.RoundTrip(req)
}

func TestSendHappyPath(t *testing.T) {
	var seenBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/bot12345:abc/sendMessage") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &seenBody)
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	p := &plugin{http: &http.Client{Timeout: 5 * time.Second, Transport: &rewriteRT{target: srv.URL}}}

	cfg, _ := json.Marshal(Config{BotToken: "12345:abc", ChatID: "777"})
	err := p.Send(context.Background(), cfg, domain.Message{
		Title: "Topic updated",
		Body:  "Episode 12 is now available",
		Link:  "https://example.com/topic/1",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if seenBody["chat_id"] != "777" {
		t.Errorf("chat_id: %v", seenBody["chat_id"])
	}
	// HTML parse mode: unlike legacy Markdown, an underscore-laden tracker URL
	// can't make Telegram reject the whole message with "can't parse entities".
	if seenBody["parse_mode"] != "HTML" {
		t.Errorf("parse_mode = %v, want HTML", seenBody["parse_mode"])
	}
	text, _ := seenBody["text"].(string)
	if !strings.Contains(text, "Topic updated") {
		t.Errorf("missing title in text: %q", text)
	}
	if !strings.Contains(text, "Episode 12") {
		t.Errorf("missing body in text: %q", text)
	}
	if !strings.Contains(text, "https://example.com/topic/1") {
		t.Errorf("missing link in text: %q", text)
	}
}

func TestTestMessage_IsMarkedAsTest(t *testing.T) {
	// The notifier's own Test() message is the first thing a user sees when
	// wiring a bot up: it must read as a test and carry the test marker.
	var seenBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if err := json.Unmarshal(body, &seenBody); err != nil {
			t.Errorf("decode request body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Send reads the response body only on a non-200, so an empty 200 is
		// the whole answer here — nothing to write and no write to check.
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &plugin{http: &http.Client{Timeout: 5 * time.Second, Transport: &rewriteRT{target: srv.URL}}}
	cfg, err := json.Marshal(Config{BotToken: "12345:abc", ChatID: "777"})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}

	if err := p.Test(context.Background(), cfg); err != nil {
		t.Fatalf("Test: %v", err)
	}
	text, ok := seenBody["text"].(string)
	if !ok {
		t.Fatalf("request body carried no text field: %#v", seenBody)
	}
	if !strings.HasPrefix(text, "🧪 ") {
		t.Errorf("test message head = %q, want the 🧪 marker", text)
	}
	if !strings.Contains(text, "Test notification") {
		t.Errorf("missing test body in text: %q", text)
	}
	// The map and Test() share one key, so the marker can't silently drift
	// away from the Event value the message carries.
	if icon := eventIcons[testEventKey]; icon != "🧪" {
		t.Errorf("eventIcons[testEventKey] = %q, want 🧪", icon)
	}
}

func TestFormatMessage_SourceURL(t *testing.T) {
	tests := []struct {
		name string
		msg  domain.Message
		want string
	}{
		{
			name: "anchorable source becomes a short anchor; dotless-host link stays plain",
			msg: domain.Message{
				Title: "T", Body: "B",
				Link:      "http://marauder/topics",
				SourceURL: "https://tracker.example/viewtopic.php?t=1",
			},
			// Telegram rejects <a href> to hosts without a dot (localhost,
			// bare service names) with a 400, so those fall back to a
			// labeled plain URL.
			want: "<b>T</b>\n\nB\n\n<a href=\"https://tracker.example/viewtopic.php?t=1\">Source</a> · Marauder: http://marauder/topics",
		},
		{
			name: "no source renders no Source entry",
			msg: domain.Message{
				Title: "T", Body: "B",
				Link: "https://marauder.example/topics",
			},
			want: "<b>T</b>\n\nB\n\n<a href=\"https://marauder.example/topics\">Marauder</a>",
		},
		{
			name: "source without link still renders",
			msg: domain.Message{
				Title: "T", Body: "B",
				SourceURL: "https://tracker.example/viewtopic.php?t=1",
			},
			want: "<b>T</b>\n\nB\n\n<a href=\"https://tracker.example/viewtopic.php?t=1\">Source</a>",
		},
		{
			name: "no links at all renders just title and body",
			msg:  domain.Message{Title: "T", Body: "B"},
			want: "<b>T</b>\n\nB",
		},
		{
			name: "markdown metacharacters pass through, HTML specials escaped in text and href",
			msg: domain.Message{
				Title: "Release <1> & _final_", Body: "B",
				SourceURL: "https://tracker.example/series/The_Show/seasons?a=1&b=2",
			},
			want: "<b>Release &lt;1&gt; &amp; _final_</b>\n\nB\n\n<a href=\"https://tracker.example/series/The_Show/seasons?a=1&amp;b=2\">Source</a>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatMessage(tt.msg); got != tt.want {
				t.Errorf("formatMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatMessage_AuthorComment(t *testing.T) {
	tests := []struct {
		name string
		msg  domain.Message
		want string
	}{
		{
			name: "comment renders as a spaced italic block between body and links",
			msg: domain.Message{
				Title: "T", Body: "B",
				AuthorComment: "Added episode 8.",
				SourceURL:     "https://tracker.example/viewtopic.php?t=1",
				Link:          "http://marauder/topics",
			},
			want: "<b>T</b>\n\nB\n\n💬 <i>Added episode 8.</i>\n\n<a href=\"https://tracker.example/viewtopic.php?t=1\">Source</a> · Marauder: http://marauder/topics",
		},
		{
			name: "no comment renders no author-comment block",
			msg: domain.Message{
				Title: "T", Body: "B",
				SourceURL: "https://tracker.example/viewtopic.php?t=1",
			},
			want: "<b>T</b>\n\nB\n\n<a href=\"https://tracker.example/viewtopic.php?t=1\">Source</a>",
		},
		{
			name: "HTML specials in the comment are escaped",
			msg: domain.Message{
				Title: "T", Body: "B",
				AuthorComment: "5 > 4 & <b>done</b>",
			},
			want: "<b>T</b>\n\nB\n\n💬 <i>5 &gt; 4 &amp; &lt;b&gt;done&lt;/b&gt;</i>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatMessage(tt.msg); got != tt.want {
				t.Errorf("formatMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatMessage_EventIconAndSeverity(t *testing.T) {
	tests := []struct {
		name string
		msg  domain.Message
		want string
	}{
		{
			name: "new release is marked with the new-release icon",
			msg:  domain.Message{Title: "T", Body: "B", Event: "release.found", Severity: "info"},
			want: "🆕 <b>T</b>\n\nB",
		},
		{
			name: "submitted delivery is marked with the outgoing icon",
			msg:  domain.Message{Title: "T", Body: "B", Event: "download.submitted", Severity: "info"},
			want: "📤 <b>T</b>\n\nB",
		},
		{
			name: "finished download is marked with the done icon",
			msg:  domain.Message{Title: "T", Body: "B", Event: "download.completed", Severity: "info"},
			want: "✅ <b>T</b>\n\nB",
		},
		{
			name: "a failed check gets the error dot before the warning icon",
			msg:  domain.Message{Title: "T", Body: "B", Event: "check.failed", Severity: "error"},
			want: "🔴 ⚠️ <b>T</b>\n\nB",
		},
		{
			name: "an expired session gets the error dot and the key icon",
			msg:  domain.Message{Title: "T", Body: "B", Event: "session.expired", Severity: "error"},
			want: "🔴 🔑 <b>T</b>\n\nB",
		},
		{
			name: "a warn-severity event gets the warn dot only",
			msg:  domain.Message{Title: "T", Body: "B", Event: "check.failed", Severity: "warn"},
			want: "🟡 ⚠️ <b>T</b>\n\nB",
		},
		{
			name: "the notifier's own test message is marked as a test",
			msg:  domain.Message{Title: "T", Body: "B", Event: "test", Severity: "info"},
			want: "🧪 <b>T</b>\n\nB",
		},
		{
			name: "an unknown event renders no icon",
			msg:  domain.Message{Title: "T", Body: "B", Event: "topic.added", Severity: "info"},
			want: "<b>T</b>\n\nB",
		},
		{
			name: "an unknown severity renders no dot",
			msg:  domain.Message{Title: "T", Body: "B", Event: "release.found", Severity: "critical"},
			want: "🆕 <b>T</b>\n\nB",
		},
		{
			name: "markers sit outside the bold run and the title stays escaped",
			msg:  domain.Message{Title: "T <x>", Body: "B", Event: "release.found", Severity: "info"},
			want: "🆕 <b>T &lt;x&gt;</b>\n\nB",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatMessage(tt.msg); got != tt.want {
				t.Errorf("formatMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSendValidationErrors(t *testing.T) {
	p := &plugin{http: &http.Client{Timeout: 5 * time.Second}}
	bad := []Config{
		{BotToken: "", ChatID: "777"},
		{BotToken: "abc", ChatID: ""},
	}
	for _, cfg := range bad {
		raw, _ := json.Marshal(cfg)
		if err := p.Send(context.Background(), raw, domain.Message{Title: "x"}); err == nil {
			t.Errorf("expected error for %+v", cfg)
		}
	}
}

func TestSendNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"ok":false,"description":"Forbidden: bot was blocked by the user"}`, http.StatusForbidden)
	}))
	defer srv.Close()

	p := &plugin{http: &http.Client{Timeout: 5 * time.Second, Transport: &rewriteRT{target: srv.URL}}}
	cfg, _ := json.Marshal(Config{BotToken: "12345:abc", ChatID: "777"})

	err := p.Send(context.Background(), cfg, domain.Message{Title: "x", Body: "y"})
	if err == nil {
		t.Fatal("expected error on non-200")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error should mention status code: %v", err)
	}
}
