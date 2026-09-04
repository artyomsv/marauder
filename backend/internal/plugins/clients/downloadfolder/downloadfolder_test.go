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

func TestAddNestsCategoryUnderBase(t *testing.T) {
	p := &plugin{}
	base := t.TempDir()
	cfg, _ := json.Marshal(Config{Path: base})
	payload := &domain.Payload{TorrentFile: []byte("xx"), FileName: "f.torrent"}
	if err := p.Add(context.Background(), cfg, payload, domain.AddOptions{Category: "Movies"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, "Movies", "f.torrent")); err != nil {
		t.Fatalf("file not nested under category: %v", err)
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

// TestSafeFileName reduces a payload name to a single path segment. This is
// the barrier for EVERY tracker plugin: several scrape the name off a page,
// where the release uploader controls it.
func TestSafeFileName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"movie.torrent", "movie.torrent"},
		{"  spaced.torrent  ", "spaced.torrent"},
		{"[R.G. Mechanics] Game.torrent", "[R.G. Mechanics] Game.torrent"},
		{"../../../etc/cron.d/evil.torrent", "evil.torrent"},
		{"/etc/passwd", "passwd"},
		{`..\..\windows\evil.torrent`, "evil.torrent"},
		{"nested/path/file.torrent", "file.torrent"},
		{"evil\x00.torrent", "evil.torrent"},
		{"", ""},
		{"..", ""},
		{".", ""},
		{"/", ""},
		{"///", ""},
	} {
		if got := safeFileName(tc.in); got != tc.want {
			t.Errorf("safeFileName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestAddTorrentFile_NameCannotEscapeTheFolder is the end-to-end version:
// filepath.Join CLEANS its result, so a name carrying `../` resolves OUTSIDE
// the configured directory instead of being rejected — which would let a
// tracker page choose where attacker-controlled bytes land.
func TestAddTorrentFile_NameCannotEscapeTheFolder(t *testing.T) {
	p := &plugin{}
	root := t.TempDir()
	dir := filepath.Join(root, "watch")
	cfg, _ := json.Marshal(Config{Path: dir})
	payload := &domain.Payload{
		TorrentFile: []byte("d8:announce..."),
		FileName:    "../../escaped.torrent",
	}
	if err := p.Add(context.Background(), cfg, payload, domain.AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Nothing outside the watch directory.
	for _, escaped := range []string{
		filepath.Join(root, "escaped.torrent"),
		filepath.Join(filepath.Dir(root), "escaped.torrent"),
	} {
		if _, err := os.Stat(escaped); err == nil {
			t.Fatalf("wrote outside the configured folder: %s", escaped)
		}
	}
	// And the file landed inside it, under a bare name.
	if _, err := os.Stat(filepath.Join(dir, "escaped.torrent")); err != nil {
		t.Errorf("expected the file inside the watch folder: %v", err)
	}
}

// TestAddTorrentFile_UnusableNameFallsBackToATimestamp: a name that is
// entirely separators leaves nothing to write to, and dropping the delivery
// would be worse than naming it by time.
func TestAddTorrentFile_UnusableNameFallsBackToATimestamp(t *testing.T) {
	p := &plugin{}
	dir := t.TempDir()
	cfg, _ := json.Marshal(Config{Path: dir})
	payload := &domain.Payload{TorrentFile: []byte("d8:announce..."), FileName: "../.."}
	if err := p.Add(context.Background(), cfg, payload, domain.AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("wrote %d files, want 1", len(entries))
	}
	if !strings.HasSuffix(entries[0].Name(), ".torrent") {
		t.Errorf("fallback name = %q, want a .torrent", entries[0].Name())
	}
}
