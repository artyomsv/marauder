package scheduler

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/events"
	"github.com/artyomsv/marauder/backend/internal/infohash"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/torrentmeta/torrentmetatest"
)

var (
	v1Files = []domain.TorrentFile{{Path: "E01.mkv", Size: 100}}
	v2Files = []domain.TorrentFile{{Path: "E01.mkv", Size: 100}, {Path: "E02.mkv", Size: 200}}
	// How a client lists v2: its own top folder, its own ids.
	v2Client = []domain.ClientFile{
		{Index: 0, Path: "Show S01/E01.mkv", Size: 100, Wanted: true},
		{Index: 1, Path: "Show S01/E02.mkv", Size: 200, Wanted: true},
	}
)

// torrentTracker is a single-release tracker whose update is the given .torrent.
func torrentTracker(data []byte) *fakeTracker {
	return &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{check: &domain.Check{Hash: "new-hash", Extra: map[string]any{}}},
		},
		downloads: []downloadResult{{payload: &domain.Payload{TorrentFile: data, FileName: "x.torrent"}}},
	}
}

// selectingFixture swaps the fixture's client plugin for one with file selection.
func selectingFixture(t *testing.T, tr *fakeTracker) (*fixture, *fakeSelectingClient) {
	t.Helper()
	f := newFixture(t, tr)
	sel := &fakeSelectingClient{fakeClientPlugin: fakeClientPlugin{name: "fakeclient"}}
	f.s.lookupClient = func(string) registry.Client { return sel }
	return f, sel
}

func submittedBody(t *testing.T, f *fixture) string {
	t.Helper()
	for _, ev := range f.emitter.events {
		if ev.Type == events.DownloadSubmitted {
			return ev.Body
		}
	}
	t.Fatal("no download.submitted event")
	return ""
}

func TestRunCheck_FirstDelivery_NotPausedButFilesRecorded(t *testing.T) {
	data := torrentmetatest.Torrent("Show S01", v1Files)
	f, sel := selectingFixture(t, torrentTracker(data))
	f.topic.LastHash = ""
	f.topic.AddPausedOnUpdate = true
	f.topic.OnlyNewFiles = true

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if sel.lastOpts.Paused {
		t.Error("first delivery added paused, want normal start")
	}
	if sel.skipCalls != 0 || sel.startCalls != 0 {
		t.Errorf("selection ran on a first delivery (skip=%d start=%d)", sel.skipCalls, sel.startCalls)
	}
	if len(f.deliveries.recorded) != 1 || !reflect.DeepEqual(f.deliveries.recorded[0].Files, v1Files) {
		t.Errorf("recorded = %+v, want one delivery carrying v1's files", f.deliveries.recorded)
	}
}

func TestRunCheck_AddPausedOnUpdate_Only(t *testing.T) {
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.AddPausedOnUpdate = true

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if !sel.lastOpts.Paused {
		t.Error("update not added paused")
	}
	if sel.filesCalls != 0 || sel.startCalls != 0 {
		t.Error("add-paused alone must not touch file selection or start")
	}
	if body := submittedBody(t, f); !strings.Contains(body, "Added paused") {
		t.Errorf("body = %q, want the paused note", body)
	}
}

func TestRunCheck_OnlyNewFiles_SkipsOldAndStarts(t *testing.T) {
	data := torrentmetatest.Torrent("Show S01", v2Files)
	f, sel := selectingFixture(t, torrentTracker(data))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files
	sel.filesSeq = [][]domain.ClientFile{v2Client}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if !sel.lastOpts.Paused {
		t.Error("only-new-files update must be added paused before selection")
	}
	if !reflect.DeepEqual(sel.skipped, []int{0}) {
		t.Errorf("skipped = %v, want [0] (E01)", sel.skipped)
	}
	wantHash, _ := infohash.FromTorrent(data)
	if sel.startCalls != 1 || sel.startedHash != wantHash {
		t.Errorf("start calls = %d hash = %q, want 1 call for %q", sel.startCalls, sel.startedHash, wantHash)
	}
	if body := submittedBody(t, f); !strings.Contains(body, "Downloading 1 new of 2 files") {
		t.Errorf("body = %q, want the selection note", body)
	}
	if rec := f.lastRecord(t); !rec.updated || rec.errMsg != "" {
		t.Errorf("record = %+v, want a clean updated check", rec)
	}
}

