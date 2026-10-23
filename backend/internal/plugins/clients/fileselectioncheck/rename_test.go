//go:build clientcheck

package fileselectioncheck

// The folder name steps (issue #225) against real clients: add a torrent
// paused under its tracker name, rename its top folder onto a folder that
// already holds its data, recheck, start during the recheck, and read back
// that the client found every byte and left the stopped state. The data is
// zero-filled, and the torrent carries the real piece hashes of zeros, so
// only a client that looked in the renamed folder can verify it.
//
// The test writes that data into each client's download volume, so it needs
// those volumes mounted and named per client URL:
//
//	docker compose -p marauder225check -f deploy/docker-compose.test-clients.yml up -d --wait
//
//	MARAUDER_CHECK_DATA_DIRS="http://qbittorrent-521:6611=/dl/qbit521,http://transmission-412:9091/transmission/rpc=/dl/tr412,..."
//
// where /dl/qbit521 is the marauder225check_qbit_521_downloads volume mounted
// into the test container (the client sees it as /downloads). A target
// without a data dir is skipped. Torrents are named TEST-225-<random> and
// removed with their data at the end.

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/infohash"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/torrentmeta"
)

const zeroPieceLength = 16384

// clientDir is where the test puts its data, as the client sees it.
const clientDir = "/downloads/test225"

func dataDirs() map[string]string {
	out := map[string]string{}
	for _, pair := range split(os.Getenv("MARAUDER_CHECK_DATA_DIRS")) {
		if url, dir, ok := strings.Cut(pair, "="); ok {
			out[url] = dir
		}
	}
	return out
}

