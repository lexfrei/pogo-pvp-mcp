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

	_, _, err := handler(t.Context(), nil, tools.CounterFinderParams{
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
	if errors.Is(err, tools.ErrEliteConflict) {
		t.Errorf("error = %v, want NO ErrEliteConflict (target must pass as-is, disallow_elite gates pool only)", err)
	}
}

// TestCounterFinder_DisallowEliteRejectsFromPoolMember is the
// companion elite sibling. Parallel to the existing legacy from-
// pool gate test, ensures the guard still applies where it should.
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
