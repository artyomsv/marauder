package deluge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

type torrentFiles struct {
	Files []struct {
		Index int    `json:"index"`
		Path  string `json:"path"`
		Size  int64  `json:"size"`
	} `json:"files"`
	FilePriorities []int `json:"file_priorities"`
}

func (p *plugin) torrentFiles(ctx context.Context, rawConfig []byte, hash string) (*session, Config, *torrentFiles, error) {
	var c Config
	if err := json.Unmarshal(rawConfig, &c); err != nil {
		return nil, c, nil, fmt.Errorf("bad config: %w", err)
	}
	s, err := p.session(ctx, c)
	if err != nil {
		return nil, c, nil, err
	}
	out, err := p.call(ctx, s, c.URL, "core.get_torrent_status", []any{hash, []string{"files", "file_priorities"}})
	if err != nil {
		return nil, c, nil, err
	}
	raw, _ := json.Marshal(out["result"])
	var tf torrentFiles
	if err := json.Unmarshal(raw, &tf); err != nil {
		return nil, c, nil, fmt.Errorf("decode torrent status: %w", err)
	}
	return s, c, &tf, nil
}

// Files implements registry.WithFileSelection. Deluge answers an unknown
// torrent id with an empty status, reported as an empty list.
func (p *plugin) Files(ctx context.Context, rawConfig []byte, hash string) ([]domain.ClientFile, error) {
	_, _, tf, err := p.torrentFiles(ctx, rawConfig, hash)
	if err != nil {
		return nil, err
	}
	out := make([]domain.ClientFile, 0, len(tf.Files))
	for _, f := range tf.Files {
		wanted := true
		if f.Index < len(tf.FilePriorities) {
			wanted = tf.FilePriorities[f.Index] != 0
		}
		out = append(out, domain.ClientFile{Index: f.Index, Path: f.Path, Size: f.Size, Wanted: wanted})
	}
	return out, nil
}

// SkipFiles implements registry.WithFileSelection. Deluge sets priorities as
// one full list, so the current list is read first and only the skipped
// entries change; set_torrent_options is used because Deluge 2 dropped
// set_torrent_file_priorities.
func (p *plugin) SkipFiles(ctx context.Context, rawConfig []byte, hash string, indices []int) error {
	if len(indices) == 0 {
		return nil
	}
	s, c, tf, err := p.torrentFiles(ctx, rawConfig, hash)
	if err != nil {
		return err
	}
	prios := make([]int, len(tf.Files))
	for i := range prios {
		prios[i] = 4 // Deluge's "normal"
		if i < len(tf.FilePriorities) {
			prios[i] = tf.FilePriorities[i]
		}
	}
	for _, idx := range indices {
		if idx < 0 || idx >= len(prios) {
			return errors.New("deluge: file index out of range")
		}
		prios[idx] = 0
	}
	_, err = p.call(ctx, s, c.URL, "core.set_torrent_options", []any{[]string{hash}, map[string]any{"file_priorities": prios}})
	return err
}

// Start implements registry.WithFileSelection.
func (p *plugin) Start(ctx context.Context, rawConfig []byte, hash string) error {
	var c Config
	if err := json.Unmarshal(rawConfig, &c); err != nil {
		return fmt.Errorf("bad config: %w", err)
	}
	s, err := p.session(ctx, c)
	if err != nil {
		return err
	}
	_, err = p.call(ctx, s, c.URL, "core.resume_torrent", []any{hash})
	return err
}
