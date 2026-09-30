//go:build clientcheck

// Package fileselectioncheck runs the download-only-new-files steps against
// real torrent clients (issue #205): add a two-version torrent's second
// version paused, skip the first version's file, start it, and read the
// result back from the client. Point it at the clients from
// deploy/docker-compose.test-clients.yml, brought up under its own project
// name so it cannot touch a running stack:
//
//	docker compose -p marauder205check -f deploy/docker-compose.test-clients.yml up -d --wait
//
// then run this inside the marauder205check_default network with:
//
//	MARAUDER_CHECK_QBIT_URLS="http://qbittorrent-521:6611,http://qbittorrent-514:6611"
//	MARAUDER_CHECK_QBIT_PASSWORDS="<pw-521>,<pw-514>"  (temporary passwords from each container log;
//	                                                   MARAUDER_CHECK_QBIT_PASSWORD if they are the same)
//	MARAUDER_CHECK_TRANSMISSION_URLS="http://transmission-412:9091/transmission/rpc,http://transmission-406:9091/transmission/rpc"
//	MARAUDER_CHECK_DELUGE_URL="http://deluge-220:8112"  MARAUDER_CHECK_DELUGE_PASSWORD=deluge
//	go test -tags=clientcheck ./internal/plugins/clients/fileselectioncheck/ -v
//
// Every torrent is named TEST-205-<random> and removed with its data at the end.
package fileselectioncheck

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/infohash"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/torrentmeta"
	"github.com/artyomsv/marauder/backend/internal/torrentmeta/torrentmetatest"

	_ "github.com/artyomsv/marauder/backend/internal/plugins/clients/deluge"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/clients/qbittorrent"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/clients/transmission"
)

type target struct {
	plugin string
	config map[string]any
}

func targets() []target {
	var out []target
	passwords := split(os.Getenv("MARAUDER_CHECK_QBIT_PASSWORDS"))
	for i, u := range split(os.Getenv("MARAUDER_CHECK_QBIT_URLS")) {
		pw := os.Getenv("MARAUDER_CHECK_QBIT_PASSWORD")
		if i < len(passwords) {
			pw = passwords[i]
		}
		out = append(out, target{"qbittorrent", map[string]any{"url": u, "username": "admin", "password": pw}})
	}
	for _, u := range split(os.Getenv("MARAUDER_CHECK_TRANSMISSION_URLS")) {
		out = append(out, target{"transmission", map[string]any{"url": u}})
	}
	if u := os.Getenv("MARAUDER_CHECK_DELUGE_URL"); u != "" {
		out = append(out, target{"deluge", map[string]any{"url": u, "password": os.Getenv("MARAUDER_CHECK_DELUGE_PASSWORD")}})
	}
	return out
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func TestSkipOldFilesOnRealClients(t *testing.T) {
	ts := targets()
	if len(ts) == 0 {
		t.Skip("no MARAUDER_CHECK_* client configured")
	}
	for _, tg := range ts {
		t.Run(tg.plugin+" "+tg.config["url"].(string), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			client := registry.GetClient(tg.plugin)
			sel, ok := client.(registry.WithFileSelection)
			if !ok {
				t.Fatalf("%s does not implement WithFileSelection", tg.plugin)
			}
			raw, _ := json.Marshal(tg.config)

			// The contract: a torrent the client does not know is an empty
			// list, not an error, so the scheduler can poll after an add.
			unknown := randomHash(t)
			files, err := sel.Files(ctx, raw, unknown)
			if err != nil || len(files) != 0 {
				t.Fatalf("Files(unknown hash) = %+v, %v; want empty list and nil error", files, err)
			}

			name := "TEST-205-" + uuid.NewString()[:8]
			v1 := []domain.TorrentFile{{Path: "E01.bin", Size: 1 << 20}}
			v2 := append(append([]domain.TorrentFile{}, v1...), domain.TorrentFile{Path: "E02.bin", Size: 2 << 20})
			data := torrentmetatest.Torrent(name, v2)
			hash, err := infohash.FromTorrent(data)
			if err != nil {
				t.Fatalf("infohash: %v", err)
			}
			t.Cleanup(func() {
				if rm, ok := client.(registry.WithRemoval); ok {
					if err := rm.Remove(context.Background(), raw, []string{hash}, true); err != nil {
						t.Errorf("cleanup Remove %s: %v", hash, err)
					}
				}
			})

			if err := client.Add(ctx, raw, &domain.Payload{TorrentFile: data, FileName: name + ".torrent"}, domain.AddOptions{Paused: true}); err != nil {
				t.Fatalf("Add paused: %v", err)
			}
			stopped := stateProbe(t, tg, raw)
			waitFor(t, ctx, func() bool {
				s, ok := stopped(ctx, hash)
				return ok && s
			}, "torrent to be listed as stopped (paused add honoured)")

			waitFor(t, ctx, func() bool {
				files, err = sel.Files(ctx, raw, hash)
				return err == nil && len(files) == len(v2)
			}, "client to list both files")
			t.Logf("client files after add: %+v", files)

			mapping, err := torrentmeta.MapClientFiles(files, v2)
			if err != nil {
				t.Fatalf("MapClientFiles(%+v): %v", files, err)
			}
			indices := torrentmeta.SkipIndices(mapping, torrentmeta.SkipSet(v1, v2))
			if len(indices) != 1 {
				t.Fatalf("skip indices %v in %+v, want exactly the old file", indices, files)
			}
			if err := sel.SkipFiles(ctx, raw, hash, indices); err != nil {
				t.Fatalf("SkipFiles: %v", err)
			}
			if err := sel.Start(ctx, raw, hash); err != nil {
				t.Fatalf("Start: %v", err)
			}

			waitFor(t, ctx, func() bool {
				files, err = sel.Files(ctx, raw, hash)
				if err != nil || len(files) != len(v2) {
					return false
				}
				for _, f := range files {
					isOld := strings.HasSuffix(f.Path, "E01.bin")
					if f.Wanted == isOld {
						return false
					}
				}
				return true
			}, "E01 skipped and E02 wanted")
			t.Logf("client files after skip+start: %+v", files)
			waitFor(t, ctx, func() bool {
				s, ok := stopped(ctx, hash)
				return ok && !s
			}, "torrent to leave the stopped state after Start")
		})
	}
}

