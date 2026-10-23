package transmission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// BytesDone implements registry.WithRename: the bytes on disk, verified or
// not. downloadedEver would miss data a check found, and data on disk is what
// a rename could overwrite.
func (p *plugin) BytesDone(ctx context.Context, rawConfig []byte, hash string) (int64, error) {
	var c Config
	if err := json.Unmarshal(rawConfig, &c); err != nil {
		return 0, fmt.Errorf("bad config: %w", err)
	}
	resp, err := p.do(ctx, c, "torrent-get", map[string]any{
		"ids":    []any{hash},
		"fields": []string{"haveValid", "haveUnchecked"},
	})
	if err != nil {
		return 0, err
	}
	if result, _ := resp["result"].(string); result != "success" {
		return 0, fmt.Errorf("transmission rejected torrent-get: %v", result)
	}
	// Safe to drop: the value came out of json.Unmarshal (see Files).
	raw, _ := json.Marshal(resp["arguments"])
	var args struct {
		Torrents []struct {
			HaveValid     int64 `json:"haveValid"`
			HaveUnchecked int64 `json:"haveUnchecked"`
		} `json:"torrents"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return 0, fmt.Errorf("decode torrent-get: %w", err)
	}
	if len(args.Torrents) == 0 {
		return 0, errors.New("torrent not found")
	}
	return args.Torrents[0].HaveValid + args.Torrents[0].HaveUnchecked, nil
}
