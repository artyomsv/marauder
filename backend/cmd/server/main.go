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
	"github.com/artyomsv/marauder/backend/internal/logging"
	"github.com/artyomsv/marauder/backend/internal/scheduler"
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
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/freetorrents"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/genericmagnet"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/generictorrentfile"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/hdclub"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/kinozal"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/lostfilm"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/nnmclub"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/rutor"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/rutracker"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/tapochek"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/toloka"
	_ "github.com/artyomsv/marauder/backend/internal/plugins/trackers/unionpeer"
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
	auditRepo := repo.NewAudit(pool)
	auditLogger := audit.NewLogger(rootCtx, auditRepo, logger)

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
	sch := scheduler.New(cfg, logger, topicsRepo, clientsRepo, master)
	go func() {
		if err := sch.Start(rootCtx); err != nil {
			logger.Error().Err(err).Msg("scheduler exited with error")
		}
	}()

	// HTTP server
	router := api.NewRouter(api.Deps{
		Cfg:       cfg,
		Log:       logger,
		Pool:      pool,
		Manager:   mgr,
		Master:    master,
		Users:     users,
		Topics:    topicsRepo,
		Clients:   clientsRepo,
		Notifiers: notifiersRepo,
		Audit:     auditRepo,
		AuditLog:  auditLogger,
		OIDC:      oidcProvider,
		Scheduler: sch,
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
