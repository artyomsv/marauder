// Package genericmagnet implements a fallback tracker plugin that accepts
// any magnet URI and treats it as a one-shot "please hand this to the
// client" request. There is no monitoring — the hash is computed once from
// the magnet URI itself and never changes.
//
// This is the simplest possible tracker plugin and serves as an end-to-end
// smoke test for the plugin pipeline.
package genericmagnet

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

type plugin struct{}

// init registers the plugin.
func init() {
	registry.RegisterTracker(&plugin{})
}

func (p *plugin) Name() string        { return "genericmagnet" }
func (p *plugin) DisplayName() string { return "Generic Magnet" }

func (p *plugin) CanParse(rawURL string) bool {
	return strings.HasPrefix(strings.TrimSpace(rawURL), "magnet:?")
}

func (p *plugin) Parse(_ context.Context, rawURL string) (*domain.Topic, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme != "magnet" {
		return nil, errors.New("not a magnet URI")
	}
	q := u.Query()
	name := q.Get("dn")
	if name == "" {
		name = "magnet"
	}
	return &domain.Topic{
		TrackerName: p.Name(),
		URL:         rawURL,
		DisplayName: name,
		Extra:       map[string]any{"btih": extractBTIH(q.Get("xt"))},
	}, nil
}

// Check always reports the same hash — the magnet URI itself. This means
// a generic-magnet topic is checked once and then is effectively dormant.
func (p *plugin) Check(_ context.Context, topic *domain.Topic, _ *domain.TrackerCredential) (*domain.Check, error) {
	return &domain.Check{
		Hash:        stableHash(topic.URL),
		DisplayName: topic.DisplayName,
		Extra:       map[string]any{"checked_at": time.Now().UTC()},
	}, nil
}

// Download returns the original magnet URI as the payload.
func (p *plugin) Download(_ context.Context, topic *domain.Topic, _ *domain.Check, _ *domain.TrackerCredential) (*domain.Payload, error) {
	return &domain.Payload{MagnetURI: topic.URL}, nil
}

// extractBTIH pulls the BTIH (v1) from an xt like "urn:btih:<hex>".
func extractBTIH(xt string) string {
	const prefix = "urn:btih:"
	if strings.HasPrefix(xt, prefix) {
		return strings.TrimPrefix(xt, prefix)
	}
	return ""
}

// stableHash is a deterministic hash of a magnet URI. We don't need a
// cryptographic hash here — the scheduler only uses it for equality.
// The hash is the magnet URI itself, so a one-shot magnet topic only
// "updates" once: the first time it is checked.
func stableHash(s string) string {
	return s
}