// stateProbe reports whether the torrent is stopped. qBittorrent and
// Transmission answer through the plugin's own WithStatus; Deluge has no
// WithStatus, so its web JSON-RPC is asked directly — without that the check
// could not tell whether add_paused and core.resume_torrent did anything.
func stateProbe(t *testing.T, tg target, raw []byte) func(context.Context, string) (bool, bool) {
	t.Helper()
	if st, ok := registry.GetClient(tg.plugin).(registry.WithStatus); ok {
		return func(ctx context.Context, hash string) (bool, bool) {
			got, err := st.Status(ctx, raw, []string{hash})
			if err != nil || len(got) != 1 {
				return false, false
			}
			t.Logf("status: %s", got[0].State)
			return got[0].State == registry.StateStopped, true
		}
	}
	if tg.plugin != "deluge" {
		t.Fatalf("no state probe for %s", tg.plugin)
	}
	d := newDelugeProbe(t, tg.config["url"].(string), tg.config["password"].(string))
	return func(ctx context.Context, hash string) (bool, bool) {
		var res struct {
			State  string `json:"state"`
			Paused bool   `json:"paused"`
		}
		if err := d.call(ctx, "core.get_torrent_status", []any{hash, []string{"state", "paused"}}, &res); err != nil || res.State == "" {
			return false, false
		}
		t.Logf("deluge state: %s paused=%v", res.State, res.Paused)
		// Right after resume_torrent Deluge reports paused=false while state
		// still says "Paused"; only both clearing counts as started.
		return res.Paused || res.State == "Paused", true
	}
}

type delugeProbe struct {
	url    string
	client *http.Client
}

func newDelugeProbe(t *testing.T, url, password string) *delugeProbe {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	d := &delugeProbe{url: strings.TrimRight(url, "/") + "/json", client: &http.Client{Jar: jar, Timeout: 10 * time.Second}}
	var ok bool
	if err := d.call(context.Background(), "auth.login", []any{password}, &ok); err != nil || !ok {
		t.Fatalf("deluge probe login: ok=%v err=%v", ok, err)
	}
	return d
}

func (d *delugeProbe) call(ctx context.Context, method string, params []any, result any) error {
	body, _ := json.Marshal(map[string]any{"method": method, "params": params, "id": 1})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  any             `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if out.Error != nil {
		return fmt.Errorf("deluge %s: %v", method, out.Error)
	}
	return json.Unmarshal(out.Result, result)
}

func randomHash(t *testing.T) string {
	t.Helper()
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func waitFor(t *testing.T, ctx context.Context, cond func() bool, what string) {
	t.Helper()
	for !cond() {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", what)
		case <-time.After(500 * time.Millisecond):
		}
	}
}
