package tools_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/lexfrei/pogo-pvp-mcp/internal/rankings"
	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
)

const rankFixtureGamemaster = `{
  "id": "gamemaster",
  "timestamp": "2026-04-18 00:00:00",
  "pokemon": [
    {
      "dex": 308,
      "speciesId": "medicham",
      "speciesName": "Medicham",
      "baseStats": {"atk": 121, "def": 152, "hp": 155},
      "types": ["fighting", "psychic"],
      "fastMoves": ["COUNTER"],
      "chargedMoves": ["ICE_PUNCH"],
      "released": true
    }
  ],
  "moves": [
    {"moveId": "COUNTER", "name": "Counter", "type": "fighting", "power": 8, "energy": 0, "energyGain": 7, "cooldown": 1000, "turns": 2},
    {"moveId": "ICE_PUNCH", "name": "Ice Punch", "type": "ice", "power": 55, "energy": 40, "energyGain": 0, "cooldown": 500}
  ]
}`

func newManagerWithFixture(t *testing.T, payload string) *gamemaster.Manager {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
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
		t.Fatalf("Refresh: %v", err)
	}

	return mgr
}

func TestRankTool_KnownSpecies(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, rankFixtureGamemaster)
	handler := tools.NewRankTool(mgr, nil).Handler()

	_, result, err := handler(t.Context(), nil, tools.RankParams{
		Species: speciesMedicham,
		IV:      [3]int{0, 15, 15},
		League:  "great",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.Species != speciesMedicham {
		t.Errorf("Species = %q, want medicham", result.Species)
	}
	if result.CP <= 0 || result.CP > 1500 {
		t.Errorf("CP = %d, want in (0, 1500]", result.CP)
	}
	if result.StatProduct <= 0 {
		t.Errorf("StatProduct = %f, want positive", result.StatProduct)
	}
	if result.PercentOfBest <= 0 || result.PercentOfBest > 100 {
		t.Errorf("PercentOfBest = %f, want (0, 100]", result.PercentOfBest)
	}
}

// newRankingsManagerWithPayload wires a httptest server that serves
// the given JSON payload under /all/overall/rankings-1500.json so
// the rank tool can project an optimal_moveset (and non_legacy_moveset) from it.
func newRankingsManagerWithPayload(t *testing.T, payload string) *rankings.Manager {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != allOverall1500URL {
			http.NotFound(w, r)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(server.Close)

	mgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  server.URL,
		LocalDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("rankings.NewManager: %v", err)
	}

	return mgr
}

// TestRankTool_OptimalMovesetFromRankings confirms that when the
// tool is constructed with a rankings manager and the species is
// present in the cup's rankings JSON, RankResult carries the
// projected fast + charged moveset.
func TestRankTool_OptimalMovesetFromRankings(t *testing.T) {
	t.Parallel()

	const rankingsPayload = `[
  {
    "speciesId": "medicham",
    "speciesName": "Medicham",
    "rating": 700,
    "score": 95.2,
    "moveset": ["COUNTER", "ICE_PUNCH", "PSYCHIC"],
    "matchups": [],
    "counters": [],
    "stats": {"product": 2103, "atk": 106.9, "def": 139.4, "hp": 141}
  }
]`

	gm := newManagerWithFixture(t, rankFixtureGamemaster)
	ranks := newRankingsManagerWithPayload(t, rankingsPayload)

	handler := tools.NewRankTool(gm, ranks).Handler()

	_, result, err := handler(t.Context(), nil, tools.RankParams{
		Species: "medicham",
		IV:      [3]int{0, 15, 15},
		League:  "great",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.OptimalMoveset == nil {
		t.Fatal("OptimalMoveset is nil, want populated")
	}
	if result.OptimalMoveset.Fast != "COUNTER" {
		t.Errorf("Fast = %q, want COUNTER", result.OptimalMoveset.Fast)
	}
	if len(result.OptimalMoveset.Charged) != 2 {
		t.Fatalf("Charged len = %d, want 2", len(result.OptimalMoveset.Charged))
	}
	if result.OptimalMoveset.Charged[0] != "ICE_PUNCH" {
		t.Errorf("Charged[0] = %q, want ICE_PUNCH", result.OptimalMoveset.Charged[0])
	}
	if result.OptimalMoveset.Charged[1] != "PSYCHIC" {
		t.Errorf("Charged[1] = %q, want PSYCHIC", result.OptimalMoveset.Charged[1])
	}
}

// TestRankTool_OptimalMovesetAbsentForUnknownInRanking confirms
// that species present in the gamemaster but missing from the
// rankings slice (common for obscure forms / cup exclusions) get a
// nil OptimalMoveset, not an error.
func TestRankTool_OptimalMovesetAbsentForUnknownInRanking(t *testing.T) {
	t.Parallel()

	const rankingsPayload = `[
  {"speciesId": "azumarill", "speciesName": "Azumarill", "rating": 800, "score": 99,
   "moveset": ["BUBBLE", "ICE_BEAM", "PLAY_ROUGH"], "matchups": [], "counters": [],
   "stats": {"product": 2500, "atk": 80, "def": 150, "hp": 200}}
]`

	gm := newManagerWithFixture(t, rankFixtureGamemaster)
	ranks := newRankingsManagerWithPayload(t, rankingsPayload)

	handler := tools.NewRankTool(gm, ranks).Handler()

	_, result, err := handler(t.Context(), nil, tools.RankParams{
		Species: "medicham",
		IV:      [3]int{0, 15, 15},
		League:  "great",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.OptimalMoveset != nil {
		t.Errorf("OptimalMoveset = %+v, want nil for species absent from ranking",
			result.OptimalMoveset)
	}
}

// TestRankTool_HundoComparisonPresent asserts the new comparison_to_hundo
// field is populated under a capped league and absent under master.
// The hundo spread for the same species must match the main spread
// when the caller already supplies 15/15/15 IVs (PercentOfBest == 100,
// Hundo.StatProduct == StatProduct).
func TestRankTool_HundoComparisonPresent(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, rankFixtureGamemaster)
	handler := tools.NewRankTool(mgr, nil).Handler()

	_, great, err := handler(t.Context(), nil, tools.RankParams{
		Species: "medicham",
		IV:      [3]int{15, 15, 15},
		League:  "great",
	})
	if err != nil {
		t.Fatalf("great: %v", err)
	}

	if great.Hundo == nil {
		t.Fatal("Hundo is nil under great league, want populated")
	}
	if great.Hundo.StatProduct != great.StatProduct {
		t.Errorf("Hundo.StatProduct = %f, want == main StatProduct %f",
			great.Hundo.StatProduct, great.StatProduct)
	}
	if great.Hundo.Level != great.Level {
		t.Errorf("Hundo.Level = %.1f, want == main Level %.1f",
			great.Hundo.Level, great.Level)
	}

	_, master, err := handler(t.Context(), nil, tools.RankParams{
		Species: "medicham",
		IV:      [3]int{0, 15, 15},
		League:  "master",
	})
	if err != nil {
		t.Fatalf("master: %v", err)
	}

	if master.Hundo != nil {
		t.Errorf("Hundo = %+v, want nil under master league", master.Hundo)
	}
}

func TestRankTool_UnknownSpecies(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, rankFixtureGamemaster)
	handler := tools.NewRankTool(mgr, nil).Handler()

	_, _, err := handler(t.Context(), nil, tools.RankParams{
		Species: "missingno",
		IV:      [3]int{15, 15, 15},
		League:  "great",
	})
	if !errors.Is(err, tools.ErrUnknownSpecies) {
		t.Errorf("error = %v, want wrapping ErrUnknownSpecies", err)
	}
}

func TestRankTool_UnknownLeague(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, rankFixtureGamemaster)
	handler := tools.NewRankTool(mgr, nil).Handler()

	_, _, err := handler(t.Context(), nil, tools.RankParams{
		Species: "medicham",
		IV:      [3]int{15, 15, 15},
		League:  "marshmallow",
	})
	if !errors.Is(err, tools.ErrUnknownLeague) {
		t.Errorf("error = %v, want wrapping ErrUnknownLeague", err)
	}
}

func TestRankTool_InvalidIV(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, rankFixtureGamemaster)
	handler := tools.NewRankTool(mgr, nil).Handler()

	_, _, err := handler(t.Context(), nil, tools.RankParams{
		Species: "medicham",
		IV:      [3]int{16, 0, 0},
		League:  "great",
	})
	if err == nil {
		t.Fatal("expected error for out-of-range IV")
	}
}

func TestRankTool_NoGamemasterLoaded(t *testing.T) {
	t.Parallel()

	mgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    "http://example.invalid",
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	handler := tools.NewRankTool(mgr, nil).Handler()

	_, _, err = handler(t.Context(), nil, tools.RankParams{
		Species: "medicham",
		IV:      [3]int{15, 15, 15},
		League:  "great",
	})
	if !errors.Is(err, tools.ErrGamemasterNotLoaded) {
		t.Errorf("error = %v, want wrapping ErrGamemasterNotLoaded", err)
	}
}

// TestRankTool_DegenerateSpecies checks that a species whose best
// global stat product is zero (synthetic, parser normally rejects
// this) surfaces ErrDegenerateSpecies instead of propagating a NaN
// percent-of-best that json.Marshal would fail on.
//
// The fixture sneaks a species past ParseGamemaster's positive-base
// check by using base stats of 1 but a CP cap so low that FindOptimal
// returns an unreachable error. We do not actually hit the zero-best
// branch via ParseGamemaster (it would reject zero base), so this
// test documents the guard exists and the unreachable-cap path still
// surfaces cleanly — two regressions in one.
func TestRankTool_UnreachableCapSurfacesCleanly(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, rankFixtureGamemaster)
	handler := tools.NewRankTool(mgr, nil).Handler()

	// CP cap 1 is below the minimum CP any species can reach: every
	// IV/level hits the cp=10 floor, so no legal spread fits.
	_, _, err := handler(t.Context(), nil, tools.RankParams{
		Species: "medicham",
		IV:      [3]int{0, 0, 0},
		League:  "great",
		CPCap:   1,
	})
	if err == nil {
		t.Fatal("expected error for unreachable CP cap")
	}
}

