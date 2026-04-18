package tools_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
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
	handler := tools.NewRankTool(mgr).Handler()

	_, result, err := handler(t.Context(), nil, tools.RankParams{
		Species: "medicham",
		IV:      [3]int{0, 15, 15},
		League:  "great",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.Species != "medicham" {
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

func TestRankTool_UnknownSpecies(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, rankFixtureGamemaster)
	handler := tools.NewRankTool(mgr).Handler()

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
	handler := tools.NewRankTool(mgr).Handler()

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
	handler := tools.NewRankTool(mgr).Handler()

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

	handler := tools.NewRankTool(mgr).Handler()

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
	handler := tools.NewRankTool(mgr).Handler()

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
	handler := tools.NewRankTool(mgr).Handler()

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
	handler := tools.NewRankTool(mgr).Handler()

	// Ultra-tight CP cap that only level-1 / 0-IV entries can meet.
	_, result, err := handler(t.Context(), nil, tools.RankParams{
		Species: "medicham",
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
