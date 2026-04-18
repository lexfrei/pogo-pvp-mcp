package rankings_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/rankings"
)

// minimalRankings is a trimmed rankings payload carrying one entry with
// every field the parser cares about.
const minimalRankings = `[
  {
    "speciesId": "medicham",
    "speciesName": "Medicham",
    "rating": 700,
    "score": 95.2,
    "moveset": ["COUNTER", "ICE_PUNCH", "POWER_UP_PUNCH"],
    "matchups": [{"opponent": "azumarill", "rating": 650}, {"opponent": "sableye", "rating": 580}],
    "counters": [{"opponent": "trevenant", "rating": 320}],
    "stats": {"product": 2103, "atk": 106.9, "def": 139.4, "hp": 141}
  }
]`

func newTestRankingsServer(t *testing.T, payload string) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/all/overall/rankings-1500.json" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(server.Close)

	return server
}

func TestManager_GetFetchesAndCaches(t *testing.T) {
	t.Parallel()

	server := newTestRankingsServer(t, minimalRankings)

	mgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  server.URL,
		LocalDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	entries, err := mgr.Get(t.Context(), 1500)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("entries count = %d, want 1", len(entries))
	}

	entry := entries[0]
	if entry.SpeciesID != "medicham" {
		t.Errorf("SpeciesID = %q, want medicham", entry.SpeciesID)
	}
	if entry.Rating != 700 {
		t.Errorf("Rating = %d, want 700", entry.Rating)
	}
	if entry.Stats.Product != 2103 {
		t.Errorf("Stats.Product = %d, want 2103", entry.Stats.Product)
	}
	if len(entry.Moveset) != 3 {
		t.Errorf("Moveset len = %d, want 3", len(entry.Moveset))
	}
	if len(entry.Matchups) != 2 {
		t.Errorf("Matchups len = %d, want 2", len(entry.Matchups))
	}
}

func TestManager_GetReturnsCachedOnSecondCall(t *testing.T) {
	t.Parallel()

	hits := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(minimalRankings))
	}))
	t.Cleanup(server.Close)

	mgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  server.URL,
		LocalDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.Get(t.Context(), 1500)
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}

	_, err = mgr.Get(t.Context(), 1500)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if hits != 1 {
		t.Errorf("server hits = %d, want 1 (second call should hit in-memory cache)", hits)
	}
}

func TestManager_GetUnknownCap(t *testing.T) {
	t.Parallel()

	server := newTestRankingsServer(t, minimalRankings)

	mgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  server.URL,
		LocalDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.Get(t.Context(), 99999)
	if !errors.Is(err, rankings.ErrUnsupportedCap) {
		t.Errorf("error = %v, want wrapping ErrUnsupportedCap", err)
	}
}

func TestManager_PersistsAndReloads(t *testing.T) {
	t.Parallel()

	server := newTestRankingsServer(t, minimalRankings)
	dir := t.TempDir()

	mgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  server.URL,
		LocalDir: dir,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.Get(t.Context(), 1500)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Fresh manager pointing at an unreachable upstream — must read from disk.
	mgr2, err := rankings.NewManager(rankings.Config{
		BaseURL:  "http://example.invalid",
		LocalDir: dir,
	})
	if err != nil {
		t.Fatalf("NewManager (second): %v", err)
	}

	entries, err := mgr2.Get(t.Context(), 1500)
	if err != nil {
		t.Fatalf("Get from disk: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("entries count = %d, want 1 (from disk)", len(entries))
	}
}

func TestManager_InvalidConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  rankings.Config
	}{
		{"empty base url", rankings.Config{LocalDir: t.TempDir()}},
		{"empty local dir", rankings.Config{BaseURL: "http://example.com"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := rankings.NewManager(tc.cfg)
			if !errors.Is(err, rankings.ErrInvalidConfig) {
				t.Errorf("error = %v, want wrapping ErrInvalidConfig", err)
			}
		})
	}
}

func TestManager_UpstreamError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	mgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  server.URL,
		LocalDir: filepath.Join(t.TempDir(), "rankings"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.Get(t.Context(), 1500)
	if err == nil {
		t.Fatal("expected error for upstream 500")
	}
}
