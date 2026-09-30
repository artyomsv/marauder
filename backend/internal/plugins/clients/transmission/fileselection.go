package transmission

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// Files implements registry.WithFileSelection. Transmission numbers files by
// their position in torrent-get's files array; a torrent it does not know
// yields an empty torrents array, reported as an empty list.
func (p *plugin) Files(ctx context.Context, rawConfig []byte, hash string) ([]domain.ClientFile, error) {
	var c Config
	if err := json.Unmarshal(rawConfig, &c); err != nil {
		return nil, fmt.Errorf("bad config: %w", err)
	}
	resp, err := p.do(ctx, c, "torrent-get", map[string]any{
		"ids":    []any{hash},
		"fields": []string{"files", "fileStats"},
	})
	if err != nil {
		return nil, err
	}
	if result, _ := resp["result"].(string); result != "success" {
		return nil, fmt.Errorf("transmission rejected torrent-get: %v", result)
	}
	// Re-decode through a typed struct rather than walking map[string]any.
	// The error is safe to drop: the value came out of json.Unmarshal, so it
	// holds only types json.Marshal encodes.
	raw, _ := json.Marshal(resp["arguments"])
	var args struct {
		Torrents []struct {
			Files []struct {
				Name   string `json:"name"`
				Length int64  `json:"length"`
			} `json:"files"`
			FileStats []struct {
				Wanted bool `json:"wanted"`
			} `json:"fileStats"`
		} `json:"torrents"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode torrent-get: %w", err)
	}
	if len(args.Torrents) == 0 {
		return nil, nil
	}
	tor := args.Torrents[0]
	out := make([]domain.ClientFile, 0, len(tor.Files))
	for i, f := range tor.Files {
		wanted := true
		if i < len(tor.FileStats) {
			wanted = tor.FileStats[i].Wanted
		}
		out = append(out, domain.ClientFile{Index: i, Path: f.Name, Size: f.Length, Wanted: wanted})
	}
	return out, nil
}

// SkipFiles implements registry.WithFileSelection via torrent-set
// files-unwanted.
func (p *plugin) SkipFiles(ctx context.Context, rawConfig []byte, hash string, indices []int) error {
	if len(indices) == 0 {
		return nil
	}
	return p.simple(ctx, rawConfig, "torrent-set", map[string]any{"ids": []any{hash}, "files-unwanted": indices})
}

// Start implements registry.WithFileSelection.
func (p *plugin) Start(ctx context.Context, rawConfig []byte, hash string) error {
	return p.simple(ctx, rawConfig, "torrent-start", map[string]any{"ids": []any{hash}})
}

func (p *plugin) simple(ctx context.Context, rawConfig []byte, method string, args map[string]any) error {
	var c Config
	if err := json.Unmarshal(rawConfig, &c); err != nil {
		return fmt.Errorf("bad config: %w", err)
	}
	resp, err := p.do(ctx, c, method, args)
	if err != nil {
		return err
	}
	if result, _ := resp["result"].(string); result != "success" {
		return fmt.Errorf("transmission rejected %s: %v", method, result)
	}
	return nil
}
