package scheduler

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/infohash"
	"github.com/artyomsv/marauder/backend/internal/metrics"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/torrentmeta"
)

// Folder name (issue #225): give the torrent's top folder in the client the
// name the topic asks for, on the first delivery and on every update, so an
// update lands in the folder that already holds the previous version. The
// torrent is added paused, renamed, and rechecked so the client finds the
// data already there. The rules match the update policy's: a step that fails
// leaves the torrent paused for the user and never fails the check, and a
// rename never runs on a torrent that has written data (the rename is a
// per-file move that could overwrite the previous version's files).

const (
	// folderSelectionTimeout bounds every client call after Add when a folder
	// is renamed: the wait for the file list, the rename, the recheck and the
	// only-new-files selection, which keeps its own shorter budget inside it.
	folderSelectionTimeout = 30 * time.Second
	// folderReadTimeout bounds keep mode's read of the previous torrent.
	folderReadTimeout = 10 * time.Second
)

// stoppedWait is how long a just-added torrent may report something other
// than stopped before it is treated as running: right after a paused add,
// qBittorrent and Transmission 4.0 report it checking for 0.5-1.5s (measured
// 2026-10-23). A var so tests can shorten it.
var stoppedWait = 10 * time.Second

// renameWait is how long the client may take to list the files under the new
// folder name after accepting a rename. A var so tests can shorten it.
var renameWait = 10 * time.Second

// Folder rename metric results (marauder_scheduler_folder_rename_total).
const (
	folderRenamed     = "renamed"
	folderSameName    = "same_name"
	folderMagnet      = "magnet"
	folderSingleFile  = "single_file"
	folderUnreadable  = "unreadable"
	folderNoRoot      = "no_root"
	folderHasData     = "has_data"
	folderRunning     = "running"
	folderUnsupported = "unsupported"
	folderFailed      = "failed"
)

// folderNameSeenSetter is an optional capability of topicsRepo: it stores
// keep mode's memory of the folder name. Best-effort.
type folderNameSeenSetter interface {
	SetFolderNameSeen(ctx context.Context, t *domain.Topic, name string) error
}

// folderPlan says how one delivery's folder is named. A plan with a result
// set could not apply and carries the note to send; otherwise the torrent is
// added paused and renamed after Add.
type folderPlan struct {
	renamer  registry.WithRename
	selector registry.WithFileSelection
	hash     string
	// manifest is the torrent's own file list; it confirms the client's top
	// folder (torrentmeta.ClientRoot), which needs it.
	manifest []domain.TorrentFile
	want     string
	// recheck is set once the folder carries the wanted name and its data is
	// unverified (a confirmed rename, or a retry that finds it renamed and
	// stopped); finishDelivery then rechecks the torrent as its last step.
	recheck bool
	result  string
	note    string
}

// renames reports whether the plan renames after Add.
func (fp *folderPlan) renames() bool { return fp != nil && fp.result == "" }

