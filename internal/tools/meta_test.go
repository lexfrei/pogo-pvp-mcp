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

// allOverall1500URL is the upstream path pvpoke publishes the
// default open-league 1500-cap overall rankings under. Hoisted here
// so the test server mux and the assertion paths stay in sync.
const allOverall1500URL = "/all/overall/rankings-1500.json"

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

// newMetaManagerWithRoles wires a httptest server that dispatches on
// the role segment of the URL so classifyRole gets real data to
// evaluate. The four payloads deliberately order species differently
// so the gap threshold (5 positions) picks a clear winner.
func newMetaManagerWithRoles(
	t *testing.T, overall, leads, switches, closers string,
) *rankings.Manager {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case allOverall1500URL:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(overall))
		case "/all/leads/rankings-1500.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(leads))
		case "/all/switches/rankings-1500.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(switches))
		case "/all/closers/rankings-1500.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(closers))
		default:
			http.NotFound(w, r)
		}
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

// TestMetaTool_AssignsRoles asserts the role classifier:
// species "a" dominates the leads ranking (position 1 there, far
// down elsewhere) and must be tagged "lead"; species "b" sits near
// the middle of all three rankings and must be tagged "flex" (no
// clear winner).
func TestMetaTool_AssignsRoles(t *testing.T) {
	t.Parallel()

	const overall = `[
  {"speciesId": "a", "speciesName": "A", "rating": 900, "score": 95, "moveset": ["M1","M2","M3"],
   "stats": {"product": 2500, "atk": 110, "def": 120, "hp": 160}},
  {"speciesId": "b", "speciesName": "B", "rating": 880, "score": 93, "moveset": ["M4","M5","M6"],
   "stats": {"product": 2400, "atk": 108, "def": 125, "hp": 150}}
]`

	const leadsRanking = `[
  {"speciesId": "a"},
  {"speciesId": "b"},
  {"speciesId": "c"}
]`

	// Species "a" shows up very late in switches/closers, but near the
	// top of leads — pure lead archetype, should pick "lead".
	const switchesRanking = `[
  {"speciesId": "x"}, {"speciesId": "y"}, {"speciesId": "z"},
  {"speciesId": "b"}, {"speciesId": "p"}, {"speciesId": "q"},
  {"speciesId": "r"}, {"speciesId": "a"}
]`

	const closersRanking = `[
  {"speciesId": "u"}, {"speciesId": "v"}, {"speciesId": "w"},
  {"speciesId": "b"}, {"speciesId": "m"}, {"speciesId": "n"},
  {"speciesId": "o"}, {"speciesId": "a"}
]`

	mgr := newMetaManagerWithRoles(t, overall, leadsRanking, switchesRanking, closersRanking)
	handler := tools.NewMetaTool(mgr, nil).Handler()

	_, result, err := handler(t.Context(), nil, tools.MetaParams{League: "great", TopN: 2})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	byID := map[string]tools.MetaEntry{}
	for _, entry := range result.Entries {
		byID[entry.SpeciesID] = entry
	}

	if byID["a"].Role != "lead" {
		t.Errorf("a.Role = %q, want lead", byID["a"].Role)
	}
	if byID["b"].Role != "flex" {
		t.Errorf("b.Role = %q, want flex", byID["b"].Role)
	}
}

func TestMetaTool_ReturnsTopN(t *testing.T) {
	t.Parallel()

	mgr := newMetaTestManager(t, metaRankingsFixture)
	handler := tools.NewMetaTool(mgr, nil).Handler()

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
	handler := tools.NewMetaTool(mgr, nil).Handler()

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
	handler := tools.NewMetaTool(mgr, nil).Handler()

	_, _, err := handler(t.Context(), nil, tools.MetaParams{League: "marshmallow"})
	if !errors.Is(err, tools.ErrUnknownLeague) {
		t.Errorf("error = %v, want wrapping ErrUnknownLeague", err)
	}
}

func TestMetaTool_NegativeTopN(t *testing.T) {
	t.Parallel()

	mgr := newMetaTestManager(t, metaRankingsFixture)
	handler := tools.NewMetaTool(mgr, nil).Handler()

	_, _, err := handler(t.Context(), nil, tools.MetaParams{
		League: "great",
		TopN:   -1,
	})
	if !errors.Is(err, tools.ErrInvalidTopN) {
		t.Errorf("error = %v, want wrapping ErrInvalidTopN", err)
	}
}
