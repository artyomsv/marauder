// Package pushover implements a Pushover.net notifier plugin.
//
// Pushover is a popular push-notification service for iOS, Android, and
// desktop. The API is a simple form POST to api.pushover.net/1/messages.json.
package pushover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// Config holds the user keys for Pushover.
type Config struct {
	UserKey  string `json:"user_key"`
	AppToken string `json:"app_token"`
}

type plugin struct {
	http    *http.Client
	apiBase string
}

func init() {
	registry.RegisterNotifier(&plugin{
		http:    &http.Client{Timeout: 10 * time.Second},
		apiBase: "https://api.pushover.net/1/messages.json",
	})
}

func (p *plugin) Name() string        { return "pushover" }
func (p *plugin) DisplayName() string { return "Pushover" }

func (p *plugin) ConfigSchema() map[string]any {
	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"properties": map[string]any{
			"user_key":  map[string]any{"type": "string", "title": "User key"},
			"app_token": map[string]any{"type": "string", "title": "App token", "format": "password"},
		},
		"required": []string{"user_key", "app_token"},
	}
}

func (p *plugin) Test(ctx context.Context, raw []byte) error {
	return p.Send(ctx, raw, domain.Message{
		Title: "Marauder",
		Body:  "Test notification — Pushover integration is working.",
	})
}

func (p *plugin) Send(ctx context.Context, raw []byte, msg domain.Message) error {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return fmt.Errorf("bad config: %w", err)
	}
	if c.UserKey == "" || c.AppToken == "" {
		return errors.New("user_key and app_token are required")
	}
	form := url.Values{
		"token":   {c.AppToken},
		"user":    {c.UserKey},
		"title":   {msg.Title},
		"message": {msg.Body},
	}
	if msg.Link != "" {
		form.Set("url", msg.Link)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiBase, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("pushover %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
