package scheduler

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/infohash"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/torrentmeta/torrentmetatest"
)

// Folder name (issue #225).

// fakeRenamingClient adds registry.WithRename to fakeSelectingClient and logs
// every client call in order. filesByHash answers Files for a hash other than
// the delivery's own (keep mode reading the previous torrent).
type fakeRenamingClient struct {
	fakeSelectingClient
	calls       []string
	filesByHash map[string][]domain.ClientFile
	bytesDone   int64
	bytesErr    error
	running     bool
	// checkingPolls is how many DiskState calls report a torrent still
	// checking (not stopped) before it settles, as a fresh paused add does.
	checkingPolls int
	renameErr     error
	recheckErr    error
	renamedFrom   string
	renamedTo     string
}

func (f *fakeRenamingClient) Files(ctx context.Context, raw []byte, hash string) ([]domain.ClientFile, error) {
	if files, ok := f.filesByHash[hash]; ok {
		f.calls = append(f.calls, "Files("+hash+")")
		return files, nil
	}
	f.calls = append(f.calls, "Files")
	return f.fakeSelectingClient.Files(ctx, raw, hash)
}

func (f *fakeRenamingClient) Start(ctx context.Context, raw []byte, hash string) error {
	f.calls = append(f.calls, "Start")
	return f.fakeSelectingClient.Start(ctx, raw, hash)
}

func (f *fakeRenamingClient) SkipFiles(ctx context.Context, raw []byte, hash string, indices []int) error {
	f.calls = append(f.calls, "SkipFiles")
	return f.fakeSelectingClient.SkipFiles(ctx, raw, hash, indices)
}

func (f *fakeRenamingClient) RenameRoot(_ context.Context, _ []byte, _ string, oldName, newName string) error {
	f.calls = append(f.calls, "RenameRoot")
	f.renamedFrom, f.renamedTo = oldName, newName
	return f.renameErr
}

func (f *fakeRenamingClient) Recheck(context.Context, []byte, string) error {
	f.calls = append(f.calls, "Recheck")
	return f.recheckErr
}

func (f *fakeRenamingClient) DiskState(context.Context, []byte, string) (registry.DiskState, error) {
	f.calls = append(f.calls, "DiskState")
	if f.checkingPolls > 0 {
		f.checkingPolls--
		return registry.DiskState{}, nil
	}
	return registry.DiskState{BytesOnDisk: f.bytesDone, Stopped: !f.running}, f.bytesErr
}

var _ registry.WithRename = (*fakeRenamingClient)(nil)

// renamedClient is v2Client after its root was renamed to "Want".
var renamedClient = []domain.ClientFile{
	{Index: 0, Path: "Want/E01.mkv", Size: 100, Wanted: true},
	{Index: 1, Path: "Want/E02.mkv", Size: 200, Wanted: true},
}

func renamingFixture(t *testing.T, tr *fakeTracker) (*fixture, *fakeRenamingClient) {
	t.Helper()
	f := newFixture(t, tr)
	rc := &fakeRenamingClient{fakeSelectingClient: fakeSelectingClient{fakeClientPlugin: fakeClientPlugin{name: "fakeclient"}}}
	rc.filesSeq = [][]domain.ClientFile{v2Client, renamedClient}
	f.s.lookupClient = func(string) registry.Client { return rc }
	return f, rc
}

func customTopic(f *fixture, name string) {
	f.topic.FolderNameMode = domain.FolderNameCustom
	f.topic.FolderName = name
}

func TestRunCheck_FolderName_Custom_RenamesRechecksAndStarts(t *testing.T) {
	f, rc := renamingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	customTopic(f, "Want")

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if !rc.lastOpts.Paused {
		t.Error("torrent not added paused before the rename")
	}
	// Recheck comes last: qBittorrent drops a start sent during a recheck,
	// and every client keeps the running or stopped state through one.
	want := []string{"Files", "DiskState", "RenameRoot", "Files", "Start", "Recheck"}
	if !reflect.DeepEqual(rc.calls, want) {
		t.Errorf("calls = %v, want %v", rc.calls, want)
	}
	if rc.renamedFrom != "Show S01" || rc.renamedTo != "Want" {
		t.Errorf("renamed %q -> %q, want Show S01 -> Want", rc.renamedFrom, rc.renamedTo)
	}
	if body := submittedBody(t, f); !strings.Contains(body, `Saved in folder "Want".`) || strings.Contains(body, "Added paused") {
		t.Errorf("body = %q, want the saved note and no paused claim", body)
	}
	if rec := f.lastRecord(t); rec.errMsg != "" {
		t.Errorf("record errMsg = %q, want none", rec.errMsg)
	}
}

