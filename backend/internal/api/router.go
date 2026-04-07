// Package api wires up the HTTP router.
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/api/handlers"
	"github.com/artyomsv/marauder/backend/internal/api/middleware"
	"github.com/artyomsv/marauder/backend/internal/audit"
	"github.com/artyomsv/marauder/backend/internal/auth"
	"github.com/artyomsv/marauder/backend/internal/config"
	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/scheduler"
)

// Deps is the bag of dependencies handed to NewRouter.
type Deps struct {
	Cfg       *config.Config
	Log       zerolog.Logger
	Pool      *pgxpool.Pool
	Manager   *auth.Manager
	Master    *crypto.MasterKey
	Users     *repo.Users
	Topics    *repo.Topics
	Clients   *repo.Clients
	Notifiers *repo.Notifiers
	Audit     *repo.Audit
	AuditLog  *audit.Logger
	OIDC      *auth.OIDCProvider
	Scheduler *scheduler.Scheduler
}

// NewRouter builds the HTTP handler tree.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()

	// Core middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger(d.Log))
	r.Use(middleware.Recover(d.Log, d.Cfg.PublicBaseURL))
	r.Use(middleware.SecurityHeaders)
	r.Use(chimw.RealIP)
	r.Use(chimw.Heartbeat("/health"))

	// CORS
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   d.Cfg.CORSOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", "X-Request-ID"},
		ExposedHeaders:   []string{"X-Request-ID"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	// Infra endpoints (unversioned)
	r.Get("/ready", func(w http.ResponseWriter, r *http.Request) {
		if err := d.Pool.Ping(r.Context()); err != nil {
			http.Error(w, "db unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	r.With(middleware.MetricsToken(d.Cfg.MetricsToken, d.Cfg.PublicBaseURL)).
		Handle("/metrics", promhttp.Handler())

	// Handler groups
	authH := &handlers.Auth{
		Users:   d.Users,
		Manager: d.Manager,
		Audit:   d.AuditLog,
		OIDC:    d.OIDC,
		BaseURL: d.Cfg.PublicBaseURL,
	}
	topicsH := &handlers.Topics{
		Topics:  d.Topics,
		BaseURL: d.Cfg.PublicBaseURL,
	}
	clientsH := &handlers.Clients{
		Clients: d.Clients,
		Master:  d.Master,
		Audit:   d.AuditLog,
		BaseURL: d.Cfg.PublicBaseURL,
	}
	notifiersH := &handlers.Notifiers{
		Notifiers: d.Notifiers,
		Master:    d.Master,
		BaseURL:   d.Cfg.PublicBaseURL,
	}
	sysH := &handlers.System{BaseURL: d.Cfg.PublicBaseURL, Scheduler: d.Scheduler, Audit: d.Audit}

	r.Route("/api/v1", func(r chi.Router) {
		// Public auth endpoints
		r.Post("/auth/login", authH.Login)
		r.Post("/auth/refresh", authH.Refresh)
		r.Post("/auth/logout", authH.Logout)
		r.Get("/auth/oidc/login", authH.OIDCLogin)
		r.Get("/auth/oidc/callback", authH.OIDCCallback)

		// System info (public but terse)
		r.Get("/system/info", sysH.Info)

		// Authenticated
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAuth(d.Manager, d.Cfg.PublicBaseURL))

			r.Get("/auth/me", authH.Me)
			r.Post("/auth/me/password", authH.ChangePassword)
			r.Get("/system/status", sysH.Status)

			r.Get("/topics", topicsH.List)
			r.Post("/topics", topicsH.Create)
			r.Get("/topics/{id}", topicsH.Get)
			r.Delete("/topics/{id}", topicsH.Delete)
			r.Post("/topics/{id}/pause", topicsH.Pause)
			r.Post("/topics/{id}/resume", topicsH.Resume)

			r.Get("/clients", clientsH.List)
			r.Post("/clients", clientsH.Create)
			r.Get("/clients/{id}", clientsH.Get)
			r.Put("/clients/{id}", clientsH.Update)
			r.Delete("/clients/{id}", clientsH.Delete)
			r.Post("/clients/{id}/test", clientsH.Test)

			r.Get("/notifiers", notifiersH.List)
			r.Post("/notifiers", notifiersH.Create)
			r.Delete("/notifiers/{id}", notifiersH.Delete)
			r.Post("/notifiers/{id}/test", notifiersH.Test)

			// Admin-only
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireAdmin(d.Cfg.PublicBaseURL))
				r.Get("/system/audit", sysH.AuditList)
			})
		})
	})

	return r
}
