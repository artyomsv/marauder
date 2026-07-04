package pushover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

func TestSend(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = map[string]string{
			"token":   r.Form.Get("token"),
			"user":    r.Form.Get("user"),
			"title":   r.Form.Get("title"),
			"message": r.Form.Get("message"),
			"url":     r.Form.Get("url"),
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"status":1,"request":"abc"}`))
	}))
	defer srv.Close()

	p := &plugin{http: srv.Client(), apiBase: srv.URL}
	cfg, _ := json.Marshal(Config{UserKey: "u", AppToken: "t"})
	if err := p.Send(context.Background(), cfg, domain.Message{
		Title: "Topic updated", Body: "ep 12", Link: "https://example.com/x",
		SourceURL: "https://tracker.example/viewtopic.php?t=1",
	}); err != nil {
		t.Fatal(err)
	}
	if got["token"] != "t" || got["user"] != "u" {
		t.Errorf("creds not sent: %+v", got)
	}
	// The single supplementary-url slot stays the Marauder link; the source
	// URL goes into the message text instead.
	if got["title"] != "Topic updated" || got["message"] != "ep 12\n\nSource: https://tracker.example/viewtopic.php?t=1" {
		t.Errorf("body not sent: %+v", got)
	}
	if got["url"] != "https://example.com/x" {
		t.Errorf("link not sent: %+v", got)
	}
}

func TestSend_NoSourceURL_MessageStaysBare(t *testing.T) {
	var gotMessage string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotMessage = r.Form.Get("message")
		w.WriteHeader(200)
		w.Write([]byte(`{"status":1,"request":"abc"}`))
	}))
	defer srv.Close()

	p := &plugin{http: srv.Client(), apiBase: srv.URL}
	cfg, _ := json.Marshal(Config{UserKey: "u", AppToken: "t"})
	if err := p.Send(context.Background(), cfg, domain.Message{
		Title: "Topic updated", Body: "ep 12", Link: "https://example.com/x",
	}); err != nil {
		t.Fatal(err)
	}
	if gotMessage != "ep 12" {
		t.Errorf("message = %q, want the bare body with no Source suffix", gotMessage)
	}
}

func TestSend_AuthorComment_AppendedBeforeSource(t *testing.T) {
	var gotMessage string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotMessage = r.Form.Get("message")
		w.WriteHeader(200)
		w.Write([]byte(`{"status":1,"request":"abc"}`))
	}))
	defer srv.Close()

	p := &plugin{http: srv.Client(), apiBase: srv.URL}
	cfg, _ := json.Marshal(Config{UserKey: "u", AppToken: "t"})
	if err := p.Send(context.Background(), cfg, domain.Message{
		Title: "Topic updated", Body: "ep 12", Link: "https://example.com/x",
		AuthorComment: "Added episode 8.",
		SourceURL:     "https://tracker.example/viewtopic.php?t=1",
	}); err != nil {
		t.Fatal(err)
	}
	want := "ep 12\n\nAuthor update:\nAdded episode 8.\n\nSource: https://tracker.example/viewtopic.php?t=1"
	if gotMessage != want {
		t.Errorf("message = %q, want %q", gotMessage, want)
	}
}

func TestSend_NoAuthorComment_MessageOmitsBlock(t *testing.T) {
	var gotMessage string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotMessage = r.Form.Get("message")
		w.WriteHeader(200)
		w.Write([]byte(`{"status":1,"request":"abc"}`))
	}))
	defer srv.Close()

	p := &plugin{http: srv.Client(), apiBase: srv.URL}
	cfg, _ := json.Marshal(Config{UserKey: "u", AppToken: "t"})
	if err := p.Send(context.Background(), cfg, domain.Message{
		Title: "Topic updated", Body: "ep 12", Link: "https://example.com/x",
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gotMessage, "Author update:") {
		t.Errorf("message = %q, must not contain an Author update block when AuthorComment is empty", gotMessage)
	}
}

func TestSendValidationErrors(t *testing.T) {
	p := &plugin{http: http.DefaultClient, apiBase: "https://api.pushover.net/1/messages.json"}
	bad := []Config{{}, {UserKey: "u"}, {AppToken: "t"}}
	for _, c := range bad {
		raw, _ := json.Marshal(c)
		if err := p.Send(context.Background(), raw, domain.Message{Title: "x"}); err == nil {
			t.Errorf("expected error for %+v", c)
		}
	}
}
