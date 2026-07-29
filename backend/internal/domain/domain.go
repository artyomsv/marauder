// Package domain defines the core types used throughout the backend.
//
// These types are the contract between the database layer, the service
// layer, the API layer, and the plugin layer. They are pure data — no
// methods, no references to sql/pgx, no references to chi. That makes them
// cheap to move around and easy to mock.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// --- Users --------------------------------------------------------------

// Role is the coarse-grained access control level.
type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

// User is the application user. Local users have a PasswordHash; OIDC-only
// users have OIDCSubject + OIDCIssuer and no password.
type User struct {
	ID           uuid.UUID
	Username     string
	Email        string
	PasswordHash string
	Role         Role
	OIDCSubject  string
	OIDCIssuer   string
	IsDisabled   bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastLoginAt  *time.Time
}

// RefreshToken is a server-side record of an issued refresh token. We store
// only a SHA-256 of the opaque token; the plaintext leaves the server once.
type RefreshToken struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	TokenHash  string
	IssuedAt   time.Time
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	ReplacedBy *uuid.UUID
	UserAgent  string
	IP         string
}

// --- Topics & trackers --------------------------------------------------

// TopicStatus enumerates the lifecycle states of a monitored topic.
type TopicStatus string

const (
	TopicStatusActive TopicStatus = "active"
	TopicStatusPaused TopicStatus = "paused"
	TopicStatusError  TopicStatus = "error"
)

// Topic represents a URL that Marauder is monitoring.
type Topic struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	TrackerName string
	URL         string
	DisplayName string
	ImageURL    string
	// DisplayNameIsPlaceholder is true while DisplayName is a tracker-generated
	// placeholder (e.g. "Kinozal topic 123") eligible for scheduler self-heal.
	// Set false once a real title is resolved (metadata, first self-heal, or a
	// user rename) so self-heal can never downgrade a good title. See issue #90.
	DisplayNameIsPlaceholder bool
	ClientID                 *uuid.UUID
	NotifierID               *uuid.UUID
	DownloadDir              string
	Category                 string
	// ReplaceOnUpdate enables the "replace previous version" delivery policy
	// (issue #101): when a single-release topic gets a new infohash, the
	// scheduler removes the previously delivered torrent from its client
	// instead of leaving updated releases to accumulate duplicate downloads.
	// ReplaceDeleteData additionally deletes the old torrent's files from disk.
	// Defaults are (false, true) — opt-in, but delete data once enabled. The
	// policy only applies to single-release topics; per-episode trackers keep
	// every episode (see scheduler.isEpisodic).
	ReplaceOnUpdate   bool
	ReplaceDeleteData bool
	Extra             map[string]any
	LastHash          string
	LastCheckedAt     *time.Time
	LastUpdatedAt     *time.Time
	NextCheckAt       time.Time
	CheckIntervalSec  int
	ConsecutiveErrors int
	Status            TopicStatus
	LastError         string
	// LastErrorCode is a stable, machine-readable classification of
	// LastError (timeout / unreachable / auth / cloudflare / solver / parse /
	// plugin_missing / unknown) so the UI can render a localised,
	// user-friendly message while LastError keeps the raw detail for
	// debugging. Empty when the last check succeeded.
	//
	// This list is the third copy of the same vocabulary — keep it in step
	// with the errCode* constants in internal/scheduler/scheduler.go and the
	// CODE_KEYS map in frontend/src/components/topics/TopicError.tsx.
	LastErrorCode string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// TrackerCredential holds a user's login details for a tracker plugin.
