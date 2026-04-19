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

// newMatchupRankingsFromFixture wires a one-shot rankings manager
// from an inline JSON payload; used by the auto-resolve shadow
// moveset test which needs both medicham and medicham_shadow
// ranked so ResolveMoveset can pick between them based on
// Options.Shadow.
func newMatchupRankingsFromFixture(t *testing.T, payload string) *rankings.Manager {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)

	mgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  srv.URL,
		LocalDir: filepath.Join(t.TempDir(), "rankings"),
	})
	if err != nil {
		t.Fatalf("NewManager rankings: %v", err)
	}

	return mgr
}

const speciesMedichamShadow = "medicham_shadow"

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

	if result.Attacker.ResolvedSpeciesID != speciesMedichamShadow {
		t.Errorf("Attacker.ResolvedSpeciesID = %q, want %q",
			result.Attacker.ResolvedSpeciesID, speciesMedichamShadow)
	}

	if !result.Attacker.Options.Shadow {
		t.Errorf("Attacker.Options.Shadow = false, want true (round-trip)")
	}

	if result.Attacker.ShadowVariantMissing {
		t.Errorf("ShadowVariantMissing = true; fixture publishes _shadow entry")
	}
}

// TestMatchupTool_ShadowMultipliersAffectDamage pins the Phase R4.7
// end-to-end path: Options.Shadow=true on a matchup combatant must
// flip Combatant.IsShadow on the engine-level pogopvp.Combatant
// (buildEngineCombatant does this), which in turn applies the
// ATK × 1.2 / DEF ÷ 1.2 multipliers inside Simulate. The test
// compares a shadow-vs-non-shadow matchup against the non-shadow
// mirror and asserts the final HP (winner or loser depending on
// asymmetry) differs — proving the multipliers propagated through
// the MCP → engine boundary.
func TestMatchupTool_ShadowMultipliersAffectDamage(t *testing.T) {
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
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"], "released": true}
  ],
  "moves": [
    {"moveId": "COUNTER", "name": "Counter", "type": "fighting",
     "power": 8, "energy": 0, "energyGain": 7, "cooldown": 1000, "turns": 2},
    {"moveId": "ICE_PUNCH", "name": "Ice Punch", "type": "ice",
     "power": 55, "energy": 40, "cooldown": 500}
  ]
}`

	mgr := newManagerWithFixture(t, shadowFixture)
	handler := tools.NewMatchupTool(mgr, nil).Handler()

	attacker := tools.Combatant{
		Species:  speciesMedicham,
		IV:       [3]int{15, 15, 15},
		Level:    40,
		FastMove: "COUNTER", ChargedMoves: []string{"ICE_PUNCH"},
	}
	defender := attacker

	// Baseline: non-shadow mirror. Identical stats — Simulate ties.
	_, baseline, err := handler(t.Context(), nil, tools.MatchupParams{
		Attacker: attacker,
		Defender: defender,
		League:   "great",
		Shields:  [2]int{0, 0},
	})
	if err != nil {
		t.Fatalf("baseline handler: %v", err)
	}

	// Shadow attacker vs non-shadow defender. Shadow multipliers on
	// the engine side change damage-per-tick on both sides (attacker
	// deals more; attacker takes more due to DEF ÷ 1.2), so the
	// fight ends sooner than the baseline mirror.
	shadowAttacker := attacker
	shadowAttacker.Options = tools.CombatantOptions{Shadow: true}

	_, withShadow, err := handler(t.Context(), nil, tools.MatchupParams{
		Attacker: shadowAttacker,
		Defender: defender,
		League:   "great",
		Shields:  [2]int{0, 0},
	})
	if err != nil {
		t.Fatalf("shadow handler: %v", err)
	}

	if withShadow.Turns >= baseline.Turns {
		t.Errorf("shadow vs non-shadow Turns = %d, want < baseline Turns = %d "+
			"(shadow ATK × 1.2 / DEF ÷ 1.2 must propagate into the simulation via IsShadow)",
			withShadow.Turns, baseline.Turns)
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

// TestMatchupTool_ShadowAutoResolvesShadowRankings pins Phase X-I
// round-1 review blocker: when Options.Shadow=true and FastMove is
// empty, ResolveMoveset must key on the shadow species id so the
// caller gets pvpoke's shadow-form recommended build, not the base
// species' moveset. The fixture ranks "medicham_shadow" with a
// distinct moveset (COUNTER + PSYCHIC) vs "medicham" (COUNTER +
// ICE_PUNCH) — the auto-resolve must pick the shadow row.
func TestMatchupTool_ShadowAutoResolvesShadowRankings(t *testing.T) {
	t.Parallel()

	const shadowFixtureGM = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 308, "speciesId": "medicham", "speciesName": "Medicham",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH","PSYCHIC"], "released": true},
    {"dex": 308, "speciesId": "medicham_shadow", "speciesName": "Medicham (Shadow)",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH","PSYCHIC"], "released": true},
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
    {"moveId": "PSYCHIC", "name": "Psychic", "type": "psychic",
     "power": 90, "energy": 55, "cooldown": 500},
    {"moveId": "CROSS_CHOP", "name": "Cross Chop", "type": "fighting",
     "power": 50, "energy": 35, "cooldown": 500}
  ]
}`

	const ranksFixture = `[
  {"speciesId": "medicham", "speciesName": "Medicham", "rating": 900,
   "moveset": ["COUNTER", "ICE_PUNCH"],
   "matchups": [], "counters": [],
   "stats": {"product": 2400, "atk": 107, "def": 139, "hp": 141}},
  {"speciesId": "medicham_shadow", "speciesName": "Medicham (Shadow)", "rating": 920,
   "moveset": ["COUNTER", "PSYCHIC"],
   "matchups": [], "counters": [],
   "stats": {"product": 2500, "atk": 130, "def": 116, "hp": 141}}
]`

	mgr := newManagerWithFixture(t, shadowFixtureGM)
	ranks := newMatchupRankingsFromFixture(t, ranksFixture)
	handler := tools.NewMatchupTool(mgr, ranks).Handler()

	_, result, err := handler(t.Context(), nil, tools.MatchupParams{
		Attacker: tools.Combatant{
			Species: speciesMedicham,
			IV:      [3]int{15, 15, 15},
			Level:   40,
			// FastMove omitted on purpose — triggers applyMovesetDefaults.
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

	wantCharged := []string{"PSYCHIC"}
	if len(result.Attacker.ChargedMoves) != 1 ||
		result.Attacker.ChargedMoves[0] != wantCharged[0] {
		t.Errorf("Attacker.ChargedMoves = %v, want %v (resolved from shadow rankings row)",
			result.Attacker.ChargedMoves, wantCharged)
	}

	if result.Attacker.ResolvedSpeciesID != speciesMedichamShadow {
		t.Errorf("Attacker.ResolvedSpeciesID = %q, want %q",
			result.Attacker.ResolvedSpeciesID, speciesMedichamShadow)
	}
}

// TestMatchupTool_ShadowOptionToleratesShadowSuffixedSpecies pins
// the dual-convention foot-gun fix: a client mixing the old suffix
// convention (Species: "medicham_shadow") with the new flag
// (Options.Shadow=true) must resolve to "medicham_shadow" without
// reporting shadow_variant_missing=true. Before the fix, the
// lookup chased "medicham_shadow_shadow", failed, fell back to
// "medicham_shadow", and misleadingly flagged the variant as
// missing even though it IS published.
func TestMatchupTool_ShadowOptionToleratesShadowSuffixedSpecies(t *testing.T) {
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
			Species:  speciesMedichamShadow,
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

	if result.Attacker.ResolvedSpeciesID != speciesMedichamShadow {
		t.Errorf("Attacker.ResolvedSpeciesID = %q, want %q (suffix should be stripped then re-added)",
			result.Attacker.ResolvedSpeciesID, speciesMedichamShadow)
	}

	if result.Attacker.ShadowVariantMissing {
		t.Errorf("ShadowVariantMissing = true; pvpoke DOES publish medicham_shadow — must not signal missing")
	}
}

// TestMatchupTool_PurifiedIsNoOpOnBattle pins an intentional design
// choice: Options.Purified affects cost estimation (×0.9 stardust
// and candy in pvp_second_move_cost) but must NOT alter combat
// resolution. Purification in Pokémon GO converts a Shadow Pokémon
// back to a normal form; the engine's simulator does not apply any
// purified-specific multipliers. A client-facing test locks the
// no-op invariant so an accidental wiring of Options.Purified into
// the battle path would be caught.
func TestMatchupTool_PurifiedIsNoOpOnBattle(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, matchupFixtureGamemaster)
	handler := tools.NewMatchupTool(mgr, nil).Handler()

	attackerBase := tools.Combatant{
		Species:      speciesMedicham,
		IV:           [3]int{15, 15, 15},
		Level:        40,
		FastMove:     "COUNTER",
		ChargedMoves: []string{"ICE_PUNCH"},
	}
	defender := tools.Combatant{
		Species:      "machamp",
		IV:           [3]int{10, 10, 10},
		Level:        30,
		FastMove:     "COUNTER",
		ChargedMoves: []string{"CROSS_CHOP"},
	}

	_, withoutPurified, err := handler(t.Context(), nil, tools.MatchupParams{
		Attacker: attackerBase,
		Defender: defender,
		Shields:  [2]int{1, 1},
	})
	if err != nil {
		t.Fatalf("baseline handler: %v", err)
	}

	attackerPurified := attackerBase
	attackerPurified.Options = tools.CombatantOptions{Purified: true}

	_, withPurified, err := handler(t.Context(), nil, tools.MatchupParams{
		Attacker: attackerPurified,
		Defender: defender,
		Shields:  [2]int{1, 1},
	})
	if err != nil {
		t.Fatalf("purified handler: %v", err)
	}

	if withoutPurified.Turns != withPurified.Turns {
		t.Errorf("Turns differ: baseline=%d purified=%d — Purified must be a no-op on combat",
			withoutPurified.Turns, withPurified.Turns)
	}

	if withoutPurified.Winner != withPurified.Winner {
		t.Errorf("Winner differs: baseline=%q purified=%q — Purified must be a no-op on combat",
			withoutPurified.Winner, withPurified.Winner)
	}

	if withoutPurified.HPRemaining != withPurified.HPRemaining {
		t.Errorf("HPRemaining differs: baseline=%v purified=%v — Purified must be a no-op on combat",
			withoutPurified.HPRemaining, withPurified.HPRemaining)
	}
}
