// Package telegram implements a notifier plugin that sends messages via
// the Telegram Bot API.
//
// Users create a bot with @BotFather, obtain a token, start a chat, and
// record their chat ID. Marauder then sends short notifications on topic
// updates or failures.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// Config is the stored config for this notifier.
type Config struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

type plugin struct {
	http *http.Client
}

func init() {
	registry.RegisterNotifier(&plugin{http: &http.Client{Timeout: 10 * time.Second}})
}

func (p *plugin) Name() string        { return "telegram" }
func (p *plugin) DisplayName() string { return "Telegram" }

func (p *plugin) ConfigSchema() map[string]any {
	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"properties": map[string]any{
			"bot_token": map[string]any{"type": "string", "title": "Bot token", "format": "password"},
			"chat_id":   map[string]any{"type": "string", "title": "Chat ID"},
		},
		"required": []string{"bot_token", "chat_id"},
	}
}

func (p *plugin) Test(ctx context.Context, rawConfig []byte) error {
	return p.Send(ctx, rawConfig, domain.Message{
		Title: "Marauder",
		Body:  "Test notification - your Telegram integration is working.",
	})
}

func (p *plugin) Send(ctx context.Context, rawConfig []byte, msg domain.Message) error {
	var c Config
	if err := json.Unmarshal(rawConfig, &c); err != nil {
		return fmt.Errorf("bad config: %w", err)
	}
	if c.BotToken == "" || c.ChatID == "" {
		return errors.New("bot_token and chat_id are required")
	}
	body := map[string]any{
		"chat_id":    c.ChatID,
		"text":       formatMessage(msg),
		"parse_mode": "HTML",
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.telegram.org/bot"+c.BotToken+"/sendMessage", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram: %d %s", resp.StatusCode, string(b))
	}
	return nil
}

// formatMessage renders for Telegram's HTML parse mode. HTML is used instead
// of (legacy) Markdown because tracker URLs and release titles routinely
// contain _/*/[ — in Markdown mode an unbalanced metacharacter makes the Bot
// API reject the whole message with 400 "can't parse entities", silently
// dropping the notification. html.EscapeString is a complete escape for the
// HTML entity set, so any title/body/URL renders verbatim.
func formatMessage(m domain.Message) string {
	s := "<b>" + html.EscapeString(m.Title) + "</b>\n" + html.EscapeString(m.Body)
	if m.SourceURL != "" {
		s += "\nSource: " + html.EscapeString(m.SourceURL)
	}
	if m.Link != "" {
		s += "\nMarauder: " + html.EscapeString(m.Link)
	}
	return s
}
