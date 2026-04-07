// Package registry holds the process-wide list of installed plugins.
//
// Plugins self-register from their package init() functions:
//
//	func init() {
//	    registry.RegisterTracker(&plugin{})
//	}
//
// A single blank import of each plugin package in cmd/server/main.go
// activates all bundled plugins via these init() functions.
package registry

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// --- Tracker interfaces -------------------------------------------------

// Tracker is the minimum contract every tracker plugin must satisfy.
type Tracker interface {
	Name() string
	DisplayName() string
	CanParse(rawURL string) bool
	Parse(ctx context.Context, rawURL string) (*domain.Topic, error)
	Check(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential) (*domain.Check, error)
	Download(ctx context.Context, topic *domain.Topic, check *domain.Check, creds *domain.TrackerCredential) (*domain.Payload, error)
}

// WithCredentials is an optional capability; a tracker that requires user
// credentials implements this interface in addition to Tracker.
type WithCredentials interface {
	Tracker
	// Login is called before any Check/Download when a credential exists.
	Login(ctx context.Context, creds *domain.TrackerCredential) error
	// Verify checks whether existing cookies/session is still valid.
	Verify(ctx context.Context, creds *domain.TrackerCredential) (bool, error)
}

// WithQuality is an optional capability for trackers that expose per-topic
// quality selection (e.g. LostFilm).
type WithQuality interface {
	Tracker
	Qualities() []string
	DefaultQuality() string
}

// WithCloudflare is an optional marker: a tracker that may return a
// Cloudflare challenge page, and should be routed through the cfsolver.
type WithCloudflare interface {
	Tracker
	UsesCloudflare() bool
}

// WithEpisodeFilter is an optional capability for trackers that
// support skipping ahead to a specific season / episode (LostFilm,
// Anidub, etc.). Plugins map topic.Extra["start_season"] /
// topic.Extra["start_episode"] to filtered Check / Download
// behaviour. Returning true is a contract — the plugin promises to
// honour those keys when present.
type WithEpisodeFilter interface {
	Tracker
	SupportsEpisodeFilter() bool
}

// --- Client & Notifier interfaces ---------------------------------------

// Client is a torrent client plugin.
type Client interface {
	Name() string
	DisplayName() string
	// ConfigSchema returns the JSON schema (as map) that the frontend uses
	// to render the add/edit form.
	ConfigSchema() map[string]any
	// Test parses config and pings the client.
	Test(ctx context.Context, rawConfig []byte) error
	// Add submits a payload.
	Add(ctx context.Context, rawConfig []byte, payload *domain.Payload, opts domain.AddOptions) error
}

// Notifier is a notification target plugin.
type Notifier interface {
	Name() string
	DisplayName() string
	ConfigSchema() map[string]any
	Test(ctx context.Context, rawConfig []byte) error
	Send(ctx context.Context, rawConfig []byte, msg domain.Message) error
}

// --- Registry storage ---------------------------------------------------

var (
	mu        sync.RWMutex
	trackers  = map[string]Tracker{}
	clients   = map[string]Client{}
	notifiers = map[string]Notifier{}
)

// RegisterTracker installs a tracker plugin. Must be called from init().
// Panics on duplicate names — this is a programmer error caught at startup.
func RegisterTracker(t Tracker) {
	mu.Lock()
	defer mu.Unlock()
	name := t.Name()
	if _, exists := trackers[name]; exists {
		panic(fmt.Sprintf("registry: tracker %q already registered", name))
	}
	trackers[name] = t
}

// RegisterClient installs a client plugin.
func RegisterClient(c Client) {
	mu.Lock()
	defer mu.Unlock()
	name := c.Name()
	if _, exists := clients[name]; exists {
		panic(fmt.Sprintf("registry: client %q already registered", name))
	}
	clients[name] = c
}

// RegisterNotifier installs a notifier plugin.
func RegisterNotifier(n Notifier) {
	mu.Lock()
	defer mu.Unlock()
	name := n.Name()
	if _, exists := notifiers[name]; exists {
		panic(fmt.Sprintf("registry: notifier %q already registered", name))
	}
	notifiers[name] = n
}

// GetTracker returns the tracker plugin by name, or nil.
func GetTracker(name string) Tracker {
	mu.RLock()
	defer mu.RUnlock()
	return trackers[name]
}

// FindTrackerForURL returns the first registered tracker whose CanParse
// returns true for the URL.
func FindTrackerForURL(rawURL string) Tracker {
	mu.RLock()
	defer mu.RUnlock()
	// Iterate in stable (sorted) order so behaviour is deterministic when
	// multiple plugins could match.
	names := make([]string, 0, len(trackers))
	for n := range trackers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if trackers[n].CanParse(rawURL) {
			return trackers[n]
		}
	}
	return nil
}

// ListTrackers returns all registered trackers, sorted by name.
func ListTrackers() []Tracker {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Tracker, 0, len(trackers))
	for _, t := range trackers {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// GetClient returns a client plugin by name, or nil.
func GetClient(name string) Client {
	mu.RLock()
	defer mu.RUnlock()
	return clients[name]
}

// ListClients returns all registered client plugins, sorted by name.
func ListClients() []Client {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Client, 0, len(clients))
	for _, c := range clients {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// GetNotifier returns a notifier plugin by name, or nil.
func GetNotifier(name string) Notifier {
	mu.RLock()
	defer mu.RUnlock()
	return notifiers[name]
}

// ListNotifiers returns all registered notifier plugins, sorted by name.
func ListNotifiers() []Notifier {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Notifier, 0, len(notifiers))
	for _, n := range notifiers {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Reset clears the registry. Only for tests.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	trackers = map[string]Tracker{}
	clients = map[string]Client{}
	notifiers = map[string]Notifier{}
}