// The secret is stored encrypted at rest.
type TrackerCredential struct {
	ID               uuid.UUID
	UserID           uuid.UUID
	TrackerName      string
	Username         string
	SecretEnc        []byte // nil if not set
	SecretNonce      []byte
	SessionEnc       []byte // encrypted JSON cookie map; plaintext JSON in-memory after decrypt
	SessionNonce     []byte
	SessionExpiredAt *time.Time // non-nil when the stored session failed validation; cleared on re-auth
	Extra            map[string]any
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// TopicDelivery records one torrent Marauder pushed to a client for a
// topic. Infohash is the BitTorrent v1 infohash of the pushed payload —
// the key used to query the client for live download status. Label is the
// human-readable form: "s02e06" for episodic trackers, the release/display
// name for single-torrent topics.
type TopicDelivery struct {
	ID          uuid.UUID
	TopicID     uuid.UUID
	Infohash    string
	Label       string
	ClientID    *uuid.UUID
	DeliveredAt time.Time
}

// InFlightDelivery is a not-yet-completed delivery joined with its topic's
// owner, notifier override, display name and tracker URL — the read model the
// progress watcher needs to poll the client and route a download.completed
// event (URL becomes the notification's SourceURL, issue #109).
type InFlightDelivery struct {
	DeliveryID  uuid.UUID
	TopicID     uuid.UUID
	UserID      uuid.UUID
	NotifierID  *uuid.UUID
	ClientID    *uuid.UUID
	Infohash    string
	Label       string
	DisplayName string
	URL         string
}

// TopicEvent is a single entry in a topic's history.
type TopicEvent struct {
	ID        int64
	TopicID   uuid.UUID
	UserID    uuid.UUID
	EventType string // "checked" | "updated" | "error" | "submitted"
	Severity  string // "info" | "warn" | "error"
	Message   string
	Data      map[string]any
	CreatedAt time.Time
}

// --- Torrent clients ----------------------------------------------------

// Client is a torrent client configuration owned by a user.
type Client struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	ClientName  string
	DisplayName string
	ConfigEnc   []byte
	ConfigNonce []byte
	IsDefault   bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// --- Notifiers ----------------------------------------------------------

// Notifier is a notification target owned by a user.
type Notifier struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	NotifierName string
	DisplayName  string
	ConfigEnc    []byte
	ConfigNonce  []byte
	Events       []string
	IsDefault    bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// --- Plugin boundary types ---------------------------------------------

// Check is the result of a tracker plugin's Check call.
//
// Hash is a tracker-specific opaque identifier that the scheduler uses to
// decide whether a topic has been updated. It is usually the sha1 of the
// underlying .torrent file, but for magnet-only trackers it may be derived
// from the magnet's btih / announce list.
type Check struct {
	Hash        string
	DisplayName string
	Extra       map[string]any
}

// Payload is the result of a tracker plugin's Download call.
//
// Exactly one of TorrentFile or MagnetURI must be set.
type Payload struct {
	TorrentFile []byte
	MagnetURI   string
	FileName    string // suggested filename for TorrentFile
}

// AddOptions carries per-add options from Marauder into a torrent client.
type AddOptions struct {
	DownloadDir string
	Category    string
	Paused      bool
}

// Message is a structured notification body. Link points at the Marauder
// UI; SourceURL is the topic's original tracker page (empty when the event
// has no topic source, e.g. a credential session expiry). AuthorComment is
// the release author's latest tracker comment (issue #110), already
// plain-text and length-capped by the scheduler; empty when the tracker
// can't provide one.
type Message struct {
	Title         string
	Body          string
	Link          string
	SourceURL     string
	AuthorComment string
}

// SonarrInstance is the runtime configuration for one Sonarr instance,
// persisted as a row in the sonarr_instances table. It is the plaintext-facing
// view: APIKey is decrypted on read and (when non-empty) encrypted on write. The
// repository never exposes the encrypted bytes past its boundary. Multiple
// instances run independently, each with its own enabled flag and history cursor.
type SonarrInstance struct {
	ID                 uuid.UUID
	Name               string
	Enabled            bool
	URL                string
	APIKey             string // decrypted; empty on read means "no key stored"
	PollIntervalSec    int
	AllowedTrackers    []string // tracker Name()s; empty = all supported
	DefaultClientID    *uuid.UUID
	DefaultCategory    string
	DefaultDownloadDir string
	UpdateExisting     bool
	OwnerUserID        *uuid.UUID
	LastSeenAt         *time.Time // history-poll cursor
	CreatedAt          time.Time
	UpdatedAt          time.Time
}
