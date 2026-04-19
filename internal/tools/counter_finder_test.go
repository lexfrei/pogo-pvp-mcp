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

const counterFinderFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 308, "speciesId": "medicham", "speciesName": "Medicham",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"],
     "released": true},
    {"dex": 68, "speciesId": "machamp", "speciesName": "Machamp",
     "baseStats": {"atk": 234, "def": 159, "hp": 207},
     "types": ["fighting"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["CROSS_CHOP"],
     "released": true},
    {"dex": 184, "speciesId": "azumarill", "speciesName": "Azumarill",
     "baseStats": {"atk": 112, "def": 152, "hp": 225},
     "types": ["water", "fairy"],
     "fastMoves": ["BUBBLE"], "chargedMoves": ["ICE_BEAM"],
     "released": true}
  ],
  "moves": [
    {"moveId": "COUNTER", "name": "Counter", "type": "fighting",
     "power": 8, "energy": 0, "energyGain": 7, "cooldown": 1000, "turns": 2},
    {"moveId": "ICE_PUNCH", "name": "Ice Punch", "type": "ice",
     "power": 55, "energy": 40, "cooldown": 500},
    {"moveId": "CROSS_CHOP", "name": "Cross Chop", "type": "fighting",
     "power": 50, "energy": 35, "cooldown": 500},
    {"moveId": "BUBBLE", "name": "Bubble", "type": "water",
     "power": 12, "energy": 0, "energyGain": 14, "cooldown": 1500, "turns": 3},
    {"moveId": "ICE_BEAM", "name": "Ice Beam", "type": "ice",
     "power": 90, "energy": 55, "cooldown": 500}
  ]
}`

func newCounterFinderTool(t *testing.T, gmJSON, ranksJSON string) *tools.CounterFinderTool {
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

	return tools.NewCounterFinderTool(gmMgr, ranksMgr)
}

// TestCounterFinder_FromPoolHappyPath pins that passing a pool
// returns sorted (desc by BattleRating) entries capped to TopN
// plus the echoed target moveset and the resolved scenarios list.
func TestCounterFinder_FromPoolHappyPath(t *testing.T) {
	t.Parallel()

	tool := newCounterFinderTool(t, counterFinderFixture, `[]`)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.CounterFinderParams{
		Target: tools.Combatant{
			Species: speciesMedicham, IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: moveCounter, ChargedMoves: []string{moveIcePunch},
		},
		FromPool: []tools.Combatant{
			{
				Species: "machamp", IV: [3]int{15, 15, 15}, Level: 40,
				FastMove: moveCounter, ChargedMoves: []string{"CROSS_CHOP"},
			},
			{
				Species: "azumarill", IV: [3]int{15, 15, 15}, Level: 40,
				FastMove: "BUBBLE", ChargedMoves: []string{"ICE_BEAM"},
			},
		},
		League: leagueGreat,
		TopN:   2,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.Target.Species != speciesMedicham {
		t.Errorf("Target.Species = %q, want %s", result.Target.Species, speciesMedicham)
	}
	if len(result.Counters) != 2 {
		t.Fatalf("Counters len = %d, want 2", len(result.Counters))
	}
	if result.Counters[0].BattleRating < result.Counters[1].BattleRating {
		t.Errorf("Counters not sorted desc: %d then %d",
			result.Counters[0].BattleRating, result.Counters[1].BattleRating)
	}
	if len(result.Scenarios) != 1 || result.Scenarios[0] != 1 {
		t.Errorf("Scenarios = %v, want [1] (default)", result.Scenarios)
	}
}

// TestCounterFinder_InvalidTopN rejects negative top_n.
func TestCounterFinder_InvalidTopN(t *testing.T) {
	t.Parallel()

	tool := newCounterFinderTool(t, counterFinderFixture, `[]`)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.CounterFinderParams{
		Target: tools.Combatant{
			Species: speciesMedicham, IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: moveCounter, ChargedMoves: []string{moveIcePunch},
		},
		FromPool: []tools.Combatant{
			{
				Species: "machamp", IV: [3]int{15, 15, 15}, Level: 40,
				FastMove: moveCounter, ChargedMoves: []string{"CROSS_CHOP"},
			},
		},
		League: leagueGreat,
		TopN:   -1,
	})
	if !errors.Is(err, tools.ErrInvalidTopN) {
		t.Errorf("error = %v, want wrapping ErrInvalidTopN", err)
	}
}
