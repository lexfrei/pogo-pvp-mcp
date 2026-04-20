// Package httpmw implements net/http middleware wrappers that protect
// the public MCP HTTP listener: panic recovery, baseline security
// headers, X-Forwarded-For-aware client IP resolution, token-bucket
// rate limiting per client IP, and a body-size cap. Each middleware
// is composable via the standard func(http.Handler) http.Handler
// signature so they chain cleanly in Chain. Production ordering
// (outer → inner) is Recover → SecurityHeaders → RealIP → RateLimit
// → MaxBytes → downstream handler.
package httpmw

import (
	"errors"
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
	if logger == nil {
		// Ambiguous contract guard: a nil logger would panic inside
		// the defer on rec!=nil. Fall back to slog.Default so tests
		// passing nil still get the stack trace on an unrelated
		// panic, and production wiring always supplies rt.Logger.
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}

				// http.ErrAbortHandler is the stdlib-documented
				// sentinel for "abort the request without logging".
				// The server framework recognises it specially; we
				// must re-panic so the connection closes without
				// our 500-write or stack-log side effects. Type-
				// assert then errors.Is to satisfy the err113 +
				// errorlint pair simultaneously.
				asErr, isErr := rec.(error)
				if isErr && errors.Is(asErr, http.ErrAbortHandler) {
					panic(rec)
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
