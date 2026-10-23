package deluge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

// BytesDone implements registry.WithRename: total_done, the bytes on disk.
func (p *plugin) BytesDone(ctx context.Context, rawConfig []byte, hash string) (int64, error) {
	var c Config
	if err := json.Unmarshal(rawConfig, &c); err != nil {
		return 0, fmt.Errorf("bad config: %w", err)
	}
	s, err := p.session(ctx, c)
	if err != nil {
		return 0, err
	}
	out, err := p.call(ctx, s, c.URL, "core.get_torrent_status", []any{hash, []string{"total_done"}})
	if err != nil {
		return 0, err
	}
	// An unknown torrent id is an empty dictionary, so a missing key is
	// "not found", not 0 bytes.
	status, _ := out["result"].(map[string]any)
	done, ok := status["total_done"].(float64)
	if !ok {
		return 0, errors.New("torrent not found")
	}
	return int64(done), nil
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