// A custom name applies to the first delivery too, not only to updates.
func TestRunCheck_FolderName_Custom_AppliesOnFirstDelivery(t *testing.T) {
	f, rc := renamingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.LastHash = ""
	customTopic(f, "Want")

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if !rc.lastOpts.Paused || rc.renamedTo != "Want" {
		t.Errorf("paused=%v renamedTo=%q, want a paused add renamed to Want", rc.lastOpts.Paused, rc.renamedTo)
	}
}

func TestRunCheck_FolderName_NothingToDo(t *testing.T) {
	episodic := torrentTracker(torrentmetatest.Torrent("Show S01", v2Files))
	episodic.episodic = true
	cases := []struct {
		name  string
		tr    *fakeTracker
		setup func(f *fixture)
	}{
		{"default mode", torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)), func(*fixture) {}},
		{"per-episode tracker", episodic, func(f *fixture) { customTopic(f, "Want") }},
		{"name already the tracker's", torrentTracker(torrentmetatest.Torrent("Want", v2Files)), func(f *fixture) { customTopic(f, "Want") }},
		{"keep with nothing known", torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)), func(f *fixture) {
			f.topic.FolderNameMode = domain.FolderNameKeep
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, rc := renamingFixture(t, tc.tr)
			tc.setup(f)

			f.s.runCheck(context.Background(), f.s.log, f.topic)

			if rc.lastOpts.Paused || len(rc.calls) != 0 {
				t.Errorf("paused=%v calls=%v, want a plain delivery", rc.lastOpts.Paused, rc.calls)
			}
			if body := submittedBody(t, f); strings.Contains(body, "folder") {
				t.Errorf("body = %q, want no folder note", body)
			}
		})
	}
}

func TestRunCheck_FolderName_CannotApply(t *testing.T) {
	cases := []struct {
		name     string
		tr       *fakeTracker
		plain    bool // a client without WithRename
		wantNote string
	}{
		{"magnet", singlePayloadTracker("c12fe1c06bba254a9dc9f519b335aa7c1367a88a"), false,
			`Could not use folder name "Want": a magnet link has no file list. Saved in the tracker's folder.`},
		{"single file", torrentTracker(torrentmetatest.SingleFile("Movie.mkv", 100)), false,
			`Could not use folder name "Want": the torrent is a single file. Saved in the tracker's folder.`},
		{"client cannot rename", torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)), true,
			`Could not use folder name "Want": this client cannot rename folders. Saved in the tracker's folder.`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, rc := renamingFixture(t, tc.tr)
			if tc.plain {
				f.s.lookupClient = func(string) registry.Client { return f.clientPlugin }
			}
			customTopic(f, "Want")

			f.s.runCheck(context.Background(), f.s.log, f.topic)

			if rc.lastOpts.Paused || f.clientPlugin.lastOpts.Paused || len(rc.calls) != 0 {
				t.Errorf("paused or client calls %v, want a plain delivery", rc.calls)
			}
			if body := submittedBody(t, f); !strings.Contains(body, tc.wantNote) {
				t.Errorf("body = %q, want %q", body, tc.wantNote)
			}
		})
	}
}

// Keep mode reads the previous version's folder in the client and remembers it.
func TestRunCheck_FolderName_Keep_ReadsPreviousTorrent(t *testing.T) {
	f, rc := renamingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.FolderNameMode = domain.FolderNameKeep
	cid := f.clients.client.ID
	f.deliveries.history = []*domain.TopicDelivery{{Infohash: "prev", ClientID: &cid, Files: v1Files}}
	rc.filesByHash = map[string][]domain.ClientFile{"prev": {{Index: 0, Path: "Want/E01.mkv", Size: 100}}}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if rc.renamedTo != "Want" {
		t.Errorf("renamedTo = %q, want the previous torrent's folder, Want", rc.renamedTo)
	}
	if len(rc.calls) == 0 || rc.calls[0] != "Files(prev)" {
		t.Errorf("calls = %v, want the previous torrent read first", rc.calls)
	}
	if !containsString(f.topics.seenCalls, "Want") {
		t.Errorf("seen = %v, want Want remembered", f.topics.seenCalls)
	}
}

