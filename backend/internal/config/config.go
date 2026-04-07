// Package config loads runtime configuration from environment variables.
//
// The philosophy is 12-factor: no files on disk, no layered TOML/YAML, just
// env vars prefixed with MARAUDER_. Every field is documented with a tag.
package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config holds all runtime configuration for the backend.
type Config struct {
	// Server
	HTTPAddr    string        `env:"MARAUDER_HTTP_ADDR" envDefault:":8679"`
	LogLevel    string        `env:"MARAUDER_LOG_LEVEL" envDefault:"info"`
	LogJSON     bool          `env:"MARAUDER_LOG_JSON" envDefault:"true"`
	CORSOrigins []string      `env:"MARAUDER_CORS_ORIGINS" envDefault:"http://localhost:5174,http://localhost:6688"`
	ShutdownTO  time.Duration `env:"MARAUDER_SHUTDOWN_TIMEOUT" envDefault:"15s"`

	// Public base URL (used in OIDC redirect, RFC7807 type URIs)
	PublicBaseURL string `env:"MARAUDER_PUBLIC_BASE_URL" envDefault:"http://localhost:6688"`

	// Database
	DatabaseURL     string        `env:"MARAUDER_DB_URL,required"`
	DBMaxConns      int32         `env:"MARAUDER_DB_MAX_CONNS" envDefault:"20"`
	DBMinConns      int32         `env:"MARAUDER_DB_MIN_CONNS" envDefault:"2"`
	DBConnLifetime  time.Duration `env:"MARAUDER_DB_CONN_LIFETIME" envDefault:"30m"`
	DBHealthCheck   time.Duration `env:"MARAUDER_DB_HEALTH_CHECK" envDefault:"1m"`
	DBMigrateOnBoot bool          `env:"MARAUDER_DB_MIGRATE_ON_BOOT" envDefault:"true"`

	// Master key: base64-encoded 32-byte AES-256 key for encrypting
	// secrets at rest (credentials, JWT private keys).
	MasterKeyB64 string `env:"MARAUDER_MASTER_KEY,required"`

	// Metrics endpoint bearer token. If empty, /metrics is disabled.
	MetricsToken string `env:"MARAUDER_METRICS_TOKEN" envDefault:""`

	// Auth
	AccessTokenTTL  time.Duration `env:"MARAUDER_ACCESS_TOKEN_TTL"  envDefault:"15m"`
	RefreshTokenTTL time.Duration `env:"MARAUDER_REFRESH_TOKEN_TTL" envDefault:"720h"` // 30 days
	JWTIssuer       string        `env:"MARAUDER_JWT_ISSUER" envDefault:"https://marauder.cc"`
	JWTAudience     string        `env:"MARAUDER_JWT_AUDIENCE" envDefault:"marauder-api"`

	// Initial admin bootstrap (only applied if no users exist yet).
	InitialAdminUser string `env:"MARAUDER_ADMIN_INITIAL_USERNAME" envDefault:""`
	InitialAdminPass string `env:"MARAUDER_ADMIN_INITIAL_PASSWORD" envDefault:""`

	// OIDC (optional)
	OIDCEnabled      bool     `env:"MARAUDER_OIDC_ENABLED" envDefault:"false"`
	OIDCIssuer       string   `env:"MARAUDER_OIDC_ISSUER" envDefault:""`
	OIDCClientID     string   `env:"MARAUDER_OIDC_CLIENT_ID" envDefault:""`
	OIDCClientSecret string   `env:"MARAUDER_OIDC_CLIENT_SECRET" envDefault:""`
	OIDCRedirectURL  string   `env:"MARAUDER_OIDC_REDIRECT_URL" envDefault:""`
	OIDCScopes       []string `env:"MARAUDER_OIDC_SCOPES" envDefault:"openid,profile,email"`

	// Scheduler
	SchedulerEnabled    bool          `env:"MARAUDER_SCHEDULER_ENABLED" envDefault:"true"`
	SchedulerTick       time.Duration `env:"MARAUDER_SCHEDULER_TICK" envDefault:"1m"`
	SchedulerWorkers    int           `env:"MARAUDER_SCHEDULER_WORKERS" envDefault:"8"`
	DefaultCheckEvery   time.Duration `env:"MARAUDER_DEFAULT_CHECK_INTERVAL" envDefault:"15m"`
	CheckMaxBackoff     time.Duration `env:"MARAUDER_CHECK_MAX_BACKOFF" envDefault:"6h"`
	TrackerHTTPTimeout  time.Duration `env:"MARAUDER_TRACKER_HTTP_TIMEOUT" envDefault:"30s"`
	TrackerHTTPProxyURL string        `env:"MARAUDER_HTTPS_PROXY" envDefault:""`
	UserAgent           string        `env:"MARAUDER_USER_AGENT" envDefault:"Marauder/0.0.0-dev (+https://marauder.cc)"`

	// Optional Cloudflare solver sidecar
	CFSolverEnabled bool   `env:"MARAUDER_CFSOLVER_ENABLED" envDefault:"false"`
	CFSolverURL     string `env:"MARAUDER_CFSOLVER_URL" envDefault:""`
}

// Load reads configuration from environment variables and validates it.
func Load() (*Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return nil, fmt.Errorf("parse env: %w", err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) validate() error {
	if len(c.MasterKeyB64) < 32 {
		return errors.New("MARAUDER_MASTER_KEY must be a base64-encoded 32-byte key")
	}
	if c.OIDCEnabled {
		if c.OIDCIssuer == "" || c.OIDCClientID == "" || c.OIDCRedirectURL == "" {
			return errors.New("OIDC is enabled but MARAUDER_OIDC_{ISSUER,CLIENT_ID,REDIRECT_URL} are not all set")
		}
	}
	if c.SchedulerWorkers < 1 {
		return errors.New("MARAUDER_SCHEDULER_WORKERS must be >= 1")
	}
	return nil
}
