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
			Species: "quagsire", IV: [3]int{15, 15, 15}, Level: 40,
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
			Species: "quagsire", IV: [3]int{15, 15, 15}, Level: 40,
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
			Species: "quagsire", IV: [3]int{15, 15, 15}, Level: 40,
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
			Species: "quagsire", IV: [3]int{15, 15, 15}, Level: 40,
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

// TestTeamAnalysis_DisallowEliteExplicit mirrors the team_builder
// test at the team_analysis layer — the client's reported 4-round
// regression was for team_analysis specifically.
func TestTeamAnalysis_DisallowEliteExplicit(t *testing.T) {
	t.Parallel()

	tool := newTeamAnalysisToolFromFixture(t, eliteFixtureGamemaster, `[]`)
	handler := tool.Handler()

	team := []tools.Combatant{
		{
			Species: "quagsire", IV: [3]int{15, 15, 15}, Level: 40,
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
