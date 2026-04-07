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
)

// ObserveHTTP is a convenience helper for the logging middleware.
func ObserveHTTP(method, route string, status int, dur time.Duration) {
	s := strconv.Itoa(status)
	HTTPRequestsTotal.WithLabelValues(method, route, s).Inc()
	HTTPRequestDurationSeconds.WithLabelValues(method, route).Observe(dur.Seconds())
}
