package repo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditEntry is one row in the audit_log table.
type AuditEntry struct {
	ID         int64
	UserID     *uuid.UUID
	Actor      string
	Action     string
	TargetType string
	TargetID   string
	Result     string
	IP         string
	UserAgent  string
	Details    map[string]any
	CreatedAt  time.Time
}

// Audit repository.
type Audit struct {
	pool *pgxpool.Pool
}

// NewAudit constructs the repository.
func NewAudit(pool *pgxpool.Pool) *Audit {
	return &Audit{pool: pool}
}

// Insert appends a single audit entry.
func (r *Audit) Insert(ctx context.Context, e *AuditEntry) error {
	if e.Result == "" {
		return errors.New("audit result is required")
	}
	var details []byte
	if e.Details != nil {
		details, _ = json.Marshal(e.Details)
	}
	const q = `
INSERT INTO audit_log (user_id, actor, action, target_type, target_id, result, ip, user_agent, details)
VALUES ($1, NULLIF($2,''), $3, NULLIF($4,''), NULLIF($5,''), $6, NULLIF($7,'')::inet, NULLIF($8,''), $9)`
	_, err := r.pool.Exec(ctx, q,
		e.UserID, e.Actor, e.Action, e.TargetType, e.TargetID,
		e.Result, e.IP, e.UserAgent, details,
	)
	return err
}

// List returns the most recent N entries (newest first).
func (r *Audit) List(ctx context.Context, limit int) ([]*AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
SELECT id, user_id, COALESCE(actor,''), action, COALESCE(target_type,''),
       COALESCE(target_id,''), result, COALESCE(host(ip), ''),
       COALESCE(user_agent,''), details, created_at
FROM audit_log
ORDER BY created_at DESC
LIMIT $1`
	rows, err := r.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*AuditEntry, 0, limit)
	for rows.Next() {
		var e AuditEntry
		var uid *uuid.UUID
		var details []byte
		if err := rows.Scan(&e.ID, &uid, &e.Actor, &e.Action, &e.TargetType,
			&e.TargetID, &e.Result, &e.IP, &e.UserAgent, &details, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.UserID = uid
		if len(details) > 0 {
			_ = json.Unmarshal(details, &e.Details)
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}
