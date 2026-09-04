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
	"path"
	"path/filepath"
	"strings"
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
	// c.Path is the base folder; a topic's category nests under it and an
	// explicit per-topic DownloadDir overrides both — uniform with the
	// networked clients.
	dir := registry.EffectiveDownloadDir(c.Path, opts.DownloadDir, opts.Category)
	if dir == "" {
		dir = c.Path
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	// 0o600: only the user running Marauder can read/write the
	// dropped file. If the downstream client (SABnzbd, qBittorrent
	// watch folder) runs as a different user, the operator should
	// either set up a shared group and chmod the directory, or run
	// both processes under the same UID.
	const fileMode os.FileMode = 0o600
	switch {
	case len(payload.TorrentFile) > 0:
		// filepath.Base before the join: FileName is scraped off a tracker
		// page by several plugins (the uploader controls it), and
		// filepath.Join CLEANS its result, so a name carrying `../` resolves
		// outside dir instead of being rejected. That would write
		// attacker-chosen bytes anywhere the backend can reach — this is the
		// only client that turns a payload into a file path, so the guard
		// belongs here where it covers every tracker at once.
		name := safeFileName(payload.FileName)
		if name == "" {
			name = stamp + ".torrent"
		}
		dest := filepath.Join(dir, name)
		if err := os.WriteFile(dest, payload.TorrentFile, fileMode); err != nil {
			return fmt.Errorf("write torrent: %w", err)
		}
	case payload.MagnetURI != "":
		dest := filepath.Join(dir, stamp+".magnet")
		if err := os.WriteFile(dest, []byte(payload.MagnetURI), fileMode); err != nil {
			return fmt.Errorf("write magnet: %w", err)
		}
	default:
		return errors.New("empty payload")
	}
	return nil
}

// safeFileName reduces a payload's file name to a single path segment, or ""
// when nothing usable is left for the caller to fall back on.
//
// filepath.Base alone is not enough on Windows, where a backslash is also a
// separator but a Linux-built binary's Base does not treat it as one; the
// separators are folded first so the result is a bare name on either OS.
// The `.`/`..` cases are what Base returns for input that was entirely
// separators, and neither is a file name.
func safeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.ReplaceAll(name, "\x00", "")
	base := path.Base(name)
	if base == "." || base == ".." || base == "/" {
		return ""
	}
	return base
}