// A retried delivery finds its own row already recorded; keep mode must read
// the version before it, not the torrent it is about to rename.
func TestRunCheck_FolderName_Keep_ExcludesOwnHash(t *testing.T) {
	data := torrentmetatest.Torrent("Show S01", v2Files)
	own, err := infohash.FromTorrent(data)
	if err != nil {
		t.Fatalf("infohash: %v", err)
	}
	f, rc := renamingFixture(t, torrentTracker(data))
	f.topic.FolderNameMode = domain.FolderNameKeep
	cid := f.clients.client.ID
	f.deliveries.history = []*domain.TopicDelivery{
		{Infohash: "prev", ClientID: &cid, Files: v1Files},
		{Infohash: own, ClientID: &cid, Files: v2Files},
	}
	rc.filesByHash = map[string][]domain.ClientFile{"prev": {{Index: 0, Path: "Want/E01.mkv", Size: 100}}}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if len(rc.calls) == 0 || rc.calls[0] != "Files(prev)" {
		t.Errorf("calls = %v, want the previous version read first, not the payload's own row", rc.calls)
	}
	if rc.renamedTo != "Want" {
		t.Errorf("renamedTo = %q, want Want from the previous version", rc.renamedTo)
	}
}

func TestRunCheck_FolderName_Keep_FallsBackToRememberedName(t *testing.T) {
	otherClient := uuid.New()
	cases := []struct {
		name    string
		history []*domain.TopicDelivery
		files   map[string][]domain.ClientFile
	}{
		{"previous torrent on another client", []*domain.TopicDelivery{{Infohash: "prev", ClientID: &otherClient}}, nil},
		{"previous torrent gone from the client", []*domain.TopicDelivery{{Infohash: "gone"}}, map[string][]domain.ClientFile{"gone": nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, rc := renamingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
			f.topic.FolderNameMode = domain.FolderNameKeep
			f.topic.FolderNameSeen = "Want"
			if tc.history != nil && tc.history[0].ClientID == nil {
				cid := f.clients.client.ID
				tc.history[0].ClientID = &cid
			}
			f.deliveries.history = tc.history
			rc.filesByHash = tc.files

			f.s.runCheck(context.Background(), f.s.log, f.topic)

			if rc.renamedTo != "Want" {
				t.Errorf("renamedTo = %q, want the remembered Want", rc.renamedTo)
			}
			if containsString(rc.calls, "Files(prev)") {
				t.Errorf("calls = %v: a torrent on another client must not be read here", rc.calls)
			}
		})
	}
}

// The retry case: the torrent is already in the client under the wanted name
// (and may be running). Nothing is renamed or rechecked.
func TestRunCheck_FolderName_SameName_SkipsRenameAndRecheck(t *testing.T) {
	f, rc := renamingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	customTopic(f, "Want")
	rc.filesSeq = [][]domain.ClientFile{renamedClient}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if want := []string{"Files", "Start"}; !reflect.DeepEqual(rc.calls, want) {
		t.Errorf("calls = %v, want %v", rc.calls, want)
	}
	if body := submittedBody(t, f); !strings.Contains(body, `Saved in folder "Want".`) {
		t.Errorf("body = %q, want the saved note", body)
	}
}

func TestRunCheck_FolderName_CannotApplyAfterAdd(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(rc *fakeRenamingClient)
		wantNote string
	}{
		{"torrent already has data", func(rc *fakeRenamingClient) { rc.bytesDone = 5 },
			`Could not use folder name "Want": the torrent already has data on disk.`},
		{"torrent already running", func(rc *fakeRenamingClient) { rc.running = true },
			`Could not use folder name "Want": the torrent is already running.`},
		{"client shows no top folder", func(rc *fakeRenamingClient) {
			rc.filesSeq = [][]domain.ClientFile{{
				{Index: 0, Path: "E01.mkv", Size: 100}, {Index: 1, Path: "E02.mkv", Size: 200},
			}}
		}, `Could not use folder name "Want": the client does not put this torrent in a folder.`},
	}
	shortWaits(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, rc := renamingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
			customTopic(f, "Want")
			tc.setup(rc)

			f.s.runCheck(context.Background(), f.s.log, f.topic)

			if containsString(rc.calls, "RenameRoot") {
				t.Errorf("calls = %v, want no rename", rc.calls)
			}
			if rc.startCalls != 1 {
				t.Errorf("start calls = %d, want the torrent started", rc.startCalls)
			}
			if body := submittedBody(t, f); !strings.Contains(body, tc.wantNote) {
				t.Errorf("body = %q, want %q", body, tc.wantNote)
			}
		})
	}
}

