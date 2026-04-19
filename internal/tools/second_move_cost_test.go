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

const secondMoveCostFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 308, "speciesId": "medicham", "speciesName": "Medicham",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"],
     "thirdMoveCost": 50000, "buddyDistance": 3,
     "released": true},
    {"dex": 308, "speciesId": "medicham_shadow", "speciesName": "Medicham (Shadow)",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"],
     "thirdMoveCost": 50000, "buddyDistance": 3,
     "released": true},
    {"dex": 340, "speciesId": "whiscash", "speciesName": "Whiscash",
     "baseStats": {"atk": 151, "def": 141, "hp": 242},
     "types": ["water", "ground"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"],
     "thirdMoveCost": 10000, "buddyDistance": 1,
     "released": true},
    {"dex": 132, "speciesId": "ditto", "speciesName": "Ditto",
     "baseStats": {"atk": 91, "def": 91, "hp": 134},
     "types": ["normal"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"],
     "released": true}
  ],
  "moves": [
    {"moveId": "COUNTER", "name": "Counter", "type": "fighting",
     "power": 8, "energy": 0, "energyGain": 7, "cooldown": 1000, "turns": 2},
    {"moveId": "ICE_PUNCH", "name": "Ice Punch", "type": "ice",
     "power": 55, "energy": 40, "cooldown": 500}
  ]
}`

func newSecondMoveCostTool(t *testing.T) *tools.SecondMoveCostTool {
	t.Helper()

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(secondMoveCostFixture))
	}))
	t.Cleanup(gmServer.Close)

	gmMgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    gmServer.URL,
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager gm: %v", err)
	}

	err = gmMgr.Refresh(t.Context())
	if err != nil {
		t.Fatalf("Refresh gm: %v", err)
	}

	return tools.NewSecondMoveCostTool(gmMgr)
}

// TestSecondMoveCost_3kmBuddy pins medicham: 50,000 stardust + 50
// candy (3km buddy). The round-1 review blocker was that the prior
// implementation reported CandyCost=50000 (equal to stardust) —
// factually wrong. This test fails against that bug.
func TestSecondMoveCost_3kmBuddy(t *testing.T) {
	t.Parallel()

	tool := newSecondMoveCostTool(t)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.SecondMoveCostParams{
		Species: speciesMedicham,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.StardustCost != 50000 {
		t.Errorf("StardustCost = %d, want 50000", result.StardustCost)
	}

	if result.CandyCost != 50 {
		t.Errorf("CandyCost = %d, want 50 (3km buddy distance)", result.CandyCost)
	}

	if result.BuddyDistanceKM != 3 {
		t.Errorf("BuddyDistanceKM = %d, want 3", result.BuddyDistanceKM)
	}

	if !result.StardustCostAvailable || !result.CandyCostAvailable {
		t.Errorf("availability flags both should be true: stardust=%v candy=%v",
			result.StardustCostAvailable, result.CandyCostAvailable)
	}

	if result.ShadowMultiplier != 1 {
		t.Errorf("ShadowMultiplier = %d, want 1 (non-shadow species)", result.ShadowMultiplier)
	}
}

// TestSecondMoveCost_1kmBuddy pins the 1km buddy rate via whiscash:
// 10,000 stardust + 25 candy.
func TestSecondMoveCost_1kmBuddy(t *testing.T) {
	t.Parallel()

	tool := newSecondMoveCostTool(t)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.SecondMoveCostParams{
		Species: "whiscash",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.StardustCost != 10000 {
		t.Errorf("StardustCost = %d, want 10000", result.StardustCost)
	}

	if result.CandyCost != 25 {
		t.Errorf("CandyCost = %d, want 25 (1km buddy distance)", result.CandyCost)
	}

	if result.BuddyDistanceKM != 1 {
		t.Errorf("BuddyDistanceKM = %d, want 1", result.BuddyDistanceKM)
	}
}

// TestSecondMoveCost_ShadowMultiplier pins the 3× shadow penalty on
// both currencies. medicham_shadow is the same base (3km buddy,
// 50,000 stardust) but the response must multiply by 3 → 150,000
// stardust + 150 candy.
func TestSecondMoveCost_ShadowMultiplier(t *testing.T) {
	t.Parallel()

	tool := newSecondMoveCostTool(t)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.SecondMoveCostParams{
		Species: "medicham_shadow",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.ShadowMultiplier != 3 {
		t.Errorf("ShadowMultiplier = %d, want 3", result.ShadowMultiplier)
	}

	if result.StardustCost != 150000 {
		t.Errorf("StardustCost = %d, want 150000 (3× shadow)", result.StardustCost)
	}

	if result.CandyCost != 150 {
		t.Errorf("CandyCost = %d, want 150 (3× shadow)", result.CandyCost)
	}

	if result.Note == "" {
		t.Errorf("Note empty on shadow response; disclaimer must be carried")
	}
}

// TestSecondMoveCost_MissingBuddyDistance pins the degraded path:
// ditto in the fixture has neither thirdMoveCost nor buddyDistance
// in the payload, so CandyCostAvailable must be false and
// StardustCostAvailable likewise.
func TestSecondMoveCost_MissingBuddyDistance(t *testing.T) {
	t.Parallel()

	tool := newSecondMoveCostTool(t)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.SecondMoveCostParams{
		Species: "ditto",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.CandyCostAvailable {
		t.Errorf("CandyCostAvailable = true; ditto has no buddyDistance in fixture")
	}

	if result.StardustCostAvailable {
		t.Errorf("StardustCostAvailable = true; ditto has no thirdMoveCost in fixture")
	}

	if result.Note == "" {
		t.Errorf("Note empty on missing-data response; caller needs the explanation")
	}
}

// TestSecondMoveCost_UnknownSpecies rejects unknown ids.
func TestSecondMoveCost_UnknownSpecies(t *testing.T) {
	t.Parallel()

	tool := newSecondMoveCostTool(t)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.SecondMoveCostParams{Species: "missingno"})
	if !errors.Is(err, tools.ErrUnknownSpecies) {
		t.Errorf("error = %v, want wrapping ErrUnknownSpecies", err)
	}
}

// TestSecondMoveCost_EmptySpecies rejects empty species early.
func TestSecondMoveCost_EmptySpecies(t *testing.T) {
	t.Parallel()

	tool := newSecondMoveCostTool(t)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.SecondMoveCostParams{Species: ""})
	if !errors.Is(err, tools.ErrUnknownSpecies) {
		t.Errorf("error = %v, want wrapping ErrUnknownSpecies", err)
	}
}

// TestSecondMoveCost_GamemasterNotLoaded pins the cold-start sentinel.
func TestSecondMoveCost_GamemasterNotLoaded(t *testing.T) {
	t.Parallel()

	gmMgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    "http://127.0.0.1:1",
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	tool := tools.NewSecondMoveCostTool(gmMgr)
	handler := tool.Handler()

	_, _, err = handler(t.Context(), nil, tools.SecondMoveCostParams{Species: speciesMedicham})
	if !errors.Is(err, tools.ErrGamemasterNotLoaded) {
		t.Errorf("error = %v, want wrapping ErrGamemasterNotLoaded", err)
	}
}
