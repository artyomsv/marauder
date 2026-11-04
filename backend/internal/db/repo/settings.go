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
	pool dbPool
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
	DefaultReplaceOnUpdate    bool
	DefaultReplaceDeleteData  bool
}

// SettingsPatch carries the fields to change; nil leaves a column alone.
type SettingsPatch struct {
	DefaultFollowTrackerTitle *bool
	DefaultReplaceOnUpdate    *bool
	DefaultReplaceDeleteData  *bool
}

// Get reads the singleton settings row.
func (r *Settings) Get(ctx context.Context) (*InstanceSettings, error) {
	var s InstanceSettings
	err := r.pool.QueryRow(ctx,
		`SELECT follow_tracker_title_default, replace_on_update_default, replace_delete_data_default FROM settings WHERE id = 1`).
		Scan(&s.DefaultFollowTrackerTitle, &s.DefaultReplaceOnUpdate, &s.DefaultReplaceDeleteData)
	if err != nil {
		return nil, fmt.Errorf("settings: get: %w", err)
	}
	return &s, nil
}

// Update applies the non-nil fields of p and returns the row as stored.
func (r *Settings) Update(ctx context.Context, p SettingsPatch) (*InstanceSettings, error) {
	var s InstanceSettings
	err := r.pool.QueryRow(ctx, `UPDATE settings SET
    follow_tracker_title_default = COALESCE($1, follow_tracker_title_default),
    replace_on_update_default = COALESCE($2, replace_on_update_default),
    replace_delete_data_default = COALESCE($3, replace_delete_data_default),
    updated_at = now()
WHERE id = 1 RETURNING follow_tracker_title_default, replace_on_update_default, replace_delete_data_default`,
		p.DefaultFollowTrackerTitle, p.DefaultReplaceOnUpdate, p.DefaultReplaceDeleteData).
		Scan(&s.DefaultFollowTrackerTitle, &s.DefaultReplaceOnUpdate, &s.DefaultReplaceDeleteData)
	if err != nil {
		return nil, fmt.Errorf("settings: update: %w", err)
	}
	return &s, nil
}
