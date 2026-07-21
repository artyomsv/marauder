// Package topics holds the tracker-aware topic-creation logic shared by
// the HTTP handler (POST /topics) and any headless creator such as the
// Sonarr poller. Centralising it here means the "match URL → parse →
// resolve metadata → build → persist" sequence lives in exactly one place.
package topics

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// metadataTimeout bounds the best-effort title/poster lookup at add time.
const metadataTimeout = 8 * time.Second

// defaultCheckIntervalSec is used when the caller doesn't specify one.
const defaultCheckIntervalSec = 900 // 15 min

// Sentinels let callers map failures back to their own error surface
// (HTTP problem documents, log lines, metrics) without string matching.
var (
	// ErrNoTracker means no installed tracker plugin claims the URL.
	ErrNoTracker = errors.New("no tracker plugin matches this url")
	// ErrParse wraps a tracker's Parse failure; the underlying message is
	// preserved via %w so callers can surface it.
	ErrParse = errors.New("parse failed")
	// ErrQualityUnsupported means the requested quality is not in the
	// tracker's declared quality list.
	ErrQualityUnsupported = errors.New("quality not supported by this tracker")
)

// Store is the minimal persistence seam BuildAndCreate needs. *repo.Topics
// satisfies it.
type Store interface {
	Create(ctx context.Context, t *domain.Topic) (*domain.Topic, error)
}

// CreateInput carries everything needed to build and persist a topic. All
// capability fields are optional; the tracker plugin's Parse defaults fill
// the gaps.
type CreateInput struct {
	UserID           uuid.UUID
	URL              string
	DisplayName      string // optional; falls back to tracker Parse / metadata
	ClientID         *uuid.UUID
	NotifierID       *uuid.UUID
	DownloadDir      string
	Category         string
	CheckIntervalSec int
	// ReplaceOnUpdate opts the topic into the "replace previous version" policy
	// (issue #101). ReplaceDeleteData controls whether the old torrent's data is
	// also deleted; nil means the default (true), so callers that don't set it
	// get the same default as the DB column.
	ReplaceOnUpdate   bool
	ReplaceDeleteData *bool
	Quality           string
	StartSeason       *int
	StartEpisode      *int
	// Source tags how the topic was created (e.g. "sonarr"). Stored in
	// extra["source"] so the UI can badge auto-imported topics. Empty for
	// manually-added topics.
	Source string
	// Extra carries trusted creator-specific tracker metadata. Values are
	// merged over the tracker's Parse defaults before the shared capability
	// fields below are applied. Callers must not supply tracker routing keys
	// such as "provider", "alias", or "slug"; those remain owned by Parse.
	Extra map[string]any
}

// Result reports what happened. Created is false (with a nil Topic) when an
// identical (user, url) topic already existed — an idempotent no-op, not an
// error.
type Result struct {
	Topic   *domain.Topic
	Created bool
}

