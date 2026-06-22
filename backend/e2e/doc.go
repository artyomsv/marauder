// Package e2e holds Marauder's full-stack end-to-end tests. The tests drive a
// running Marauder stack (and a real qBittorrent) over HTTP and are guarded by
// the "e2e" build tag, so they run only via `go test -tags=e2e ./e2e/...` and
// never during the normal `go test ./...` suite.
package e2e
