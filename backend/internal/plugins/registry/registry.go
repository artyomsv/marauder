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

// WithCredentials is an optional capability; a tracker that can use user
// credentials implements this interface in addition to Tracker. It does NOT
// by itself mean credentials are mandatory — a tracker that also implements
// WithAnonymousDownload works without them (see below).
type WithCredentials interface {
	Tracker
	// Login is called before any Check/Download when a credential exists.
	Login(ctx context.Context, creds *domain.TrackerCredential) error
	// Verify checks whether existing cookies/session is still valid.
	Verify(ctx context.Context, creds *domain.TrackerCredential) (bool, error)
}

// WithAnonymousDownload is an optional marker for a WithCredentials tracker
// whose Check/Download also succeed without credentials (e.g. an anonymous
// magnet on the topic page). The AddTopic form uses it to present credentials
// as optional (an enhancement, e.g. enabling .torrent downloads) rather than
// required. A WithCredentials tracker that does NOT implement this is treated
// as credentials-required.
type WithAnonymousDownload interface {
	Tracker
	SupportsAnonymousDownload() bool
}

// LoginChallenge is a captcha to present to the user during interactive
// login. Image holds the raw bytes; MIMEType comes from the captcha
// response Content-Type (LostFilm serves image/gif).
type LoginChallenge struct {
	ChallengeID string
	Image       []byte
	MIMEType    string
}

// SessionCookies maps cookie name -> value. It is persisted (encrypted)
// and rehydrated into the plugin's HTTP cookie jar on each check.
type SessionCookies map[string]string

// WithInteractiveLogin is an optional capability for trackers that gate
// login behind a captcha the user must solve. BeginLogin returns exactly
// one of (challenge, cookies): a captcha to solve, or — if the tracker
// did not demand one — the session straight away.
type WithInteractiveLogin interface {
	Tracker
	BeginLogin(ctx context.Context, creds *domain.TrackerCredential) (*LoginChallenge, SessionCookies, error)
	CompleteLogin(ctx context.Context, challengeID, answer string) (SessionCookies, error)
	RefreshChallenge(ctx context.Context, challengeID string) (*LoginChallenge, error)
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

// Season is one released season and its released episode numbers.
type Season struct {
	Number   int   `json:"number"`
	Episodes []int `json:"episodes"`
}

// WithSeasonCatalog is implemented by trackers that can enumerate a
// series' released seasons/episodes from its URL.
type WithSeasonCatalog interface {
	Tracker
	SeasonCatalog(ctx context.Context, url string) ([]Season, error)
}

// Metadata is human-facing descriptive data a tracker can resolve from a
// topic URL: a real display title and a poster/preview image. Either field
// may be empty when the tracker can't determine it.
type Metadata struct {
	Title    string `json:"title"`
	ImageURL string `json:"image_url"`
}

// WithMetadata is implemented by trackers that can resolve a human-readable
// title and a poster/preview image from a topic URL. It powers the real
// display name (replacing "RuTracker topic 123" placeholders) and the topic
// image. creds may be nil — implementations should fall back to whatever the
// public page exposes when no credentials are available.
type WithMetadata interface {
	Tracker
	ResolveMetadata(ctx context.Context, rawURL string, creds *domain.TrackerCredential) (*Metadata, error)
}

// WithAuthorComment is implemented by trackers that can fetch the release
// author's latest comment from the topic's discussion thread (issue #110).
// The scheduler calls it best-effort after an update is detected and renders
// the excerpt in update notifications. Implementations return plain text
// (tags stripped, whitespace collapsed) — the caller length-caps it — and
// ("", nil) when the author has posted no comment beyond the release post
// itself. creds may be nil for trackers whose topic pages are public.
type WithAuthorComment interface {
	Tracker
	AuthorComment(ctx context.Context, rawURL string, creds *domain.TrackerCredential) (string, error)
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

// TorrentStatus is a client's live report for one torrent, keyed by its
// BitTorrent v1 infohash (lowercase hex). PercentDone is 0..1. State is a
// normalised lifecycle word (see the State* constants) so the frontend can
// translate it uniformly regardless of which client produced it.
type TorrentStatus struct {
	Hash        string  `json:"hash"`
	PercentDone float64 `json:"percent_done"`
	State       string  `json:"state"`
}

// Normalised torrent lifecycle states. Each WithStatus implementation maps
// its client's native states onto this small shared vocabulary.
const (
	StateDownloading = "downloading"
	StateSeeding     = "seeding"
	StateStopped     = "stopped"
	StateChecking    = "checking"
	StateQueued      = "queued"
	StateError       = "error"
	StateUnknown     = "unknown"
)

// WithStatus is an optional client capability: report live download status
// for a set of infohashes. Clients that can't (µTorrent, downloadfolder)
// simply don't implement it, and callers fall back to "delivered" labels.
// The returned slice only includes hashes the client currently knows about;
// a hash the client has forgotten (removed torrent) is silently absent.
type WithStatus interface {
	Client
	Status(ctx context.Context, rawConfig []byte, hashes []string) ([]TorrentStatus, error)
}

// WithRemoval is an optional client capability: remove torrents by infohash.
// When deleteData is true the client also deletes the downloaded files from
// disk. It powers the per-topic "replace previous version on update" policy
// (issue #101): when a single-release topic gets a new infohash, the scheduler
// removes the previously delivered torrent so updated releases don't pile up
// duplicate downloads on disk. Hashes the client no longer knows are ignored
// (idempotent). Clients without a remove concept (downloadfolder) simply don't
// implement it, and the scheduler keeps the old torrent in place.
type WithRemoval interface {
	Client
	Remove(ctx context.Context, rawConfig []byte, hashes []string, deleteData bool) error
}

// WithCategories is an optional client capability: enumerate the categories
// the client already knows about, so the UI can offer them as suggestions when
// picking a topic's category. Clients without a category concept (Transmission,
// downloadfolder, …) simply don't implement it, and callers fall back to plain
// free-text entry. Category remains a path segment in Marauder (see
// EffectiveDownloadDir / SanitizeCategory) — this list is a convenience for
// picking a value, not a constraint on it.
type WithCategories interface {
	Client
	Categories(ctx context.Context, rawConfig []byte) ([]string, error)
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
	SetDomainResolver(nil)
}
