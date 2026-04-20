// Package httpmw implements net/http middleware wrappers that protect
// the public MCP HTTP listener: panic recovery, X-Forwarded-For-aware
// client IP resolution, token-bucket rate limiting per client IP, and
// a body-size cap. Each middleware is composable via the standard
// func(http.Handler) http.Handler signature so they chain cleanly in
// Chain. Ordering (outer → inner) is Recover → RealIP → RateLimit →
// MaxBytes → downstream handler.
package httpmw

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recover wraps h so panics in downstream handlers surface as a 500
// response and a slog.Error entry (with the stack trace) instead of
// crashing the process. Returns a middleware factory so the logger is
// captured once at wiring time.
//
// The wrapper does NOT attempt to re-invoke the handler on panic — a
// panic is a bug or a resource exhaustion signal, not a retriable
// condition. The client gets a single 500; any subsequent request
// reaches a fresh handler invocation.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}

				// Log with stack trace so operators can triage.
				// The stack from runtime/debug includes the panic
				// frame itself, not just the recover-defer frame.
				logger.Error("http handler panicked",
					slog.Any("panic", rec),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("stack", string(debug.Stack())))

				// If the handler already wrote headers we can't
				// override them; the best we can do is stop. The
				// connection will be closed by the server and the
				// client sees a truncated response. This is rare
				// (handlers typically panic before first write) but
				// possible; log via the first-write path.
				w.WriteHeader(http.StatusInternalServerError)
			}()

			next.ServeHTTP(w, r)
		})
	}
}
