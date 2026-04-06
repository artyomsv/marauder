// Package middleware collects the HTTP middlewares used by the API.
package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/auth"
	"github.com/artyomsv/marauder/backend/internal/problem"
)

type ctxKey string

const (
	// CtxClaims is the context key for the parsed JWT claims.
	CtxClaims ctxKey = "claims"
	// CtxRequestID is the context key for the current request's ID.
	CtxRequestID ctxKey = "reqid"
)

// RequestID populates X-Request-ID (generating one if absent) and stores it
// on the context.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), CtxRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Logger wraps every request in a zerolog event.
func Logger(log zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			defer func() {
				reqID, _ := r.Context().Value(CtxRequestID).(string)
				log.Info().
					Str("method", r.Method).
					Str("path", r.URL.Path).
					Int("status", ww.Status()).
					Int("bytes", ww.BytesWritten()).
					Dur("dur", time.Since(start)).
					Str("remote", r.RemoteAddr).
					Str("req_id", reqID).
					Msg("request")
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

// Recover turns a panic into a 500 response with a trace ID.
func Recover(log zerolog.Logger, baseURL string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error().Interface("panic", rec).Str("path", r.URL.Path).Msg("panic")
					problem.Write(w, r, baseURL, problem.ErrInternal("an unexpected error occurred"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeaders sets a conservative baseline of HTTP security headers.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), interest-cohort=()")
		next.ServeHTTP(w, r)
	})
}

// RequireAuth returns a middleware that rejects requests missing a valid
// access token. It places the parsed claims onto the request context.
func RequireAuth(mgr *auth.Manager, baseURL string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hdr := r.Header.Get("Authorization")
			if hdr == "" || !strings.HasPrefix(hdr, "Bearer ") {
				problem.Write(w, r, baseURL, problem.ErrUnauthorized("missing bearer token"))
				return
			}
			raw := strings.TrimPrefix(hdr, "Bearer ")
			claims, err := mgr.Parse(raw)
			if err != nil {
				problem.Write(w, r, baseURL, problem.ErrUnauthorized("invalid token: "+err.Error()))
				return
			}
			ctx := context.WithValue(r.Context(), CtxClaims, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAdmin chains after RequireAuth and checks the role claim.
func RequireAdmin(baseURL string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := r.Context().Value(CtxClaims).(*auth.Claims)
			if !ok || claims == nil || claims.Role != "admin" {
				problem.Write(w, r, baseURL, problem.ErrForbidden("admin role required"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MetricsToken is a one-shot bearer-token gate for the /metrics endpoint.
func MetricsToken(expected string, baseURL string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if expected == "" {
				problem.Write(w, r, baseURL, problem.ErrNotFound("metrics disabled"))
				return
			}
			hdr := r.Header.Get("Authorization")
			if !strings.HasPrefix(hdr, "Bearer ") || strings.TrimPrefix(hdr, "Bearer ") != expected {
				problem.Write(w, r, baseURL, problem.ErrUnauthorized("invalid metrics token"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClaimsFromContext is a convenience for handlers.
func ClaimsFromContext(ctx context.Context) *auth.Claims {
	if c, ok := ctx.Value(CtxClaims).(*auth.Claims); ok {
		return c
	}
	return nil
}
