package handlers

import (
	"net/http"
	"runtime"

	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/scheduler"
	"github.com/artyomsv/marauder/backend/internal/version"
)

// System handles /system/*.
type System struct {
	BaseURL   string
	Scheduler *scheduler.Scheduler
}

// Info handles GET /system/info.
func (h *System) Info(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":   version.Current(),
		"trackers":  listPluginNames(registry.ListTrackers()),
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
			"goroutines":     runtime.NumGoroutine(),
			"alloc_bytes":    ms.Alloc,
			"sys_bytes":      ms.Sys,
			"heap_objects":   ms.HeapObjects,
			"gc_cycles":      ms.NumGC,
		},
		"version": version.Current(),
	})
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
