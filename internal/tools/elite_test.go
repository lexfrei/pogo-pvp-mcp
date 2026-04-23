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

// newTeamAnalysisToolFromFixture mirrors newTeamBuilderToolFromFixture
// for the sibling tool. The elite tests need control over both the
// eliteMoves block and an empty rankings slice (the DisallowElite /
// DisallowLegacy path short-circuits before hitting rankings).
func newTeamAnalysisToolFromFixture(t *testing.T, gmJSON, ranksJSON string) *tools.TeamAnalysisTool {
	t.Helper()

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(gmJSON))
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

	rankServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ranksJSON))
	}))
	t.Cleanup(rankServer.Close)

	ranksMgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  rankServer.URL,
		LocalDir: filepath.Join(t.TempDir(), "rankings"),
	})
	if err != nil {
		t.Fatalf("NewManager rankings: %v", err)
	}

	return tools.NewTeamAnalysisTool(gmMgr, ranksMgr)
}

// moveAquaTail is the elite (Community Day) charged move on
// quagsire in the pvpoke gamemaster. Every elite-path test in
// this file uses it because it is the root-cause Bug #2 scenario.
const moveAquaTail = "AQUA_TAIL"

// speciesQuagsire is the elite-move carrier species used across
// the elite-path tests.
const speciesQuagsire = "quagsire"

// eliteFixtureGamemaster mirrors legacyFixtureGamemaster but adds
// quagsire with AQUA_TAIL in the eliteMoves block — the Bug #1 / #2
// reproduction target. Medicham keeps its legacy PSYCHIC so tests
// can assert legacy-only vs elite-only disallow flags in isolation.
const eliteFixtureGamemaster = `{
  "id": "gamemaster",
  "timestamp": "2026-04-23 00:00:00",
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
      "released": true
    },
    {
      "dex": 195,
      "speciesId": "quagsire",
      "speciesName": "Quagsire",
      "baseStats": {"atk": 152, "def": 143, "hp": 216},
      "types": ["water", "ground"],
      "fastMoves": ["MUD_SHOT"],
      "chargedMoves": ["AQUA_TAIL", "STONE_EDGE", "MUD_BOMB"],
      "eliteMoves": ["AQUA_TAIL"],
      "released": true
    },
    {
      "dex": 184,
      "speciesId": "azumarill",
      "speciesName": "Azumarill",
      "baseStats": {"atk": 112, "def": 152, "hp": 225},
      "types": ["water", "fairy"],
      "fastMoves": ["BUBBLE"],
      "chargedMoves": ["ICE_BEAM", "PLAY_ROUGH"],
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
    {"moveId": "MUD_SHOT", "name": "Mud Shot", "type": "ground",
     "power": 3, "energy": 0, "energyGain": 9, "cooldown": 500, "turns": 1},
    {"moveId": "AQUA_TAIL", "name": "Aqua Tail", "type": "water",
     "power": 50, "energy": 35, "cooldown": 500},
    {"moveId": "STONE_EDGE", "name": "Stone Edge", "type": "rock",
     "power": 100, "energy": 55, "cooldown": 500},
    {"moveId": "MUD_BOMB", "name": "Mud Bomb", "type": "ground",
     "power": 55, "energy": 40, "cooldown": 500},
    {"moveId": "BUBBLE", "name": "Bubble", "type": "water",
     "power": 12, "energy": 0, "energyGain": 14, "cooldown": 1500, "turns": 3},
    {"moveId": "ICE_BEAM", "name": "Ice Beam", "type": "ice",
     "power": 90, "energy": 55, "cooldown": 500},
    {"moveId": "PLAY_ROUGH", "name": "Play Rough", "type": "fairy",
     "power": 90, "energy": 60, "cooldown": 500}
  ]
}`

// TestTeamBuilder_DisallowEliteRejectsExplicit reproduces Bug #1:
// before R6 the quagsire AQUA_TAIL case silently passed even with
// disallow_legacy=true because pvpoke stores community-day moves
// in eliteMoves, not legacyMoves. R6 adds disallow_elite which
// must surface ErrEliteConflict on this exact input.
func TestTeamBuilder_DisallowEliteRejectsExplicit(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderToolFromFixture(t, eliteFixtureGamemaster, `[]`)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{
			Species: speciesQuagsire, IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "MUD_SHOT", ChargedMoves: []string{moveAquaTail},
		},
		{
			Species: "azumarill", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "BUBBLE", ChargedMoves: []string{"ICE_BEAM"},
		},
		{
			Species: "medicham", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "COUNTER", ChargedMoves: []string{"ICE_PUNCH"},
		},
	}

	_, _, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:          pool,
		League:        leagueGreat,
		DisallowElite: true,
	})
	if !errors.Is(err, tools.ErrEliteConflict) {
		t.Fatalf("handler err = %v, want ErrEliteConflict", err)
	}
}