// planFolderName decides the folder name for one payload. nil means nothing
// to do: the default mode, a per-episode tracker, no name known in keep mode,
// or a torrent whose folder already has the name. files is the payload's
// file list from planDelivery (nil when unknown).
func (s *Scheduler) planFolderName(ctx context.Context, log zerolog.Logger, t *domain.Topic, episodic bool, cfg *domain.Client, rawConfig []byte, clientPlugin registry.Client, payload *domain.Payload, files []domain.TorrentFile) *folderPlan {
	if episodic || t.NotifyOnly {
		return nil
	}
	hash, _ := infohash.FromPayload(payload.MagnetURI, payload.TorrentFile)
	var want string
	switch t.FolderNameMode {
	case domain.FolderNameCustom:
		want = t.FolderName
	case domain.FolderNameKeep:
		want = s.keepFolderName(ctx, log, t, cfg, rawConfig, clientPlugin, hash)
	}
	if want == "" {
		return nil
	}
	cannot := func(result, reason string) *folderPlan {
		return &folderPlan{want: want, result: result, note: cannotNote(want, reason)}
	}
	if payload.MagnetURI != "" || len(payload.TorrentFile) == 0 {
		return cannot(folderMagnet, "a magnet link has no file list")
	}
	name, multiFile, err := torrentmeta.RootName(payload.TorrentFile)
	switch {
	case err != nil:
		log.Debug().Err(err).Msg("folder name: could not read the torrent's name")
		return cannot(folderUnreadable, "could not read the torrent")
	case !multiFile:
		return cannot(folderSingleFile, "the torrent is a single file")
	case name == want:
		s.rememberFolderName(ctx, log, t, want)
		return nil
	}
	// The rename needs the torrent's own file list to tell its top folder
	// from an inner one. planDelivery drops the list above maxStoredFiles,
	// a storage cap only, so read it here when it is missing.
	if files == nil {
		if files, err = torrentmeta.Files(payload.TorrentFile); err != nil {
			log.Debug().Err(err).Msg("folder name: could not read the torrent's file list")
			return cannot(folderUnreadable, "could not read the torrent")
		}
	}
	renamer, canRename := clientPlugin.(registry.WithRename)
	selector, canSelect := clientPlugin.(registry.WithFileSelection)
	if !canRename || !canSelect {
		log.Warn().Str("client", clientPlugin.Name()).
			Msg("folder name set but the client cannot rename folders; adding normally")
		return cannot(folderUnsupported, "this client cannot rename folders")
	}
	return &folderPlan{
		renamer:  renamer,
		selector: selector,
		hash:     hash,
		manifest: files,
		want:     want,
	}
}

// keepFolderName is keep mode's name: the folder the previous version has in
// the client, else the name remembered from an earlier delivery. The previous
// delivery is read only when it went to this same client — another client's
// config is not at hand — and the payload's own row is passed over, since a
// retried delivery has already recorded it.
func (s *Scheduler) keepFolderName(ctx context.Context, log zerolog.Logger, t *domain.Topic, cfg *domain.Client, rawConfig []byte, clientPlugin registry.Client, hash string) string {
	selector, canSelect := clientPlugin.(registry.WithFileSelection)
	if s.deliveries == nil || !canSelect || cfg == nil {
		return t.FolderNameSeen
	}
	prev, err := s.deliveries.LatestOther(ctx, t.ID, hash)
	if err != nil {
		log.Warn().Err(err).Msg("folder name: load previous delivery failed")
		return t.FolderNameSeen
	}
	if prev == nil || prev.ClientID == nil || *prev.ClientID != cfg.ID {
		return t.FolderNameSeen
	}
	readCtx, cancel := context.WithTimeout(ctx, folderReadTimeout)
	defer cancel()
	files, err := selector.Files(readCtx, rawConfig, prev.Infohash)
	if err != nil {
		log.Debug().Err(err).Msg("folder name: could not read the previous torrent's files")
		return t.FolderNameSeen
	}
	// The previous delivery's own file list (nil when unknown) confirms the
	// folder: a client set to "no subfolder" shows a torrent whose files share
	// one inner folder as if that were the top folder.
	root, ok := torrentmeta.ClientRoot(files, prev.Files)
	if !ok {
		return t.FolderNameSeen
	}
	s.rememberFolderName(ctx, log, t, root)
	return root
}

