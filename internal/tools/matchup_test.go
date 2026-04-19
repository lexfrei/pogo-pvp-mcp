package tools_test

import (
	"errors"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
)

const matchupFixtureGamemaster = `{
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
    },
    {
      "dex": 68,
      "speciesId": "machamp",
      "speciesName": "Machamp",
      "baseStats": {"atk": 234, "def": 159, "hp": 207},
      "types": ["fighting", "none"],
      "fastMoves": ["COUNTER"],
      "chargedMoves": ["CROSS_CHOP"],
      "released": true
    }
  ],
  "moves": [
    {"moveId": "COUNTER", "name": "Counter", "type": "fighting", "power": 8, "energy": 0, "energyGain": 7, "cooldown": 1000, "turns": 2},
    {"moveId": "ICE_PUNCH", "name": "Ice Punch", "type": "ice", "power": 55, "energy": 40, "energyGain": 0, "cooldown": 500},
    {"moveId": "CROSS_CHOP", "name": "Cross Chop", "type": "fighting", "power": 50, "energy": 35, "energyGain": 0, "cooldown": 500}
  ]
}`

func TestMatchupTool_ReturnsBattleResult(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, matchupFixtureGamemaster)
	handler := tools.NewMatchupTool(mgr, nil).Handler()

	_, result, err := handler(t.Context(), nil, tools.MatchupParams{
		Attacker: tools.Combatant{
			Species:      "medicham",
			IV:           [3]int{15, 15, 15},
			Level:        40,
			FastMove:     "COUNTER",
			ChargedMoves: []string{"ICE_PUNCH"},
		},
		Defender: tools.Combatant{
			Species:      "machamp",
			IV:           [3]int{10, 10, 10},
			Level:        30,
			FastMove:     "COUNTER",
			ChargedMoves: []string{"CROSS_CHOP"},
		},
		Shields: [2]int{2, 2},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.Turns <= 0 {
		t.Errorf("Turns = %d, want > 0", result.Turns)
	}
	if result.Winner != "attacker" && result.Winner != "defender" && result.Winner != "tie" {
		t.Errorf("Winner = %q, want attacker|defender|tie", result.Winner)
	}
}

func TestMatchupTool_UnknownSpecies(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, matchupFixtureGamemaster)
	handler := tools.NewMatchupTool(mgr, nil).Handler()

	_, _, err := handler(t.Context(), nil, tools.MatchupParams{
		Attacker: tools.Combatant{
			Species:  "missingno",
			IV:       [3]int{15, 15, 15},
			Level:    40,
			FastMove: "COUNTER",
		},
		Defender: tools.Combatant{
			Species:  "machamp",
			IV:       [3]int{10, 10, 10},
			Level:    30,
			FastMove: "COUNTER",
		},
	})
	if !errors.Is(err, tools.ErrUnknownSpecies) {
		t.Errorf("error = %v, want wrapping ErrUnknownSpecies", err)
	}
}

func TestMatchupTool_UnknownFastMove(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, matchupFixtureGamemaster)
	handler := tools.NewMatchupTool(mgr, nil).Handler()

	_, _, err := handler(t.Context(), nil, tools.MatchupParams{
		Attacker: tools.Combatant{
			Species:  "medicham",
			IV:       [3]int{15, 15, 15},
			Level:    40,
			FastMove: "NOT_A_MOVE",
		},
		Defender: tools.Combatant{
			Species:  "machamp",
			IV:       [3]int{10, 10, 10},
			Level:    30,
			FastMove: "COUNTER",
		},
	})
	if !errors.Is(err, tools.ErrUnknownMove) {
		t.Errorf("error = %v, want wrapping ErrUnknownMove", err)
	}
}

func TestMatchupTool_FastMoveUsedAsCharged(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, matchupFixtureGamemaster)
	handler := tools.NewMatchupTool(mgr, nil).Handler()

	_, _, err := handler(t.Context(), nil, tools.MatchupParams{
		Attacker: tools.Combatant{
			Species:      "medicham",
			IV:           [3]int{15, 15, 15},
			Level:        40,
			FastMove:     "COUNTER",
			ChargedMoves: []string{"COUNTER"}, // COUNTER is a fast move
		},
		Defender: tools.Combatant{
			Species:  "machamp",
			IV:       [3]int{10, 10, 10},
			Level:    30,
			FastMove: "COUNTER",
		},
	})
	if !errors.Is(err, tools.ErrMoveCategoryMismatch) {
		t.Errorf("error = %v, want wrapping ErrMoveCategoryMismatch", err)
	}
}

func TestMatchupTool_ChargedMoveUsedAsFast(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, matchupFixtureGamemaster)
	handler := tools.NewMatchupTool(mgr, nil).Handler()

	_, _, err := handler(t.Context(), nil, tools.MatchupParams{
		Attacker: tools.Combatant{
			Species:  "medicham",
			IV:       [3]int{15, 15, 15},
			Level:    40,
			FastMove: "ICE_PUNCH", // ICE_PUNCH is a charged move
		},
		Defender: tools.Combatant{
			Species:  "machamp",
			IV:       [3]int{10, 10, 10},
			Level:    30,
			FastMove: "COUNTER",
		},
	})
	if !errors.Is(err, tools.ErrMoveCategoryMismatch) {
		t.Errorf("error = %v, want wrapping ErrMoveCategoryMismatch", err)
	}
}

