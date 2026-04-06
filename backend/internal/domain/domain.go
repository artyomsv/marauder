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
	ID                uuid.UUID
	UserID            uuid.UUID
	TrackerName       string
	URL               string
	DisplayName       string
	ClientID          *uuid.UUID
	DownloadDir       string
	Extra             map[string]any
	LastHash          string
	LastCheckedAt     *time.Time
	LastUpdatedAt     *time.Time
	NextCheckAt       time.Time
	CheckIntervalSec  int
	ConsecutiveErrors int
	Status            TopicStatus
	LastError         string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// TrackerCredential holds a user's login details for a tracker plugin.
// The secret is stored encrypted at rest.
type TrackerCredential struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	TrackerName string
	Username    string
	SecretEnc   []byte // nil if not set
	SecretNonce []byte
	Extra       map[string]any
	CreatedAt   time.Time
	UpdatedAt   time.Time
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

// Message is a structured notification body.
type Message struct {
	Title string
	Body  string
	Link  string
}
