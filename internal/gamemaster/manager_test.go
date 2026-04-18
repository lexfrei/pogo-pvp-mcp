package gamemaster_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
)

// minimalGamemaster is the smallest valid pvpoke gamemaster payload the
// engine parser will accept: document id "gamemaster", one Pokémon, one
// move the Pokémon uses.
const minimalGamemaster = `{
  "id": "gamemaster",
  "timestamp": "2026-04-18 00:00:00",
  "pokemon": [
    {
      "dex": 1,
      "speciesId": "bulbasaur",
      "speciesName": "Bulbasaur",
      "baseStats": {"atk": 118, "def": 111, "hp": 128},
      "types": ["grass", "poison"],
      "fastMoves": ["VINE_WHIP"],
      "chargedMoves": ["SLUDGE_BOMB"],
      "released": true
    }
  ],
  "moves": [
    {"moveId": "VINE_WHIP", "name": "Vine Whip", "type": "grass", "power": 5, "energy": 0, "energyGain": 8, "cooldown": 1000, "turns": 2},
    {"moveId": "SLUDGE_BOMB", "name": "Sludge Bomb", "type": "poison", "power": 80, "energy": 50, "energyGain": 0, "cooldown": 500}
  ]
}`

func newTestServer(t *testing.T, payload string) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(server.Close)

	return server
}

func TestManager_RefreshParsesUpstream(t *testing.T) {
	t.Parallel()

	server := newTestServer(t, minimalGamemaster)

	mgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    server.URL,
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	err = mgr.Refresh(t.Context())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	gm := mgr.Current()
	if gm == nil {
		t.Fatal("Current = nil after Refresh")
	}
	if _, ok := gm.Pokemon["bulbasaur"]; !ok {
		t.Error("bulbasaur missing from parsed gamemaster")
	}
	if _, ok := gm.Moves["VINE_WHIP"]; !ok {
		t.Error("VINE_WHIP missing from parsed gamemaster")
	}
}

func TestManager_CurrentNilBeforeRefresh(t *testing.T) {
	t.Parallel()

	mgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    "http://example.invalid",
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if gm := mgr.Current(); gm != nil {
		t.Errorf("Current = %+v, want nil before Refresh", gm)
	}
}

func TestManager_RefreshPersistsLocalCopy(t *testing.T) {
	t.Parallel()

	server := newTestServer(t, minimalGamemaster)
	localPath := filepath.Join(t.TempDir(), "gm.json")

	mgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    server.URL,
		LocalPath: localPath,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	err = mgr.Refresh(t.Context())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// Second manager, no network — must load from local path.
	mgr2, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    "http://example.invalid",
		LocalPath: localPath,
	})
	if err != nil {
		t.Fatalf("NewManager (second): %v", err)
	}

	err = mgr2.LoadLocal()
	if err != nil {
		t.Fatalf("LoadLocal: %v", err)
	}

	gm := mgr2.Current()
	if gm == nil {
		t.Fatal("Current nil after LoadLocal on persisted file")
	}
	if _, ok := gm.Pokemon["bulbasaur"]; !ok {
		t.Error("bulbasaur missing from locally-loaded gamemaster")
	}
}

func TestManager_RefreshETagShortCircuits(t *testing.T) {
	t.Parallel()

	const etag = `"abc123"`
	hits := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++

		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(minimalGamemaster))
	}))
	t.Cleanup(server.Close)

	mgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    server.URL,
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	err = mgr.Refresh(t.Context())
	if err != nil {
		t.Fatalf("first Refresh: %v", err)
	}

	err = mgr.Refresh(t.Context())
	if err != nil {
		t.Fatalf("second Refresh: %v", err)
	}

	if hits != 2 {
		t.Errorf("server hit count = %d, want 2", hits)
	}

	// Both refreshes must leave a non-nil current gamemaster.
	if mgr.Current() == nil {
		t.Error("Current nil after second Refresh despite 304")
	}
}

func TestManager_RefreshUpstreamError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	mgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    server.URL,
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	err = mgr.Refresh(t.Context())
	if err == nil {
		t.Error("Refresh returned nil error on upstream 500")
	}
}

func TestManager_RefreshRespectsContextCancel(t *testing.T) {
	t.Parallel()

	server := newTestServer(t, minimalGamemaster)

	mgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    server.URL,
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err = mgr.Refresh(ctx)
	if err == nil {
		t.Error("Refresh on cancelled context returned nil error")
	}
}
