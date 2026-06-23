package handlers

import (
	"net/http"
	"runtime"
	"strconv"

	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/scheduler"
	"github.com/artyomsv/marauder/backend/internal/version"
)

// System handles /system/*.
type System struct {
	BaseURL   string
	Scheduler *scheduler.Scheduler
	Audit     *repo.Audit
}

// Info handles GET /system/info.
func (h *System) Info(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":   version.Current(),
		"trackers":  listTrackerInfos(registry.ListTrackers()),
		"clients":   listPluginNames(registry.ListClients()),
		"notifiers": listPluginNames(registry.ListNotifiers()),
	})
}

// Status handles GET /system/status. It returns the live state of the
// scheduler plus a memory snapshot.
func (h *System) Status(w http.ResponseWriter, _ *http.Request) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	hist := []scheduler.RunSummary{}
	paused := false
	if h.Scheduler != nil {
		hist = h.Scheduler.History()
		paused = h.Scheduler.Paused()
	}
	// Last run is the newest entry (or nil)
	var last *scheduler.RunSummary
	if len(hist) > 0 {
		last = &hist[len(hist)-1]
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"scheduler": map[string]any{
			"paused":   paused,
			"last_run": last,
			"history":  hist,
		},
		"runtime": map[string]any{
			"goroutines":   runtime.NumGoroutine(),
			"alloc_bytes":  ms.Alloc,
			"sys_bytes":    ms.Sys,
			"heap_objects": ms.HeapObjects,
			"gc_cycles":    ms.NumGC,
		},
		"version": version.Current(),
	})
}

// Audit list endpoint (admin only).
func (h *System) AuditList(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	entries, err := h.Audit.List(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

type namedPlugin interface {
	Name() string
	DisplayName() string
}

func listPluginNames[T namedPlugin](items []T) []map[string]string {
	out := make([]map[string]string, 0, len(items))
	for _, i := range items {
		out = append(out, map[string]string{
			"name":         i.Name(),
			"display_name": i.DisplayName(),
		})
	}
	return out
}

// listTrackerInfos is like listPluginNames but adds tracker capability
// flags the credentials UI needs by name (the add-credential form picks a
// tracker by name and has no URL to call /trackers/match with).
func listTrackerInfos(items []registry.Tracker) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, t := range items {
		_, interactive := t.(registry.WithInteractiveLogin)
		_, hasCreds := t.(registry.WithCredentials)
		out = append(out, map[string]any{
			"name":                       t.Name(),
			"display_name":               t.DisplayName(),
			"supports_interactive_login": interactive,
			"supports_credentials":       hasCreds,
		})
	}
	return out
}
