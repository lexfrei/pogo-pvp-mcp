package tools_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
)

// movePsychic is the legacy-on-medicham move id exercised across
// the info lookup tests; hoisted to placate goconst.
const movePsychic = "PSYCHIC"

// infoFixtureGamemaster carries two species — medicham with legacy
// moves declared, bulbasaur without — and a handful of moves sufficient
// for both species_info and move_info lookups. Kept small so tests
// stay readable; the parser-level legacy fixture lives in the engine.
const infoFixtureGamemaster = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {
      "dex": 308,
      "speciesId": "medicham",
      "speciesName": "Medicham",
      "baseStats": {"atk": 121, "def": 152, "hp": 155},
      "types": ["fighting", "psychic"],
      "fastMoves": ["COUNTER"],
      "chargedMoves": ["ICE_PUNCH", "PSYCHIC"],
      "legacyMoves": ["PSYCHIC"],
      "family": {"id": "FAMILY_MEDICHAM", "parent": "meditite"},
      "released": true
    },
    {
      "dex": 1,
      "speciesId": "bulbasaur",
      "speciesName": "Bulbasaur",
      "baseStats": {"atk": 118, "def": 111, "hp": 128},
      "types": ["grass", "poison"],
      "fastMoves": ["VINE_WHIP"],
      "chargedMoves": ["SLUDGE_BOMB"],
      "family": {"id": "FAMILY_BULBASAUR", "evolutions": ["ivysaur"]},
      "released": true
    }
  ],
  "moves": [
    {"moveId": "COUNTER", "name": "Counter", "type": "fighting",
     "power": 8, "energy": 0, "energyGain": 7, "cooldown": 1000, "turns": 2},
    {"moveId": "ICE_PUNCH", "name": "Ice Punch", "type": "ice",
     "power": 55, "energy": 40, "cooldown": 500},
    {"moveId": "PSYCHIC", "name": "Psychic", "type": "psychic",
     "power": 90, "energy": 55, "cooldown": 500},
    {"moveId": "VINE_WHIP", "name": "Vine Whip", "type": "grass",
     "power": 5, "energy": 0, "energyGain": 8, "cooldown": 1000, "turns": 2},
    {"moveId": "SLUDGE_BOMB", "name": "Sludge Bomb", "type": "poison",
     "power": 80, "energy": 50, "cooldown": 500}
  ]
}`

func TestSpeciesInfo_HappyPath(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, infoFixtureGamemaster)
	handler := tools.NewSpeciesInfoTool(mgr, nil).Handler()

	_, result, err := handler(t.Context(), nil, tools.SpeciesInfoParams{
		Species: speciesMedicham,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.Species != speciesMedicham {
		t.Errorf("Species = %q, want %s", result.Species, speciesMedicham)
	}
	if result.Dex != 308 || result.Name != "Medicham" {
		t.Errorf("unexpected dex/name: %d/%q", result.Dex, result.Name)
	}
	if result.BaseStats.Atk != 121 {
		t.Errorf("BaseStats.Atk = %d, want 121", result.BaseStats.Atk)
	}
	if result.PreEvolution != "meditite" {
		t.Errorf("PreEvolution = %q, want meditite", result.PreEvolution)
	}
	if !slices.Equal(result.LegacyMoves, []string{"PSYCHIC"}) {
		t.Errorf("LegacyMoves = %v, want [PSYCHIC]", result.LegacyMoves)
	}

	var psyLegacy bool

	for _, move := range result.ChargedMoves {
		if move.ID == movePsychic {
			psyLegacy = move.Legacy

			break
		}
	}

	if !psyLegacy {
		t.Error("ChargedMoves[PSYCHIC].Legacy = false, want true")
	}
}

func TestSpeciesInfo_UnknownSpecies(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, infoFixtureGamemaster)
	handler := tools.NewSpeciesInfoTool(mgr, nil).Handler()

	_, _, err := handler(t.Context(), nil, tools.SpeciesInfoParams{
		Species: "missingno",
	})
	if !errors.Is(err, tools.ErrUnknownSpecies) {
		t.Errorf("error = %v, want wrapping ErrUnknownSpecies", err)
	}
}

func TestMoveInfo_HappyPath(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, infoFixtureGamemaster)
	handler := tools.NewMoveInfoTool(mgr).Handler()

	_, result, err := handler(t.Context(), nil, tools.MoveInfoParams{
		MoveID: movePsychic,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.ID != movePsychic {
		t.Errorf("ID = %q, want %s", result.ID, movePsychic)
	}
	if result.Category != "charged" {
		t.Errorf("Category = %q, want charged", result.Category)
	}
	if result.Power != 90 {
		t.Errorf("Power = %d, want 90", result.Power)
	}
	if !slices.Equal(result.LegacyOnSpecies, []string{"medicham"}) {
		t.Errorf("LegacyOnSpecies = %v, want [medicham] (only species with PSYCHIC legacy)",
			result.LegacyOnSpecies)
	}
}

func TestMoveInfo_UnknownMove(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, infoFixtureGamemaster)
	handler := tools.NewMoveInfoTool(mgr).Handler()

	_, _, err := handler(t.Context(), nil, tools.MoveInfoParams{MoveID: "NOT_A_MOVE"})
	if !errors.Is(err, tools.ErrUnknownMove) {
		t.Errorf("error = %v, want wrapping ErrUnknownMove", err)
	}
}

func TestTypeMatchup_DoubleResistance(t *testing.T) {
	t.Parallel()

	handler := tools.NewTypeMatchupTool().Handler()

	// Grass vs Water(1.6) × Ground(1.6) = 2.56 — classic Swampert-
	// over-Water pattern used as a sanity check for the chart math.
	_, result, err := handler(t.Context(), nil, tools.TypeMatchupParams{
		AttackerType:  "grass",
		DefenderTypes: []string{"water", "ground"},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.Multiplier < 2.55 || result.Multiplier > 2.57 {
		t.Errorf("Multiplier = %.4f, want ~2.56", result.Multiplier)
	}
}

func TestTypeMatchup_MissingAttackerType(t *testing.T) {
	t.Parallel()

	handler := tools.NewTypeMatchupTool().Handler()

	_, _, err := handler(t.Context(), nil, tools.TypeMatchupParams{
		DefenderTypes: []string{"water"},
	})
	if !errors.Is(err, tools.ErrMissingAttackerType) {
		t.Errorf("error = %v, want wrapping ErrMissingAttackerType", err)
	}
}