func TestRunCheck_FolderName_FailureLeavesPaused(t *testing.T) {
	cases := []struct {
		name  string
		setup func(rc *fakeRenamingClient)
		step  string
	}{
		{"progress read fails", func(rc *fakeRenamingClient) { rc.bytesErr = errors.New("<html>boom</html>") }, "could not read the torrent's progress"},
		{"rename fails", func(rc *fakeRenamingClient) { rc.renameErr = errors.New("<html>boom</html>") }, "renaming failed"},
		{"no file list", func(rc *fakeRenamingClient) { rc.filesErr = errors.New("<html>boom</html>") }, "could not list the files"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, rc := renamingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
			customTopic(f, "Want")
			tc.setup(rc)

			f.s.runCheck(context.Background(), f.s.log, f.topic)

			if rc.startCalls != 0 {
				t.Errorf("start calls = %d, want the torrent left paused", rc.startCalls)
			}
			body := submittedBody(t, f)
			want := "Added paused: renaming the folder failed (" + tc.step + "). Rename it in your client and start it."
			if !strings.Contains(body, want) {
				t.Errorf("body = %q, want %q", body, want)
			}
			if strings.Contains(body, "boom") {
				t.Errorf("body = %q carries the raw client error", body)
			}
			if rec := f.lastRecord(t); rec.errMsg != "" {
				t.Errorf("record errMsg = %q, want none: the delivery itself succeeded", rec.errMsg)
			}
		})
	}
}

// With only-new-files the selection runs on the list read after the rename,
// and the notes are joined policy first.
func TestRunCheck_FolderName_WithOnlyNewFiles(t *testing.T) {
	f, rc := renamingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	customTopic(f, "Want")
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	want := []string{"Files", "DiskState", "RenameRoot", "Files", "SkipFiles", "Start", "Recheck"}
	if !reflect.DeepEqual(rc.calls, want) {
		t.Errorf("calls = %v, want %v", rc.calls, want)
	}
	if !reflect.DeepEqual(rc.skipped, []int{0}) {
		t.Errorf("skipped = %v, want [0] (E01)", rc.skipped)
	}
	if body := submittedBody(t, f); !strings.Contains(body, `Downloading 1 new of 2 files. Saved in folder "Want".`) {
		t.Errorf("body = %q, want the selection note then the folder note", body)
	}
}

func TestRunCheck_FolderName_WithAddPaused_DoesNotStart(t *testing.T) {
	f, rc := renamingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	customTopic(f, "Want")
	f.topic.AddPausedOnUpdate = true

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if rc.renamedTo != "Want" || rc.startCalls != 0 {
		t.Errorf("renamedTo=%q start=%d, want renamed and left paused", rc.renamedTo, rc.startCalls)
	}
	if body := submittedBody(t, f); !strings.Contains(body, `Added paused. Saved in folder "Want".`) {
		t.Errorf("body = %q, want both notes", body)
	}
}

// Keep mode remembers the previous version's folder even when the update
// already carries that name, so the name survives the old torrent's removal.
func TestRunCheck_FolderName_Keep_RemembersNameWithoutRename(t *testing.T) {
	f, rc := renamingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.FolderNameMode = domain.FolderNameKeep
	cid := f.clients.client.ID
	f.deliveries.history = []*domain.TopicDelivery{{Infohash: "prev", ClientID: &cid, Files: v1Files}}
	rc.filesByHash = map[string][]domain.ClientFile{"prev": {{Index: 0, Path: "Show S01/E01.mkv", Size: 100}}}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if rc.lastOpts.Paused || containsString(rc.calls, "RenameRoot") {
		t.Errorf("paused=%v calls=%v, want a plain delivery", rc.lastOpts.Paused, rc.calls)
	}
	if !reflect.DeepEqual(f.topics.seenCalls, []string{"Show S01"}) {
		t.Errorf("seen = %v, want [Show S01]", f.topics.seenCalls)
	}
}

