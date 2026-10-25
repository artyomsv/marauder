package repo

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Settings is the repository for the singleton `settings` row (id = 1): the
// instance-wide knobs every user's new topics are created with. A stored
// default is materialised onto a topic at create time, so editing it here
// never changes an existing topic.
type Settings struct {
	pool *pgxpool.Pool
}

// NewSettings constructs the repository.
func NewSettings(pool *pgxpool.Pool) *Settings {
	return &Settings{pool: pool}
}

// InstanceSettings is the value of the singleton settings row.
type InstanceSettings struct {
	// DefaultFollowTrackerTitle seeds new topics' follow_tracker_title when
	// the creator does not send an explicit choice.
	DefaultFollowTrackerTitle bool
}

// Get reads the singleton settings row.
func (r *Settings) Get(ctx context.Context) (*InstanceSettings, error) {
	var s InstanceSettings
	err := r.pool.QueryRow(ctx,
		`SELECT follow_tracker_title_default FROM settings WHERE id = 1`).
		Scan(&s.DefaultFollowTrackerTitle)
	if err != nil {
		return nil, fmt.Errorf("settings: get: %w", err)
	}
	return &s, nil
}

// SetDefaultFollowTrackerTitle stores the default applied to new topics.
func (r *Settings) SetDefaultFollowTrackerTitle(ctx context.Context, v bool) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE settings SET follow_tracker_title_default = $1, updated_at = now() WHERE id = 1`, v)
	if err != nil {
		return fmt.Errorf("settings: set default follow tracker title: %w", err)
	}
	return nil
}
