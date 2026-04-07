package pushover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	}); err != nil {
		t.Fatal(err)
	}
	if got["token"] != "t" || got["user"] != "u" {
		t.Errorf("creds not sent: %+v", got)
	}
	if got["title"] != "Topic updated" || got["message"] != "ep 12" {
		t.Errorf("body not sent: %+v", got)
	}
	if got["url"] != "https://example.com/x" {
		t.Errorf("link not sent: %+v", got)
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
