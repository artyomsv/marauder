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

// SettingsRepo is the persistence seam (implemented by repo.TrackerSettings).
type SettingsRepo interface {
	List(ctx context.Context) ([]repo.TrackerSetting, error)
	Upsert(ctx context.Context, trackerName, activeDomain string, customDomains []string) error
}

// Store caches tracker domain configuration in memory.
type Store struct {
	mu         sync.RWMutex
	persistMu  sync.Mutex // serializes Upsert calls from Set and ReportFailure
	cfg        map[string]registry.DomainConfig
	lastRotate map[string]time.Time
	settings   SettingsRepo
	log        zerolog.Logger
	onRotate   func(tracker, from, to string)
	now        func() time.Time
}

// New constructs a Store backed by settings for persistence. Call Load once
// at boot to populate the in-memory cache before serving Resolve/Get.
func New(settings SettingsRepo, log zerolog.Logger) *Store {
	return &Store{
		cfg:        map[string]registry.DomainConfig{},
		lastRotate: map[string]time.Time{},
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

// ReportFailure rotates the tracker's active domain to the next candidate
// in the ring (known domains + custom domains), at most once per
// RotateCooldown. Persistence failure is logged, the in-memory rotation
// still applies (fail-open: next boot reloads the old value, worst case
// one extra rotation). No-op for unknown trackers, trackers without the
// WithDomains capability, and rings of length < 2.
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
	if last, ok := s.lastRotate[trackerName]; ok && s.now().Sub(last) < RotateCooldown {
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
	s.lastRotate[trackerName] = s.now()
	hook := s.onRotate
	s.mu.Unlock()

	metrics.TrackerDomainRotations.WithLabelValues(trackerName).Inc()
	s.log.Warn().Str("tracker", trackerName).Str("from", from).Str("to", to).
		Msg("tracker domain rotated after network failures")

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
