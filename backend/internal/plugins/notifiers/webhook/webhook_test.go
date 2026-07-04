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
		Title:     "Topic updated",
		Body:      "ep 12",
		Link:      "https://example.com/topic/1",
		SourceURL: "https://tracker.example/viewtopic.php?t=1",
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
	if got["source_url"] != "https://tracker.example/viewtopic.php?t=1" {
		t.Errorf("source_url: %v", got["source_url"])
	}
}

func TestSendPostsJSON_NoSourceURL_OmitsField(t *testing.T) {
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
		Title: "Session expired", Body: "x", Link: "https://example.com/credentials",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Non-topic events (e.g. session.expired) must not grow an empty
	// source_url field — strict-schema consumers would see a phantom key.
	if _, present := got["source_url"]; present {
		t.Errorf("source_url must be omitted when empty, got payload: %v", got)
	}
}

func TestSendPostsJSON_AuthorComment_IncludedAsField(t *testing.T) {
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
		Title: "Topic updated", Body: "ep 12",
		AuthorComment: "Added episode 8.",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got["author_comment"] != "Added episode 8." {
		t.Errorf("author_comment: %v", got["author_comment"])
	}
}

func TestSendPostsJSON_NoAuthorComment_OmitsField(t *testing.T) {
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
		Title: "Topic updated", Body: "ep 12",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Same shape-stability contract as source_url: absent, not empty.
	if _, present := got["author_comment"]; present {
		t.Errorf("author_comment must be omitted when empty, got payload: %v", got)
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