// An unchanged remembered name is not written again.
func TestRunCheck_FolderName_Keep_NoRedundantWrite(t *testing.T) {
	f, rc := renamingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.FolderNameMode = domain.FolderNameKeep
	f.topic.FolderNameSeen = "Want"

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if rc.renamedTo != "Want" || len(f.topics.seenCalls) != 0 {
		t.Errorf("renamedTo=%q seen=%v, want renamed with no write", rc.renamedTo, f.topics.seenCalls)
	}
}

// A failed rename must not abandon the only-new-files selection: the user is
// told to start the torrent by hand, and the old files must already be
// skipped by then. The selection does not start it, and its list becomes the
// next update's baseline.
func TestRunCheck_FolderName_RenameFails_StillSelectsNewFiles(t *testing.T) {
	data := torrentmetatest.Torrent("Show S01", v2Files)
	f, rc := renamingFixture(t, torrentTracker(data))
	customTopic(f, "Want")
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files
	rc.renameErr = errors.New("<html>409</html>")

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if !reflect.DeepEqual(rc.skipped, []int{0}) {
		t.Errorf("skipped = %v, want [0] (E01) even though the rename failed", rc.skipped)
	}
	if rc.startCalls != 0 {
		t.Errorf("start calls = %d, want the torrent left paused", rc.startCalls)
	}
	hash, _ := infohash.FromTorrent(data)
	if len(f.deliveries.setFilesCalls) != 1 || f.deliveries.setFilesCalls[0].infohash != hash {
		t.Errorf("setFiles = %+v, want the selected list stored as the baseline", f.deliveries.setFilesCalls)
	}
	body := submittedBody(t, f)
	if !strings.Contains(body, "Added paused with 1 new of 2 files selected.") ||
		!strings.Contains(body, "renaming the folder failed (renaming failed)") {
		t.Errorf("body = %q, want the selection note and the rename failure", body)
	}
}

// Keep mode confirms the previous torrent's folder with that torrent's own
// file list: a client set to "no subfolder" shows a torrent whose files share
// one inner folder as if it were the top folder.
func TestRunCheck_FolderName_Keep_ConfirmsPreviousRootWithItsFiles(t *testing.T) {
	f, rc := renamingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.FolderNameMode = domain.FolderNameKeep
	f.topic.FolderNameSeen = "Want"
	cid := f.clients.client.ID
	f.deliveries.history = []*domain.TopicDelivery{{
		Infohash: "prev", ClientID: &cid,
		Files: []domain.TorrentFile{{Path: "Season 1/E01.mkv", Size: 100}},
	}}
	rc.filesByHash = map[string][]domain.ClientFile{"prev": {{Index: 0, Path: "Season 1/E01.mkv", Size: 100}}}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if rc.renamedTo != "Want" {
		t.Errorf("renamedTo = %q, want the remembered Want, not the inner folder", rc.renamedTo)
	}
	if containsString(f.topics.seenCalls, "Season 1") {
		t.Errorf("seen = %v: an inner folder must not be remembered", f.topics.seenCalls)
	}
}

// The rename worked; only the start failed. The note must not say otherwise.
func TestRunCheck_FolderName_StartFails(t *testing.T) {
	f, rc := renamingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	customTopic(f, "Want")
	rc.startErr = errors.New("<html>boom</html>")

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	body := submittedBody(t, f)
	if !strings.Contains(body, `Saved in folder "Want".`) ||
		!strings.Contains(body, "Starting the torrent failed. Start it in your client.") {
		t.Errorf("body = %q, want the saved note and the start failure", body)
	}
	if strings.Contains(body, "renaming the folder failed") || strings.Contains(body, "boom") {
		t.Errorf("body = %q blames the rename or carries the raw error", body)
	}
}

// shortWaits shortens the polls so a torrent that never settles does not
// hold a test for stoppedWait.
func shortWaits(t *testing.T) {
	t.Helper()
	origPoll, origWait := filesPollInterval, stoppedWait
	filesPollInterval, stoppedWait = time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { filesPollInterval, stoppedWait = origPoll, origWait })
}

