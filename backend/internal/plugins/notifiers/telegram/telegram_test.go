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
