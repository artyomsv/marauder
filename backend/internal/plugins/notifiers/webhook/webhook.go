// Package webhook implements a generic webhook notifier plugin.
//
// Posts a small JSON payload to a user-supplied URL on every event.
// Useful for plumbing into Slack, Discord, Mattermost, Home Assistant,
// custom dashboards, or anything else that accepts a JSON POST.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// Config holds the user-provided webhook config.
type Config struct {
	URL string `json:"url"`
}

type plugin struct {
	http *http.Client
}

func init() {
	registry.RegisterNotifier(&plugin{http: &http.Client{Timeout: 10 * time.Second}})
}

func (p *plugin) Name() string        { return "webhook" }
func (p *plugin) DisplayName() string { return "Webhook" }

func (p *plugin) ConfigSchema() map[string]any {
	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"properties": map[string]any{
			"url": map[string]any{"type": "string", "title": "URL", "format": "uri"},
		},
		"required": []string{"url"},
	}
}

func (p *plugin) Test(ctx context.Context, raw []byte) error {
	return p.Send(ctx, raw, domain.Message{
		Title: "Marauder",
		Body:  "Test webhook — Marauder is configured to ping this URL.",
	})
}

func (p *plugin) Send(ctx context.Context, raw []byte, msg domain.Message) error {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return fmt.Errorf("bad config: %w", err)
	}
	if c.URL == "" {
		return errors.New("url is required")
	}
	body, _ := json.Marshal(map[string]any{
		"source": "marauder",
		"title":  msg.Title,
		"body":   msg.Body,
		"link":   msg.Link,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Marauder/0.4 (+https://marauder.cc)")
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("webhook %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
