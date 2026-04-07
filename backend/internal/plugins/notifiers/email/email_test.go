package email

import (
	"bytes"
	"context"
	"encoding/json"
	"net/smtp"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

func TestSendCallsSMTP(t *testing.T) {
	var got struct {
		addr string
		from string
		to   []string
		msg  []byte
	}
	p := &plugin{
		sender: func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
			got.addr, got.from, got.to, got.msg = addr, from, to, msg
			_ = auth
			return nil
		},
	}
	cfg, _ := json.Marshal(Config{
		SMTPHost: "smtp.example.com",
		SMTPPort: 587,
		Username: "user",
		Password: "pass",
		From:     "from@example.com",
		To:       "to@example.com",
	})
	err := p.Send(context.Background(), cfg, domain.Message{
		Title: "Topic updated",
		Body:  "Episode 12 dropped",
		Link:  "https://example.com/topic/1",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.addr != "smtp.example.com:587" {
		t.Errorf("addr = %q", got.addr)
	}
	if got.from != "from@example.com" {
		t.Errorf("from = %q", got.from)
	}
	if len(got.to) != 1 || got.to[0] != "to@example.com" {
		t.Errorf("to = %v", got.to)
	}
	if !bytes.Contains(got.msg, []byte("Subject: Topic updated")) {
		t.Errorf("missing subject in message: %s", got.msg)
	}
	if !bytes.Contains(got.msg, []byte("Episode 12 dropped")) {
		t.Errorf("missing body in message: %s", got.msg)
	}
	if !bytes.Contains(got.msg, []byte("https://example.com/topic/1")) {
		t.Errorf("missing link in message: %s", got.msg)
	}
}

func TestSendValidationErrors(t *testing.T) {
	p := &plugin{sender: func(string, smtp.Auth, string, []string, []byte) error { return nil }}
	bad := []Config{
		{SMTPPort: 587, From: "f", To: "t"},       // no host
		{SMTPHost: "h", From: "f", To: "t"},       // no port
		{SMTPHost: "h", SMTPPort: 587, To: "t"},   // no from
		{SMTPHost: "h", SMTPPort: 587, From: "f"}, // no to
	}
	for _, c := range bad {
		raw, _ := json.Marshal(c)
		if err := p.Send(context.Background(), raw, domain.Message{Title: "x"}); err == nil {
			t.Errorf("expected error for %+v", c)
		}
	}
}

func TestBuildMessageDefaults(t *testing.T) {
	m := buildMessage("a@x", "b@y", domain.Message{})
	if !strings.Contains(string(m), "Subject: Marauder") {
		t.Errorf("default subject missing: %s", m)
	}
}