func TestRunCheck_OnlyNewFiles_WithAddPaused_DoesNotStart(t *testing.T) {
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true
	f.topic.AddPausedOnUpdate = true
	f.deliveries.latestFiles = v1Files
	sel.filesSeq = [][]domain.ClientFile{v2Client}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if sel.skipCalls != 1 || sel.startCalls != 0 {
		t.Errorf("skip=%d start=%d, want skip once and no start", sel.skipCalls, sel.startCalls)
	}
}

func TestRunCheck_OnlyNewFiles_RenamedRootStillSkipsOld(t *testing.T) {
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01E01-02", v2Files)))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files
	sel.filesSeq = [][]domain.ClientFile{{
		{Index: 0, Path: "Show S01E01-02/E01.mkv", Size: 100},
		{Index: 1, Path: "Show S01E01-02/E02.mkv", Size: 200},
	}}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if !reflect.DeepEqual(sel.skipped, []int{0}) || sel.startCalls != 1 {
		t.Errorf("skipped=%v start=%d, want [0] and started", sel.skipped, sel.startCalls)
	}
}

func TestRunCheck_OnlyNewFiles_PollsUntilClientListsFiles(t *testing.T) {
	orig := filesPollInterval
	filesPollInterval = time.Millisecond
	t.Cleanup(func() { filesPollInterval = orig })

	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files
	sel.filesSeq = [][]domain.ClientFile{nil, nil, v2Client}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if sel.filesCalls != 3 || sel.startCalls != 1 {
		t.Errorf("files calls = %d start = %d, want 3 polls then a start", sel.filesCalls, sel.startCalls)
	}
}

func TestRunCheck_OnlyNewFiles_PartialMatchStaysPaused(t *testing.T) {
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files
	// The client shows E01 with another size: the old file cannot be found.
	sel.filesSeq = [][]domain.ClientFile{{
		{Index: 0, Path: "Show S01/E01.mkv", Size: 999},
		{Index: 1, Path: "Show S01/E02.mkv", Size: 200},
	}}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if sel.skipCalls != 0 || sel.startCalls != 0 {
		t.Errorf("skip=%d start=%d, want neither: the torrent must stay paused", sel.skipCalls, sel.startCalls)
	}
	if body := submittedBody(t, f); !strings.Contains(body, "Added paused") {
		t.Errorf("body = %q, want a paused note", body)
	}
	if rec := f.lastRecord(t); rec.errMsg != "" {
		t.Errorf("record errMsg = %q, want none: the delivery itself succeeded", rec.errMsg)
	}
}

func TestRunCheck_OnlyNewFiles_NoBaselineStaysPaused(t *testing.T) {
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if !sel.lastOpts.Paused || sel.filesCalls != 0 || sel.startCalls != 0 {
		t.Errorf("paused=%v files=%d start=%d, want paused and untouched", sel.lastOpts.Paused, sel.filesCalls, sel.startCalls)
	}
	if body := submittedBody(t, f); !strings.Contains(body, "no earlier file list") {
		t.Errorf("body = %q, want the no-baseline note", body)
	}
}

func TestRunCheck_OnlyNewFiles_MagnetStaysPaused(t *testing.T) {
	tr := singlePayloadTracker("c12fe1c06bba254a9dc9f519b335aa7c1367a88a")
	f, sel := selectingFixture(t, tr)
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if !sel.lastOpts.Paused || sel.filesCalls != 0 {
		t.Errorf("paused=%v files=%d, want paused without selection", sel.lastOpts.Paused, sel.filesCalls)
	}
	if body := submittedBody(t, f); !strings.Contains(body, "magnet") {
		t.Errorf("body = %q, want the magnet note", body)
	}
}

func TestRunCheck_OnlyNewFiles_UnsupportedClientWarns(t *testing.T) {
	f := newFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if f.clientPlugin.addCalls != 1 {
		t.Fatalf("add calls = %d, want 1", f.clientPlugin.addCalls)
	}
	if body := submittedBody(t, f); !strings.Contains(body, "cannot pause or select files") {
		t.Errorf("body = %q, want the unsupported note", body)
	}
}