// BuildAndCreate matches the URL to a tracker, parses it, best-effort
// resolves a real title + poster (fail-open), overlays optional capability
// fields, and persists the topic. A unique-constraint violation on
// (user_id, url) is treated as an idempotent skip (Result{Created:false}).
func BuildAndCreate(ctx context.Context, store Store, in CreateInput) (*Result, error) {
	tracker := registry.FindTrackerForURL(in.URL)
	if tracker == nil {
		return nil, ErrNoTracker
	}
	in.URL = CanonicalTopicURL(tracker, in.URL)

	parsed, err := tracker.Parse(ctx, in.URL)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrParse, err)
	}

	interval := in.CheckIntervalSec
	if interval <= 0 {
		interval = defaultCheckIntervalSec
	}
	displayName := in.DisplayName
	resolved := in.DisplayName != "" // caller-supplied name is authoritative
	if displayName == "" {
		displayName = parsed.DisplayName
	}

	// Best-effort metadata: a real title + poster straight from the page so
	// a fresh topic isn't a "RuTracker topic 123" placeholder. Fail-open —
	// any error leaves the placeholder and never blocks creation; the
	// scheduler self-heals the title on the first check.
	imageURL := resolveMetadata(ctx, tracker, in, &displayName, &resolved)

	extra := parsed.Extra
	if extra == nil {
		extra = map[string]any{}
	}
	for key, value := range in.Extra {
		extra[key] = value
	}
	if in.Quality != "" {
		if wq, ok := tracker.(registry.WithQuality); ok && !slices.Contains(wq.Qualities(), in.Quality) {
			return nil, fmt.Errorf("%w: %q", ErrQualityUnsupported, in.Quality)
		}
		extra["quality"] = in.Quality
	}
	if in.StartSeason != nil {
		extra["start_season"] = *in.StartSeason
	}
	if in.StartEpisode != nil {
		extra["start_episode"] = *in.StartEpisode
	}
	if in.Source != "" {
		extra["source"] = in.Source
	}

	// Replace-on-update: keep the DB column's default (delete data = true) when
	// the caller doesn't specify, so an omitted flag matches the column default.
	replaceDeleteData := true
	if in.ReplaceDeleteData != nil {
		replaceDeleteData = *in.ReplaceDeleteData
	}

	t := &domain.Topic{
		UserID:                   in.UserID,
		TrackerName:              tracker.Name(),
		URL:                      in.URL,
		DisplayName:              displayName,
		DisplayNameIsPlaceholder: !resolved,
		ImageURL:                 imageURL,
		ClientID:                 in.ClientID,
		NotifierID:               in.NotifierID,
		DownloadDir:              in.DownloadDir,
		Category:                 in.Category,
		ReplaceOnUpdate:          in.ReplaceOnUpdate,
		ReplaceDeleteData:        replaceDeleteData,
		Extra:                    extra,
		CheckIntervalSec:         interval,
		NextCheckAt:              time.Now().UTC(),
		Status:                   domain.TopicStatusActive,
	}

	created, err := store.Create(ctx, t)
	if err != nil {
		if isUniqueViolation(err) {
			return &Result{Created: false}, nil
		}
		return nil, err
	}
	return &Result{Topic: created, Created: true}, nil
}

// resolveMetadata returns the poster URL and, when the user didn't supply a
// name, upgrades displayName in place and flips *resolved to true. Fail-open:
// errors are swallowed, leaving the placeholder name and *resolved unchanged.
func resolveMetadata(ctx context.Context, tracker registry.Tracker, in CreateInput, displayName *string, resolved *bool) string {
	wm, ok := tracker.(registry.WithMetadata)
	if !ok {
		return ""
	}
	mctx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()
	meta, err := wm.ResolveMetadata(mctx, in.URL, nil)
	if err != nil || meta == nil {
		return ""
	}
	if in.DisplayName == "" && meta.Title != "" {
		*displayName = meta.Title
		*resolved = true
	}
	return meta.ImageURL
}

// CanonicalTopicURL rewrites a mirror-host topic URL onto the tracker's
// canonical (default) domain so the same topic added via different mirrors
// dedups to one row. The active mirror is a fetch-time concern
// (effectiveDomain) — stored identity stays stable across rotations.
//
// Exported so headless creators (e.g. the Sonarr poller) can canonicalize a
// raw URL themselves before a store lookup, not only at persist time inside
// BuildAndCreate — otherwise a mirror-host URL would miss a dedup pre-check
// against a row already stored under the canonical host.
func CanonicalTopicURL(tr registry.Tracker, rawURL string) string {
	wd, ok := tr.(registry.WithDomains)
	if !ok || len(wd.Domains()) == 0 {
		return rawURL
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return rawURL
	}
	canonical := wd.Domains()[0]
	if strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.") ==
		strings.TrimPrefix(strings.ToLower(canonical), "www.") {
		return rawURL
	}
	u.Scheme = "https"
	u.Host = canonical
	return u.String()
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
