package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/infohash"
	"github.com/artyomsv/marauder/backend/internal/metrics"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/torrentmeta"
)

// Update policy (issue #205): the per-topic "add updates paused" and
// "download only new files" settings. The rule that shapes everything here:
// once the torrent is in the client, no failure may let an old file download.
// Anything that cannot prove which files are new leaves the torrent paused
// for the user to finish by hand, and never fails the check — the delivery
// itself succeeded.

const (
	// maxStoredFiles caps the file list stored with a delivery. Above it the
	// list is not stored, and the topic's next update arrives paused.
	maxStoredFiles = 5000
	// fileSelectionTimeout bounds the wait for the client to list the new
	// torrent's files plus the skip and start calls.
	fileSelectionTimeout = 15 * time.Second
)

// filesPollInterval is how often the client is asked for a file list it does
// not have yet (a qBittorrent add is asynchronous). A var so tests can shorten it.
var filesPollInterval = 500 * time.Millisecond

// File-selection metric results (marauder_scheduler_file_selection_total).
const (
	selSelected         = "selected"
	selNoNewFiles       = "no_new_files"
	selPausedNoBaseline = "paused_no_baseline"
	selPausedMagnet     = "paused_magnet"
	selPausedUnreadable = "paused_unreadable"
	selUnsupported      = "unsupported"
	selFailed           = "failed"
)

// deliveryPlan says how one payload is handed to the client.
type deliveryPlan struct {
	paused bool
	// files is the payload's own file list, stored with the delivery so the
	// next update has a baseline. Nil when unknown.
	files []domain.TorrentFile
	// selection is set when only-new-files runs after Add.
	selection *fileSelection
	// fallbackResult/fallbackNote are set when only-new-files applies but
	// cannot run.
	fallbackResult string
	fallbackNote   string
}

type fileSelection struct {
	selector registry.WithFileSelection
	hash     string
	skip     []domain.TorrentFile
	total    int
}

// planDelivery decides the update policy for one payload. It runs before the
// pre-submit check-state guard so its database read does not widen the gap
// between that guard and Add.
func (s *Scheduler) planDelivery(ctx context.Context, log zerolog.Logger, t *domain.Topic, episodic bool, clientPlugin registry.Client, payload *domain.Payload) deliveryPlan {
	var plan deliveryPlan
	unreadable := false
	if payload.MagnetURI == "" && len(payload.TorrentFile) > 0 {
		files, err := torrentmeta.Files(payload.TorrentFile)
		switch {
		case err != nil:
			log.Debug().Err(err).Msg("could not read the torrent's file list")
			unreadable = true
		case len(files) > maxStoredFiles:
			log.Debug().Int("files", len(files)).Msg("torrent file list too long to store")
			unreadable = true
		default:
			plan.files = files
		}
	}
	// Per-episode trackers deliver one new episode per torrent already, and a
	// first delivery (or the first after a reset) has nothing to compare with.
	if episodic || t.LastHash == "" || (!t.AddPausedOnUpdate && !t.OnlyNewFiles) {
		return plan
	}
	plan.paused = true

	selector, canSelect := clientPlugin.(registry.WithFileSelection)
	if !canSelect {
		// Such a client ignores Paused too (µTorrent, downloadfolder); the
		// form says so, so this is only reached through the API or a changed
		// default client. Not marked paused: the torrent starts regardless,
		// and a paused flag would make the notification say otherwise.
		log.Warn().Str("client", clientPlugin.Name()).
			Msg("update policy set but the client cannot pause or select files; adding normally")
		plan.paused = false
		if t.OnlyNewFiles {
			plan.fallbackResult = selUnsupported
			plan.fallbackNote = "This client cannot pause or select files, so all files download."
		}
		return plan
	}
	if !t.OnlyNewFiles {
		return plan
	}
	switch {
	case payload.MagnetURI != "":
		plan.fallbackResult = selPausedMagnet
		plan.fallbackNote = "Added paused: a magnet link has no file list. Pick the new files in your client."
		return plan
	case unreadable || plan.files == nil:
		plan.fallbackResult = selPausedUnreadable
		plan.fallbackNote = "Added paused: could not read the torrent's file list. Pick the new files in your client."
		return plan
	}
	// The hash comes first: the baseline read must pass over this update's own
	// row, which a retried delivery already recorded on an earlier tick.
	hash, err := infohash.FromTorrent(payload.TorrentFile)
	if err != nil {
		plan.fallbackResult = selPausedUnreadable
		plan.fallbackNote = "Added paused: could not read the torrent's file list. Pick the new files in your client."
		return plan
	}
	baseline := s.latestFiles(ctx, log, t.ID, hash)
	if baseline == nil {
		plan.fallbackResult = selPausedNoBaseline
		plan.fallbackNote = "Added paused: no earlier file list to compare with. Pick the new files in your client."
		return plan
	}
	plan.selection = &fileSelection{
		selector: selector,
		hash:     hash,
		skip:     torrentmeta.SkipSet(baseline, plan.files),
		total:    len(plan.files),
	}
	return plan
}

