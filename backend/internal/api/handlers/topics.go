package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/api/middleware"
	"github.com/artyomsv/marauder/backend/internal/audit"
	"github.com/artyomsv/marauder/backend/internal/clientremove"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/events"
	"github.com/artyomsv/marauder/backend/internal/metrics"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/problem"
	"github.com/artyomsv/marauder/backend/internal/topics"
)

// topicStore is the consumer-side seam over *repo.Topics so the handler
// is unit-testable with a fake store (mirrors credentialStore and the
// scheduler's consumer-interface pattern). *repo.Topics satisfies it, so
// production wiring is unchanged.
type topicStore interface {
	Create(ctx context.Context, t *domain.Topic) (*domain.Topic, error)
	GetByID(ctx context.Context, id uuid.UUID, userID *uuid.UUID) (*domain.Topic, error)
	ListForUser(ctx context.Context, userID uuid.UUID) ([]*domain.Topic, error)
	Delete(ctx context.Context, id, userID uuid.UUID) error
	UpdateStatus(ctx context.Context, id, userID uuid.UUID, status domain.TopicStatus) error
	Update(ctx context.Context, id, userID uuid.UUID, displayName string, clientID, notifierID *uuid.UUID, downloadDir, category string, replaceOnUpdate, replaceDeleteData bool, extra map[string]any) (*domain.Topic, error)
	ResetCheckState(ctx context.Context, id, userID uuid.UUID) error
	QueueRecheck(ctx context.Context, id, userID uuid.UUID) (bool, error)
}

// deliveriesStore is the consumer seam over *repo.Deliveries for the
// status endpoint. Nil-safe: when unset, the status endpoint reports no
// deliveries rather than failing.
type deliveriesStore interface {
	ListForTopic(ctx context.Context, topicID uuid.UUID) ([]*domain.TopicDelivery, error)
	DeleteForTopic(ctx context.Context, topicID, userID uuid.UUID) (int64, error)
}

// clientsLookup is the consumer seam over *repo.Clients used to resolve a
// topic's client so its live torrent status can be queried, and to validate
// ownership on topic write operations.
type clientsLookup interface {
	GetByID(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*domain.Client, error)
	GetDefault(ctx context.Context, userID uuid.UUID) (*domain.Client, error)
}

// notifiersLookup resolves a user's notifier so the handler can reject a
// topic that references a notifier the user does not own.
type notifiersLookup interface {
	GetByID(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*domain.Notifier, error)
}

// configDecryptor is the subset of *crypto.MasterKey used to decrypt a
// client config blob before handing it to the plugin's Status call.
type configDecryptor interface {
	Decrypt(ct, nonce []byte) ([]byte, error)
}

// torrentRemover is the consumer seam over *clientremove.Remover so handler
// tests can substitute a fake. Nil-safe: an unset remover skips client removal
// and reports it as a warning rather than failing the reset.
type torrentRemover interface {
	Remove(ctx context.Context, userID uuid.UUID, byClient map[uuid.UUID][]string, deleteData bool) []clientremove.Result
}

// Topics is the handler group for /topics.
type Topics struct {
	Topics     topicStore
	Deliveries deliveriesStore
	Clients    clientsLookup
	Notifiers  notifiersLookup
	Master     configDecryptor
	Audit      *audit.Logger
	BaseURL    string
	// Emit is an optional hook called after a topic is successfully created.
	// Nil-safe: existing handler tests that don't set Emit continue to pass.
	Emit func(ctx context.Context, ev events.Event)
	// Remover removes already-delivered torrents from their client on reset.
	// Nil-safe (see torrentRemover).
	Remover torrentRemover

	// resetInFlight gates POST /topics/{id}/reset to one running reset per
	// topic (topicID -> struct{}). Deliberately keyed by topic, not by user:
	// a bulk reset fires one concurrent request per selected topic and must
	// keep working, while a double-click, an impatient retry after the
	// "retry the reset" 500, or two tabs racing the same topic must not.
	resetInFlight sync.Map
}

