package tools_test

import (
	"errors"
	"path/filepath"
	"testing"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
)

// levelFromCPFixture carries medicham's stat line plus dragonite so
// the tests cover both a CP-bound species (medicham easy to hit 1500)
// and a heavyweight (dragonite saturates earlier).
const levelFromCPFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 308, "speciesId": "medicham", "speciesName": "Medicham",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"],
     "released": true},
    {"dex": 149, "speciesId": "dragonite", "speciesName": "Dragonite",
     "baseStats": {"atk": 263, "def": 198, "hp": 209},
     "types": ["dragon", "flying"],
     "fastMoves": ["DRAGON_TAIL"], "chargedMoves": ["OUTRAGE"],
     "released": true}
  ],
  "moves": [
    {"moveId": "COUNTER", "name": "Counter", "type": "fighting",
     "power": 8, "energy": 0, "energyGain": 7, "cooldown": 1000, "turns": 2},
    {"moveId": "ICE_PUNCH", "name": "Ice Punch", "type": "ice",
     "power": 55, "energy": 40, "cooldown": 500},
    {"moveId": "DRAGON_TAIL", "name": "Dragon Tail", "type": "dragon",
     "power": 13, "energy": 0, "energyGain": 9, "cooldown": 1500, "turns": 3},
    {"moveId": "OUTRAGE", "name": "Outrage", "type": "dragon",
     "power": 110, "energy": 50, "cooldown": 500}
  ]
}`

func TestLevelFromCP_FitsUnderCap(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, levelFromCPFixture)
	handler := tools.NewLevelFromCPTool(mgr).Handler()

	_, result, err := handler(t.Context(), nil, tools.LevelFromCPParams{
		Species: speciesMedicham,
		IV:      [3]int{0, 15, 15},
		CP:      1500,
		XL:      true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.CP > 1500 {
		t.Errorf("CP = %d, want ≤ 1500", result.CP)
	}
	if result.Level < 1.0 || result.Level > 51.0 {
		t.Errorf("Level = %.1f outside [1.0, 51.0]", result.Level)
	}
	if result.StatProduct <= 0 {
		t.Errorf("StatProduct = %f, want positive", result.StatProduct)
	}
	if result.HP <= 0 {
		t.Errorf("HP = %d, want positive", result.HP)
	}
}

// TestLevelFromCP_ExactRoundTrip builds a known (species, iv, level)
// triple, computes its CP via the engine, feeds that CP back to the
// tool, and asserts Exact=true plus Level recovers the input.
func TestLevelFromCP_ExactRoundTrip(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, levelFromCPFixture)
	handler := tools.NewLevelFromCPTool(mgr).Handler()

	// Compute the medicham-40.0 CP via the engine so the test
	// stays immune to pvpoke stat-table drift.
	const level = 40.0

	ivs, err := pogopvp.NewIV(0, 15, 15)
	if err != nil {
		t.Fatalf("NewIV: %v", err)
	}

	cpm, err := pogopvp.CPMAt(level)
	if err != nil {
		t.Fatalf("CPMAt: %v", err)
	}

	base := pogopvp.BaseStats{Atk: 121, Def: 152, HP: 155}
	targetCP := pogopvp.ComputeCP(base, ivs, cpm)

	_, result, err := handler(t.Context(), nil, tools.LevelFromCPParams{
		Species: speciesMedicham,
		IV:      [3]int{0, 15, 15},
		CP:      targetCP,
		XL:      true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if !result.Exact {
		t.Errorf("Exact = false, want true (round-trip CP must recover exactly)")
	}
	if result.CP != targetCP {
		t.Errorf("CP = %d, want %d", result.CP, targetCP)
	}
	if result.Level < level {
		t.Errorf("Level = %.1f, want ≥ %.1f", result.Level, level)
	}
}

func TestLevelFromCP_UnknownSpecies(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, levelFromCPFixture)
	handler := tools.NewLevelFromCPTool(mgr).Handler()

	_, _, err := handler(t.Context(), nil, tools.LevelFromCPParams{
		Species: "missingno",
		IV:      [3]int{15, 15, 15},
		CP:      1500,
	})
	if !errors.Is(err, tools.ErrUnknownSpecies) {
		t.Errorf("error = %v, want wrapping ErrUnknownSpecies", err)
	}
}

func TestLevelFromCP_InvalidIV(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, levelFromCPFixture)
	handler := tools.NewLevelFromCPTool(mgr).Handler()

	_, _, err := handler(t.Context(), nil, tools.LevelFromCPParams{
		Species: speciesMedicham,
		IV:      [3]int{16, 0, 0},
		CP:      1500,
	})
	if err == nil {
		t.Fatal("expected error for out-of-range IV")
	}
}

// TestLevelFromCP_TooLow pins the engine ErrCPTooLow propagation:
// a target CP below the species' level-1 minimum surfaces the
// engine sentinel unchanged so callers can branch on it.
func TestLevelFromCP_TooLow(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, levelFromCPFixture)
	handler := tools.NewLevelFromCPTool(mgr).Handler()

	_, _, err := handler(t.Context(), nil, tools.LevelFromCPParams{
		Species: "dragonite",
		IV:      [3]int{15, 15, 15},
		CP:      5,
	})
	if !errors.Is(err, pogopvp.ErrCPTooLow) {
		t.Errorf("error = %v, want wrapping pogopvp.ErrCPTooLow", err)
	}
}

func TestLevelFromCP_NoGamemasterLoaded(t *testing.T) {
	t.Parallel()

	mgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    "http://example.invalid",
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	handler := tools.NewLevelFromCPTool(mgr).Handler()

	_, _, err = handler(t.Context(), nil, tools.LevelFromCPParams{
		Species: speciesMedicham,
		IV:      [3]int{15, 15, 15},
		CP:      1500,
	})
	if !errors.Is(err, tools.ErrGamemasterNotLoaded) {
		t.Errorf("error = %v, want wrapping ErrGamemasterNotLoaded", err)
	}
}