// TestTeamBuilder_DisallowLegacyAllowsEliteMoves pins the
// independence of the two flags: disallow_legacy=true alone must
// NOT reject elite moves. R5 + earlier clients incorrectly
// expected this to filter AQUA_TAIL because the data was
// mislabelled; now they must add disallow_elite explicitly.
func TestTeamBuilder_DisallowLegacyAllowsEliteMoves(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderToolFromFixture(t, eliteFixtureGamemaster, `[]`)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{
			Species: speciesQuagsire, IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "MUD_SHOT", ChargedMoves: []string{moveAquaTail},
		},
		{
			Species: "azumarill", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "BUBBLE", ChargedMoves: []string{"ICE_BEAM"},
		},
		{
			Species: "medicham", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "COUNTER", ChargedMoves: []string{"ICE_PUNCH"},
		},
	}

	_, _, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:           pool,
		League:         leagueGreat,
		DisallowLegacy: true,
	})
	if errors.Is(err, tools.ErrEliteConflict) {
		t.Fatalf("handler err = %v, want NOT ErrEliteConflict "+
			"(disallow_legacy=true must not reject elite moves)", err)
	}
	if errors.Is(err, tools.ErrLegacyConflict) {
		t.Fatalf("handler err = %v, want NOT ErrLegacyConflict "+
			"(no legacy moves in pool)", err)
	}
}

// TestTeamBuilder_DisallowLegacyRejectsLegacyOnly confirms the
// complement: disallow_legacy=true still catches Medicham PSYCHIC
// (legacy) even when the pool also contains an elite move, so the
// split does not accidentally widen the legacy gate.
func TestTeamBuilder_DisallowLegacyRejectsLegacyOnly(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderToolFromFixture(t, eliteFixtureGamemaster, `[]`)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{
			Species: speciesQuagsire, IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "MUD_SHOT", ChargedMoves: []string{moveAquaTail},
		},
		{
			Species: "azumarill", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "BUBBLE", ChargedMoves: []string{"ICE_BEAM"},
		},
		{
			Species: "medicham", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "COUNTER", ChargedMoves: []string{movePsychic},
		},
	}

	_, _, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:           pool,
		League:         leagueGreat,
		DisallowLegacy: true,
	})
	if !errors.Is(err, tools.ErrLegacyConflict) {
		t.Fatalf("handler err = %v, want ErrLegacyConflict "+
			"(medicham PSYCHIC is legacy even with quagsire AQUA_TAIL in pool)", err)
	}
}

// TestTeamBuilder_DisallowBothRejectsEitherCategory validates the
// union semantic: both flags set rejects the first conflict found,
// whichever category it happens to belong to.
func TestTeamBuilder_DisallowBothRejectsEitherCategory(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderToolFromFixture(t, eliteFixtureGamemaster, `[]`)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{
			Species: speciesQuagsire, IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "MUD_SHOT", ChargedMoves: []string{moveAquaTail},
		},
		{
			Species: "azumarill", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "BUBBLE", ChargedMoves: []string{"ICE_BEAM"},
		},
		{
			Species: "medicham", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "COUNTER", ChargedMoves: []string{"ICE_PUNCH"},
		},
	}

	_, _, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:           pool,
		League:         leagueGreat,
		DisallowLegacy: true,
		DisallowElite:  true,
	})
	if !errors.Is(err, tools.ErrEliteConflict) {
		t.Fatalf("handler err = %v, want ErrEliteConflict "+
			"(quagsire AQUA_TAIL is elite; both flags on should still catch it)", err)
	}
}

// TestMoveRef_NewMoveRefTagsElite pins MoveRef.Elite population
// via the live fixture. Quagsire AQUA_TAIL is elite (not legacy);
// Quagsire MUD_SHOT is neither; Medicham PSYCHIC is legacy (not
// elite). All three checks run through the same tool path.
func TestSpeciesInfo_EliteMovesSurfaced(t *testing.T) {
	t.Parallel()

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(eliteFixtureGamemaster))
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

	handler := tools.NewSpeciesInfoTool(gmMgr, nil).Handler()

	_, result, err := handler(t.Context(), nil, tools.SpeciesInfoParams{Species: speciesQuagsire})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.EliteMoves) != 1 || result.EliteMoves[0] != moveAquaTail {
		t.Errorf("EliteMoves = %v, want [%s]", result.EliteMoves, moveAquaTail)
	}
	if len(result.LegacyMoves) != 0 {
		t.Errorf("LegacyMoves = %v, want empty (quagsire has no legacyMoves)", result.LegacyMoves)
	}

	// Per-move refs: AQUA_TAIL.elite=true, AQUA_TAIL.legacy=false,
	// STONE_EDGE.elite=false, STONE_EDGE.legacy=false.
	for _, ref := range result.ChargedMoves {
		switch ref.ID {
		case moveAquaTail:
			if !ref.Elite {
				t.Errorf("ChargedMoves[%s].Elite = false, want true", ref.ID)
			}
			if ref.Legacy {
				t.Errorf("ChargedMoves[%s].Legacy = true, want false", ref.ID)
			}
		case "STONE_EDGE":
			if ref.Elite {
				t.Errorf("ChargedMoves[%s].Elite = true, want false", ref.ID)
			}
			if ref.Legacy {
				t.Errorf("ChargedMoves[%s].Legacy = true, want false", ref.ID)
			}
		}
	}
}