type createTopicReq struct {
	URL              string     `json:"url"`
	DisplayName      string     `json:"display_name"`
	ClientID         *uuid.UUID `json:"client_id"`
	NotifierID       *uuid.UUID `json:"notifier_id"`
	DownloadDir      string     `json:"download_dir"`
	Category         string     `json:"category"`
	CheckIntervalSec int        `json:"check_interval_sec"`
	// ReplaceOnUpdate opts the topic into the "replace previous version" policy
	// (issue #101); ReplaceDeleteData (default true when omitted) also deletes
	// the old torrent's data. Pointer so an omitted flag falls back to the
	// delete-data default rather than false.
	ReplaceOnUpdate   bool  `json:"replace_on_update,omitempty"`
	ReplaceDeleteData *bool `json:"replace_delete_data,omitempty"`
	// Optional capability-driven fields. The frontend learns whether a
	// tracker accepts these via GET /api/v1/trackers/match. Plugins read
	// them from topic.Extra in Check / Download.
	Quality      string `json:"quality,omitempty"`
	StartSeason  *int   `json:"start_season,omitempty"`
	StartEpisode *int   `json:"start_episode,omitempty"`
}

// List handles GET /topics.
func (h *Topics) List(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	list, err := h.Topics.ListForUser(r.Context(), uid)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"topics": list})
}

// Create handles POST /topics.
func (h *Topics) Create(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}

	var req createTopicReq
	if jerr := json.NewDecoder(r.Body).Decode(&req); jerr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	if req.URL == "" {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("url is required"))
		return
	}

	// Ownership of the referenced client/notifier is validated here (the
	// handler holds those lookups); the tracker-match → parse → metadata →
	// persist sequence lives in the shared topics package so the Sonarr
	// poller reuses the exact same logic.
	if perr := h.validateOwnership(r.Context(), uid, req.NotifierID, req.ClientID); perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}

	res, err := topics.BuildAndCreate(r.Context(), h.Topics, topics.CreateInput{
		UserID:            uid,
		URL:               req.URL,
		DisplayName:       req.DisplayName,
		ClientID:          req.ClientID,
		NotifierID:        req.NotifierID,
		DownloadDir:       req.DownloadDir,
		Category:          req.Category,
		CheckIntervalSec:  req.CheckIntervalSec,
		ReplaceOnUpdate:   req.ReplaceOnUpdate,
		ReplaceDeleteData: req.ReplaceDeleteData,
		Quality:           req.Quality,
		StartSeason:       req.StartSeason,
		StartEpisode:      req.StartEpisode,
	})
	if err != nil {
		problem.Write(w, r, h.BaseURL, topicCreateProblem(err, req.URL))
		return
	}
	if !res.Created {
		problem.Write(w, r, h.BaseURL,
			problem.New(http.StatusConflict, "topic-already-exists",
				"Topic already exists",
				"A topic for this URL already exists."))
		return
	}
	created := res.Topic
	if h.Emit != nil {
		h.Emit(r.Context(), events.Event{
			UserID:     created.UserID,
			TopicID:    &created.ID,
			NotifierID: created.NotifierID,
			Type:       events.TopicAdded,
			Severity:   "info",
			Title:      created.DisplayName,
			Body:       "Topic added",
			Link:       h.BaseURL + "/topics",
			SourceURL:  created.URL,
		})
	}
	writeJSON(w, http.StatusCreated, created)
}

// topicCreateProblem maps a topics.BuildAndCreate error to an RFC-7807
// response, preserving the prior per-case status codes and messages.
func topicCreateProblem(err error, url string) error {
	switch {
	case errors.Is(err, topics.ErrNoTracker):
		return problem.New(http.StatusUnprocessableEntity,
			"topic-url-not-recognized",
			"No tracker plugin matches this URL",
			"The URL '"+url+"' is not parseable by any installed tracker plugin.")
	case errors.Is(err, topics.ErrParse):
		// err is "parse failed: <tracker error>" (multi-%w wrapped), so use
		// its full message — errors.Unwrap returns nil on a multi-wrap.
		return problem.ErrUnprocessable(err.Error())
	case errors.Is(err, topics.ErrQualityUnsupported):
		return problem.ErrUnprocessable(err.Error())
	default:
		return problem.ErrInternal("create topic: " + err.Error())
	}
}

