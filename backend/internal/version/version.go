// Package version exposes build metadata set at link time via -ldflags.
package version

// These are overwritten at build time:
//
//	go build -ldflags "-X github.com/artyomsv/marauder/backend/internal/version.Version=0.1.0 ..."
var (
	Version   = "0.0.0-dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Info is a small struct returned by /api/v1/system/info.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"buildDate"`
}

// Current returns the current build information.
func Current() Info {
	return Info{Version: Version, Commit: Commit, BuildDate: BuildDate}
}