// applyFolderName renames the just-added torrent's top folder; the recheck
// comes last, in finishDelivery, after any start. It returns the client's file
// list as last read (after the rename when that worked), the note, and
// ok=false when a step failed and the torrent must stay paused. The list comes
// back on failure too, so the update policy can still skip the old files of a
// torrent the user is told to start.
func (s *Scheduler) applyFolderName(ctx context.Context, log zerolog.Logger, clientName string, rawConfig []byte, fp *folderPlan, clientFiles []domain.ClientFile) ([]domain.ClientFile, string, bool) {
	root, hasRoot := torrentmeta.ClientRoot(clientFiles, fp.manifest)
	if !hasRoot {
		s.countFolder(log, clientName, folderNoRoot)
		return clientFiles, cannotNote(fp.want, "the client does not put this torrent in a folder"), true
	}
	if root == fp.want {
		// A retry: an earlier tick may have renamed the folder and failed
		// before its recheck. A stopped torrent with nothing verified on disk
		// gets the recheck now; a running one is left alone.
		if state, err := fp.renamer.DiskState(ctx, rawConfig, fp.hash); err == nil && state.Stopped && state.BytesOnDisk == 0 {
			fp.recheck = true
		}
		s.countFolder(log, clientName, folderSameName)
		return clientFiles, savedNote(fp.want), true
	}
	// Only a stopped torrent with nothing on disk is renamed. A retried
	// delivery is a duplicate add, which no client pauses, so the torrent may
	// be running and write its first piece between this read and the rename.
	state, err := waitForStopped(ctx, rawConfig, fp.renamer, fp.hash)
	if err != nil {
		return clientFiles, s.folderFailed(log, clientName, "could not read the torrent's progress", err), false
	}
	if !state.Stopped {
		s.countFolder(log, clientName, folderRunning)
		return clientFiles, cannotNote(fp.want, "the torrent is already running"), true
	}
	if state.BytesOnDisk > 0 {
		s.countFolder(log, clientName, folderHasData)
		return clientFiles, cannotNote(fp.want, "the torrent already has data on disk"), true
	}
	if err := fp.renamer.RenameRoot(ctx, rawConfig, fp.hash, root, fp.want); err != nil {
		return clientFiles, s.folderFailed(log, clientName, "renaming failed", err), false
	}
	// qBittorrent answers renameFolder before it moves anything, so the
	// rename counts only once the client lists the files under the new name.
	// Indices do not change with a rename, so on failure the old list still
	// selects.
	renamed, err := waitForRoot(ctx, rawConfig, fp)
	if err != nil {
		return clientFiles, s.folderFailed(log, clientName, "could not list the renamed files", err), false
	}
	if renamed == nil {
		return clientFiles, s.folderFailed(log, clientName, "the client did not show the renamed folder",
			errors.New("the client still lists the old folder")), false
	}
	fp.recheck = true
	s.countFolder(log, clientName, folderRenamed)
	return renamed, savedNote(fp.want), true
}

// waitForRoot reads the client's file list until it shows the torrent under
// fp.want, and returns that list; nil when renameWait passes first.
func waitForRoot(ctx context.Context, rawConfig []byte, fp *folderPlan) ([]domain.ClientFile, error) {
	deadline := time.Now().Add(renameWait)
	for {
		files, err := fp.selector.Files(ctx, rawConfig, fp.hash)
		if err != nil {
			return nil, err
		}
		if root, ok := torrentmeta.ClientRoot(files, fp.manifest); ok && root == fp.want {
			return files, nil
		}
		if time.Now().After(deadline) {
			return nil, nil
		}
		select {
		case <-ctx.Done():
			return nil, nil
		case <-time.After(filesPollInterval):
		}
	}
}

// waitForStopped reads the torrent's disk state until it reports stopped or
// stoppedWait passes, and returns the last state read.
func waitForStopped(ctx context.Context, rawConfig []byte, renamer registry.WithRename, hash string) (registry.DiskState, error) {
	deadline := time.Now().Add(stoppedWait)
	for {
		state, err := renamer.DiskState(ctx, rawConfig, hash)
		if err != nil || state.Stopped || time.Now().After(deadline) {
			return state, err
		}
		select {
		case <-ctx.Done():
			return state, nil
		case <-time.After(filesPollInterval):
		}
	}
}

