package scheduler

import (
	"context"
	"errors"
	"fmt"
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

func TestRunCheck_OnlyNewFiles_LayoutMismatchStaysPaused(t *testing.T) {
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

// The same safety rule as a normal selection: a client list that does not
// pair one to one with the torrent cannot be trusted to name the old files,
// so the result is a failure note, not a claim that every file is skipped.
func TestRunCheck_OnlyNewFiles_NoNewFilesLayoutMismatchFails(t *testing.T) {
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
	if body := submittedBody(t, f); !strings.Contains(body, "the client's file list does not match the torrent") {
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

// Finding A (PR #210 review): a new file whose path equals an old file's path
// after dropping its first folder must not be skipped as that old file. The
// client lists this torrent without a top folder, so only the verbatim layout
// fits and Extras/E01.mkv is a new file.
func TestRunCheck_OnlyNewFiles_RootlessLayoutKeepsLookalikeNewFile(t *testing.T) {
	next := []domain.TorrentFile{{Path: "E01.mkv", Size: 100}, {Path: "Extras/E01.mkv", Size: 100}}
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", next)))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = []domain.TorrentFile{{Path: "E01.mkv", Size: 100}}
	sel.filesSeq = [][]domain.ClientFile{{
		{Index: 0, Path: "E01.mkv", Size: 100, Wanted: true},
		{Index: 1, Path: "Extras/E01.mkv", Size: 100, Wanted: true},
	}}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if !reflect.DeepEqual(sel.skipped, []int{0}) || sel.startCalls != 1 {
		t.Errorf("skipped=%v start=%d, want only the old E01.mkv (index 0) skipped and a start", sel.skipped, sel.startCalls)
	}
	if body := submittedBody(t, f); !strings.Contains(body, "Downloading 1 new of 2 files") {
		t.Errorf("body = %q, want the selection note", body)
	}
}

// The rooted twin: the torrent's top folder is "Show", the old version had a
// subfolder "Show" of its own, and the new version adds a same-size E01.mkv at
// the top. Every client path carries the root, so only the rooted layout fits.
func TestRunCheck_OnlyNewFiles_RootedLayoutKeepsLookalikeNewFile(t *testing.T) {
	next := []domain.TorrentFile{{Path: "Show/E01.mkv", Size: 100}, {Path: "E01.mkv", Size: 100}}
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show", next)))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = []domain.TorrentFile{{Path: "Show/E01.mkv", Size: 100}}
	sel.filesSeq = [][]domain.ClientFile{{
		{Index: 0, Path: "Show/Show/E01.mkv", Size: 100, Wanted: true},
		{Index: 1, Path: "Show/E01.mkv", Size: 100, Wanted: true},
	}}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if !reflect.DeepEqual(sel.skipped, []int{0}) || sel.startCalls != 1 {
		t.Errorf("skipped=%v start=%d, want only the old Show/E01.mkv (index 0) skipped and a start", sel.skipped, sel.startCalls)
	}
	if body := submittedBody(t, f); !strings.Contains(body, "Downloading 1 new of 2 files") {
		t.Errorf("body = %q, want the selection note", body)
	}
}

// --- Finding B (PR #210 review): when a delivery's file list is stored ------
//
// A delivery's files mean "the version Marauder offered the user". Stored at
// record time for every delivery without a selection (plain, add-paused only,
// paused fallbacks); for a selection only after it succeeds, so only a failed
// selection leaves no list.

func lastSubmittedBody(t *testing.T, f *fixture) string {
	t.Helper()
	body := ""
	for _, ev := range f.emitter.events {
		if ev.Type == events.DownloadSubmitted {
			body = ev.Body
		}
	}
	if body == "" {
		t.Fatal("no download.submitted event")
	}
	return body
}

func TestRunCheck_OnlyNewFiles_FailedSelectionStoresNoFiles(t *testing.T) {
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files
	sel.filesSeq = [][]domain.ClientFile{v2Client}
	sel.skipErr = errors.New("skip refused")

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if len(f.deliveries.recorded) != 1 || f.deliveries.recorded[0].Files != nil {
		t.Errorf("recorded = %+v, want one delivery with no file list", f.deliveries.recorded)
	}
	if len(f.deliveries.setFilesCalls) != 0 {
		t.Errorf("SetFiles calls = %+v, want none after a failed selection", f.deliveries.setFilesCalls)
	}
}

func TestRunCheck_OnlyNewFiles_SuccessfulSelectionStoresFilesAfter(t *testing.T) {
	data := torrentmetatest.Torrent("Show S01", v2Files)
	hash, err := infohash.FromTorrent(data)
	if err != nil {
		t.Fatalf("infohash: %v", err)
	}
	f, sel := selectingFixture(t, torrentTracker(data))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files
	sel.filesSeq = [][]domain.ClientFile{v2Client}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if sel.startCalls != 1 {
		t.Fatalf("start calls = %d, want 1", sel.startCalls)
	}
	if len(f.deliveries.filesAtRecord) != 1 || f.deliveries.filesAtRecord[0] != nil {
		t.Errorf("files at record = %+v, want nil: selection had not run yet", f.deliveries.filesAtRecord)
	}
	want := []setFilesCall{{infohash: hash, files: v2Files}}
	if !reflect.DeepEqual(f.deliveries.setFilesCalls, want) {
		t.Errorf("SetFiles calls = %+v, want %+v (the full manifest)", f.deliveries.setFilesCalls, want)
	}
}

// Kept paused by add-paused, but the selection was applied: the old files
// are skipped and the new ones wanted, so the list is a trustworthy baseline.
func TestRunCheck_OnlyNewFiles_WithAddPaused_StoresFilesAfterSelection(t *testing.T) {
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true
	f.topic.AddPausedOnUpdate = true
	f.deliveries.latestFiles = v1Files
	sel.filesSeq = [][]domain.ClientFile{v2Client}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if len(f.deliveries.setFilesCalls) != 1 || !reflect.DeepEqual(f.deliveries.setFilesCalls[0].files, v2Files) {
		t.Errorf("SetFiles calls = %+v, want one with v2's files", f.deliveries.setFilesCalls)
	}
}

func TestRunCheck_OnlyNewFiles_NoNewFiles_StoresFilesAfterSkippingAll(t *testing.T) {
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v2Files
	sel.filesSeq = [][]domain.ClientFile{v2Client}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if len(f.deliveries.setFilesCalls) != 1 || !reflect.DeepEqual(f.deliveries.setFilesCalls[0].files, v2Files) {
		t.Errorf("SetFiles calls = %+v, want one with v2's files", f.deliveries.setFilesCalls)
	}
}

// A failed SetFiles is logged, not a failed check: the torrent is in the
// client and selected. The next update then finds no baseline and is paused.
func TestRunCheck_OnlyNewFiles_SetFilesErrorDoesNotFailCheck(t *testing.T) {
	f, sel := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files
	f.deliveries.setFilesErr = errors.New("db down")
	sel.filesSeq = [][]domain.ClientFile{v2Client}

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if len(f.deliveries.setFilesCalls) != 1 {
		t.Errorf("SetFiles calls = %d, want 1", len(f.deliveries.setFilesCalls))
	}
	if rec := f.lastRecord(t); !rec.updated || rec.errMsg != "" {
		t.Errorf("record = %+v, want a clean updated check", rec)
	}
	if body := submittedBody(t, f); !strings.Contains(body, "Downloading 1 new of 2 files") {
		t.Errorf("body = %q, want the selection note", body)
	}
}

// Paused for the user to pick files by hand: they saw every file of this
// version and chose, so its list is stored at record time and the next
// update compares with it.
func TestRunCheck_AddPausedOnUpdate_Only_StoresFilesAtRecord(t *testing.T) {
	f, _ := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.AddPausedOnUpdate = true

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if len(f.deliveries.filesAtRecord) != 1 || !reflect.DeepEqual(f.deliveries.filesAtRecord[0], v2Files) {
		t.Errorf("files at record = %+v, want v2's files", f.deliveries.filesAtRecord)
	}
	if len(f.deliveries.setFilesCalls) != 0 {
		t.Errorf("SetFiles calls = %+v, want none", f.deliveries.setFilesCalls)
	}
}

// The no-baseline fallback is paused for hand-picking too: its list is stored
// at record time, so the update after it has a baseline again.
func TestRunCheck_OnlyNewFiles_NoBaseline_StoresFilesAtRecord(t *testing.T) {
	f, _ := selectingFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if len(f.deliveries.filesAtRecord) != 1 || !reflect.DeepEqual(f.deliveries.filesAtRecord[0], v2Files) {
		t.Errorf("files at record = %+v, want v2's files", f.deliveries.filesAtRecord)
	}
}

// A client that cannot pause downloads everything: a plain delivery, so its
// list is stored at record time.
func TestRunCheck_OnlyNewFiles_UnsupportedClient_StoresFilesAtRecord(t *testing.T) {
	f := newFixture(t, torrentTracker(torrentmetatest.Torrent("Show S01", v2Files)))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if len(f.deliveries.filesAtRecord) != 1 || !reflect.DeepEqual(f.deliveries.filesAtRecord[0], v2Files) {
		t.Errorf("files at record = %+v, want v2's files", f.deliveries.filesAtRecord)
	}
}

// seqTracker delivers one .torrent per update, in order, each under its own
// check hash.
func seqTracker(versions ...[]domain.TorrentFile) *fakeTracker {
	tr := &fakeTracker{name: "faketracker"}
	for i, files := range versions {
		tr.checks = append(tr.checks, checkResult{check: &domain.Check{Hash: fmt.Sprintf("v%d-hash", i+2), Extra: map[string]any{}}})
		tr.downloads = append(tr.downloads, downloadResult{payload: &domain.Payload{
			TorrentFile: torrentmetatest.Torrent("Show S01", files), FileName: fmt.Sprintf("v%d.torrent", i+2),
		}})
	}
	return tr
}

// rootedClient lists files the way the fixtures' client does: under the
// torrent's top folder, numbered by position.
func rootedClient(files []domain.TorrentFile) []domain.ClientFile {
	out := make([]domain.ClientFile, len(files))
	for i, f := range files {
		out[i] = domain.ClientFile{Index: i, Path: "Show S01/" + f.Path, Size: f.Size, Wanted: true}
	}
	return out
}

// Update 1 fails its selection (Marauder's own failure), so its row has no
// list and update 2 arrives paused once. Update 2 is offered to the user whole
// and stores its list, so update 3 selects normally against it: the policy
// does not stay paused after one failure.
func TestRunCheck_OnlyNewFiles_FailedSelectionPausesOnlyTheNextUpdate(t *testing.T) {
	v3Files := append(append([]domain.TorrentFile{}, v2Files...), domain.TorrentFile{Path: "E03.mkv", Size: 300})
	v4Files := append(append([]domain.TorrentFile{}, v3Files...), domain.TorrentFile{Path: "E04.mkv", Size: 400})
	f, sel := selectingFixture(t, seqTracker(v2Files, v3Files, v4Files))
	f.topic.OnlyNewFiles = true
	f.deliveries.latestFiles = v1Files

	// Update 1 (v2): the skip fails; the row keeps no list.
	sel.filesSeq = [][]domain.ClientFile{rootedClient(v2Files)}
	sel.skipErr = errors.New("skip refused")
	f.s.runCheck(context.Background(), f.s.log, f.topic)
	if body := lastSubmittedBody(t, f); !strings.Contains(body, "skipping the old files failed") {
		t.Fatalf("update 1 body = %q, want the failed selection", body)
	}
	if f.deliveries.recorded[0].Files != nil || len(f.deliveries.setFilesCalls) != 0 {
		t.Fatalf("update 1 row files = %+v, SetFiles = %+v; want NULL and no SetFiles",
			f.deliveries.recorded[0].Files, f.deliveries.setFilesCalls)
	}

	// Update 2 (v3): no baseline, added paused, but its list is stored.
	sel.skipErr = nil
	f.topic.LastHash = "v2-hash"
	f.s.runCheck(context.Background(), f.s.log, f.topic)
	if body := lastSubmittedBody(t, f); !strings.Contains(body, "no earlier file list") {
		t.Fatalf("update 2 body = %q, want the no-baseline note", body)
	}
	if !sel.lastOpts.Paused || sel.skipCalls != 1 || sel.startCalls != 0 {
		t.Fatalf("update 2 paused=%v skip=%d start=%d, want paused and untouched", sel.lastOpts.Paused, sel.skipCalls, sel.startCalls)
	}
	if !reflect.DeepEqual(f.deliveries.filesAtRecord[1], v3Files) {
		t.Fatalf("update 2 files at record = %+v, want v3's files", f.deliveries.filesAtRecord[1])
	}

	// Update 3 (v4): compared with v3, so only E04 downloads.
	sel.filesSeq = [][]domain.ClientFile{rootedClient(v4Files)}
	f.topic.LastHash = "v3-hash"
	f.s.runCheck(context.Background(), f.s.log, f.topic)
	if !reflect.DeepEqual(sel.skipped, []int{0, 1, 2}) || sel.startCalls != 1 {
		t.Errorf("update 3 skipped=%v start=%d, want E01-E03 skipped and a start", sel.skipped, sel.startCalls)
	}
	if body := lastSubmittedBody(t, f); !strings.Contains(body, "Downloading 1 new of 4 files") {
		t.Errorf("update 3 body = %q, want the selection note", body)
	}
}

// An update the user finished by hand (add-paused only) is a baseline: when
// only-new-files is then turned on, the next update selects against it.
func TestRunCheck_AddPausedOnlyUpdate_IsNextUpdatesBaseline(t *testing.T) {
	v3Files := append(append([]domain.TorrentFile{}, v2Files...), domain.TorrentFile{Path: "E03.mkv", Size: 300})
	f, sel := selectingFixture(t, seqTracker(v2Files, v3Files))
	f.topic.AddPausedOnUpdate = true

	f.s.runCheck(context.Background(), f.s.log, f.topic)
	if !sel.lastOpts.Paused || sel.filesCalls != 0 {
		t.Fatalf("update 1 paused=%v files=%d, want paused and untouched", sel.lastOpts.Paused, sel.filesCalls)
	}

	f.topic.AddPausedOnUpdate = false
	f.topic.OnlyNewFiles = true
	f.topic.LastHash = "v2-hash"
	sel.filesSeq = [][]domain.ClientFile{rootedClient(v3Files)}
	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if !reflect.DeepEqual(sel.skipped, []int{0, 1}) || sel.startCalls != 1 {
		t.Errorf("update 2 skipped=%v start=%d, want v2's E01-E02 skipped and a start", sel.skipped, sel.startCalls)
	}
	if body := lastSubmittedBody(t, f); !strings.Contains(body, "Downloading 1 new of 3 files") {
		t.Errorf("update 2 body = %q, want the selection note", body)
	}
}