func TestMatchupTool_ShieldsCountedInResult(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, matchupFixtureGamemaster)
	handler := tools.NewMatchupTool(mgr, nil).Handler()

	_, result, err := handler(t.Context(), nil, tools.MatchupParams{
		Attacker: tools.Combatant{
			Species:      "medicham",
			IV:           [3]int{15, 15, 15},
			Level:        40,
			FastMove:     "COUNTER",
			ChargedMoves: []string{"ICE_PUNCH"},
		},
		Defender: tools.Combatant{
			Species:      "machamp",
			IV:           [3]int{10, 10, 10},
			Level:        30,
			FastMove:     "COUNTER",
			ChargedMoves: []string{"CROSS_CHOP"},
		},
		Shields: [2]int{1, 2},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.ShieldsUsed[0] > 1 {
		t.Errorf("ShieldsUsed[0] = %d, exceeds configured 1", result.ShieldsUsed[0])
	}
	if result.ShieldsUsed[1] > 2 {
		t.Errorf("ShieldsUsed[1] = %d, exceeds configured 2", result.ShieldsUsed[1])
	}
}

// TestMatchupTool_ShadowOptionResolvesToShadowEntry pins Phase X:
// Options.Shadow=true causes the tool to look up the "_shadow"
// gamemaster entry. The fixture publishes medicham AND
// medicham_shadow; the resolved_species_id echoes the latter.
func TestMatchupTool_ShadowOptionResolvesToShadowEntry(t *testing.T) {
	t.Parallel()

	const shadowFixture = `{
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
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"], "released": true},
    {"dex": 68, "speciesId": "machamp", "speciesName": "Machamp",
     "baseStats": {"atk": 234, "def": 159, "hp": 207},
     "types": ["fighting"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["CROSS_CHOP"], "released": true}
  ],
  "moves": [
    {"moveId": "COUNTER", "name": "Counter", "type": "fighting",
     "power": 8, "energy": 0, "energyGain": 7, "cooldown": 1000, "turns": 2},
    {"moveId": "ICE_PUNCH", "name": "Ice Punch", "type": "ice",
     "power": 55, "energy": 40, "cooldown": 500},
    {"moveId": "CROSS_CHOP", "name": "Cross Chop", "type": "fighting",
     "power": 50, "energy": 35, "cooldown": 500}
  ]
}`

	mgr := newManagerWithFixture(t, shadowFixture)
	handler := tools.NewMatchupTool(mgr, nil).Handler()

	_, result, err := handler(t.Context(), nil, tools.MatchupParams{
		Attacker: tools.Combatant{
			Species:  speciesMedicham,
			IV:       [3]int{15, 15, 15},
			Level:    40,
			FastMove: "COUNTER", ChargedMoves: []string{"ICE_PUNCH"},
			Options: tools.CombatantOptions{Shadow: true},
		},
		Defender: tools.Combatant{
			Species:  "machamp",
			IV:       [3]int{15, 15, 15},
			Level:    40,
			FastMove: "COUNTER", ChargedMoves: []string{"CROSS_CHOP"},
		},
		League:  "great",
		Shields: [2]int{1, 1},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.Attacker.Species != speciesMedicham {
		t.Errorf("Attacker.Species = %q, want %q (echo of input)",
			result.Attacker.Species, speciesMedicham)
	}

	if result.Attacker.ResolvedSpeciesID != "medicham_shadow" {
		t.Errorf("Attacker.ResolvedSpeciesID = %q, want \"medicham_shadow\"",
			result.Attacker.ResolvedSpeciesID)
	}

	if !result.Attacker.Options.Shadow {
		t.Errorf("Attacker.Options.Shadow = false, want true (round-trip)")
	}

	if result.Attacker.ShadowVariantMissing {
		t.Errorf("ShadowVariantMissing = true; fixture publishes _shadow entry")
	}
}

// TestMatchupTool_ShadowOptionFallsBackWhenVariantMissing pins the
// fallback path: Options.Shadow=true but no "_shadow" entry in the
// snapshot (fixture publishes only base medicham). Resolver returns
// base with ShadowVariantMissing=true.
func TestMatchupTool_ShadowOptionFallsBackWhenVariantMissing(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, matchupFixtureGamemaster)
	handler := tools.NewMatchupTool(mgr, nil).Handler()

	_, result, err := handler(t.Context(), nil, tools.MatchupParams{
		Attacker: tools.Combatant{
			Species:  speciesMedicham,
			IV:       [3]int{15, 15, 15},
			Level:    40,
			FastMove: "COUNTER", ChargedMoves: []string{"ICE_PUNCH"},
			Options: tools.CombatantOptions{Shadow: true},
		},
		Defender: tools.Combatant{
			Species:  "machamp",
			IV:       [3]int{15, 15, 15},
			Level:    40,
			FastMove: "COUNTER", ChargedMoves: []string{"CROSS_CHOP"},
		},
		League:  "great",
		Shields: [2]int{1, 1},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.Attacker.ResolvedSpeciesID != "medicham" {
		t.Errorf("Attacker.ResolvedSpeciesID = %q, want base \"medicham\" (fallback)",
			result.Attacker.ResolvedSpeciesID)
	}

	if !result.Attacker.ShadowVariantMissing {
		t.Errorf("ShadowVariantMissing = false; fixture doesn't publish _shadow entry — must signal")
	}
}
