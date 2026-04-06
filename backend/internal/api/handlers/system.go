package handlers

import (
	"net/http"

	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/version"
)

// System handles /system/*.
type System struct {
	BaseURL string
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
