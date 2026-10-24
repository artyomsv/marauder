package deluge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// RenameRoot implements registry.WithRename via core.rename_folder
// (issue #225). Deluge names folders with a trailing slash.
func (p *plugin) RenameRoot(ctx context.Context, rawConfig []byte, hash, oldName, newName string) error {
	return p.simpleCall(ctx, rawConfig, "core.rename_folder", []any{hash, folderPath(oldName), folderPath(newName)})
}

// Recheck implements registry.WithRename. core.force_recheck takes a list.
func (p *plugin) Recheck(ctx context.Context, rawConfig []byte, hash string) error {
	return p.simpleCall(ctx, rawConfig, "core.force_recheck", []any{[]string{hash}})
}

// Stop implements registry.WithRename. Deluge 2 takes one torrent id here,
// as core.resume_torrent does for Start.
func (p *plugin) Stop(ctx context.Context, rawConfig []byte, hash string) error {
	return p.simpleCall(ctx, rawConfig, "core.pause_torrent", []any{hash})
}

// DiskState implements registry.WithRename: total_done, the bytes on disk,
// and state. Deluge's "paused" flag clears before the state does after a
// resume, so only state "Paused" counts as stopped.
func (p *plugin) DiskState(ctx context.Context, rawConfig []byte, hash string) (registry.DiskState, error) {
	var c Config
	if err := json.Unmarshal(rawConfig, &c); err != nil {
		return registry.DiskState{}, fmt.Errorf("bad config: %w", err)
	}
	s, err := p.session(ctx, c)
	if err != nil {
		return registry.DiskState{}, err
	}
	out, err := p.call(ctx, s, c.URL, "core.get_torrent_status", []any{hash, []string{"total_done", "state"}})
	if err != nil {
		return registry.DiskState{}, err
	}
	// An unknown torrent id is an empty dictionary, so a missing key is
	// "not found", not 0 bytes.
	status, _ := out["result"].(map[string]any)
	done, ok := status["total_done"].(float64)
	if !ok {
		return registry.DiskState{}, errors.New("torrent not found")
	}
	state, _ := status["state"].(string)
	return registry.DiskState{BytesOnDisk: int64(done), Stopped: state == "Paused"}, nil
}

func (p *plugin) simpleCall(ctx context.Context, rawConfig []byte, method string, params []any) error {
	var c Config
	if err := json.Unmarshal(rawConfig, &c); err != nil {
		return fmt.Errorf("bad config: %w", err)
	}
	s, err := p.session(ctx, c)
	if err != nil {
		return err
	}
	_, err = p.call(ctx, s, c.URL, method, params)
	return err
}

func folderPath(name string) string {
	return strings.TrimSuffix(name, "/") + "/"
}
