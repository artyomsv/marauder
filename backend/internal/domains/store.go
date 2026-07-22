// Package domains holds the runtime per-tracker domain configuration
// (issue #126): which domain each plugin should use and the admin-added
// custom mirrors. It is the registry's DomainResolver backing store and
// the Phase-2 rotation engine. Single-process by design (same assumption
// as sse.Hub); a Redis-backed store is the multi-replica escape hatch.
package domains

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/metrics"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// RotateCooldown is the minimum interval between two automatic rotations
// of the same tracker — a burst of failing topics must not spin the ring
// past the working mirror before it gets a chance to serve a check.
const RotateCooldown = 10 * time.Minute

// RotateFailureThreshold is the number of network-classified failures (within
// RotateFailureWindow) a tracker must accumulate before its active domain is
// rotated. A gate of >1 stops a single transient blip (one 5xx/timeout on one
// topic) from switching the whole tracker onto a mirror; a genuine outage
// trips it quickly because many topics fail in close succession (issue #126).
const (
	RotateFailureThreshold = 2
	RotateFailureWindow    = 5 * time.Minute
)

// SettingsRepo is the persistence seam (implemented by repo.TrackerSettings).
type SettingsRepo interface {
	List(ctx context.Context) ([]repo.TrackerSetting, error)
	Upsert(ctx context.Context, trackerName, activeDomain string, customDomains []string) error
}

// Store caches tracker domain configuration in memory.
type Store struct {
	mu            sync.RWMutex
	persistMu     sync.Mutex // serializes Upsert calls from Set and ReportFailure
	cfg           map[string]registry.DomainConfig
	lastRotate    map[string]time.Time
	failCount     map[string]int       // network failures in the current window, per tracker
	failWindow    map[string]time.Time // start of the current failure window, per tracker
	settings      SettingsRepo
	log           zerolog.Logger
	onRotate      func(tracker, from, to string)
	now           func() time.Time
	beforePersist func() // test seam: invoked after a rotation is cached, before it persists
}

// New constructs a Store backed by settings for persistence. Call Load once
// at boot to populate the in-memory cache before serving Resolve/Get.
func New(settings SettingsRepo, log zerolog.Logger) *Store {
	return &Store{
		cfg:        map[string]registry.DomainConfig{},
		lastRotate: map[string]time.Time{},
		failCount:  map[string]int{},
		failWindow: map[string]time.Time{},
		settings:   settings,
		log:        log,
		now:        time.Now,
	}
}

// SetOnRotate installs the rotation notification hook (wired in main.go).
func (s *Store) SetOnRotate(fn func(tracker, from, to string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onRotate = fn
}

// Load reads all persisted rows into the cache. Called once at boot.
func (s *Store) Load(ctx context.Context) error {
	rows, err := s.settings.List(ctx)
	if err != nil {
		return fmt.Errorf("domains: load: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range rows {
		s.cfg[r.TrackerName] = registry.DomainConfig{Active: r.ActiveDomain, Custom: r.CustomDomains}
	}
	return nil
}

// Resolve implements registry.DomainResolver.
func (s *Store) Resolve(trackerName string) registry.DomainConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg[trackerName]
}

// Get returns the current configuration for the handler layer.
func (s *Store) Get(trackerName string) (active string, custom []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.cfg[trackerName]
	return c.Active, append([]string{}, c.Custom...)
}

// Set persists and caches one tracker's configuration. Upsert calls are
// serialized against ReportFailure's rotation persists via persistMu so the
// two writers can never interleave and clobber each other's DB row.
func (s *Store) Set(ctx context.Context, trackerName, active string, custom []string) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	if err := s.settings.Upsert(ctx, trackerName, active, custom); err != nil {
		return err
	}
	s.mu.Lock()
	s.cfg[trackerName] = registry.DomainConfig{Active: active, Custom: custom}
	s.mu.Unlock()
	return nil
}

// ReportFailure records a network-classified check failure for the tracker and,
// once RotateFailureThreshold failures accumulate within RotateFailureWindow,
// rotates the active domain to the next candidate in the ring (known domains +
// custom domains) — at most once per RotateCooldown. The threshold stops a
// single transient blip from switching the whole tracker onto a mirror.
// Persistence failure is logged, the in-memory rotation still applies (fail-open:
// next boot reloads the old value, worst case one extra rotation). No-op for
// unknown trackers, trackers without the WithDomains capability, and rings of
// length < 2.
func (s *Store) ReportFailure(ctx context.Context, trackerName string) {
	tr := registry.GetTracker(trackerName)
	wd, ok := tr.(registry.WithDomains)
	if !ok {
		return
	}
	s.mu.Lock()
	cur := s.cfg[trackerName]
	ring := append(append([]string{}, wd.Domains()...), cur.Custom...)
	if len(ring) < 2 {
		s.mu.Unlock()
		return
	}
	now := s.now()
	// Cooldown: after a rotation, ignore failures for RotateCooldown so a burst
	// of failing topics doesn't spin the ring past a working mirror.
	if last, ok := s.lastRotate[trackerName]; ok && now.Sub(last) < RotateCooldown {
		s.mu.Unlock()
		return
	}
	// Accumulate failures within a sliding window; only rotate once the
	// threshold is reached so a single transient failure can't strand the tracker.
	if start, ok := s.failWindow[trackerName]; !ok || now.Sub(start) > RotateFailureWindow {
		s.failWindow[trackerName] = now
		s.failCount[trackerName] = 0
	}
	s.failCount[trackerName]++
	if s.failCount[trackerName] < RotateFailureThreshold {
		s.mu.Unlock()
		return
	}
	from := cur.Active
	if from == "" {
		from = ring[0]
	}
	idx := 0
	for i, d := range ring {
		if d == from {
			idx = i
			break
		}
	}
	to := ring[(idx+1)%len(ring)]
	cur.Active = to
	s.cfg[trackerName] = cur
	s.lastRotate[trackerName] = now
	// Reset the failure window: the next rotation needs a fresh threshold.
	s.failCount[trackerName] = 0
	delete(s.failWindow, trackerName)
	hook := s.onRotate
	s.mu.Unlock()

	metrics.TrackerDomainRotations.WithLabelValues(trackerName).Inc()
	s.log.Warn().Str("tracker", trackerName).Str("from", from).Str("to", to).
		Msg("tracker domain rotated after network failures")

	// Test seam: park here (rotation cached, not yet persisted) so a
	// concurrency test can interleave a Store.Set before the persist phase.
	if s.beforePersist != nil {
		s.beforePersist()
	}

	// Persist under persistMu, serialized against Set, and re-read the
	// custom-domain list right before writing so a concurrent admin edit
	// (Store.Set adding/removing a custom domain) can never be clobbered by
	// a stale snapshot captured before the rotation decision (issue #126).
	s.persistMu.Lock()
	s.mu.RLock()
	custom := append([]string{}, s.cfg[trackerName].Custom...)
	s.mu.RUnlock()
	if err := s.settings.Upsert(ctx, trackerName, to, custom); err != nil {
		s.log.Warn().Err(err).Str("tracker", trackerName).Msg("persist rotated domain failed")
	}
	s.persistMu.Unlock()

	if hook != nil {
		hook(trackerName, from, to)
	}
}
