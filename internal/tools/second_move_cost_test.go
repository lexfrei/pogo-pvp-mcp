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
     "thirdMoveCost": 50000,
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

// TestSecondMoveCost_PublishedCost pins the happy path: a species
// with a non-zero thirdMoveCost surfaces Available=true and equal
// stardust / candy values.
func TestSecondMoveCost_PublishedCost(t *testing.T) {
	t.Parallel()

	tool := newSecondMoveCostTool(t)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.SecondMoveCostParams{
		Species: speciesMedicham,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if !result.Available {
		t.Errorf("Available = false, want true (medicham has thirdMoveCost=50000)")
	}

	if result.StardustCost != 50000 {
		t.Errorf("StardustCost = %d, want 50000", result.StardustCost)
	}

	if result.CandyCost != 50000 {
		t.Errorf("CandyCost = %d, want 50000 (candy mirrors stardust)", result.CandyCost)
	}
}

// TestSecondMoveCost_NoPublishedCost pins the absent-field path:
// species whose payload does not carry thirdMoveCost (like ditto in
// the fixture) surface Available=false with a Note and zero values.
func TestSecondMoveCost_NoPublishedCost(t *testing.T) {
	t.Parallel()

	tool := newSecondMoveCostTool(t)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.SecondMoveCostParams{
		Species: "ditto",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.Available {
		t.Errorf("Available = true; ditto has no thirdMoveCost in the fixture")
	}

	if result.StardustCost != 0 || result.CandyCost != 0 {
		t.Errorf("costs = (%d, %d), want (0, 0) on unavailable species",
			result.StardustCost, result.CandyCost)
	}

	if result.Note == "" {
		t.Errorf("Note empty on Available=false; caller needs the explanation")
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
