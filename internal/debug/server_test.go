package debug_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
	"github.com/lexfrei/pogo-pvp-mcp/internal/debug"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
)

// debugFixtureGamemaster is a trimmed payload so the manager can be
// primed with a valid snapshot for the /healthz ready signal.
const debugFixtureGamemaster = `{
  "id": "gamemaster",
  "timestamp": "2026-04-18 00:00:00",
  "pokemon": [
    {"dex": 1, "speciesId": "bulbasaur", "speciesName": "Bulbasaur",
     "baseStats": {"atk": 118, "def": 111, "hp": 128},
     "types": ["grass", "poison"],
     "fastMoves": ["VINE_WHIP"], "chargedMoves": ["SLUDGE_BOMB"],
     "released": true}
  ],
  "moves": [
    {"moveId": "VINE_WHIP", "name": "Vine Whip", "type": "grass",
     "power": 5, "energy": 0, "energyGain": 8, "cooldown": 1000, "turns": 2},
    {"moveId": "SLUDGE_BOMB", "name": "Sludge Bomb", "type": "poison",
     "power": 80, "energy": 50, "energyGain": 0, "cooldown": 500}
  ]
}`

func newDebugTestManager(t *testing.T) *gamemaster.Manager {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(debugFixtureGamemaster))
	}))
	t.Cleanup(server.Close)

	mgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    server.URL,
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	return mgr
}

func TestHandler_HealthzReadyAfterRefresh(t *testing.T) {
	t.Parallel()

	mgr := newDebugTestManager(t)

	err := mgr.Refresh(t.Context())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	handler := debug.NewHandler(mgr)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("/healthz status = %d, want 200 (gamemaster loaded)", rec.Code)
	}
}

func TestHandler_HealthzUnreadyBeforeRefresh(t *testing.T) {
	t.Parallel()

	mgr := newDebugTestManager(t)

	handler := debug.NewHandler(mgr)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/healthz status = %d, want 503 (no gamemaster)", rec.Code)
	}
}

func TestHandler_RefreshTriggersFetch(t *testing.T) {
	t.Parallel()

	mgr := newDebugTestManager(t)

	handler := debug.NewHandler(mgr)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/refresh", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("/refresh status = %d, want 200", rec.Code)
	}
	if mgr.Current() == nil {
		t.Error("Current nil after /refresh — manager was not primed")
	}
}

func TestHandler_RefreshRejectsGET(t *testing.T) {
	t.Parallel()

	mgr := newDebugTestManager(t)

	handler := debug.NewHandler(mgr)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/refresh", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("/refresh GET status = %d, want 405", rec.Code)
	}
}

func TestHandler_GamemasterInfoAfterRefresh(t *testing.T) {
	t.Parallel()

	mgr := newDebugTestManager(t)

	err := mgr.Refresh(t.Context())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	handler := debug.NewHandler(mgr)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/debug/gamemaster", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/debug/gamemaster status = %d, want 200", rec.Code)
	}

	var payload struct {
		PokemonCount int    `json:"pokemon_count"`
		MovesCount   int    `json:"moves_count"`
		Version      string `json:"version"`
	}

	err = json.NewDecoder(rec.Body).Decode(&payload)
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if payload.PokemonCount != 1 {
		t.Errorf("pokemon_count = %d, want 1", payload.PokemonCount)
	}
	if payload.MovesCount != 2 {
		t.Errorf("moves_count = %d, want 2", payload.MovesCount)
	}
	if payload.Version == "" {
		t.Error("version is empty")
	}
}

func TestHandler_UnknownPathReturns404(t *testing.T) {
	t.Parallel()

	mgr := newDebugTestManager(t)

	handler := debug.NewHandler(mgr)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/not-a-real-endpoint", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown path status = %d, want 404", rec.Code)
	}
}

func TestHandler_RefreshRespectsContextCancel(t *testing.T) {
	t.Parallel()

	mgr := newDebugTestManager(t)

	handler := debug.NewHandler(mgr)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/refresh", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code < 400 {
		t.Errorf("/refresh with cancelled ctx status = %d, want 4xx/5xx",
			rec.Code)
	}

	if !strings.Contains(rec.Body.String(), "cancel") &&
		!strings.Contains(rec.Body.String(), "canceled") &&
		!strings.Contains(rec.Body.String(), "context") {
		t.Logf("/refresh body: %q", rec.Body.String())
	}
}