// TestMoveInfo_EliteReverseIndex reproduces the elite_of reverse
// lookup. AQUA_TAIL is elite on quagsire only (in the fixture);
// its legacy_on_species list must be empty. Conversely PSYCHIC
// appears in legacy_on_species on medicham and absent from
// elite_on_species.
func TestMoveInfo_EliteReverseIndex(t *testing.T) {
	t.Parallel()

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(eliteFixtureGamemaster))
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

	handler := tools.NewMoveInfoTool(gmMgr).Handler()

	_, aquaResult, err := handler(t.Context(), nil, tools.MoveInfoParams{MoveID: moveAquaTail})
	if err != nil {
		t.Fatalf("handler AQUA_TAIL: %v", err)
	}

	if len(aquaResult.EliteOnSpecies) != 1 || aquaResult.EliteOnSpecies[0] != speciesQuagsire {
		t.Errorf("AQUA_TAIL.EliteOnSpecies = %v, want [quagsire]", aquaResult.EliteOnSpecies)
	}
	if len(aquaResult.LegacyOnSpecies) != 0 {
		t.Errorf("AQUA_TAIL.LegacyOnSpecies = %v, want empty", aquaResult.LegacyOnSpecies)
	}

	_, psychicResult, err := handler(t.Context(), nil, tools.MoveInfoParams{MoveID: movePsychic})
	if err != nil {
		t.Fatalf("handler PSYCHIC: %v", err)
	}

	if len(psychicResult.LegacyOnSpecies) != 1 || psychicResult.LegacyOnSpecies[0] != speciesMedicham {
		t.Errorf("PSYCHIC.LegacyOnSpecies = %v, want [%s]",
			psychicResult.LegacyOnSpecies, speciesMedicham)
	}
	if len(psychicResult.EliteOnSpecies) != 0 {
		t.Errorf("PSYCHIC.EliteOnSpecies = %v, want empty", psychicResult.EliteOnSpecies)
	}
}

// TestTeamBuilder_DisallowEliteRejectsResolvedElite is the
// auto-fill sibling of TestTeamBuilder_DisallowLegacyRejectsResolvedLegacy.
// When the pvpoke recommendation contains an elite move and the
// combatant leaves FastMove empty, the rejection must fire inside
// applyMovesetDefaults via rejectResolvedElite — not only on
// explicit moveset input.
func TestTeamBuilder_DisallowEliteRejectsResolvedElite(t *testing.T) {
	t.Parallel()

	// Ranking fixture recommends AQUA_TAIL (elite on quagsire).
	const ranksJSON = `[
  {"speciesId": "quagsire", "speciesName": "Quagsire", "rating": 700,
   "moveset": ["MUD_SHOT", "AQUA_TAIL", "STONE_EDGE"],
   "matchups": [], "counters": [],
   "stats": {"product": 2100, "atk": 100, "def": 130, "hp": 180}}
]`

	tool := newTeamBuilderToolFromFixture(t, eliteFixtureGamemaster, ranksJSON)
	handler := tool.Handler()

	// Quagsire with empty moveset → auto-fill pulls AQUA_TAIL from
	// the rankings recommendation; DisallowElite must trip
	// ErrEliteConflict before simulation.
	pool := []tools.Combatant{
		{Species: speciesQuagsire, IV: [3]int{15, 15, 15}, Level: 40},
		{
			Species: "azumarill", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "BUBBLE", ChargedMoves: []string{"ICE_BEAM"},
		},
		{
			Species: "medicham", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "COUNTER", ChargedMoves: []string{"ICE_PUNCH"},
		},
	}

	_, _, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:          pool,
		League:        leagueGreat,
		DisallowElite: true,
	})
	if !errors.Is(err, tools.ErrEliteConflict) {
		t.Errorf("error = %v, want wrapping ErrEliteConflict (auto-fill landed on elite AQUA_TAIL)", err)
	}
}

