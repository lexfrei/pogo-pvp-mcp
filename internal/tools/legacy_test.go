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

// newTeamBuilderToolFromFixture builds a TeamBuilderTool bound to a
// custom gamemaster fixture + rankings payload. Legacy tests need
// control over both the legacyMoves block and the empty rankings
// slice (the DisallowLegacy path short-circuits before hitting
// rankings, so an empty fixture suffices).
func newTeamBuilderToolFromFixture(t *testing.T, gmJSON, ranksJSON string) *tools.TeamBuilderTool {
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

	return tools.NewTeamBuilderTool(gmMgr, ranksMgr)
}

// legacyFixtureGamemaster mirrors a slimmed version of infoFixtureGamemaster
// with medicham marking PSYCHIC as legacy. Enough to exercise the
// MoveRef tagging and the DisallowLegacy guard end-to-end.
const legacyFixtureGamemaster = `{
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
    },
    {
      "dex": 68,
      "speciesId": "machamp",
      "speciesName": "Machamp",
      "baseStats": {"atk": 234, "def": 159, "hp": 207},
      "types": ["fighting"],
      "fastMoves": ["COUNTER"],
      "chargedMoves": ["CROSS_CHOP"],
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
    {"moveId": "BUBBLE", "name": "Bubble", "type": "water",
     "power": 12, "energy": 0, "energyGain": 14, "cooldown": 1500, "turns": 3},
    {"moveId": "ICE_BEAM", "name": "Ice Beam", "type": "ice",
     "power": 90, "energy": 55, "cooldown": 500},
    {"moveId": "PLAY_ROUGH", "name": "Play Rough", "type": "fairy",
     "power": 90, "energy": 60, "cooldown": 500},
    {"moveId": "CROSS_CHOP", "name": "Cross Chop", "type": "fighting",
     "power": 50, "energy": 35, "cooldown": 500}
  ]
}`

// TestTeamBuilder_DisallowLegacyExplicit pins the Phase-1C hard
// rejection path: a combatant passed in with an explicit legacy
// move under DisallowLegacy=true must surface ErrLegacyConflict
// before any simulation.
func TestTeamBuilder_DisallowLegacyExplicit(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderToolFromFixture(t, legacyFixtureGamemaster, `[]`)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{
			Species: "medicham", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "COUNTER", ChargedMoves: []string{movePsychic},
		},
		{
			Species: "azumarill", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "BUBBLE", ChargedMoves: []string{"ICE_BEAM"},
		},
		{
			Species: "machamp", IV: [3]int{15, 15, 15}, Level: 30,
			FastMove: "COUNTER", ChargedMoves: []string{"CROSS_CHOP"},
		},
	}

	_, _, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:           pool,
		League:         leagueGreat,
		DisallowLegacy: true,
	})
	if !errors.Is(err, tools.ErrLegacyConflict) {
		t.Errorf("error = %v, want wrapping ErrLegacyConflict (medicham PSYCHIC is legacy)", err)
	}
}

// TestTeamBuilder_DisallowLegacyAllowsNonLegacy confirms the
// converse: DisallowLegacy=true with non-legacy explicit movesets
// succeeds without error.
func TestTeamBuilder_DisallowLegacyAllowsNonLegacy(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderToolFromFixture(t, legacyFixtureGamemaster, `[]`)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{
			Species: "medicham", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "COUNTER", ChargedMoves: []string{"ICE_PUNCH"},
		},
		{
			Species: "azumarill", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "BUBBLE", ChargedMoves: []string{"ICE_BEAM"},
		},
		{
			Species: "machamp", IV: [3]int{15, 15, 15}, Level: 30,
			FastMove: "COUNTER", ChargedMoves: []string{"CROSS_CHOP"},
		},
	}

	_, _, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:           pool,
		League:         leagueGreat,
		DisallowLegacy: true,
	})
	if err != nil {
		t.Errorf("handler: %v (non-legacy movesets should pass the gate)", err)
	}
}
