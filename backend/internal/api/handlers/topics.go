package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/api/middleware"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/problem"
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
	Update(ctx context.Context, id, userID uuid.UUID, displayName string, clientID, notifierID *uuid.UUID, downloadDir, category string, extra map[string]any) (*domain.Topic, error)
}

// deliveriesStore is the consumer seam over *repo.Deliveries for the
// status endpoint. Nil-safe: when unset, the status endpoint reports no
// deliveries rather than failing.
type deliveriesStore interface {
	ListForTopic(ctx context.Context, topicID uuid.UUID) ([]*domain.TopicDelivery, error)
}

// clientsLookup is the consumer seam over *repo.Clients used to resolve a
// topic's client so its live torrent status can be queried.
type clientsLookup interface {
	GetByID(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*domain.Client, error)
	GetDefault(ctx context.Context, userID uuid.UUID) (*domain.Client, error)
}

// configDecryptor is the subset of *crypto.MasterKey used to decrypt a
// client config blob before handing it to the plugin's Status call.
type configDecryptor interface {
	Decrypt(ct, nonce []byte) ([]byte, error)
}

// Topics is the handler group for /topics.
type Topics struct {
	Topics     topicStore
	Deliveries deliveriesStore
	Clients    clientsLookup
	Master     configDecryptor
	BaseURL    string
}

type createTopicReq struct {
	URL              string     `json:"url"`
	DisplayName      string     `json:"display_name"`
	ClientID         *uuid.UUID `json:"client_id"`
	NotifierID       *uuid.UUID `json:"notifier_id"`
	DownloadDir      string     `json:"download_dir"`
	Category         string     `json:"category"`
	CheckIntervalSec int        `json:"check_interval_sec"`
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
	topics, err := h.Topics.ListForUser(r.Context(), uid)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"topics": topics})
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

	tracker := registry.FindTrackerForURL(req.URL)
	if tracker == nil {
		problem.Write(w, r, h.BaseURL,
			problem.New(http.StatusUnprocessableEntity,
				"topic-url-not-recognized",
				"No tracker plugin matches this URL",
				"The URL '"+req.URL+"' is not parseable by any installed tracker plugin.",
			))
		return
	}

	parsed, parseErr := tracker.Parse(r.Context(), req.URL)
	if parseErr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("parse: "+parseErr.Error()))
		return
	}

	interval := req.CheckIntervalSec
	if interval <= 0 {
		interval = 900 // 15 min default
	}
	displayName := req.DisplayName
	if displayName == "" {
		displayName = parsed.DisplayName
	}

	// Best-effort metadata resolution: ask the tracker for a real title and a
	// poster image straight from the page so a freshly-added topic shows them
	// immediately instead of a "RuTracker topic 123" placeholder. This is
	// fail-open — metadata is enhancement, not core: any error (timeout, login
	// wall, parse miss) leaves the placeholder in place and never blocks the
	// add. The scheduler later self-heals the title on the first check.
	var imageURL string
	if wm, ok := tracker.(registry.WithMetadata); ok {
		mctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		meta, merr := wm.ResolveMetadata(mctx, req.URL, nil)
		cancel()
		if merr == nil && meta != nil {
			if req.DisplayName == "" && meta.Title != "" {
				displayName = meta.Title
			}
			imageURL = meta.ImageURL
		}
	}

	// Overlay any user-supplied capability fields onto the Extra map
	// the plugin's Parse() returned. The plugin's defaults stay in
	// place for any field the user didn't specify.
	extra := parsed.Extra
	if extra == nil {
		extra = map[string]any{}
	}
	if req.Quality != "" {
		// Validate against the plugin's declared quality list, if any.
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
		extra["quality"] = req.Quality
	}
	if req.StartSeason != nil {
		extra["start_season"] = *req.StartSeason
	}
	if req.StartEpisode != nil {
		extra["start_episode"] = *req.StartEpisode
	}

	t := &domain.Topic{
		UserID:           uid,
		TrackerName:      tracker.Name(),
		URL:              req.URL,
		DisplayName:      displayName,
		ImageURL:         imageURL,
		ClientID:         req.ClientID,
		NotifierID:       req.NotifierID,
		DownloadDir:      req.DownloadDir,
		Category:         req.Category,
		Extra:            extra,
		CheckIntervalSec: interval,
		NextCheckAt:      time.Now().UTC(),
		Status:           domain.TopicStatusActive,
	}
	created, cerr := h.Topics.Create(r.Context(), t)
	if cerr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("create topic: "+cerr.Error()))
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

type updateTopicReq struct {
	DisplayName  string     `json:"display_name"`
	ClientID     *uuid.UUID `json:"client_id"`
	NotifierID   *uuid.UUID `json:"notifier_id"`
	DownloadDir  string     `json:"download_dir"`
	Category     string     `json:"category"`
	Quality      string     `json:"quality,omitempty"`
	StartSeason  *int       `json:"start_season,omitempty"`
	StartEpisode *int       `json:"start_episode,omitempty"`
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

	updated, uerr := h.Topics.Update(r.Context(), id, uid, req.DisplayName, req.ClientID, req.NotifierID, req.DownloadDir, req.Category, extra)
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

// --- helpers ------------------------------------------------------------

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
