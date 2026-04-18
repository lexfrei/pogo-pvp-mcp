// Package debug exposes a small HTTP surface for introspection: a
// readiness probe, an on-demand gamemaster refresh trigger, and a
// lightweight gamemaster summary. The server is wired in by the
// cobra serve command only when --http-port is non-zero.
package debug

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
)

// jsonContentType is the content-type header value used for all JSON
// responses produced by this handler.
const jsonContentType = "application/json"

// NewHandler returns the http.Handler that multiplexes the debug
// endpoints:
//
//	GET  /healthz           — 200 when gamemaster is loaded, 503 otherwise
//	POST /refresh           — triggers a synchronous upstream refresh
//	GET  /debug/gamemaster  — summary of the currently loaded gamemaster
//
// Unknown paths return 404.
func NewHandler(manager *gamemaster.Manager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthzHandler(manager))
	mux.HandleFunc("/refresh", refreshHandler(manager))
	mux.HandleFunc("/debug/gamemaster", gamemasterInfoHandler(manager))

	return mux
}

// healthzHandler returns 200 when the manager has a loaded snapshot,
// 503 otherwise.
func healthzHandler(manager *gamemaster.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if manager.Current() == nil {
			http.Error(w, "gamemaster not loaded", http.StatusServiceUnavailable)

			return
		}

		w.Header().Set("Content-Type", jsonContentType)
		w.WriteHeader(http.StatusOK)

		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}
}

// refreshHandler triggers a synchronous upstream refresh on POST.
// GET and other verbs return 405. A client-side cancellation
// (context.Canceled) maps to 503 Service Unavailable rather than
// the default 500 so observability dashboards do not treat client
// disconnects as server errors.
func refreshHandler(manager *gamemaster.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

			return
		}

		err := manager.Refresh(r.Context())
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, context.Canceled) {
				status = http.StatusServiceUnavailable
			}

			http.Error(w, fmt.Sprintf("refresh failed: %v", err), status)

			return
		}

		w.Header().Set("Content-Type", jsonContentType)
		w.WriteHeader(http.StatusOK)

		_, _ = w.Write([]byte(`{"status":"refreshed"}`))
	}
}

// gamemasterInfoHandler emits a small JSON summary describing the
// currently loaded gamemaster: Pokémon / move counts and the upstream
// version string. Useful for smoke-checking a running server.
func gamemasterInfoHandler(manager *gamemaster.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		snapshot := manager.Current()
		if snapshot == nil {
			http.Error(w, "gamemaster not loaded", http.StatusServiceUnavailable)

			return
		}

		payload := map[string]any{
			"pokemon_count": len(snapshot.Pokemon),
			"moves_count":   len(snapshot.Moves),
			"version":       snapshot.Version,
		}

		w.Header().Set("Content-Type", jsonContentType)

		err := json.NewEncoder(w).Encode(payload)
		if err != nil {
			slog.Error("failed to encode debug gamemaster payload",
				slog.String("error", err.Error()))
		}
	}
}