type updateTopicReq struct {
	DisplayName string     `json:"display_name"`
	ClientID    *uuid.UUID `json:"client_id"`
	NotifierID  *uuid.UUID `json:"notifier_id"`
	DownloadDir string     `json:"download_dir"`
	Category    string     `json:"category"`
	// ReplaceOnUpdate / ReplaceDeleteData are pointers so an omitted field
	// preserves the topic's current value (issue #101).
	ReplaceOnUpdate   *bool  `json:"replace_on_update,omitempty"`
	ReplaceDeleteData *bool  `json:"replace_delete_data,omitempty"`
	Quality           string `json:"quality,omitempty"`
	StartSeason       *int   `json:"start_season,omitempty"`
	StartEpisode      *int   `json:"start_episode,omitempty"`
}

// Update handles PUT /topics/{id}.
func (h *Topics) Update(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	id, ierr := uuid.Parse(chi.URLParam(r, "id"))
	if ierr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid topic id"))
		return
	}
	var req updateTopicReq
	if derr := json.NewDecoder(r.Body).Decode(&req); derr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	existing, gerr := h.Topics.GetByID(r.Context(), id, &uid)
	if gerr != nil {
		if errors.Is(gerr, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("topic not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(gerr.Error()))
		return
	}
	if existing == nil {
		problem.Write(w, r, h.BaseURL, problem.ErrNotFound("topic not found"))
		return
	}

	// Start from the existing extra map and overlay capability fields,
	// mirroring Create's logic exactly (same keys, same validation).
	extra := map[string]any{}
	for k, v := range existing.Extra {
		extra[k] = v
	}
	if req.Quality != "" {
		tracker := registry.GetTracker(existing.TrackerName)
		if tracker != nil {
			if wq, ok := tracker.(registry.WithQuality); ok {
				allowed := false
				for _, q := range wq.Qualities() {
					if q == req.Quality {
						allowed = true
						break
					}
				}
				if !allowed {
					problem.Write(w, r, h.BaseURL,
						problem.ErrUnprocessable("quality '"+req.Quality+"' not supported by this tracker"))
					return
				}
			}
		}
		extra["quality"] = req.Quality
	}
	if req.StartSeason != nil {
		extra["start_season"] = *req.StartSeason
	}
	if req.StartEpisode != nil {
		extra["start_episode"] = *req.StartEpisode
	}

	if perr := h.validateOwnership(r.Context(), uid, req.NotifierID, req.ClientID); perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}

	// Pointer flags preserve the topic's current value when omitted.
	replaceOnUpdate := existing.ReplaceOnUpdate
	if req.ReplaceOnUpdate != nil {
		replaceOnUpdate = *req.ReplaceOnUpdate
	}
	replaceDeleteData := existing.ReplaceDeleteData
	if req.ReplaceDeleteData != nil {
		replaceDeleteData = *req.ReplaceDeleteData
	}

	updated, uerr := h.Topics.Update(r.Context(), id, uid, req.DisplayName, req.ClientID, req.NotifierID, req.DownloadDir, req.Category, replaceOnUpdate, replaceDeleteData, extra)
	if uerr != nil {
		if errors.Is(uerr, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("topic not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("update topic: "+uerr.Error()))
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// Get handles GET /topics/{id}.
func (h *Topics) Get(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	id, ierr := uuid.Parse(chi.URLParam(r, "id"))
	if ierr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid id"))
		return
	}
	t, gerr := h.Topics.GetByID(r.Context(), id, &uid)
	if gerr != nil {
		if errors.Is(gerr, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("topic not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(gerr.Error()))
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// deliveryStatusItem is one row in the topic status response. State is the
// normalised client lifecycle word, or "delivered" when the client can't
// report live status (no capability) or no longer knows the torrent.
// PercentDone is nil unless the client reported a live figure (0..1).
type deliveryStatusItem struct {
	Label       string    `json:"label"`
	Infohash    string    `json:"infohash"`
	DeliveredAt time.Time `json:"delivered_at"`
	State       string    `json:"state"`
	PercentDone *float64  `json:"percent_done"`
}

// Status handles GET /topics/{id}/status. It lists what the topic has
// delivered to its client and, when that client supports live status
// (qBittorrent, Transmission), augments each item with download percent
// and state. Live status is fail-open enhancement: a query error degrades
// to "delivered" labels rather than failing the request.
func (h *Topics) Status(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	id, ierr := uuid.Parse(chi.URLParam(r, "id"))
	if ierr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid id"))
		return
	}

	// Ownership check (and gives us the topic's client + display name).
	topic, gerr := h.Topics.GetByID(r.Context(), id, &uid)
	if gerr != nil {
		if errors.Is(gerr, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("topic not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(gerr.Error()))
		return
	}

	var deliveries []*domain.TopicDelivery
	if h.Deliveries != nil {
		var derr error
		deliveries, derr = h.Deliveries.ListForTopic(r.Context(), id)
		if derr != nil {
			problem.Write(w, r, h.BaseURL, problem.ErrInternal(derr.Error()))
			return
		}
	}

	supportsStatus, live := h.liveStatus(r.Context(), topic, uid, deliveries)

	items := make([]deliveryStatusItem, 0, len(deliveries))
	for _, d := range deliveries {
		item := deliveryStatusItem{
			Label:       d.Label,
			Infohash:    d.Infohash,
			DeliveredAt: d.DeliveredAt,
			State:       "delivered",
		}
		if st, ok := live[strings.ToLower(d.Infohash)]; ok {
			pct := st.PercentDone
			item.PercentDone = &pct
			item.State = st.State
		}
		items = append(items, item)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"client_supports_status": supportsStatus,
		"deliveries":             items,
	})
}

// liveStatus resolves the topic's client and, if it supports WithStatus,
// queries live torrent status for the delivered infohashes. Returns
// whether the client supports status and a map keyed by lowercase
// infohash. Every failure path is fail-open: it returns (supports, empty)
// or (false, nil) so the caller still renders delivered labels.
func (h *Topics) liveStatus(ctx context.Context, topic *domain.Topic, uid uuid.UUID, deliveries []*domain.TopicDelivery) (bool, map[string]registry.TorrentStatus) {
	empty := map[string]registry.TorrentStatus{}
	if len(deliveries) == 0 || h.Clients == nil || h.Master == nil {
		return false, empty
	}
	client := h.resolveClient(ctx, topic, uid)
	if client == nil {
		return false, empty
	}
	ws, ok := registry.GetClient(client.ClientName).(registry.WithStatus)
	if !ok {
		return false, empty
	}
	rawCfg, derr := h.Master.Decrypt(client.ConfigEnc, client.ConfigNonce)
	if derr != nil {
		return true, empty // capability exists, config unreadable — degrade gracefully
	}
	hashes := make([]string, 0, len(deliveries))
	for _, d := range deliveries {
		hashes = append(hashes, d.Infohash)
	}
	qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	statuses, serr := ws.Status(qctx, rawCfg, hashes)
	if serr != nil {
		return true, empty // client unreachable — still report capability, no live data
	}
	out := make(map[string]registry.TorrentStatus, len(statuses))
	for _, st := range statuses {
		out[strings.ToLower(st.Hash)] = st
	}
	return true, out
}

// resolveClient returns the topic's explicit client, or the user's default
// client when the topic has none. Returns nil when neither resolves.
func (h *Topics) resolveClient(ctx context.Context, topic *domain.Topic, uid uuid.UUID) *domain.Client {
	if topic.ClientID != nil {
		c, err := h.Clients.GetByID(ctx, *topic.ClientID, uid)
		if err != nil {
			return nil
		}
		return c
	}
	c, err := h.Clients.GetDefault(ctx, uid)
	if err != nil {
		return nil
	}
	return c
}

// Delete handles DELETE /topics/{id}.
func (h *Topics) Delete(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	id, ierr := uuid.Parse(chi.URLParam(r, "id"))
	if ierr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid id"))
		return
	}
	if derr := h.Topics.Delete(r.Context(), id, uid); derr != nil {
		if errors.Is(derr, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("topic not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(derr.Error()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Pause handles POST /topics/{id}/pause.
func (h *Topics) Pause(w http.ResponseWriter, r *http.Request) {
	h.setStatus(w, r, domain.TopicStatusPaused)
}

// Resume handles POST /topics/{id}/resume.
func (h *Topics) Resume(w http.ResponseWriter, r *http.Request) {
	h.setStatus(w, r, domain.TopicStatusActive)
}

func (h *Topics) setStatus(w http.ResponseWriter, r *http.Request, status domain.TopicStatus) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	id, ierr := uuid.Parse(chi.URLParam(r, "id"))
	if ierr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid id"))
		return
	}
	if uerr := h.Topics.UpdateStatus(r.Context(), id, uid, status); uerr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(uerr.Error()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Recheck handles POST /topics/{id}/recheck: bring the topic's next scheduled
// check forward to now.
//
// This exists because a failing topic backs off up to six hours, which is
// correct while a tracker is down and wrong the moment the operator fixes the
// cause. Reset is the only other way to force a check and it is destructive —
// it removes delivered torrents from the client.
//
// Nothing but next_check_at changes, so the stale error stays on screen until
// the check that follows actually says otherwise.
func (h *Topics) Recheck(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	id, ierr := uuid.Parse(chi.URLParam(r, "id"))
	if ierr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid id"))
		return
	}
	queued, qerr := h.Topics.QueueRecheck(r.Context(), id, uid)
	if qerr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(qerr.Error()))
		return
	}
	if queued {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// No row matched: the topic is paused, or it is not this user's. Only the
	// rare failing path pays for the extra lookup, and it buys an honest
	// answer — a 404 for a paused topic would be a lie, and a 204 would
	// promise a check the scheduler is never going to run.
	t, gerr := h.Topics.GetByID(r.Context(), id, &uid)
	if gerr != nil || t == nil {
		problem.Write(w, r, h.BaseURL, problem.ErrNotFound("topic not found"))
		return
	}
	if t.Status != domain.TopicStatusPaused {
		// It was ineligible when the UPDATE ran but is not paused now — it was
		// resumed in between. Reporting "paused" here would describe a state
		// that no longer holds and tell the user to resume an active topic, so
		// retry the queue instead. Exactly once: the topic is eligible now, and
		// a loop here would be a spin on a moving row.
		requeued, rerr := h.Topics.QueueRecheck(r.Context(), id, uid)
		if rerr != nil {
			problem.Write(w, r, h.BaseURL, problem.ErrInternal(rerr.Error()))
			return
		}
		if requeued {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// Still nothing: the row went away between the two statements.
		problem.Write(w, r, h.BaseURL, problem.ErrNotFound("topic not found"))
		return
	}
	problem.Write(w, r, h.BaseURL, problem.ErrConflict("topic is paused; resume it first"))
}

// resetFinishTimeout bounds the reset's two fail-closed steps once they have
// been detached from the request context. Long enough for a DB round-trip on a
// loaded instance, short enough that a wedged pool cannot leak the goroutine.
const resetFinishTimeout = 10 * time.Second

// resetTopicReq is the POST /topics/{id}/reset body.
type resetTopicReq struct {
	// DeleteData also deletes the torrents' on-disk data. Defaults to false
	// when omitted: deleting data is irreversible, so it is opt-in. It is a
	// per-action choice, deliberately independent of the topic's stored
	// replace_delete_data policy.
	DeleteData bool `json:"delete_data"`
}

// resetTopicResp reports what the reset managed to do. Warnings is never null
// so the frontend can iterate it unconditionally.
type resetTopicResp struct {
	// Removed counts torrents confirmed removed from a client — not delivery
	// rows deleted. The two differ whenever a removal failed, because rows are
	// deleted regardless.
	Removed  int      `json:"removed"`
	Warnings []string `json:"warnings"`
}

// Reset handles POST /topics/{id}/reset. It discards the topic's check and
// download state so the next check re-detects the current release as new and
// re-delivers it, after removing the already-delivered torrents from the
// client(s) that hold them.
//
// Client removal is fail-open: the broken client is usually the reason the
// user is resetting, so a removal failure becomes a warning rather than
// blocking the reset. Everything after it is fail-closed, and the state reset
// runs last because it is the step that arms the scheduler — if an earlier
// step dies, the topic is simply not reset yet and the action can be retried.
//
// Concurrent resets of the same topic are rejected with 429 (see the
// per-topic single-flight gate below).
func (h *Topics) Reset(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	id, ierr := uuid.Parse(chi.URLParam(r, "id"))
	if ierr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid topic id"))
		return
	}
	var req resetTopicReq
	// An empty or malformed body means "reset with defaults" — delete_data
	// false. The only field is a bool, so there is nothing to reject.
	_ = json.NewDecoder(r.Body).Decode(&req)

	topic, gerr := h.Topics.GetByID(r.Context(), id, &uid)
	if gerr != nil {
		if errors.Is(gerr, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("topic not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(gerr.Error()))
		return
	}

	// Per-topic single-flight. This endpoint can delete a user's files from
	// disk and forces immediate outbound work against their torrent client,
	// so overlapping resets of one topic are refused rather than serialised:
	// the second caller's removal snapshot would be the first one's leftovers,
	// and both would race the same delivery rows.
	//
	// Placed after the ownership check on purpose — a caller who does not own
	// the topic gets 404 either way and cannot use the 429 to probe whether
	// somebody else's topic is being reset. Released by defer, which also
	// runs while a panic unwinds, so the gate can never latch shut.
	if _, busy := h.resetInFlight.LoadOrStore(id, struct{}{}); busy {
		problem.Write(w, r, h.BaseURL, problem.ErrTooManyRequests(
			"a reset is already running for this topic; wait for it to finish"))
		return
	}
	defer h.resetInFlight.Delete(id)

	removed, warnings := h.removeDeliveredTorrents(r.Context(), topic, uid, req.DeleteData)

	// Steps 2 and 3 are past the point of no return — torrents may already be
	// gone from the client and their data off disk. r.Context() is cancelled
	// the moment the client connection closes, which would abandon them
	// half-done: rows intact, hash unchanged, scheduler not armed, nothing
	// re-downloaded, and the 500 explaining it written into a socket nobody is
	// reading. Detach so a disconnecting browser cannot cause exactly the state
	// these fail-closed steps exist to prevent.
	finish, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), resetFinishTimeout)
	defer cancel()

	if h.Deliveries != nil {
		if _, derr := h.Deliveries.DeleteForTopic(finish, id, uid); derr != nil {
			// Fail-closed: a surviving row makes the re-delivery record a
			// silent no-op (unique index + ON CONFLICT DO NOTHING), so the
			// topic must not be armed for a re-check. removed may already be
			// non-zero here — step 1 can have already removed torrents from
			// the client before this step failed — so the message must say
			// so and tell the user to retry, or the removal is silently lost
			// (the topic's hash is unchanged, so the scheduler won't
			// re-download on its own).
			problem.Write(w, r, h.BaseURL, problem.ErrInternal(fmt.Sprintf(
				"removed %d torrent(s) from the client but could not clear the delivery records; retry the reset: %v",
				removed, derr)))
			return
		}
	}

	if rerr := h.Topics.ResetCheckState(finish, id, uid); rerr != nil {
		if errors.Is(rerr, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("topic not found"))
			return
		}
		// Same reasoning as the delivery-delete failure above: torrents may
		// already be gone from the client, so the message must carry the
		// count and tell the user to retry.
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(fmt.Sprintf(
			"removed %d torrent(s) and cleared delivery records but could not reset the topic's check state; retry the reset: %v",
			removed, rerr)))
		return
	}

	if h.Audit != nil {
		// This is the only endpoint that can delete a user's files from disk,
		// so what it did has to be recoverable from the audit log alone.
		ip, ua := audit.FromRequest(r)
		h.Audit.Generic(&uid, "topic_reset", "topic", id.String(), "success",
			map[string]any{
				"delete_data": req.DeleteData,
				"removed":     removed,
				"warnings":    len(warnings),
				"ip":          ip,
				"ua":          ua,
			})
	}

	if h.Emit != nil {
		h.Emit(finish, events.Event{
			UserID:     topic.UserID,
			TopicID:    &topic.ID,
			NotifierID: topic.NotifierID,
			Type:       events.TopicReset,
			Severity:   "info",
			Title:      topic.DisplayName,
			Body:       "Topic reset — will re-download from scratch",
			Link:       h.BaseURL + "/topics",
			SourceURL:  topic.URL,
		})
	}

	writeJSON(w, http.StatusOK, resetTopicResp{Removed: removed, Warnings: warnings})
}

// removeDeliveredTorrents removes every torrent the topic has delivered from
// the client it was delivered to. Returns the number of torrents confirmed
// removed and a human-readable warning per client that refused or could not be
// reached. Never fails the reset.
func (h *Topics) removeDeliveredTorrents(ctx context.Context, topic *domain.Topic, uid uuid.UUID, deleteData bool) (int, []string) {
	warnings := []string{}
	if h.Deliveries == nil {
		// Say so, exactly as the Remover == nil seam below does. A silent
		// return here would arm the scheduler with every delivery row intact
		// and hand back a clean 200 — the precise state the fail-closed
		// delivery-delete exists to prevent, reported as a success.
		return 0, append(warnings,
			"delivery tracking is not configured; the old torrents were left in the client and their records kept")
	}
	deliveries, err := h.Deliveries.ListForTopic(ctx, topic.ID)
	if err != nil {
		return 0, append(warnings, "could not list delivered torrents: "+err.Error())
	}
	byClient := clientremove.GroupByClient(deliveries)

	// GroupByClient drops rows with no client id — there is nothing to address
	// the removal to. The reset deletes those rows anyway, so the torrent stays
	// in whatever client holds it, now untracked. Say so, or the user is told
	// "Removed 0 torrent(s)" with no explanation at all.
	grouped := 0
	for _, hashes := range byClient {
		grouped += len(hashes)
	}
	if orphaned := len(deliveries) - grouped; orphaned > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"%d torrent(s) had no recorded client and were left in place", orphaned))
	}

	if len(byClient) == 0 {
		return 0, warnings
	}
	if h.Remover == nil {
		return 0, append(warnings, "torrent removal is not configured; the old torrents were left in the client")
	}

	removed := 0
	for _, res := range h.Remover.Remove(ctx, uid, byClient, deleteData) {
		n := float64(len(res.Hashes))
		name := clientremove.ClientLabel(res.ClientName)
		if res.OK {
			removed += len(res.Hashes)
			metrics.TopicResetRemovedTotal.WithLabelValues(name, "ok").Add(n)
			continue
		}
		metrics.TopicResetRemovedTotal.WithLabelValues(name, res.Reason).Add(n)
		warnings = append(warnings, name+": "+removalWarning(res))
	}
	return removed, warnings
}

// removalWarning renders one failed removal as a sentence the user can act on.
func removalWarning(res clientremove.Result) string {
	switch res.Reason {
	case clientremove.ReasonLookup:
		return "the client this torrent was sent to no longer exists; it was left in place"
	case clientremove.ReasonNoPlugin:
		return "this client's plugin is not installed; the torrent was left in place"
	case clientremove.ReasonUnsupported:
		return "this client cannot remove torrents; remove it there by hand"
	case clientremove.ReasonDecrypt:
		return "the stored client config could not be read; the torrent was left in place"
	default:
		if res.Err != nil {
			return "removal failed: " + res.Err.Error()
		}
		return "removal failed"
	}
}

// --- helpers ------------------------------------------------------------

// validateOwnership checks that the referenced notifier and client (when
// non-nil) belong to the requesting user. It is nil-safe on the lookup
// fields so that unit tests which don't wire h.Notifiers / h.Clients skip
// validation and remain green. Returns a non-nil *problem.Error on failure.
func (h *Topics) validateOwnership(ctx context.Context, uid uuid.UUID, notifierID, clientID *uuid.UUID) *problem.Error {
	if notifierID != nil && h.Notifiers != nil {
		if _, err := h.Notifiers.GetByID(ctx, *notifierID, uid); err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return problem.ErrUnprocessable("notifier not found")
			}
			return problem.ErrInternal(err.Error())
		}
	}
	if clientID != nil && h.Clients != nil {
		if _, err := h.Clients.GetByID(ctx, *clientID, uid); err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return problem.ErrUnprocessable("client not found")
			}
			return problem.ErrInternal(err.Error())
		}
	}
	return nil
}

func currentUserID(r *http.Request) (uuid.UUID, *problem.Error) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		return uuid.Nil, problem.ErrUnauthorized("no claims")
	}
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		return uuid.Nil, problem.ErrUnauthorized("bad claims")
	}
	return uid, nil
}