// recheckRenamed is finishDelivery's last step after a rename: the client
// verifies the data already in the folder. It comes after any start because
// qBittorrent drops a start sent during a recheck, while every client keeps a
// torrent's running or stopped state through one (measured on qBittorrent
// 5.2.1/5.1.4, Transmission 4.1.2/4.0.6 and Deluge 2.2.0).
func (s *Scheduler) recheckRenamed(ctx context.Context, log zerolog.Logger, clientName string, rawConfig []byte, fp *folderPlan) string {
	if !fp.recheck {
		return ""
	}
	err := fp.renamer.Recheck(ctx, rawConfig, fp.hash)
	if err == nil {
		return ""
	}
	log.Warn().Err(err).Str("client", clientName).Str("result", folderFailed).Str("step", "recheck").
		Msg("folder renamed but the recheck failed; stopping the torrent")
	metrics.SchedulerFolderRenameTotal.WithLabelValues(clientName, folderFailed).Inc()
	// The torrent may already be running (started above, or by the
	// selection), and unchecked it would download the data already in the
	// folder again. Stopping a stopped torrent changes nothing.
	if err := fp.renamer.Stop(ctx, rawConfig, fp.hash); err != nil {
		log.Warn().Err(err).Str("client", clientName).Msg("stopping the torrent after a failed recheck failed")
		return "Rechecking the torrent failed and it could not be stopped. Stop and recheck it in your client."
	}
	return "Added paused: rechecking the torrent failed. Recheck it in your client and start it."
}

// folderCannotApply counts and returns the note of a plan that could not
// apply ("" for none). Called after a successful Add only.
func (s *Scheduler) folderCannotApply(log zerolog.Logger, clientName string, fp *folderPlan) string {
	if fp == nil || fp.renames() {
		return ""
	}
	s.countFolder(log, clientName, fp.result)
	return fp.note
}

// folderFailed logs the client error and returns the notification note, which
// names the step only: a client error can carry an HTML error page.
func (s *Scheduler) folderFailed(log zerolog.Logger, clientName, step string, err error) string {
	log.Warn().Err(err).Str("client", clientName).Str("result", folderFailed).Str("step", step).
		Msg("folder rename failed; torrent left paused")
	metrics.SchedulerFolderRenameTotal.WithLabelValues(clientName, folderFailed).Inc()
	return "Added paused: renaming the folder failed (" + step + "). Rename it in your client and start it."
}

func (s *Scheduler) countFolder(log zerolog.Logger, clientName, result string) {
	log.Info().Str("client", clientName).Str("result", result).Msg("folder name applied")
	metrics.SchedulerFolderRenameTotal.WithLabelValues(clientName, result).Inc()
}

// rememberFolderName stores a keep-mode topic's folder name. It runs where
// keep mode learns a name — the previous torrent's live folder, or a payload
// that already carries the wanted name — so the name a later rename sets is
// always one already stored. Best-effort: on
// a DB error the next update falls back to an older memory or the tracker's
// name, which is logged, not a failed check.
func (s *Scheduler) rememberFolderName(ctx context.Context, log zerolog.Logger, t *domain.Topic, name string) {
	if t.FolderNameMode != domain.FolderNameKeep || name == t.FolderNameSeen {
		return
	}
	setter, ok := s.topics.(folderNameSeenSetter)
	if !ok {
		return
	}
	if err := setter.SetFolderNameSeen(ctx, t, name); err != nil {
		log.Warn().Err(err).Msg("folder name: store the remembered name failed")
		return
	}
	// The worker's copy, so a later step of the same delivery does not
	// write the same name again.
	t.FolderNameSeen = name
}

func savedNote(name string) string {
	return `Saved in folder "` + name + `".`
}

func cannotNote(name, reason string) string {
	return `Could not use folder name "` + name + `": ` + reason + `. Saved in the tracker's folder.`
}

// joinNotes joins the non-empty notes with one space.
func joinNotes(notes ...string) string {
	out := make([]string, 0, len(notes))
	for _, n := range notes {
		if n != "" {
			out = append(out, n)
		}
	}
	return strings.Join(out, " ")
}
