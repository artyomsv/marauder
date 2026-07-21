package repo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// trackerSettingsPool is the minimal pgxpool subset used by TrackerSettings.
type trackerSettingsPool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// TrackerSetting is one tracker's domain configuration row.
type TrackerSetting struct {
	TrackerName   string
	ActiveDomain  string // "" = plugin default
	CustomDomains []string
}

// TrackerSettings is the repository for the tracker_settings table (issue #126).
type TrackerSettings struct {
	pool trackerSettingsPool
}

// NewTrackerSettings constructs the repository.
func NewTrackerSettings(pool *pgxpool.Pool) *TrackerSettings {
	return &TrackerSettings{pool: pool}
}

// List returns every configured tracker row.
func (r *TrackerSettings) List(ctx context.Context) ([]TrackerSetting, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT tracker_name, COALESCE(active_domain,''), custom_domains FROM tracker_settings ORDER BY tracker_name`)
	if err != nil {
		return nil, fmt.Errorf("tracker_settings: list: %w", err)
	}
	defer rows.Close()
	out := []TrackerSetting{}
	for rows.Next() {
		var s TrackerSetting
		var raw []byte
		if err := rows.Scan(&s.TrackerName, &s.ActiveDomain, &raw); err != nil {
			return nil, fmt.Errorf("tracker_settings: scan: %w", err)
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &s.CustomDomains); err != nil {
				return nil, fmt.Errorf("tracker_settings: custom_domains: %w", err)
			}
		}
		if s.CustomDomains == nil {
			s.CustomDomains = []string{}
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tracker_settings: rows: %w", err)
	}
	return out, nil
}

// Upsert writes one tracker's domain configuration. activeDomain "" is
// stored as NULL (plugin default).
func (r *TrackerSettings) Upsert(ctx context.Context, trackerName, activeDomain string, customDomains []string) error {
	if customDomains == nil {
		customDomains = []string{}
	}
	raw, err := json.Marshal(customDomains)
	if err != nil {
		return fmt.Errorf("tracker_settings: marshal custom_domains: %w", err)
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO tracker_settings (tracker_name, active_domain, custom_domains, updated_at)
		 VALUES ($1, NULLIF($2,''), $3, now())
		 ON CONFLICT (tracker_name) DO UPDATE
		 SET active_domain = EXCLUDED.active_domain,
		     custom_domains = EXCLUDED.custom_domains,
		     updated_at = now()`,
		trackerName, activeDomain, raw)
	if err != nil {
		return fmt.Errorf("tracker_settings: upsert: %w", err)
	}
	return nil
}
