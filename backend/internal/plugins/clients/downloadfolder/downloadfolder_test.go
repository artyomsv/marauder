package downloadfolder

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

func TestTest(t *testing.T) {
	p := &plugin{}
	dir := t.TempDir()
	cfg, _ := json.Marshal(Config{Path: dir})
	if err := p.Test(context.Background(), cfg); err != nil {
		t.Fatalf("Test on existing dir: %v", err)
	}
	bad, _ := json.Marshal(Config{Path: filepath.Join(dir, "doesnotexist")})
	if err := p.Test(context.Background(), bad); err == nil {
		t.Fatal("expected error on missing dir")
	}
}

func TestAddTorrentFile(t *testing.T) {
	p := &plugin{}
	dir := t.TempDir()
	cfg, _ := json.Marshal(Config{Path: dir})
	payload := &domain.Payload{
		TorrentFile: []byte("d8:announce..."),
		FileName:    "movie.torrent",
	}
	if err := p.Add(context.Background(), cfg, payload, domain.AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	want := filepath.Join(dir, "movie.torrent")
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "d8:announce..." {
		t.Errorf("body mismatch")
	}
}

func TestAddMagnet(t *testing.T) {
	p := &plugin{}
	dir := t.TempDir()
	cfg, _ := json.Marshal(Config{Path: dir})
	payload := &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:abc&dn=test"}
	if err := p.Add(context.Background(), cfg, payload, domain.AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 file, got %d", len(entries))
	}
	if !strings.HasSuffix(entries[0].Name(), ".magnet") {
		t.Errorf("expected .magnet extension, got %s", entries[0].Name())
	}
	body, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if string(body) != payload.MagnetURI {
		t.Errorf("magnet body mismatch")
	}
}

func TestAddRespectsOptOverrideDir(t *testing.T) {
	p := &plugin{}
	defaultDir := t.TempDir()
	overrideDir := t.TempDir()
	cfg, _ := json.Marshal(Config{Path: defaultDir})
	payload := &domain.Payload{TorrentFile: []byte("xx"), FileName: "f.torrent"}
	if err := p.Add(context.Background(), cfg, payload, domain.AddOptions{DownloadDir: overrideDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(overrideDir, "f.torrent")); err != nil {
		t.Fatalf("file not in override dir: %v", err)
	}
}