func TestRenameOnRealClients(t *testing.T) {
	ts := targets()
	if len(ts) == 0 {
		t.Skip("no MARAUDER_CHECK_* client configured")
	}
	dirs := dataDirs()
	for _, tg := range ts {
		url := tg.config["url"].(string)
		t.Run(tg.plugin+" "+url, func(t *testing.T) {
			local, ok := dirs[url]
			if !ok {
				t.Skipf("no MARAUDER_CHECK_DATA_DIRS entry for %s", url)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			client := registry.GetClient(tg.plugin)
			sel, okSel := client.(registry.WithFileSelection)
			ren, okRen := client.(registry.WithRename)
			if !okSel || !okRen {
				t.Fatalf("%s does not implement WithFileSelection and WithRename", tg.plugin)
			}
			raw, _ := json.Marshal(tg.config)

			id := uuid.NewString()[:8]
			trackerName := "TEST-225-" + id
			want := "TEST-225-" + id + "-renamed"
			files := []domain.TorrentFile{
				{Path: "E01.bin", Size: 1 << 20},
				{Path: "Extras/E02.bin", Size: 2<<20 + 123},
			}
			var total int64
			for _, f := range files {
				total += f.Size
			}
			data := zeroTorrent(trackerName, files)
			hash, err := infohash.FromTorrent(data)
			if err != nil {
				t.Fatalf("infohash: %v", err)
			}
			writeZeroData(t, filepath.Join(local, "test225", want), files)
			t.Cleanup(func() {
				if rm, ok := client.(registry.WithRemoval); ok {
					if err := rm.Remove(context.Background(), raw, []string{hash}, true); err != nil {
						t.Errorf("cleanup Remove %s: %v", hash, err)
					}
				}
				_ = os.RemoveAll(filepath.Join(local, "test225", want))
				_ = os.RemoveAll(filepath.Join(local, "test225", trackerName))
			})

			// 1. Added paused under the tracker's name: nothing on disk there.
			if err := client.Add(ctx, raw, &domain.Payload{TorrentFile: data, FileName: trackerName + ".torrent"},
				domain.AddOptions{DownloadDir: clientDir, Paused: true}); err != nil {
				t.Fatalf("Add paused: %v", err)
			}
			var listed []domain.ClientFile
			waitFor(t, ctx, func() bool {
				listed, err = sel.Files(ctx, raw, hash)
				return err == nil && len(listed) == len(files)
			}, "client to list every file")
			root, ok := torrentmeta.ClientRoot(listed, files)
			if !ok || root != trackerName {
				t.Fatalf("ClientRoot(%+v) = (%q, %v), want (%q, true)", listed, root, ok, trackerName)
			}
			// The scheduler renames only a stopped torrent with nothing on disk.
			// Right after a paused add a client may still be checking it; log
			// how long that lasts.
			var state registry.DiskState
			addedAt := time.Now()
			probe := stateProbe(t, tg, raw)
			waitFor(t, ctx, func() bool {
				state, err = ren.DiskState(ctx, raw, hash)
				probe(ctx, hash)
				return err == nil && state.Stopped
			}, "torrent to report stopped after the paused add")
			t.Logf("stopped %v after the add", time.Since(addedAt).Round(100*time.Millisecond))
			if state.BytesOnDisk != 0 {
				t.Fatalf("DiskState before rename = %+v, want 0 bytes", state)
			}

			// 2. Rename onto the folder that holds the data, read the list back.
			if err := ren.RenameRoot(ctx, raw, hash, trackerName, want); err != nil {
				t.Fatalf("RenameRoot: %v", err)
			}
			waitFor(t, ctx, func() bool {
				listed, err = sel.Files(ctx, raw, hash)
				r, ok := torrentmeta.ClientRoot(listed, files)
				return err == nil && ok && r == want
			}, "client to list the renamed folder")
			t.Logf("files after rename: %+v", listed)

			// 3. Start, then recheck last: a client keeps a torrent's running
			// or stopped state through a recheck, while qBittorrent drops a
			// start sent during one (measured: 5.2.1 and 5.1.4 stayed stopped).
			if err := sel.Start(ctx, raw, hash); err != nil {
				t.Fatalf("Start: %v", err)
			}
			if err := ren.Recheck(ctx, raw, hash); err != nil {
				t.Fatalf("Recheck: %v", err)
			}
			waitFor(t, ctx, func() bool {
				state, err = ren.DiskState(ctx, raw, hash)
				return err == nil && state.BytesOnDisk == total
			}, fmt.Sprintf("recheck to find all %d bytes in the renamed folder", total))
			stopped := stateProbe(t, tg, raw)
			waitFor(t, ctx, func() bool {
				s, ok := stopped(ctx, hash)
				return ok && !s
			}, "torrent to leave the stopped state (a start sent before the recheck kept)")

			// 4. Renaming to the name it already has: the scheduler never does
			// this (same_name skips it); record what the client says.
			err = ren.RenameRoot(ctx, raw, hash, want, want)
			t.Logf("RenameRoot to the current name: %v", err)
		})
	}
}

// zeroTorrent is torrentmetatest.Torrent with the real piece hashes of
// zero-filled content, so a client can verify zero data on disk.
func zeroTorrent(name string, files []domain.TorrentFile) []byte {
	var b strings.Builder
	var total int64
	b.WriteString("d4:infod5:filesl")
	for _, f := range files {
		total += f.Size
		fmt.Fprintf(&b, "d6:lengthi%de4:pathl", f.Size)
		for _, part := range strings.Split(f.Path, "/") {
			fmt.Fprintf(&b, "%d:%s", len(part), part)
		}
		b.WriteString("ee")
	}
	b.WriteString("e")
	full := sha1.Sum(make([]byte, zeroPieceLength))
	var pieces strings.Builder
	for left := total; left > 0; left -= zeroPieceLength {
		if left >= zeroPieceLength {
			pieces.Write(full[:])
			continue
		}
		last := sha1.Sum(make([]byte, left))
		pieces.Write(last[:])
	}
	fmt.Fprintf(&b, "4:name%d:%s12:piece lengthi%de6:pieces%d:%see",
		len(name), name, zeroPieceLength, pieces.Len(), pieces.String())
	return []byte(b.String())
}

// writeZeroData creates the files under dir, readable and writable by the
// client's own user (the test runs as another one), so the client can verify
// them and delete them on cleanup.
func writeZeroData(t *testing.T, dir string, files []domain.TorrentFile) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, make([]byte, f.Size), 0o666); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	// MkdirAll and WriteFile are subject to the umask; set the modes outright.
	err := filepath.Walk(filepath.Dir(dir), func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.Chmod(p, 0o777)
		}
		return os.Chmod(p, 0o666)
	})
	if err != nil {
		t.Fatalf("chmod: %v", err)
	}
}