// latestFiles loads the baseline for the update with infohash hash: the
// newest other delivery's list, nil when that is unknown. A read error
// degrades to "no baseline", which adds the torrent paused — never to
// downloading everything.
func (s *Scheduler) latestFiles(ctx context.Context, log zerolog.Logger, topicID uuid.UUID, hash string) []domain.TorrentFile {
	if s.deliveries == nil {
		return nil
	}
	files, err := s.deliveries.LatestFiles(ctx, topicID, hash)
	if err != nil {
		log.Warn().Err(err).Msg("only-new-files: load previous file list failed")
		return nil
	}
	return files
}

// finishDelivery runs after a successful Add and returns the note for the
// download.submitted notification ("" for a plain delivery).
func (s *Scheduler) finishDelivery(ctx context.Context, log zerolog.Logger, t *domain.Topic, clientName string, rawConfig []byte, plan deliveryPlan) string {
	switch {
	case plan.selection != nil:
		return s.selectNewFiles(ctx, log, t, clientName, rawConfig, plan.selection)
	case plan.fallbackResult != "":
		log.Info().Str("client", clientName).Str("result", plan.fallbackResult).
			Msg("only-new-files could not select files")
		metrics.SchedulerFileSelectionTotal.WithLabelValues(clientName, plan.fallbackResult).Inc()
		return plan.fallbackNote
	case plan.paused:
		return "Added paused."
	}
	return ""
}

// selectNewFiles skips the previous version's files in a torrent that was
// just added paused, then starts it unless the topic also asks for paused
// updates.
func (s *Scheduler) selectNewFiles(ctx context.Context, log zerolog.Logger, t *domain.Topic, clientName string, rawConfig []byte, sel *fileSelection) string {
	newCount := sel.total - len(sel.skip)
	if newCount == 0 {
		log.Info().Str("client", clientName).Str("result", selNoNewFiles).
			Msg("only-new-files: update has no new files; torrent left paused")
		metrics.SchedulerFileSelectionTotal.WithLabelValues(clientName, selNoNewFiles).Inc()
		return "Added paused: this update has no new files."
	}
	ctx, cancel := context.WithTimeout(ctx, fileSelectionTimeout)
	defer cancel()
	// fail names the step for the notification and keeps the error for the
	// log only: a client error can carry an HTTP body (an HTML error page),
	// which has no place in a Telegram message or an email.
	fail := func(step string, err error) string {
		log.Warn().Err(err).Str("client", clientName).Str("result", selFailed).Str("step", step).
			Msg("file selection failed; torrent left paused")
		metrics.SchedulerFileSelectionTotal.WithLabelValues(clientName, selFailed).Inc()
		return fmt.Sprintf("Added paused: file selection did not finish (%s). Check the torrent in your client.", step)
	}
	// Always wait for the file list, even with nothing to skip: a qBittorrent
	// add is asynchronous, and a Start sent before the client knows the
	// torrent is lost, leaving it paused behind a "Downloading" note.
	clientFiles, err := waitForFiles(ctx, rawConfig, sel)
	if err != nil {
		return fail("could not list the files", err)
	}
	if len(sel.skip) > 0 {
		indices, matched := torrentmeta.MatchSkip(clientFiles, sel.skip)
		if matched != len(sel.skip) {
			// Starting now would download an old file the client did not show.
			return fail(fmt.Sprintf("the client listed %d of %d old files", matched, len(sel.skip)), nil)
		}
		if err := sel.selector.SkipFiles(ctx, rawConfig, sel.hash, indices); err != nil {
			return fail("skipping the old files failed", err)
		}
	}
	if t.AddPausedOnUpdate {
		log.Info().Str("client", clientName).Str("result", selSelected).
			Int("new", newCount).Int("total", sel.total).
			Msg("only-new-files: new files selected; torrent left paused")
		metrics.SchedulerFileSelectionTotal.WithLabelValues(clientName, selSelected).Inc()
		return fmt.Sprintf("Added paused with %d new of %d files selected.", newCount, sel.total)
	}
	if err := sel.selector.Start(ctx, rawConfig, sel.hash); err != nil {
		return fail("starting the torrent failed", err)
	}
	log.Info().Str("client", clientName).Str("result", selSelected).
		Int("new", newCount).Int("total", sel.total).
		Msg("only-new-files: new files selected; torrent started")
	metrics.SchedulerFileSelectionTotal.WithLabelValues(clientName, selSelected).Inc()
	return fmt.Sprintf("Downloading %d new of %d files.", newCount, sel.total)
}

// waitForFiles polls the client until it lists the torrent's files.
func waitForFiles(ctx context.Context, rawConfig []byte, sel *fileSelection) ([]domain.ClientFile, error) {
	for {
		files, err := sel.selector.Files(ctx, rawConfig, sel.hash)
		if err != nil {
			return nil, fmt.Errorf("list files: %w", err)
		}
		if len(files) > 0 {
			return files, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("the client did not list the torrent's files in time: %w", ctx.Err())
		case <-time.After(filesPollInterval):
		}
	}
}
