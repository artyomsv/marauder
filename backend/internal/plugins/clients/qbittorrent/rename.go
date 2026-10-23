package qbittorrent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// RenameRoot implements registry.WithRename via torrents/renameFolder
// (issue #225). qBittorrent answers 409 when the new path is in use; that and
// every other non-200 is an error.
func (p *plugin) RenameRoot(ctx context.Context, rawConfig []byte, hash, oldName, newName string) error {
	return p.postForm(ctx, rawConfig, "/api/v2/torrents/renameFolder", url.Values{
		"hash":    {hash},
		"oldPath": {oldName},
		"newPath": {newName},
	})
}

// Recheck implements registry.WithRename.
func (p *plugin) Recheck(ctx context.Context, rawConfig []byte, hash string) error {
	return p.postForm(ctx, rawConfig, "/api/v2/torrents/recheck", url.Values{"hashes": {hash}})
}

// DiskState implements registry.WithRename. It reads "completed" — the bytes
// the client has on disk, found by a check or downloaded — rather than
// "downloaded", which counts only what came from peers this session, and
// "state": a stopped torrent is "paused*" before qBittorrent 5.0 and
// "stopped*" since.
func (p *plugin) DiskState(ctx context.Context, rawConfig []byte, hash string) (registry.DiskState, error) {
	var cfg Config
	if err := json.Unmarshal(rawConfig, &cfg); err != nil {
		return registry.DiskState{}, fmt.Errorf("bad config: %w", err)
	}
	s, err := p.session(ctx, cfg)
	if err != nil {
		return registry.DiskState{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(cfg.URL, "/")+"/api/v2/torrents/info?hashes="+url.QueryEscape(hash), nil)
	if err != nil {
		return registry.DiskState{}, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return registry.DiskState{}, fmt.Errorf("torrent info: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return registry.DiskState{}, fmt.Errorf("qbit read info response (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK {
		return registry.DiskState{}, fmt.Errorf("qbit info status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var list []struct {
		Completed int64  `json:"completed"`
		State     string `json:"state"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return registry.DiskState{}, fmt.Errorf("decode info: %w", err)
	}
	if len(list) == 0 {
		return registry.DiskState{}, errors.New("torrent not found")
	}
	stopped := strings.HasPrefix(list[0].State, "paused") || strings.HasPrefix(list[0].State, "stopped")
	return registry.DiskState{BytesOnDisk: list[0].Completed, Stopped: stopped}, nil
}
