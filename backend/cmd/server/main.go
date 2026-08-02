// Command marauder-server is the main Marauder HTTP backend entrypoint.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/artyomsv/marauder/backend/internal/api"
	"github.com/artyomsv/marauder/backend/internal/audit"
	"github.com/artyomsv/marauder/backend/internal/auth"
	"github.com/artyomsv/marauder/backend/internal/config"
	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/db"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/domains"
	"github.com/artyomsv/marauder/backend/internal/events"
	"github.com/artyomsv/marauder/backend/internal/flaresolverr"
	"github.com/artyomsv/marauder/backend/internal/logging"
	"github.com/artyomsv/marauder/backend/internal/notify"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/progress"
	"github.com/artyomsv/marauder/backend/internal/retention"
	"github.com/artyomsv/marauder/backend/internal/scheduler"
	"github.com/artyomsv/marauder/backend/internal/sonarr"
	"github.com/artyomsv/marauder/backend/internal/sse"
	"github.com/artyomsv/marauder/backend/internal/version"

	// Register bundled plugins via blank imports. This activates their
	// init() functions which self-register with the plugin registry.
	_ "github.com/artyomsv/marauder/backend/internal/plugins/clients/deluge"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/clients/downloadfolder"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/clients/qbittorrent"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/clients/transmission"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/clients/utorrent"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/notifiers/email"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/notifiers/pushover"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/notifiers/telegram"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/notifiers/webhook"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/anidub"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/anilibria"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/genericmagnet"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/generictorrentfile"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/kinozal"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/lostfilm"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/newznab"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/nnmclub"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/rutor"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/rutracker"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/tapochek"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/toloka"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/torznab"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := logging.Setup(cfg.LogLevel, cfg.LogJSON)
	logger.Info().
		Interface("version", version.Current()).
		Str("addr", cfg.HTTPAddr).
		Bool("oidc_enabled", cfg.OIDCEnabled).
		Msg("marauder starting")

	master, err := crypto.LoadMasterKey(cfg.MasterKeyB64)
	if err != nil {
		return fmt.Errorf("master key: %w", err)
	}

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if cfg.DBMigrateOnBoot {
		logger.Info().Msg("running database migrations")
		if err := db.Migrate(rootCtx, cfg.DatabaseURL); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}

	pool, err := db.Open(rootCtx, cfg)
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}
	defer pool.Close()

	// Repositories
	users := repo.NewUsers(pool)
	refresh := repo.NewRefreshTokens(pool)
	keys := repo.NewJWTKeys(pool)
	topicsRepo := repo.NewTopics(pool)
	clientsRepo := repo.NewClients(pool)
	notifiersRepo := repo.NewNotifiers(pool)
	credsRepo := repo.NewTrackerCredentials(pool)
	deliveriesRepo := repo.NewDeliveries(pool)
	sonarrInstancesRepo := repo.NewSonarrInstances(pool)
	auditRepo := repo.NewAudit(pool)
	auditLogger := audit.NewLogger(rootCtx, auditRepo, logger)

	// Tracker domain overrides (issue #126): admin-configured active/custom
	// domains per tracker, loaded once at boot and installed as the
	// process-wide registry.DomainResolver.
	trackerSettingsRepo := repo.NewTrackerSettings(pool)
	domainStore := domains.New(trackerSettingsRepo, logger)
	if err := domainStore.Load(rootCtx); err != nil {
		logger.Warn().Err(err).Msg("tracker domain settings load failed; using plugin defaults")
	}
	registry.SetDomainResolver(domainStore.Resolve)

	// Cloudflare clearance: RuTracker sits behind a managed challenge on every
	// /forum/ path. FlareSolverr solves it once and we replay the resulting
	// cf_clearance + User-Agent from ordinary Go requests.
	//
	// This deliberately does NOT install the solver as a fetch transport. An
	// earlier revision did, on the belief that cf_clearance was bound to the
	// TLS fingerprint of the client that earned it and so could not leave the
	// browser. Measured against live RuTracker on 2026-08-01, the binding is to
	// the User-Agent, not the fingerprint: replayed with the browser's own UA
	// the cookie is accepted from plain net/http on every gated path. Proxying
	// each fetch through the browser therefore bought nothing and cost a great
	// deal — a browser round-trip per request, serialised solves, no binary
	// bodies (so no .torrent), and no login (so no search).
	if cfg.FlareSolverrURL != "" {
		solver := flaresolverr.New(cfg.FlareSolverrURL, cfg.FlareSolverrTimeout)
		// Session-less solves silently reinstate the per-request challenge
		// re-solve that caused the 2026-07-30 outage, and that was invisible
		// because nothing reported it. Log it (the metric counterpart is
		// marauder_flaresolverr_sessions_total{result="degraded"}).
		solver.OnDegraded = func(err error) {
			logger.Warn().Err(err).
				Msg("flaresolverr session unavailable; solving without one (slower)")
		}
		registry.SetClearanceProvider(solver)
		// The minter keeps one long-lived browser session on the solver so a
		// re-mint reuses an already-cleared context rather than starting a
		// fresh challenge. Release it on shutdown so restarts don't accumulate
		// orphaned browsers.
		defer func() {
			shCtx, shCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer shCancel()
			if err := solver.Close(shCtx); err != nil {
				logger.Warn().Err(err).Msg("flaresolverr session cleanup failed")
			}
		}()
		logger.Info().
			Str("url", cfg.FlareSolverrURL).
			Dur("timeout", cfg.FlareSolverrTimeout).
			Msg("flaresolverr clearance provider enabled")

		// Warm the clearance in the background.
		//
		// A cold solve takes ~10s, which exceeds the tracker-search handler's
		// per-tracker budget — so without this the FIRST search after every
		// restart fails with "tracker login failed" and only the second
		// succeeds. Warming costs one solve per challenge-gated tracker at
		// boot; the cookie then lasts about a year.
		//
		// Detached from any request: it gets its own generous deadline and
		// failures are logged, never fatal. A tracker whose warm-up fails still
		// mints lazily on first use, exactly as before.
		go func() {
			for _, t := range registry.ListTrackers() {
				probe, ok := t.(registry.WithChallengeProbe)
				if !ok {
					continue
				}
				ctx, cancel := context.WithTimeout(rootCtx, cfg.FlareSolverrTimeout+15*time.Second)
				start := time.Now()
				_, err := registry.ClearanceFor(ctx, probe.ChallengeProbeURL())
				cancel()
				if err != nil {
					logger.Warn().Str("tracker", t.Name()).Err(err).
						Msg("cloudflare clearance warm-up failed; will retry on first use")
					continue
				}
				logger.Info().Str("tracker", t.Name()).
					Dur("took", time.Since(start)).
					Msg("cloudflare clearance warmed")
			}
		}()
	}

	// Optional OIDC provider (nil when MARAUDER_OIDC_ENABLED=false)
	oidcProvider, err := auth.NewOIDCProvider(rootCtx, cfg)
	if err != nil {
		return fmt.Errorf("oidc provider: %w", err)
	}
	if oidcProvider != nil {
		logger.Info().Str("issuer", cfg.OIDCIssuer).Msg("OIDC enabled")
	}

	// Auth manager (issues/validates JWTs)
	mgr, err := auth.NewManager(rootCtx, auth.ManagerConfig{
		Issuer:     cfg.JWTIssuer,
		Audience:   cfg.JWTAudience,
		AccessTTL:  cfg.AccessTokenTTL,
		RefreshTTL: cfg.RefreshTokenTTL,
		Master:     master,
		KeysRepo:   keys,
		TokensRepo: refresh,
	})
	if err != nil {
		return fmt.Errorf("auth manager: %w", err)
	}

	// Bootstrap the first admin if configured
	if cfg.InitialAdminUser != "" && cfg.InitialAdminPass != "" {
		if err := ensureAdmin(rootCtx, users, cfg.InitialAdminUser, cfg.InitialAdminPass); err != nil {
			logger.Warn().Err(err).Msg("initial admin bootstrap failed")
		}
	}

	// Scheduler
	topicEventsRepo := repo.NewTopicEvents(pool)
	disp := notify.New(notifiersRepo, master, logger)

	// Notify the initial admin when the domain store auto-rotates a tracker
	// to a mirror after repeated failures (issue #126 Phase 2). The hook fires
	// synchronously from a scheduler worker goroutine, so the notification I/O
	// (DB lookup + third-party notifier sends) runs in its own goroutine to
	// avoid blocking the check loop (bulkhead).
	domainStore.SetOnRotate(func(tracker, from, to string) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			admin, aerr := users.GetInitialAdmin(ctx)
			if aerr != nil || admin == nil {
				logger.Warn().Err(aerr).Msg("domain rotation: no admin to notify")
				return
			}
			disp.Send(ctx, admin.ID, string(events.CheckFailed), domain.Message{
				Title: fmt.Sprintf("Tracker %s switched to mirror %s", tracker, to),
				Body:  fmt.Sprintf("Checks against %s were failing; Marauder now uses %s. Revert or adjust under Settings → Tracker domains.", from, to),
			})
		}()
	})

	hub := sse.NewHub(logger)
	tickets := sse.NewTicketStore()
	bus := events.New(topicEventsRepo, disp, hub, logger) // hub is the live SSE publisher
	sch := scheduler.New(cfg, logger, topicsRepo, clientsRepo, credsRepo, deliveriesRepo, master, bus, domainStore)
	go func() {
		if err := sch.Start(rootCtx); err != nil {
			logger.Error().Err(err).Msg("scheduler exited with error")
		}
	}()

	// Progress watcher: detects when in-flight deliveries finish downloading
	// and emits download.completed events. Gated by MARAUDER_PROGRESS_WATCHER_ENABLED.
	if cfg.ProgressWatcherEnabled {
		watcher := progress.New(
			deliveriesRepo, clientsRepo, master, bus,
			progress.Config{PollInterval: cfg.ProgressPollInterval, PublicBaseURL: cfg.PublicBaseURL},
			logger,
		)
		if err := watcher.Start(rootCtx); err != nil {
			logger.Error().Err(err).Msg("progress watcher failed to start")
		}
	}

	// History retention: topic_events is append-only, so without this the
	// table (and the per-topic timeline built on it) grows forever.
	// MARAUDER_EVENTS_RETENTION_DAYS=0 keeps everything.
	if cfg.EventsRetentionDays > 0 {
		pruner := retention.New(
			topicEventsRepo,
			retention.Config{
				MaxAge:   time.Duration(cfg.EventsRetentionDays) * 24 * time.Hour,
				Interval: cfg.EventsPruneInterval,
			},
			logger,
		)
		if err := pruner.Start(rootCtx); err != nil {
			logger.Error().Err(err).Msg("history pruner failed to start")
		}
	}

	// Sonarr integration poller. Self-gates on the DB-stored instances
	// (sonarr_instances.enabled), so it's always started; it no-ops while no
	// instance is enabled. See issues #86, #92.
	sonarrPoller := sonarr.New(logger, master, sonarrInstancesRepo, users, topicsRepo, cfg.TrackerHTTPTimeout)
	go func() {
		if err := sonarrPoller.Start(rootCtx); err != nil {
			logger.Error().Err(err).Msg("sonarr poller exited with error")
		}
	}()

	// HTTP server
	router := api.NewRouter(api.Deps{
		Cfg:             cfg,
		Log:             logger,
		Pool:            pool,
		Manager:         mgr,
		Master:          master,
		Users:           users,
		Topics:          topicsRepo,
		Clients:         clientsRepo,
		Notifiers:       notifiersRepo,
		Creds:           credsRepo,
		Deliveries:      deliveriesRepo,
		TopicEvents:     topicEventsRepo,
		SonarrInstances: sonarrInstancesRepo,
		TrackerSettings: domainStore,
		Audit:           auditRepo,
		AuditLog:        auditLogger,
		OIDC:            oidcProvider,
		Scheduler:       sch,
		Emit:            bus.Emit,
		Hub:             hub,
		Tickets:         tickets,
	})
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info().Str("addr", srv.Addr).Msg("http listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case <-rootCtx.Done():
		logger.Info().Msg("shutting down")
	case err := <-serverErr:
		if err != nil {
			return err
		}
	}

	shutdownCtx, scancel := context.WithTimeout(context.Background(), cfg.ShutdownTO)
	defer scancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn().Err(err).Msg("graceful shutdown failed")
	}
	log.Info().Msg("goodbye")
	return nil
}

func ensureAdmin(ctx context.Context, users *repo.Users, username, password string) error {
	n, err := users.Count(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil // someone already exists
	}
	hash, err := crypto.HashPassword(password)
	if err != nil {
		return err
	}
	_, err = users.Create(ctx, &domain.User{
		Username:     username,
		PasswordHash: hash,
		Role:         domain.RoleAdmin,
	})
	return err
}
