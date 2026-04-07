// Package email implements an SMTP notifier plugin.
//
// Uses the standard library net/smtp with PLAIN auth over STARTTLS.
package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/smtp"
	"strconv"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// Config is the persisted configuration for the email notifier.
type Config struct {
	SMTPHost string `json:"smtp_host"`
	SMTPPort int    `json:"smtp_port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	To       string `json:"to"`
}

type plugin struct {
	// sender is overridable for tests.
	sender func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error
}

func init() {
	registry.RegisterNotifier(&plugin{sender: smtp.SendMail})
}

func (p *plugin) Name() string        { return "email" }
func (p *plugin) DisplayName() string { return "Email (SMTP)" }

func (p *plugin) ConfigSchema() map[string]any {
	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"properties": map[string]any{
			"smtp_host": map[string]any{"type": "string", "title": "SMTP host"},
			"smtp_port": map[string]any{"type": "integer", "title": "Port", "default": 587},
			"username":  map[string]any{"type": "string", "title": "Username"},
			"password":  map[string]any{"type": "string", "title": "Password", "format": "password"},
			"from":      map[string]any{"type": "string", "title": "From"},
			"to":        map[string]any{"type": "string", "title": "To"},
		},
		"required": []string{"smtp_host", "smtp_port", "from", "to"},
	}
}

func (p *plugin) Test(ctx context.Context, raw []byte) error {
	return p.Send(ctx, raw, domain.Message{
		Title: "Marauder",
		Body:  "Test notification — your SMTP integration is working.",
	})
}

func (p *plugin) Send(_ context.Context, raw []byte, msg domain.Message) error {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return fmt.Errorf("bad config: %w", err)
	}
	if c.SMTPHost == "" || c.SMTPPort == 0 || c.From == "" || c.To == "" {
		return errors.New("smtp_host, smtp_port, from, and to are required")
	}
	addr := c.SMTPHost + ":" + strconv.Itoa(c.SMTPPort)
	var auth smtp.Auth
	if c.Username != "" {
		auth = smtp.PlainAuth("", c.Username, c.Password, c.SMTPHost)
	}
	body := buildMessage(c.From, c.To, msg)
	return p.sender(addr, auth, c.From, []string{c.To}, body)
}

func buildMessage(from, to string, m domain.Message) []byte {
	subject := m.Title
	if subject == "" {
		subject = "Marauder"
	}
	body := m.Body
	if m.Link != "" {
		body += "\r\n\r\n" + m.Link
	}
	return []byte(
		"From: " + from + "\r\n" +
			"To: " + to + "\r\n" +
			"Subject: " + subject + "\r\n" +
			"MIME-Version: 1.0\r\n" +
			"Content-Type: text/plain; charset=UTF-8\r\n" +
			"\r\n" + body + "\r\n",
	)
}
