package transmission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// RenameRoot implements registry.WithRename via torrent-rename-path
// (issue #225). Renaming the root also changes the torrent's name in
// Transmission's list.
func (p *plugin) RenameRoot(ctx context.Context, rawConfig []byte, hash, oldName, newName string) error {
	return p.simple(ctx, rawConfig, "torrent-rename-path", map[string]any{
		"ids":  []any{hash},
		"path": oldName,
		"name": newName,
	})
}

// Recheck implements registry.WithRename.
func (p *plugin) Recheck(ctx context.Context, rawConfig []byte, hash string) error {
	return p.simple(ctx, rawConfig, "torrent-verify", map[string]any{"ids": []any{hash}})
}

// DiskState implements registry.WithRename: the bytes on disk, verified or
// not (downloadedEver would miss data a check found, and data on disk is what
// a rename could overwrite), and whether the torrent is stopped (status 0).
func (p *plugin) DiskState(ctx context.Context, rawConfig []byte, hash string) (registry.DiskState, error) {
	var c Config
	if err := json.Unmarshal(rawConfig, &c); err != nil {
		return registry.DiskState{}, fmt.Errorf("bad config: %w", err)
	}
	resp, err := p.do(ctx, c, "torrent-get", map[string]any{
		"ids":    []any{hash},
		"fields": []string{"haveValid", "haveUnchecked", "status"},
	})
	if err != nil {
		return registry.DiskState{}, err
	}
	if result, _ := resp["result"].(string); result != "success" {
		return registry.DiskState{}, fmt.Errorf("transmission rejected torrent-get: %v", result)
	}
	// Safe to drop: the value came out of json.Unmarshal (see Files).
	raw, _ := json.Marshal(resp["arguments"])
	var args struct {
		Torrents []struct {
			HaveValid     int64 `json:"haveValid"`
			HaveUnchecked int64 `json:"haveUnchecked"`
			Status        int   `json:"status"`
		} `json:"torrents"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return registry.DiskState{}, fmt.Errorf("decode torrent-get: %w", err)
	}
	if len(args.Torrents) == 0 {
		return registry.DiskState{}, errors.New("torrent not found")
	}
	tor := args.Torrents[0]
	return registry.DiskState{BytesOnDisk: tor.HaveValid + tor.HaveUnchecked, Stopped: tor.Status == 0}, nil
}
