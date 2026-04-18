package tools_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/rankings"
	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
)

const metaRankingsFixture = `[
  {"speciesId": "a", "speciesName": "A", "rating": 900, "score": 95.0,
   "moveset": ["M1","M2","M3"],
   "stats": {"product": 2500, "atk": 110, "def": 120, "hp": 160}},
  {"speciesId": "b", "speciesName": "B", "rating": 880, "score": 93.0,
   "moveset": ["M4","M5","M6"],
   "stats": {"product": 2400, "atk": 108, "def": 125, "hp": 150}},
  {"speciesId": "c", "speciesName": "C", "rating": 850, "score": 91.0,
   "moveset": ["M7","M8","M9"],
   "stats": {"product": 2300, "atk": 105, "def": 130, "hp": 140}},
  {"speciesId": "d", "speciesName": "D", "rating": 820, "score": 89.0,
   "moveset": ["M10","M11","M12"],
   "stats": {"product": 2200, "atk": 103, "def": 128, "hp": 145}}
]`

func newMetaTestManager(t *testing.T, payload string) *rankings.Manager {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(server.Close)

	mgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  server.URL,
		LocalDir: filepath.Join(t.TempDir(), "rankings"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	return mgr
}

func TestMetaTool_ReturnsTopN(t *testing.T) {
	t.Parallel()

	mgr := newMetaTestManager(t, metaRankingsFixture)
	handler := tools.NewMetaTool(mgr).Handler()

	_, result, err := handler(t.Context(), nil, tools.MetaParams{
		League: "great",
		TopN:   3,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.League != "great" {
		t.Errorf("League = %q, want great", result.League)
	}
	if result.CPCap != 1500 {
		t.Errorf("CPCap = %d, want 1500", result.CPCap)
	}
	if len(result.Entries) != 3 {
		t.Fatalf("Entries len = %d, want 3", len(result.Entries))
	}
	if result.Entries[0].SpeciesID != "a" || result.Entries[0].Rank != 1 {
		t.Errorf("first entry = %+v, want a @ rank 1", result.Entries[0])
	}
	if result.Entries[2].SpeciesID != "c" || result.Entries[2].Rank != 3 {
		t.Errorf("third entry = %+v, want c @ rank 3", result.Entries[2])
	}
}

func TestMetaTool_DefaultTopN(t *testing.T) {
	t.Parallel()

	mgr := newMetaTestManager(t, metaRankingsFixture)
	handler := tools.NewMetaTool(mgr).Handler()

	_, result, err := handler(t.Context(), nil, tools.MetaParams{League: "great"})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	// Fixture has 4 entries; default TopN (30) > 4, so we get all 4.
	if len(result.Entries) != 4 {
		t.Errorf("Entries len = %d, want 4 (fixture size)", len(result.Entries))
	}
}

func TestMetaTool_UnknownLeague(t *testing.T) {
	t.Parallel()

	mgr := newMetaTestManager(t, metaRankingsFixture)
	handler := tools.NewMetaTool(mgr).Handler()

	_, _, err := handler(t.Context(), nil, tools.MetaParams{League: "marshmallow"})
	if !errors.Is(err, tools.ErrUnknownLeague) {
		t.Errorf("error = %v, want wrapping ErrUnknownLeague", err)
	}
}

func TestMetaTool_NegativeTopN(t *testing.T) {
	t.Parallel()

	mgr := newMetaTestManager(t, metaRankingsFixture)
	handler := tools.NewMetaTool(mgr).Handler()

	_, _, err := handler(t.Context(), nil, tools.MetaParams{
		League: "great",
		TopN:   -1,
	})
	if !errors.Is(err, tools.ErrInvalidTopN) {
		t.Errorf("error = %v, want wrapping ErrInvalidTopN", err)
	}
}