// A client that cannot pause starts the torrent whatever Paused says, so the
// notification must not claim the update was added paused.
func TestRunCheck_AddPausedOnUpdate_UnsupportedClient_NoPausedClaim(t *testing.T) {
	f := newFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.AddPausedOnUpdate = true

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if f.clientPlugin.addCalls != 1 {
		t.Fatalf("add calls = %d, want 1", f.clientPlugin.addCalls)
	}
	body := submittedBody(t, f)
	if strings.Contains(body, "paused") {
		t.Errorf("body = %q, want no paused claim for a client that cannot pause", body)
	}
	// The user asked for paused updates: say that it could not be honoured.
	if !strings.Contains(body, "This client cannot pause, so the update started.") {
		t.Errorf("body = %q, want the cannot-pause note", body)
	}
}

// An update with no new file is left paused with every file skipped, so a
// user who presses Start does not re-download the whole pack.
func TestRunCheck_OnlyNewFiles_NoNewFilesSkipsAllAndStaysPaused(t *testing.T) {
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v2Files
	sel.filesSeq = [][]domain.ClientFile{v2Client}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if !sel.lastOpts.Paused {
		t.Error("no-new-files update not added paused")
	}
	if !reflect.DeepEqual(sel.skipped, []int{0, 1}) || sel.startCalls != 0 {
		t.Errorf("skipped=%v start=%d, want every file skipped and no start", sel.skipped, sel.startCalls)
	}
	body := submittedBody(t, f)
	if !strings.Contains(body, "Added paused: this update has no new files; all its files are skipped.") {
		t.Errorf("body = %q, want the no-new-files note", body)
	}
	if rec := f.lastRecord(t); !rec.updated || rec.errMsg != "" {
		t.Errorf("record = %+v, want a clean updated check", rec)
	}
}

// The same safety rule as a normal selection: a file the client did not
// show cannot be skipped, so the result is a failure note, not a claim that
// every file is skipped.
func TestRunCheck_OnlyNewFiles_NoNewFilesPartialMatchFails(t *testing.T) {
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v2Files
	sel.filesSeq = [][]domain.ClientFile{{
		{Index: 0, Path: "Show S01/E01.mkv", Size: 100},
		{Index: 1, Path: "Show S01/E02.mkv", Size: 999},
	}}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if sel.skipCalls != 0 || sel.startCalls != 0 {
		t.Errorf("skip=%d start=%d, want neither", sel.skipCalls, sel.startCalls)
	}
	if body := submittedBody(t, f); !strings.Contains(body, "the client listed 1 of 2 old files") {
		t.Errorf("body = %q, want the failed step named", body)
	}
}

func TestRunCheck_OnlyNewFiles_SkipErrorStaysPaused(t *testing.T) {
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files
	sel.filesSeq = [][]domain.ClientFile{v2Client}
	// Shaped like qbitStatusError: an HTML body must never reach a notifier.
	sel.skipErr = errors.New("setFilePrio -> 500: <html>internal failure</html>")

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if sel.startCalls != 0 {
		t.Error("started after a failed skip")
	}
	if rec := f.lastRecord(t); !rec.updated || rec.errMsg != "" {
		t.Errorf("record = %+v, want a clean updated check", rec)
	}
	body := submittedBody(t, f)
	if strings.Contains(body, "<html>") || strings.Contains(body, "setFilePrio") {
		t.Errorf("body = %q leaks the raw client error", body)
	}
	if !strings.Contains(body, "skipping the old files failed") {
		t.Errorf("body = %q, want the failed step named", body)
	}
}

// With a baseline that shares no file with the update there is nothing to
// skip, but Start must still wait until the client knows the torrent: a
// qBittorrent add is asynchronous, and a Start sent too early is lost.
func TestRunCheck_OnlyNewFiles_DisjointBaselineWaitsBeforeStart(t *testing.T) {
	orig := filesPollInterval
	filesPollInterval = time.Millisecond
	t.Cleanup(func() { filesPollInterval = orig })

	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = []domain.TorrentFile{{Path: "Old.mkv", Size: 1}}
	sel.filesSeq = [][]domain.ClientFile{nil, v2Client}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if sel.filesCalls != 2 || sel.skipCalls != 0 || sel.startCalls != 1 {
		t.Errorf("files=%d skip=%d start=%d, want 2 polls, no skip, one start",
			sel.filesCalls, sel.skipCalls, sel.startCalls)
	}
}

