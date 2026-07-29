// Package metrics defines the Prometheus collectors used by the backend.
//
// Collectors are registered with the default prometheus registry, so the
// promhttp handler in router.go automatically exposes them at /metrics.
//
// Naming follows the Prometheus convention `marauder_<subsystem>_<name>_<unit>`.
package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// HTTP request metrics ----------------------------------------------------

var (
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "marauder_http_requests_total",
			Help: "Number of HTTP requests, partitioned by method, route, and status.",
		},
		[]string{"method", "route", "status"},
	)

	HTTPRequestDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "marauder_http_request_duration_seconds",
			Help:    "HTTP request duration histogram, partitioned by method and route.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "route"},
	)
)

// Scheduler metrics -------------------------------------------------------

var (
	SchedulerRunsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "marauder_scheduler_runs_total",
			Help: "Number of scheduler dispatch ticks, partitioned by result.",
		},
		[]string{"result"}, // "ok" | "error"
	)

	SchedulerTopicChecksTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "marauder_scheduler_topic_checks_total",
			Help: "Number of topic check attempts, partitioned by tracker and result.",
		},
		[]string{"tracker", "result"},
	)

	SchedulerTopicCheckDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "marauder_scheduler_topic_check_duration_seconds",
			Help:    "Topic check duration histogram, partitioned by tracker.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
		},
		[]string{"tracker"},
	)

	// TrackerSearchTotal counts interactive tracker searches (issue #129),
	// partitioned by tracker and outcome.
	TrackerSearchTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "marauder_tracker_search_total",
			Help: "Number of tracker search attempts, partitioned by tracker and result.",
		},
		[]string{"tracker", "result"}, // "ok" | "error" | "no_credentials" | "login_failed"
	)

	TrackerUpdatesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "marauder_tracker_updates_total",
			Help: "Number of detected topic updates, partitioned by tracker.",
		},
		[]string{"tracker"},
	)

	ClientSubmitTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "marauder_client_submit_total",
			Help: "Number of client submission attempts, partitioned by client and result.",
		},
		[]string{"client", "result"},
	)

	// SchedulerEpisodesPerTickCappedTotal counts the number of times a
	// per-episode download loop was terminated by hitting the per-tick
	// cap (config.SchedulerMaxEpisodesPerTick). A non-zero value here is
	// an operator signal that the cap may be too low for a tracker that
	// has built up a large backlog.
	SchedulerEpisodesPerTickCappedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "marauder_scheduler_episodes_per_tick_capped_total",
			Help: "Number of scheduler ticks where the per-episode download loop hit the per-tick cap.",
		},
		[]string{"tracker"},
	)

	// SchedulerReplacedPreviousTotal counts how many previously delivered
	// torrents the "replace previous version" policy (issue #101) removed from
	// a client when a single-release topic was updated, partitioned by client
	// and result ("ok" / "error").
	SchedulerReplacedPreviousTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "marauder_scheduler_replaced_previous_total",
			Help: "Number of previously delivered torrents removed by the replace-on-update policy, partitioned by client and result.",
		},
		[]string{"client", "result"},
	)
)

// Tracker domain metrics --------------------------------------------------

var (
	// TrackerDomainRotations counts automatic domain rotations (issue #126)
	// triggered by ReportFailure, partitioned by tracker. A rising count for
	// a given tracker signals its current mirror is failing checks.
	TrackerDomainRotations = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "marauder_tracker_domain_rotations_total",
			Help: "Number of automatic tracker domain rotations after network failures, partitioned by tracker.",
		},
		[]string{"tracker"},
	)

	// FlareSolverrSessionsTotal counts challenge-solver session lifecycle
	// events by result: "created", "replaced" (the previous one was gone and
	// has been destroyed), "error" (sessions.create failed), and "degraded" (a
	// fetch had to run without a session).
	//
	// "degraded" is the important one: without a session, FlareSolverr
	// re-solves the Cloudflare challenge on every request (10-20s) and
	// serialises them, so concurrent checks queue past the scheduler's budget
	// and fail. That is the 2026-07-30 RuTracker outage, and it was invisible
	// because nothing measured it. A non-zero rate here means checks are
	// silently slow.
	FlareSolverrSessionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "marauder_flaresolverr_sessions_total",
			Help: "Challenge-solver session lifecycle events, partitioned by result (created/replaced/error/degraded).",
		},
		[]string{"result"},
	)
)

// Notifier metrics --------------------------------------------------------

var (
	// NotificationsSentTotal counts notifier dispatch attempts by result.
	NotificationsSentTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "marauder_notifications_sent_total",
			Help: "Notification dispatch attempts by notifier and result.",
		},
		[]string{"notifier", "result"},
	)
)

// Progress watcher metrics -----------------------------------------------

var (
	// ProgressCompletionsTotal counts download.completed events the watcher fired.
	ProgressCompletionsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "marauder_progress_completions_total",
		Help: "Total downloads the progress watcher detected as finished.",
	})
)

// Sonarr integration metrics ---------------------------------------------

var (
	// SonarrPollsTotal counts Sonarr history-poll ticks by result.
	SonarrPollsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "marauder_sonarr_polls_total",
			Help: "Sonarr history-poll ticks, partitioned by result.",
		},
		[]string{"result"}, // "ok" | "error"
	)

	// SonarrTopicsCreatedTotal counts topics auto-created from Sonarr grabs.
	SonarrTopicsCreatedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "marauder_sonarr_topics_created_total",
			Help: "Topics auto-created from Sonarr grab history.",
		},
	)

	// SonarrRecordsProcessedTotal counts grab-history records by outcome.
	SonarrRecordsProcessedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "marauder_sonarr_records_processed_total",
			Help: "Sonarr grab-history records processed, partitioned by outcome.",
		},
		[]string{"outcome"}, // created|updated|duplicate|no_tracker|disallowed|error
	)
)

// Client metrics ---------------------------------------------------------

var (
	// ClientCategoriesFailOpenTotal counts times the category-list fetch for a
	// client failed and the GET /clients/{id}/categories endpoint fell open to
	// an empty list (the AddTopic field degrades to free-text). A persistently
	// non-zero value for a client signals its category fetch is quietly broken.
	ClientCategoriesFailOpenTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "marauder_client_categories_fail_open_total",
			Help: "Times a client category list fetch failed and degraded to free-text, by client.",
		},
		[]string{"client"},
	)
)

// SSE metrics ------------------------------------------------------------

var (
	// SSEDroppedFramesTotal counts SSE frames dropped because a subscriber's
	// buffer was full (slow client) — drop-on-full keeps the hub non-blocking.
	SSEDroppedFramesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "marauder_sse_dropped_frames_total",
		Help: "SSE frames dropped due to a full subscriber buffer.",
	})
)

// ObserveHTTP is a convenience helper for the logging middleware.
func ObserveHTTP(method, route string, status int, dur time.Duration) {
	s := strconv.Itoa(status)
	HTTPRequestsTotal.WithLabelValues(method, route, s).Inc()
	HTTPRequestDurationSeconds.WithLabelValues(method, route).Observe(dur.Seconds())
}
