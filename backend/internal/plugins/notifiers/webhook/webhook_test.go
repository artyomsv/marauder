package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

func TestSendPostsJSON(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(204)
	}))
	defer srv.Close()

	p := &plugin{http: srv.Client()}
	cfg, _ := json.Marshal(Config{URL: srv.URL})
	err := p.Send(context.Background(), cfg, domain.Message{
		Title: "Topic updated",
		Body:  "ep 12",
		Link:  "https://example.com/topic/1",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got["title"] != "Topic updated" {
		t.Errorf("title: %v", got["title"])
	}
	if got["body"] != "ep 12" {
		t.Errorf("body: %v", got["body"])
	}
	if got["source"] != "marauder" {
		t.Errorf("source: %v", got["source"])
	}
}

func TestSendNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	defer srv.Close()
	p := &plugin{http: srv.Client()}
	cfg, _ := json.Marshal(Config{URL: srv.URL})
	if err := p.Send(context.Background(), cfg, domain.Message{Title: "x"}); err == nil {
		t.Fatal("expected error on 400")
	}
}

func TestEmptyURL(t *testing.T) {
	p := &plugin{http: http.DefaultClient}
	cfg, _ := json.Marshal(Config{})
	if err := p.Send(context.Background(), cfg, domain.Message{}); err == nil {
		t.Fatal("expected error on empty URL")
	}
}
