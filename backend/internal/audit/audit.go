// Package audit provides a tiny logger that handlers call to record
// security-relevant events. It is a thin wrapper over repo.Audit so
// every call site has the same shape and we never accidentally write
// raw SQL from a handler.
package audit

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/db/repo"
)

// Logger writes audit entries asynchronously so the request path is
// never blocked by an audit insert. The buffer drops on overflow with
// a logged warning rather than blocking the producer.
type Logger struct {
	repo *repo.Audit
	log  zerolog.Logger
	ch   chan *repo.AuditEntry
}

// NewLogger spawns a single background goroutine that drains the buffer.
func NewLogger(ctx context.Context, r *repo.Audit, log zerolog.Logger) *Logger {
	l := &Logger{
		repo: r,
		log:  log.With().Str("component", "audit").Logger(),
		ch:   make(chan *repo.AuditEntry, 256),
	}
	go l.run(ctx)
	return l
}

func (l *Logger) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-l.ch:
			if err := l.repo.Insert(ctx, e); err != nil {
				l.log.Warn().Err(err).Str("action", e.Action).Msg("audit insert failed")
			}
		}
	}
}

// Record schedules an audit entry for insertion. Drops if the buffer
// is full to keep the request path non-blocking.
func (l *Logger) Record(e *repo.AuditEntry) {
	select {
	case l.ch <- e:
	default:
		l.log.Warn().Str("action", e.Action).Msg("audit buffer full; dropping entry")
	}
}

// FromRequest extracts IP and User-Agent from an *http.Request.
func FromRequest(r *http.Request) (ip, ua string) {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		ip = strings.TrimSpace(parts[0])
	} else if i := strings.LastIndex(r.RemoteAddr, ":"); i >= 0 {
		ip = r.RemoteAddr[:i]
	} else {
		ip = r.RemoteAddr
	}
	ua = r.UserAgent()
	return
}

// Helper constructors for the most common shapes ----------------------

// LoginSuccess records a successful login.
func (l *Logger) LoginSuccess(userID uuid.UUID, username, ip, ua string) {
	l.Record(&repo.AuditEntry{
		UserID: &userID, Actor: username,
		Action: "auth.login", Result: "success",
		IP: ip, UserAgent: ua,
	})
}

// LoginFailure records a failed login attempt without revealing whether
// the username existed.
func (l *Logger) LoginFailure(username, ip, ua string) {
	l.Record(&repo.AuditEntry{
		Actor:  username,
		Action: "auth.login", Result: "failure",
		IP: ip, UserAgent: ua,
	})
}

// Logout records a session revocation.
func (l *Logger) Logout(userID *uuid.UUID, ip, ua string) {
	l.Record(&repo.AuditEntry{
		UserID: userID,
		Action: "auth.logout", Result: "success",
		IP: ip, UserAgent: ua,
	})
}

// Generic creates an arbitrary audit entry.
func (l *Logger) Generic(userID *uuid.UUID, action, targetType, targetID, result string, details map[string]any) {
	l.Record(&repo.AuditEntry{
		UserID: userID,
		Action: action, TargetType: targetType, TargetID: targetID,
		Result: result, Details: details,
	})
}
