package handlers

import (
	"context"
	"net/http"
	"time"
)

// slowOperationBudget bounds a handler that talks to a tracker.
//
// Chosen against two hard ceilings above it: the gateway proxies /api/ with
// proxy_read_timeout 60s, and the server's WriteTimeout is 30s. 45s sits under
// nginx while leaving room for a Cloudflare clearance mint (measured 10-55s
// against RuTracker), and a healthy tracker answers in one or two seconds, so
// this only ever bites when something is genuinely wrong.
const slowOperationBudget = 45 * time.Second

// slowOperation prepares a handler that can legitimately outrun the server's
// WriteTimeout, and returns the context its work must use.
//
// Why this exists: a handler that exceeds WriteTimeout does not return a slow
// response — it returns NO valid HTTP response. Go closes the connection
// mid-write, the gateway reports "upstream prematurely closed connection" and
// serves its own HTML 502, and the SPA fails with
// `Unexpected token '<' ... is not valid JSON`. That is exactly what the
// credential Test button did once tracker login could involve a clearance mint:
// the handler finished at 30.002s against a 30s WriteTimeout.
//
// Two things are therefore needed, and neither alone is enough:
//
//   - the write deadline is pushed out, so a legitimately slow answer can still
//     be written (same mechanism the SSE handler uses to stream indefinitely);
//   - the work is bounded by a deadline strictly below that, so the handler is
//     guaranteed to finish and produce a real JSON error rather than dangle.
//
// The returned cancel must be deferred by the caller.
func slowOperation(w http.ResponseWriter, r *http.Request, budget time.Duration) (context.Context, context.CancelFunc) {
	// Give the write deadline headroom over the work budget so the error
	// response itself is never the thing that gets truncated. Failure is
	// ignored on purpose: ResponseWriters that do not support deadlines (an
	// httptest recorder, a wrapped writer) must not break the handler — they
	// simply keep whatever deadline they had.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(budget + 15*time.Second))
	return context.WithTimeout(r.Context(), budget)
}
