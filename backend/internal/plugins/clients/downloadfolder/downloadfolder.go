// Package downloadfolder implements a trivial client plugin: it writes
// .torrent files (or a .magnet text file) to a local directory. Use it as
// the simplest possible delivery target for development and as a fallback
// when no real client is configured.
package downloadfolder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// Config is the persisted configuration for a download folder client.
type Config struct {
	Path string `json:"path"`
}

type plugin struct{}

func init() {
	registry.RegisterClient(&plugin{})
}

func (p *plugin) Name() string        { return "downloadfolder" }
func (p *plugin) DisplayName() string { return "Download to folder" }

func (p *plugin) ConfigSchema() map[string]any {
	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "title": "Folder path"},
		},
		"required": []string{"path"},
	}
}

func (p *plugin) Test(_ context.Context, rawConfig []byte) error {
	var c Config
	if err := json.Unmarshal(rawConfig, &c); err != nil {
		return fmt.Errorf("bad config: %w", err)
	}
	if c.Path == "" {
		return errors.New("path is required")
	}
	fi, err := os.Stat(c.Path)
	if err != nil {
		return fmt.Errorf("stat %q: %w", c.Path, err)
	}
	if !fi.IsDir() {
		return errors.New("path is not a directory")
	}
	return nil
}

func (p *plugin) Add(_ context.Context, rawConfig []byte, payload *domain.Payload, opts domain.AddOptions) error {
	var c Config
	if err := json.Unmarshal(rawConfig, &c); err != nil {
		return fmt.Errorf("bad config: %w", err)
	}
	dir := c.Path
	if opts.DownloadDir != "" {
		dir = opts.DownloadDir
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	switch {
	case len(payload.TorrentFile) > 0:
		name := payload.FileName
		if name == "" {
			name = stamp + ".torrent"
		}
		dest := filepath.Join(dir, name)
		if err := os.WriteFile(dest, payload.TorrentFile, 0o640); err != nil {
			return fmt.Errorf("write torrent: %w", err)
		}
	case payload.MagnetURI != "":
		dest := filepath.Join(dir, stamp+".magnet")
		if err := os.WriteFile(dest, []byte(payload.MagnetURI), 0o640); err != nil {
			return fmt.Errorf("write magnet: %w", err)
		}
	default:
		return errors.New("empty payload")
	}
	return nil
}