// The delivery row (the next update's baseline) is written before file
// selection starts, so a shutdown during the selection wait cannot lose it.
func TestRunCheck_OnlyNewFiles_RecordsDeliveryBeforeSelection(t *testing.T) {
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files
	sel.filesSeq = [][]domain.ClientFile{v2Client}
	recordedAtSelection := -1
	sel.onFiles = func() {
		if recordedAtSelection < 0 {
			recordedAtSelection = len(f.deliveries.recorded)
		}
	}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if recordedAtSelection != 1 {
		t.Errorf("deliveries recorded when selection began = %d, want 1", recordedAtSelection)
	}
}

// A tick that delivered v2 but whose check result was then discarded (a
// shutdown mid-selection, a stale check-state token) leaves last_hash on v1,
// so the next tick delivers v2 again — and v2's own row is already recorded.
// Comparing v2 with itself would skip every file; the baseline must be the
// newest row of ANOTHER infohash.
func TestRunCheck_OnlyNewFiles_RetryIgnoresOwnDeliveryRow(t *testing.T) {
	data := torrentmetatest.Torrent("Show S01", v2Files)
	hash, err := infohash.FromTorrent(data)
	if err != nil {
		t.Fatalf("infohash: %v", err)
	}
	f, sel := selectingFixture(t, torrentTracker(data))
	f.topic.OnlyNewFiles = true
	f.deliveries.history = []*domain.TopicDelivery{
		{Infohash: "v1-hash", Files: v1Files},
		{Infohash: hash, Files: v2Files},
	}
	sel.filesSeq = [][]domain.ClientFile{v2Client}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if !reflect.DeepEqual(sel.skipped, []int{0}) || sel.startCalls != 1 {
		t.Errorf("skipped=%v start=%d, want only v1's E01 skipped and a start", sel.skipped, sel.startCalls)
	}
	if body := submittedBody(t, f); !strings.Contains(body, "Downloading 1 new of 2 files") {
		t.Errorf("body = %q, want the selection note", body)
	}
}

// v1 (.torrent) -> v2 (magnet: no file list; the user picked files by hand)
// -> v3 (.torrent). v2's files are unknown, so comparing v3 with v1 would
// re-download everything v2 added: v3 must arrive paused for the user.
func TestRunCheck_OnlyNewFiles_MagnetInBetweenMeansNoBaseline(t *testing.T) {
	v3Files := append(append([]domain.TorrentFile{}, v2Files...), domain.TorrentFile{Path: "E03.mkv", Size: 300})
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v3Files)))
	f.topic.OnlyNewFiles = true
	f.deliveries.history = []*domain.TopicDelivery{
		{Infohash: "v1-hash", Files: v1Files},
		{Infohash: "v2-magnet-hash"},
	}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if !sel.lastOpts.Paused || sel.filesCalls != 0 || sel.skipCalls != 0 || sel.startCalls != 0 {
		t.Errorf("paused=%v files=%d skip=%d start=%d, want paused and untouched",
			sel.lastOpts.Paused, sel.filesCalls, sel.skipCalls, sel.startCalls)
	}
	if body := submittedBody(t, f); !strings.Contains(body, "no earlier file list") {
		t.Errorf("body = %q, want the no-baseline note", body)
	}
}

func TestRunCheck_UpdatePolicy_IgnoredForEpisodicTracker(t *testing.T) {
	tr := torrentTracker(torrentmetatest.Torrent("Show S01", v2Files))
	tr.episodic = true
	f, sel := selectingFixture(t, tr)
	f.topic.OnlyNewFiles = true
	f.topic.AddPausedOnUpdate = true
	f.deliveries.latestFiles = v1Files

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if sel.lastOpts.Paused || sel.filesCalls != 0 {
		t.Errorf("paused=%v files=%d, want the plain delivery", sel.lastOpts.Paused, sel.filesCalls)
	}
}
