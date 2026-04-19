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

// TestCounterFinder_MetaFallback pins the empty-FromPool branch:
// candidates come from the top-N meta, scored and sorted.
func TestCounterFinder_MetaFallback(t *testing.T) {
	t.Parallel()

	const ranksJSON = `[
  {"speciesId": "machamp", "speciesName": "Machamp", "rating": 900,
   "moveset": ["COUNTER", "CROSS_CHOP"],
   "matchups": [], "counters": [],
   "stats": {"product": 2400, "atk": 170, "def": 130, "hp": 180}},
  {"speciesId": "azumarill", "speciesName": "Azumarill", "rating": 880,
   "moveset": ["BUBBLE", "ICE_BEAM"],
   "matchups": [], "counters": [],
   "stats": {"product": 2500, "atk": 80, "def": 150, "hp": 200}}
]`

	tool := newCounterFinderTool(t, counterFinderFixture, ranksJSON)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.CounterFinderParams{
		Target: tools.Combatant{
			Species: speciesMedicham, IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: moveCounter, ChargedMoves: []string{moveIcePunch},
		},
		League: leagueGreat,
		TopN:   5,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Counters) == 0 {
		t.Fatal("meta fallback produced 0 counters; want ≥1")
	}
}

// TestCounterFinder_InvalidMetaTopN pins the negative-MetaTopN
// guard — without it, the slice op `entries[:negative]` panics.
func TestCounterFinder_InvalidMetaTopN(t *testing.T) {
	t.Parallel()

	tool := newCounterFinderTool(t, counterFinderFixture, `[]`)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.CounterFinderParams{
		Target: tools.Combatant{
			Species: speciesMedicham, IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: moveCounter, ChargedMoves: []string{moveIcePunch},
		},
		League:   leagueGreat,
		MetaTopN: -1,
	})
	if !errors.Is(err, tools.ErrInvalidTopN) {
		t.Errorf("error = %v, want wrapping ErrInvalidTopN", err)
	}
}

// TestCounterFinder_PoolTooLarge pins the MaxPoolSize guard on
// FromPool.
func TestCounterFinder_PoolTooLarge(t *testing.T) {
	t.Parallel()

	tool := newCounterFinderTool(t, counterFinderFixture, `[]`)
	handler := tool.Handler()

	pool := make([]tools.Combatant, tools.MaxPoolSize+1)
	for i := range pool {
		pool[i] = tools.Combatant{
			Species: "machamp", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: moveCounter, ChargedMoves: []string{"CROSS_CHOP"},
		}
	}

	_, _, err := handler(t.Context(), nil, tools.CounterFinderParams{
		Target: tools.Combatant{
			Species: speciesMedicham, IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: moveCounter, ChargedMoves: []string{moveIcePunch},
		},
		FromPool: pool,
		League:   leagueGreat,
	})
	if !errors.Is(err, tools.ErrPoolTooLarge) {
		t.Errorf("error = %v, want wrapping ErrPoolTooLarge", err)
	}
}

// TestCounterFinder_InvalidShields rejects out-of-range shield
// values (≥3 or <0) before any simulation.
func TestCounterFinder_InvalidShields(t *testing.T) {
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
		League:  leagueGreat,
		Shields: []int{3},
	})
	if !errors.Is(err, tools.ErrInvalidShields) {
		t.Errorf("error = %v, want wrapping ErrInvalidShields", err)
	}
}

// TestCounterFinder_MultiScenarioAveraging confirms BattleRating
// averages across the provided scenarios list and ScenarioResults
// carries one entry per scenario that simulated successfully.
func TestCounterFinder_MultiScenarioAveraging(t *testing.T) {
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
		},
		League:  leagueGreat,
		Shields: []int{0, 1, 2},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Counters) != 1 {
		t.Fatalf("Counters len = %d, want 1", len(result.Counters))
	}

	sr := result.Counters[0].ScenarioResults
	if len(sr) != 3 {
		t.Fatalf("ScenarioResults len = %d, want 3", len(sr))
	}

	seenShields := map[int]bool{}
	for _, s := range sr {
		seenShields[s.Shields] = true
	}

	for _, want := range []int{0, 1, 2} {
		if !seenShields[want] {
			t.Errorf("ScenarioResults missing shields=%d: %+v", want, sr)
		}
	}
}

// TestCounterFinder_GamemasterNotLoaded pins the defensive branch
// when the gamemaster manager has no snapshot yet.
func TestCounterFinder_GamemasterNotLoaded(t *testing.T) {
	t.Parallel()

	mgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    "http://example.invalid",
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	handler := tools.NewCounterFinderTool(mgr, nil).Handler()

	_, _, err = handler(t.Context(), nil, tools.CounterFinderParams{
		Target: tools.Combatant{
			Species: speciesMedicham, IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: moveCounter, ChargedMoves: []string{moveIcePunch},
		},
		League: leagueGreat,
	})
	if !errors.Is(err, tools.ErrGamemasterNotLoaded) {
		t.Errorf("error = %v, want wrapping ErrGamemasterNotLoaded", err)
	}
}

// TestCounterFinder_TopNLargerThanCandidates guards against
// over-indexing when TopN exceeds the candidate count.
func TestCounterFinder_TopNLargerThanCandidates(t *testing.T) {
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
		},
		League: leagueGreat,
		TopN:   50,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Counters) != 1 {
		t.Errorf("Counters len = %d, want 1 (clipped to pool size)", len(result.Counters))
	}
}

// TestCounterFinder_DisallowLegacyRejectsTarget pins the target-side
// DisallowLegacy gate.
func TestCounterFinder_DisallowLegacyRejectsTarget(t *testing.T) {
	t.Parallel()

	// legacyFixtureGamemaster already declares PSYCHIC as legacy on
	// medicham; reusing that fixture to exercise the guard.
	tool := newCounterFinderTool(t, legacyFixtureGamemaster, `[]`)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.CounterFinderParams{
		Target: tools.Combatant{
			Species: speciesMedicham, IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: moveCounter, ChargedMoves: []string{movePsychic},
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
	if !errors.Is(err, tools.ErrLegacyConflict) {
		t.Errorf("error = %v, want wrapping ErrLegacyConflict (target uses legacy PSYCHIC)", err)
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