// budgetETMFixture extends eliteFixtureGamemaster with a second
// species (azumarill) that also has an elite charged move, so the
// three-member pool needed by team_builder can contain 2+ elite
// charged moves — enough to exercise the real ETM reject gate.
// eliteFixtureGamemaster alone only has one elite-armed species,
// giving max 1 elite move per team; EliteChargedTM=0 is treated
// as "off" (consistent with StardustLimit), so we need 2+ to
// actually trip the gate.
const budgetETMFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-23 00:00:00",
  "pokemon": [
    {
      "dex": 308, "speciesId": "medicham", "speciesName": "Medicham",
      "baseStats": {"atk": 121, "def": 152, "hp": 155},
      "types": ["fighting", "psychic"],
      "fastMoves": ["COUNTER", "PSYCHO_CUT"], "chargedMoves": ["ICE_PUNCH", "DYNAMIC_PUNCH"],
      "eliteMoves": ["DYNAMIC_PUNCH", "PSYCHO_CUT"],
      "released": true
    },
    {
      "dex": 195, "speciesId": "quagsire", "speciesName": "Quagsire",
      "baseStats": {"atk": 152, "def": 143, "hp": 216},
      "types": ["water", "ground"],
      "fastMoves": ["MUD_SHOT"], "chargedMoves": ["AQUA_TAIL", "STONE_EDGE", "MUD_BOMB"],
      "eliteMoves": ["AQUA_TAIL"],
      "released": true
    },
    {
      "dex": 184, "speciesId": "azumarill", "speciesName": "Azumarill",
      "baseStats": {"atk": 112, "def": 152, "hp": 225},
      "types": ["water", "fairy"],
      "fastMoves": ["BUBBLE"], "chargedMoves": ["ICE_BEAM", "HYDRO_PUMP", "PLAY_ROUGH"],
      "eliteMoves": ["HYDRO_PUMP"],
      "released": true
    }
  ],
  "moves": [
    {"moveId": "COUNTER", "name": "Counter", "type": "fighting",
     "power": 8, "energy": 0, "energyGain": 7, "cooldown": 1000, "turns": 2},
    {"moveId": "PSYCHO_CUT", "name": "Psycho Cut", "type": "psychic",
     "power": 3, "energy": 0, "energyGain": 9, "cooldown": 500, "turns": 1},
    {"moveId": "ICE_PUNCH", "name": "Ice Punch", "type": "ice",
     "power": 55, "energy": 40, "cooldown": 500},
    {"moveId": "DYNAMIC_PUNCH", "name": "Dynamic Punch", "type": "fighting",
     "power": 90, "energy": 50, "cooldown": 500},
    {"moveId": "MUD_SHOT", "name": "Mud Shot", "type": "ground",
     "power": 3, "energy": 0, "energyGain": 9, "cooldown": 500, "turns": 1},
    {"moveId": "AQUA_TAIL", "name": "Aqua Tail", "type": "water",
     "power": 50, "energy": 35, "cooldown": 500},
    {"moveId": "STONE_EDGE", "name": "Stone Edge", "type": "rock",
     "power": 100, "energy": 55, "cooldown": 500},
    {"moveId": "MUD_BOMB", "name": "Mud Bomb", "type": "ground",
     "power": 55, "energy": 40, "cooldown": 500},
    {"moveId": "BUBBLE", "name": "Bubble", "type": "water",
     "power": 12, "energy": 0, "energyGain": 14, "cooldown": 1500, "turns": 3},
    {"moveId": "ICE_BEAM", "name": "Ice Beam", "type": "ice",
     "power": 90, "energy": 55, "cooldown": 500},
    {"moveId": "HYDRO_PUMP", "name": "Hydro Pump", "type": "water",
     "power": 130, "energy": 75, "cooldown": 500},
    {"moveId": "PLAY_ROUGH", "name": "Play Rough", "type": "fairy",
     "power": 90, "energy": 60, "cooldown": 500}
  ]
}`

// TestTeamBuilder_BudgetETMChargedDropsOverBudget pins R7.P3:
// a pool with 3 members each explicitly using their species' elite
// charged move produces teams with 3 elite charged moves total.
// BudgetSpec.EliteChargedTM=1 rejects such teams; =3 keeps them.
func TestTeamBuilder_BudgetETMChargedDropsOverBudget(t *testing.T) {
	t.Parallel()

	const ranksJSON = `[
  {"speciesId": "quagsire", "speciesName": "Quagsire", "rating": 700,
   "moveset": ["MUD_SHOT", "STONE_EDGE", "MUD_BOMB"],
   "stats": {"product": 2100, "atk": 100, "def": 130, "hp": 180}},
  {"speciesId": "azumarill", "speciesName": "Azumarill", "rating": 680,
   "moveset": ["BUBBLE", "ICE_BEAM", "PLAY_ROUGH"],
   "stats": {"product": 2000, "atk": 80, "def": 150, "hp": 200}},
  {"speciesId": "medicham", "speciesName": "Medicham", "rating": 650,
   "moveset": ["COUNTER", "ICE_PUNCH"],
   "stats": {"product": 2050, "atk": 106, "def": 139, "hp": 141}}
]`

	tool := newTeamBuilderToolFromFixture(t, budgetETMFixture, ranksJSON)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{
			Species: "quagsire", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "MUD_SHOT", ChargedMoves: []string{moveAquaTail},
		},
		{
			Species: "azumarill", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "BUBBLE", ChargedMoves: []string{"HYDRO_PUMP"},
		},
		{
			Species: speciesMedicham, IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "COUNTER", ChargedMoves: []string{"DYNAMIC_PUNCH"},
		},
	}

	// Budget EliteChargedTM=1: team needs 3, gate rejects.
	_, tight, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:   pool,
		League: leagueGreat,
		Budget: &tools.BudgetSpec{EliteChargedTM: 1},
	})
	if err != nil {
		t.Fatalf("handler tight: %v", err)
	}
	if len(tight.Teams) != 0 {
		t.Errorf("Teams len = %d, want 0 (EliteChargedTM=1 with 3 elite moves in pool must reject)",
			len(tight.Teams))
	}

	// Budget EliteChargedTM=3: gate fits, team kept.
	_, loose, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:   pool,
		League: leagueGreat,
		Budget: &tools.BudgetSpec{EliteChargedTM: 3},
	})
	if err != nil {
		t.Fatalf("handler loose: %v", err)
	}
	if len(loose.Teams) == 0 {
		t.Fatal("Teams empty; EliteChargedTM=3 must allow a 3-elite-move team")
	}
}

// TestTeamBuilder_BudgetETMFastDropsOverBudget pins the fast-ETM
// path specifically. Medicham in budgetETMFixture has PSYCHO_CUT
// in its eliteMoves; a pool that uses it consumes 1 EliteFastTM
// per team member. EliteFastTM=0 (gate off) keeps; EliteFastTM=1
// with only one fast-elite user in the pool also keeps; if that
// count ever climbed above budget, the gate would drop.
func TestTeamBuilder_BudgetETMFastDropsOverBudget(t *testing.T) {
	t.Parallel()

	const ranksJSON = `[
  {"speciesId": "quagsire", "speciesName": "Quagsire", "rating": 700,
   "moveset": ["MUD_SHOT", "STONE_EDGE", "MUD_BOMB"],
   "stats": {"product": 2100, "atk": 100, "def": 130, "hp": 180}},
  {"speciesId": "azumarill", "speciesName": "Azumarill", "rating": 680,
   "moveset": ["BUBBLE", "ICE_BEAM", "PLAY_ROUGH"],
   "stats": {"product": 2000, "atk": 80, "def": 150, "hp": 200}},
  {"speciesId": "medicham", "speciesName": "Medicham", "rating": 650,
   "moveset": ["PSYCHO_CUT", "ICE_PUNCH"],
   "stats": {"product": 2050, "atk": 106, "def": 139, "hp": 141}}
]`

	tool := newTeamBuilderToolFromFixture(t, budgetETMFixture, ranksJSON)
	handler := tool.Handler()

	// All three pool members use their elite FAST move (quagsire
	// and azumarill have MUD_SHOT / BUBBLE, which are NOT elite in
	// this fixture — only PSYCHO_CUT on medicham is. So medicham
	// is the only fast-ETM consumer. Pool designed so the team
	// needs exactly 1 EliteFastTM.)
	pool := []tools.Combatant{
		{
			Species: "quagsire", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "MUD_SHOT", ChargedMoves: []string{"STONE_EDGE"},
		},
		{
			Species: "azumarill", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "BUBBLE", ChargedMoves: []string{"ICE_BEAM"},
		},
		{
			Species: speciesMedicham, IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "PSYCHO_CUT", ChargedMoves: []string{"ICE_PUNCH"},
		},
	}

	// Positive case: EliteFastTM=1 allows the 1-fast-elite team.
	_, fit, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:   pool,
		League: leagueGreat,
		Budget: &tools.BudgetSpec{EliteFastTM: 1},
	})
	if err != nil {
		t.Fatalf("handler fit: %v", err)
	}
	if len(fit.Teams) == 0 {
		t.Fatal("Teams empty; EliteFastTM=1 with one fast-elite member must keep the team")
	}

	// Negative case: force all three members to use elite fast
	// moves. Only medicham has one, so we duplicate the pool to
	// force two medicham slots + one other — gives fastNeeded=2,
	// EliteFastTM=1 rejects.
	dupPool := []tools.Combatant{
		{
			Species: speciesMedicham, IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "PSYCHO_CUT", ChargedMoves: []string{"ICE_PUNCH"},
		},
		{
			Species: speciesMedicham, IV: [3]int{14, 15, 15}, Level: 40,
			FastMove: "PSYCHO_CUT", ChargedMoves: []string{"ICE_PUNCH"},
		},
		{
			Species: "quagsire", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "MUD_SHOT", ChargedMoves: []string{"STONE_EDGE"},
		},
	}

	_, rejected, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:   dupPool,
		League: leagueGreat,
		Budget: &tools.BudgetSpec{EliteFastTM: 1},
	})
	if err != nil {
		t.Fatalf("handler rejected: %v", err)
	}
	if len(rejected.Teams) != 0 {
		t.Errorf("Teams len = %d, want 0 (two medicham elite-fast members exceed EliteFastTM=1)",
			len(rejected.Teams))
	}
}

// TestTeamBuilder_BudgetETMZeroTreatedAsOff pins the convention
// that EliteChargedTM=0 / EliteFastTM=0 is treated as "gate not
// configured" (same as StardustLimit=0). A team with elite moves
// must still come through — R7.P3 docs this explicitly for
// consistency with the stardust gate.
func TestTeamBuilder_BudgetETMZeroTreatedAsOff(t *testing.T) {
	t.Parallel()

	const ranksJSON = `[
  {"speciesId": "quagsire", "speciesName": "Quagsire", "rating": 700,
   "moveset": ["MUD_SHOT", "STONE_EDGE", "MUD_BOMB"],
   "stats": {"product": 2100, "atk": 100, "def": 130, "hp": 180}},
  {"speciesId": "azumarill", "speciesName": "Azumarill", "rating": 680,
   "moveset": ["BUBBLE", "ICE_BEAM", "PLAY_ROUGH"],
   "stats": {"product": 2000, "atk": 80, "def": 150, "hp": 200}},
  {"speciesId": "medicham", "speciesName": "Medicham", "rating": 650,
   "moveset": ["COUNTER", "ICE_PUNCH"],
   "stats": {"product": 2050, "atk": 106, "def": 139, "hp": 141}}
]`

	tool := newTeamBuilderToolFromFixture(t, budgetETMFixture, ranksJSON)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{
			Species: "quagsire", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "MUD_SHOT", ChargedMoves: []string{moveAquaTail},
		},
		{
			Species: "azumarill", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "BUBBLE", ChargedMoves: []string{"HYDRO_PUMP"},
		},
		{
			Species: speciesMedicham, IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "COUNTER", ChargedMoves: []string{"DYNAMIC_PUNCH"},
		},
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:   pool,
		League: leagueGreat,
		Budget: &tools.BudgetSpec{EliteChargedTM: 0, EliteFastTM: 0},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(result.Teams) == 0 {
		t.Fatal("Teams empty; EliteChargedTM=0 must behave as 'gate off' (consistent with StardustLimit=0)")
	}
}

// TestCounterFinder_DisallowEliteFiltersMetaFallback is the elite
// sibling of TestCounterFinder_DisallowLegacyFiltersMetaFallback.
// With an empty from_pool the tool scans the top-N pvpoke meta; a
// ranking entry whose recommended moveset contains an elite move
// for its own species must drop out under disallow_elite=true so
// the tool never recommends an unobtainable moveset.
func TestCounterFinder_DisallowEliteFiltersMetaFallback(t *testing.T) {
	t.Parallel()

	const ranksJSON = `[
  {"speciesId": "quagsire", "speciesName": "Quagsire", "rating": 900,
   "moveset": ["MUD_SHOT", "AQUA_TAIL"],
   "matchups": [], "counters": [],
   "stats": {"product": 2400, "atk": 100, "def": 130, "hp": 180}},
  {"speciesId": "azumarill", "speciesName": "Azumarill", "rating": 880,
   "moveset": ["BUBBLE", "ICE_BEAM"],
   "matchups": [], "counters": [],
   "stats": {"product": 2500, "atk": 80, "def": 150, "hp": 200}}
]`

	tool := newCounterFinderTool(t, eliteFixtureGamemaster, ranksJSON)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.CounterFinderParams{
		Target: tools.Combatant{
			Species: speciesMedicham, IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: moveCounter, ChargedMoves: []string{"ICE_PUNCH"},
		},
		League:        leagueGreat,
		TopN:          5,
		DisallowElite: true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	for _, counter := range result.Counters {
		if counter.Counter.Species == speciesQuagsire {
			t.Errorf(
				"quagsire surfaced under disallow_elite=true with elite AQUA_TAIL in recommended moveset; "+
					"counters = %+v", result.Counters)
		}
	}

	if len(result.Counters) == 0 {
		t.Error("Counters empty; expected azumarill (all-regular moveset) to remain after filtering")
	}
}

// TestRank_OptimalHasEliteDetected pins the new Moveset.HasElite
// aggregate: quagsire's recommended build in the fixture includes
// AQUA_TAIL (elite on quagsire) so HasElite must be true while
// HasLegacy stays false.
func TestRank_OptimalHasEliteDetected(t *testing.T) {
	t.Parallel()

	const ranksJSON = `[
  {"speciesId": "quagsire", "speciesName": "Quagsire", "rating": 800,
   "moveset": ["MUD_SHOT", "AQUA_TAIL", "STONE_EDGE"],
   "matchups": [], "counters": [],
   "stats": {"product": 2100, "atk": 100, "def": 130, "hp": 180}}
]`

	tool := newRankToolFromFixture(t, eliteFixtureGamemaster, ranksJSON)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.RankParams{
		Species: speciesQuagsire,
		IV:      [3]int{0, 15, 15},
		League:  leagueGreat,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.OptimalMoveset == nil {
		t.Fatal("OptimalMoveset = nil, want populated")
	}
	if !result.OptimalMoveset.HasElite {
		t.Error("OptimalMoveset.HasElite = false, want true (AQUA_TAIL is elite on quagsire)")
	}
	if result.OptimalMoveset.HasLegacy {
		t.Error("OptimalMoveset.HasLegacy = true, want false (no legacy moves on quagsire)")
	}
	if result.NonEliteMoveset == nil {
		t.Fatal("NonEliteMoveset = nil, want populated when optimal has elite")
	}
	if result.NonEliteMoveset.Fast != "MUD_SHOT" {
		t.Errorf("NonEliteMoveset.Fast = %q, want MUD_SHOT", result.NonEliteMoveset.Fast)
	}
	// Non-elite fallback should pick from {STONE_EDGE, MUD_BOMB},
	// not AQUA_TAIL. Assert AQUA_TAIL is absent rather than pinning
	// a specific choice (rating-tied fallbacks can swap).
	for _, id := range result.NonEliteMoveset.Charged {
		if id == moveAquaTail {
			t.Errorf("NonEliteMoveset.Charged includes %s; want fallback without elite moves", moveAquaTail)
		}
	}
}

// TestCounterFinder_DisallowEliteIgnoredForTarget pins r7 finding
// #13 on the elite axis: a target with an elite move (Quagsire
// AQUA_TAIL — what the enemy actually uses in the ladder) must
// pass through even when disallow_elite=true. The flag is for
// the caller's own pool, never the opponent's build.
func TestCounterFinder_DisallowEliteIgnoredForTarget(t *testing.T) {
	t.Parallel()

	tool := newCounterFinderTool(t, eliteFixtureGamemaster, `[]`)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.CounterFinderParams{
		Target: tools.Combatant{
			Species: speciesQuagsire, IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "MUD_SHOT", ChargedMoves: []string{moveAquaTail},
		},
		FromPool: []tools.Combatant{
			{
				Species: "azumarill", IV: [3]int{15, 15, 15}, Level: 40,
				FastMove: "BUBBLE", ChargedMoves: []string{"ICE_BEAM"},
			},
		},
		League:        leagueGreat,
		DisallowElite: true,
	})
	if err != nil {
		t.Fatalf("handler: %v (target must pass as-is, disallow_elite gates pool only)", err)
	}
	if len(result.Counters) == 0 {
		t.Fatal("Counters empty; expected azumarill to score against the elite-AQUA_TAIL quagsire target")
	}
	if result.Counters[0].Counter.Species != "azumarill" {
		t.Errorf("Counters[0].Counter.Species = %q, want azumarill",
			result.Counters[0].Counter.Species)
	}
}

// TestCounterFinder_DisallowLegacyIgnoredForTargetAutoFill pins the
// auto-fill half of r7 finding #13: an empty-moveset target whose
// pvpoke recommendation contains a legacy move must still be
// accepted and filled with the recommended (legacy) build, because
// the target represents the enemy's actual build. Without this
// test, a future refactor flipping the hardcoded (false, false) in
// applyMovesetDefaults back to (params.DisallowLegacy, ...) would
// regress silently — every other counter_finder test passes an
// explicit FastMove, short-circuiting the auto-fill path.
func TestCounterFinder_DisallowLegacyIgnoredForTargetAutoFill(t *testing.T) {
	t.Parallel()

	// Rankings recommend medicham with legacy PSYCHIC → auto-fill
	// would normally reject under DisallowLegacy=true; r7 fix
	// shields the target from this gate.
	const ranksJSON = `[
  {"speciesId": "medicham", "speciesName": "Medicham", "rating": 700,
   "moveset": ["COUNTER", "PSYCHIC", "ICE_PUNCH"],
   "matchups": [], "counters": [],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}}
]`

	tool := newCounterFinderTool(t, legacyFixtureGamemaster, ranksJSON)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.CounterFinderParams{
		Target: tools.Combatant{
			Species: speciesMedicham, IV: [3]int{15, 15, 15}, Level: 40,
			// FastMove + ChargedMoves intentionally omitted — forces
			// applyMovesetDefaults onto the auto-fill path where the
			// fix's hardcoded (false, false) flag-bypass lives.
		},
		FromPool: []tools.Combatant{
			{
				Species: "machamp", IV: [3]int{15, 15, 15}, Level: 40,
				FastMove: moveCounter, ChargedMoves: []string{"CROSS_CHOP"},
			},
		},
		League:         leagueGreat,
		DisallowLegacy: true,
	})
	if err != nil {
		t.Fatalf("handler: %v (target auto-fill must bypass disallow_legacy)", err)
	}
	if len(result.Counters) == 0 {
		t.Fatal("Counters empty; expected machamp to be scored")
	}
}

// TestCounterFinder_DisallowEliteIgnoredForTargetAutoFill is the
// elite-axis sibling of the legacy auto-fill test above. Uses
// quagsire + AQUA_TAIL in the rankings recommendation.
func TestCounterFinder_DisallowEliteIgnoredForTargetAutoFill(t *testing.T) {
	t.Parallel()

	const ranksJSON = `[
  {"speciesId": "quagsire", "speciesName": "Quagsire", "rating": 700,
   "moveset": ["MUD_SHOT", "AQUA_TAIL", "STONE_EDGE"],
   "matchups": [], "counters": [],
   "stats": {"product": 2100, "atk": 100, "def": 130, "hp": 180}}
]`

	tool := newCounterFinderTool(t, eliteFixtureGamemaster, ranksJSON)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.CounterFinderParams{
		Target: tools.Combatant{
			Species: speciesQuagsire, IV: [3]int{15, 15, 15}, Level: 40,
			// Empty moveset → auto-fill picks AQUA_TAIL (elite).
		},
		FromPool: []tools.Combatant{
			{
				Species: "azumarill", IV: [3]int{15, 15, 15}, Level: 40,
				FastMove: "BUBBLE", ChargedMoves: []string{"ICE_BEAM"},
			},
		},
		League:        leagueGreat,
		DisallowElite: true,
	})
	if err != nil {
		t.Fatalf("handler: %v (target auto-fill must bypass disallow_elite)", err)
	}
	if len(result.Counters) == 0 {
		t.Fatal("Counters empty; expected azumarill to be scored")
	}
}

// TestCounterFinder_DisallowEliteRejectsFromPoolMember is the
// companion elite sibling of the legacy from-pool gate test,
// ensuring the guard still applies where it should — on the
// candidate pool (what the caller can field), not the target.
func TestCounterFinder_DisallowEliteRejectsFromPoolMember(t *testing.T) {
	t.Parallel()

	tool := newCounterFinderTool(t, eliteFixtureGamemaster, `[]`)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.CounterFinderParams{
		Target: tools.Combatant{
			Species: "azumarill", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "BUBBLE", ChargedMoves: []string{"ICE_BEAM"},
		},
		FromPool: []tools.Combatant{
			{
				Species: speciesQuagsire, IV: [3]int{15, 15, 15}, Level: 40,
				FastMove: "MUD_SHOT", ChargedMoves: []string{moveAquaTail},
			},
		},
		League:        leagueGreat,
		DisallowElite: true,
	})
	if !errors.Is(err, tools.ErrEliteConflict) {
		t.Errorf("error = %v, want wrapping ErrEliteConflict (pool member uses elite AQUA_TAIL)", err)
	}
}

// TestRank_RankingsByCupCarriesHasElite pins that the per-cup
// Moveset rows emit HasElite=true when the recommended moveset is
// elite — the parallel axis to TestRank_OptimalHasEliteDetected.
// Before round-2's movesetFromEntry fix, RankingsByCup[*].Moveset
// would emit HasLegacy correctly but HasElite=false silently.
func TestRank_RankingsByCupCarriesHasElite(t *testing.T) {
	t.Parallel()

	const ranksJSON = `[
  {"speciesId": "quagsire", "speciesName": "Quagsire", "rating": 800,
   "moveset": ["MUD_SHOT", "AQUA_TAIL", "STONE_EDGE"],
   "matchups": [], "counters": [],
   "stats": {"product": 2100, "atk": 100, "def": 130, "hp": 180}}
]`

	tool := newRankToolFromFixture(t, eliteFixtureGamemaster, ranksJSON)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.RankParams{
		Species: speciesQuagsire,
		IV:      [3]int{0, 15, 15},
		League:  leagueGreat,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.RankingsByCup) == 0 {
		t.Fatal("RankingsByCup empty; expected at least the open-league entry")
	}

	found := false
	for _, entry := range result.RankingsByCup {
		if entry.Moveset == nil {
			continue
		}
		found = true
		if !entry.Moveset.HasElite {
			t.Errorf("RankingsByCup[%s].Moveset.HasElite = false, want true (AQUA_TAIL is elite)", entry.Cup)
		}
		if entry.Moveset.HasLegacy {
			t.Errorf("RankingsByCup[%s].Moveset.HasLegacy = true, want false", entry.Cup)
		}
	}
	if !found {
		t.Fatal("no RankingsByCup entry carried a non-nil Moveset")
	}
}

// TestTeamAnalysis_DisallowEliteExplicit mirrors the team_builder
// test at the team_analysis layer — the client's reported 4-round
// regression was for team_analysis specifically.
func TestTeamAnalysis_DisallowEliteExplicit(t *testing.T) {
	t.Parallel()

	tool := newTeamAnalysisToolFromFixture(t, eliteFixtureGamemaster, `[]`)
	handler := tool.Handler()

	team := []tools.Combatant{
		{
			Species: speciesQuagsire, IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "MUD_SHOT", ChargedMoves: []string{moveAquaTail},
		},
		{
			Species: "azumarill", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "BUBBLE", ChargedMoves: []string{"ICE_BEAM"},
		},
		{
			Species: "medicham", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "COUNTER", ChargedMoves: []string{"ICE_PUNCH"},
		},
	}

	_, _, err := handler(t.Context(), nil, tools.TeamAnalysisParams{
		Team:          team,
		League:        leagueGreat,
		DisallowElite: true,
	})
	if !errors.Is(err, tools.ErrEliteConflict) {
		t.Fatalf("handler err = %v, want ErrEliteConflict", err)
	}
}