// Right after a paused add, qBittorrent and Transmission report the torrent
// as checking for up to a few seconds (measured 0.5-1.5s); the scheduler
// waits for it to settle instead of calling it running.
func TestRunCheck_FolderName_WaitsForStoppedAfterAdd(t *testing.T) {
	shortWaits(t)
	f, rc := renamingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	customTopic(f, "Want")
	rc.checkingPolls = 3

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if rc.renamedTo != "Want" {
		t.Errorf("renamedTo = %q, want Want once the torrent settled", rc.renamedTo)
	}
}

// A failed recheck comes after the start: say so, and do not blame the rename.
func TestRunCheck_FolderName_RecheckFails(t *testing.T) {
	f, rc := renamingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	customTopic(f, "Want")
	rc.recheckErr = errors.New("<html>boom</html>")

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	body := submittedBody(t, f)
	if !strings.Contains(body, "Rechecking the torrent failed. Recheck it in your client.") {
		t.Errorf("body = %q, want the recheck failure note", body)
	}
	if strings.Contains(body, "renaming the folder failed") || strings.Contains(body, "boom") {
		t.Errorf("body = %q blames the rename or carries the raw error", body)
	}
}

// With add-paused the torrent is rechecked but not started.
func TestRunCheck_FolderName_WithAddPaused_RechecksWithoutStart(t *testing.T) {
	f, rc := renamingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	customTopic(f, "Want")
	f.topic.AddPausedOnUpdate = true

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if want := []string{"Files", "DiskState", "RenameRoot", "Files", "Recheck"}; !reflect.DeepEqual(rc.calls, want) {
		t.Errorf("calls = %v, want %v", rc.calls, want)
	}
}

// Without the previous delivery's file list (a magnet, a row from before
// migration 0017) keep mode cannot tell the folder from an inner folder, so it
// does not learn one from the client (PR #226 review).
func TestRunCheck_FolderName_Keep_NoStoredListUsesRememberedName(t *testing.T) {
	f, rc := renamingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.FolderNameMode = domain.FolderNameKeep
	f.topic.FolderNameSeen = "Want"
	cid := f.clients.client.ID
	f.deliveries.history = []*domain.TopicDelivery{{Infohash: "prev", ClientID: &cid}}
	rc.filesByHash = map[string][]domain.ClientFile{"prev": {{Index: 0, Path: "Season 1/E01.mkv", Size: 100}}}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if rc.renamedTo != "Want" || len(f.topics.seenCalls) != 0 {
		t.Errorf("renamedTo=%q seen=%v, want the remembered Want and nothing learned", rc.renamedTo, f.topics.seenCalls)
	}
}

// A torrent above the 5000-file storage cap has no stored list, but the
// rename still checks the client's folder against the torrent's own list: a
// "no subfolder" client showing every file under Extras/ is not renamed
// (PR #226 review).
func TestRunCheck_FolderName_LargeTorrentChecksItsOwnList(t *testing.T) {
	manifest := make([]domain.TorrentFile, maxStoredFiles+1)
	client := make([]domain.ClientFile, len(manifest))
	for i := range manifest {
		p := fmt.Sprintf("Extras/f%05d.bin", i)
		manifest[i] = domain.TorrentFile{Path: p, Size: 1}
		client[i] = domain.ClientFile{Index: i, Path: p, Size: 1}
	}
	f, rc := renamingFixture(t, torrentTracker(torrentmetatest.Torrent("Pack", manifest)))
	customTopic(f, "Want")
	rc.filesSeq = [][]domain.ClientFile{client}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if containsString(rc.calls, "RenameRoot") {
		t.Errorf("renamed %q -> %q: an inner folder must never be renamed", rc.renamedFrom, rc.renamedTo)
	}
	if body := submittedBody(t, f); !strings.Contains(body, `Could not use folder name "Want"`) {
		t.Errorf("body = %q, want the cannot-apply note", body)
	}
}

func TestJoinNotes(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"", ""}, ""},
		{[]string{"A.", ""}, "A."},
		{[]string{"", "B."}, "B."},
		{[]string{"A.", "B."}, "A. B."},
	}
	for _, tc := range cases {
		if got := joinNotes(tc.in...); got != tc.want {
			t.Errorf("joinNotes(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