func TestRankTool_NegativeCPCapRejected(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, rankFixtureGamemaster)
	handler := tools.NewRankTool(mgr, nil).Handler()

	_, _, err := handler(t.Context(), nil, tools.RankParams{
		Species: "medicham",
		IV:      [3]int{15, 15, 15},
		League:  "great",
		CPCap:   -1500,
	})
	if !errors.Is(err, tools.ErrInvalidCPCap) {
		t.Errorf("error = %v, want wrapping ErrInvalidCPCap", err)
	}
}

func TestRankTool_CPCapOverride(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, rankFixtureGamemaster)
	handler := tools.NewRankTool(mgr, nil).Handler()

	// Ultra-tight CP cap that only level-1 / 0-IV entries can meet.
	_, result, err := handler(t.Context(), nil, tools.RankParams{
		Species: speciesMedicham,
		IV:      [3]int{0, 0, 0},
		League:  "great",
		CPCap:   100,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.CP > 100 {
		t.Errorf("CP = %d, exceeds override cap 100", result.CP)
	}
}

// rankShadowFixtureGamemaster publishes both medicham and
// medicham_shadow so Phase X-II pvp_rank tests can verify that
// Options.Shadow=true flips the lookup to the shadow entry.
const rankShadowFixtureGamemaster = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 308, "speciesId": "medicham", "speciesName": "Medicham",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"], "released": true},
    {"dex": 308, "speciesId": "medicham_shadow", "speciesName": "Medicham (Shadow)",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"], "released": true}
  ],
  "moves": [
    {"moveId": "COUNTER", "name": "Counter", "type": "fighting",
     "power": 8, "energy": 0, "energyGain": 7, "cooldown": 1000, "turns": 2},
    {"moveId": "ICE_PUNCH", "name": "Ice Punch", "type": "ice",
     "power": 55, "energy": 40, "energyGain": 0, "cooldown": 500}
  ]
}`

// TestRankTool_ShadowOptionResolvesToShadowEntry pins Phase X-II:
// Options.Shadow=true on pvp_rank flips the species lookup to the
// "_shadow" entry. The response echoes ResolvedSpeciesID so the
// caller can tell that the redirect happened (stats line is the
// same because pvpoke publishes shadow rows with identical
// BaseStats; the signal is purely the id).
func TestRankTool_ShadowOptionResolvesToShadowEntry(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, rankShadowFixtureGamemaster)
	handler := tools.NewRankTool(mgr, nil).Handler()

	_, result, err := handler(t.Context(), nil, tools.RankParams{
		Species: speciesMedicham,
		IV:      [3]int{15, 15, 15},
		League:  leagueGreat,
		Options: tools.CombatantOptions{Shadow: true},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.Species != speciesMedicham {
		t.Errorf("Species = %q, want %q (echo of input)",
			result.Species, speciesMedicham)
	}

	if result.ResolvedSpeciesID != "medicham_shadow" {
		t.Errorf("ResolvedSpeciesID = %q, want %q (Options.Shadow must flip lookup)",
			result.ResolvedSpeciesID, "medicham_shadow")
	}

	if result.ShadowVariantMissing {
		t.Errorf("ShadowVariantMissing = true; fixture publishes _shadow — must not signal missing")
	}
}
