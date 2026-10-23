package scheduler

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
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

// Folder rename metric results (marauder_scheduler_folder_rename_total).
const (
	folderRenamed     = "renamed"
	folderSameName    = "same_name"
	folderMagnet      = "magnet"
	folderSingleFile  = "single_file"
	folderUnreadable  = "unreadable"
	folderNoRoot      = "no_root"
	folderHasData     = "has_data"
	folderUnsupported = "unsupported"
	folderFailed      = "failed"
)

// folderNameSeenSetter is an optional capability of topicsRepo: it stores
// keep mode's memory of the folder name. Best-effort.
type folderNameSeenSetter interface {
	SetFolderNameSeen(ctx context.Context, id uuid.UUID, name string) error
}

// folderPlan says how one delivery's folder is named. A plan with a result
// set could not apply and carries the note to send; otherwise the torrent is
// added paused and renamed after Add.
type folderPlan struct {
	renamer  registry.WithRename
	selector registry.WithFileSelection
	hash     string
	// manifest is the torrent's file list, nil when unknown; it confirms the
	// client's top folder (torrentmeta.ClientRoot).
	manifest []domain.TorrentFile
	want     string
	result   string
	note     string
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
	root, ok := torrentmeta.ClientRoot(files, nil)
	if !ok {
		return t.FolderNameSeen
	}
	s.rememberFolderName(ctx, log, t, root)
	return root
}

// applyFolderName renames the just-added torrent's top folder and rechecks
// it. It returns the client's file list as it is afterwards, the note, and
// ok=false when a step failed and the torrent must stay paused.
func (s *Scheduler) applyFolderName(ctx context.Context, log zerolog.Logger, clientName string, rawConfig []byte, fp *folderPlan, clientFiles []domain.ClientFile) ([]domain.ClientFile, string, bool) {
	root, hasRoot := torrentmeta.ClientRoot(clientFiles, fp.manifest)
	if !hasRoot {
		s.countFolder(log, clientName, folderNoRoot)
		return clientFiles, cannotNote(fp.want, "the client does not put this torrent in a folder"), true
	}
	if root == fp.want {
		s.countFolder(log, clientName, folderSameName)
		return clientFiles, savedNote(fp.want), true
	}
	done, err := fp.renamer.BytesDone(ctx, rawConfig, fp.hash)
	if err != nil {
		return nil, s.folderFailed(log, clientName, "could not read the torrent's progress", err), false
	}
	if done > 0 {
		s.countFolder(log, clientName, folderHasData)
		return clientFiles, cannotNote(fp.want, "the torrent already has data on disk"), true
	}
	if err := fp.renamer.RenameRoot(ctx, rawConfig, fp.hash, root, fp.want); err != nil {
		return nil, s.folderFailed(log, clientName, "renaming failed", err), false
	}
	renamed, err := fp.selector.Files(ctx, rawConfig, fp.hash)
	if err == nil && len(renamed) == 0 {
		err = errors.New("the client listed no files after the rename")
	}
	if err != nil {
		return nil, s.folderFailed(log, clientName, "could not list the renamed files", err), false
	}
	if err := fp.renamer.Recheck(ctx, rawConfig, fp.hash); err != nil {
		return nil, s.folderFailed(log, clientName, "rechecking failed", err), false
	}
	s.countFolder(log, clientName, folderRenamed)
	return renamed, savedNote(fp.want), true
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
	if err := setter.SetFolderNameSeen(ctx, t.ID, name); err != nil {
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
