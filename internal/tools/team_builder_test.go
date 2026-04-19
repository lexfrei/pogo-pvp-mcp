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

// paretoLabelBestOverall is the label the production code writes onto
// overall-ranked teams; hoisted here so the test assertions and the
// quoted-string compare against one source of truth.
const paretoLabelBestOverall = "best overall"

func newTeamBuilderTool(t *testing.T) *tools.TeamBuilderTool {
	t.Helper()

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(teamAnalysisFixtureGamemaster))
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
		_, _ = w.Write([]byte(teamAnalysisRankingsFixture))
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

func baseCombatant(id string) tools.Combatant {
	return tools.Combatant{
		Species:      id,
		IV:           [3]int{15, 15, 15},
		Level:        40,
		FastMove:     "FAST1",
		ChargedMoves: []string{"CH1"},
	}
}

func TestTeamBuilderTool_HappyPath(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	pool := []tools.Combatant{
		baseCombatant("a"),
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:   pool,
		League: leagueGreat,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.PoolSize != 3 {
		t.Errorf("PoolSize = %d, want 3", result.PoolSize)
	}
	if result.Evaluated != 1 {
		t.Errorf("Evaluated = %d, want 1 combination", result.Evaluated)
	}
	if len(result.Teams) != 1 {
		t.Fatalf("Teams len = %d, want 1", len(result.Teams))
	}
	if result.Teams[0].TeamScore < 0 || result.Teams[0].TeamScore > 1000 {
		t.Errorf("TeamScore %.2f outside [0, 1000]", result.Teams[0].TeamScore)
	}
}

func TestTeamBuilderTool_PoolTooSmall(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool: []tools.Combatant{
			baseCombatant("a"),
			baseCombatant("b"),
		},
		League: leagueGreat,
	})
	if !errors.Is(err, tools.ErrPoolTooSmall) {
		t.Errorf("error = %v, want wrapping ErrPoolTooSmall", err)
	}
}

func TestTeamBuilderTool_BannedSpecies(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	pool := []tools.Combatant{
		baseCombatant("a"),
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, _, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:   pool,
		League: leagueGreat,
		Banned: []string{"b"},
	})
	if !errors.Is(err, tools.ErrPoolTooSmall) {
		t.Errorf("error = %v, want wrapping ErrPoolTooSmall (banned reduced pool to 2)", err)
	}
}

func TestTeamBuilderTool_RequiredAnchor(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	pool := []tools.Combatant{
		baseCombatant("a"),
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:     pool,
		League:   leagueGreat,
		Required: []string{"a"},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	for _, team := range result.Teams {
		found := false

		for _, member := range team.Members {
			if member.Species == "a" {
				found = true

				break
			}
		}

		if !found {
			t.Errorf("team %+v missing required anchor 'a'", team.Members)
		}
	}
}

func TestTeamBuilderTool_RequiredNotInPool(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	pool := []tools.Combatant{
		baseCombatant("a"),
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, _, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:     pool,
		League:   leagueGreat,
		Required: []string{"not_in_pool"},
	})
	if !errors.Is(err, tools.ErrRequiredNotInPool) {
		t.Errorf("error = %v, want wrapping ErrRequiredNotInPool", err)
	}
}

func TestTeamBuilderTool_RequiredWithDuplicateSpecies(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	aHigh := baseCombatant("a")
	aLow := baseCombatant("a")
	aLow.IV = [3]int{0, 15, 15}

	pool := []tools.Combatant{
		aHigh,
		aLow,
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:     pool,
		League:   leagueGreat,
		Required: []string{"a"},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	// With "a" appearing twice in the pool and only one required, the
	// team-builder must produce at least one triple that contains
	// exactly one "a" (not forced to take both copies).
	sawSingleA := false

	for _, team := range result.Teams {
		count := 0

		for _, member := range team.Members {
			if member.Species == "a" {
				count++
			}
		}

		if count == 1 {
			sawSingleA = true

			break
		}
	}

	if !sawSingleA {
		t.Error("no triple containing exactly one 'a' — duplicates were probably both forced in")
	}
}

func TestTeamBuilderTool_NegativeTopN(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool: []tools.Combatant{
			baseCombatant("a"),
			baseCombatant("b"),
			baseCombatant("c"),
		},
		League: leagueGreat,
		TopN:   -1,
	})
	if !errors.Is(err, tools.ErrInvalidTopN) {
		t.Errorf("error = %v, want wrapping ErrInvalidTopN", err)
	}
}

func TestTeamBuilderTool_InvalidShields(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool: []tools.Combatant{
			baseCombatant("a"),
			baseCombatant("b"),
			baseCombatant("c"),
		},
		League:  leagueGreat,
		Shields: []int{3, 1},
	})
	if !errors.Is(err, tools.ErrInvalidShields) {
		t.Errorf("error = %v, want wrapping ErrInvalidShields", err)
	}
}

func TestTeamBuilderTool_PoolTooLarge(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	// Build a pool one over the hard cap by repeating the same
	// (species, IV, moves) spec — pvpoke rankings / gamemaster
	// lookups do not matter because validation fires first.
	pool := make([]tools.Combatant, tools.MaxPoolSize+1)
	for i := range pool {
		pool[i] = baseCombatant("a")
	}

	_, _, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:   pool,
		League: leagueGreat,
	})
	if !errors.Is(err, tools.ErrPoolTooLarge) {
		t.Errorf("error = %v, want wrapping ErrPoolTooLarge", err)
	}
}

func TestTeamBuilderTool_TooManyRequired(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool: []tools.Combatant{
			baseCombatant("a"),
			baseCombatant("b"),
			baseCombatant("c"),
		},
		League:   leagueGreat,
		Required: []string{"a", "b", "c", "d"},
	})
	if !errors.Is(err, tools.ErrTooManyRequired) {
		t.Errorf("error = %v, want wrapping ErrTooManyRequired", err)
	}
}

func TestTeamBuilderTool_ReturnsPoolIndices(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	pool := []tools.Combatant{
		baseCombatant("a"),
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:   pool,
		League: leagueGreat,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(result.Teams) == 0 {
		t.Fatal("no teams returned")
	}

	team := result.Teams[0]
	if len(team.PoolIndices) != 3 {
		t.Errorf("PoolIndices len = %d, want 3", len(team.PoolIndices))
	}

	for idx, poolIdx := range team.PoolIndices {
		if poolIdx < 0 || poolIdx >= len(pool) {
			t.Errorf("PoolIndices[%d] = %d out of pool range", idx, poolIdx)
		}

		if pool[poolIdx].Species != team.Members[idx].Species {
			t.Errorf("PoolIndices[%d]->%s does not match Members[%d].Species=%s",
				idx, pool[poolIdx].Species, idx, team.Members[idx].Species)
		}
	}
}

// TestTeamBuilderTool_ParetoLabelPopulated confirms the default
// overall pipeline labels every returned team paretoLabelBestOverall — the
// hardcoded "highest average battle rating..." string is gone.
func TestTeamBuilderTool_ParetoLabelPopulated(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool: []tools.Combatant{
			baseCombatant("a"),
			baseCombatant("b"),
			baseCombatant("c"),
		},
		League: leagueGreat,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	for i, team := range result.Teams {
		if team.ParetoLabel != paretoLabelBestOverall {
			t.Errorf("Teams[%d].ParetoLabel = %q, want \"best overall\"", i, team.ParetoLabel)
		}
	}
}

// TestTeamBuilderTool_AllParetoScenarioCoverage confirms that
// optimize_for=all_pareto returns one paretoLabelBestOverall plus up to
// three per-scenario bests. The exact team count depends on whether
// the same triple wins multiple axes (deduplicated), but the label
// set must be a subset of the expected Pareto labels and always
// include paretoLabelBestOverall.
func TestTeamBuilderTool_AllParetoScenarioCoverage(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool: []tools.Combatant{
			baseCombatant("a"),
			baseCombatant("b"),
			baseCombatant("c"),
		},
		League:      leagueGreat,
		OptimizeFor: "all_pareto",
		MaxResults:  10,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("Teams is empty, want at least one Pareto team")
	}

	if len(result.Teams) > 4 {
		t.Errorf("Teams len = %d, want ≤ 4 (overall + 3 scenarios)", len(result.Teams))
	}

	validLabels := map[string]bool{
		paretoLabelBestOverall: true,
		"best 0-shield":        true,
		"best 1-shield":        true,
		"best 2-shield":        true,
	}

	sawOverall := false

	for i, team := range result.Teams {
		if !validLabels[team.ParetoLabel] {
			t.Errorf("Teams[%d].ParetoLabel = %q, not in Pareto label set", i, team.ParetoLabel)
		}
		if team.ParetoLabel == paretoLabelBestOverall {
			sawOverall = true
		}
	}

	if !sawOverall {
		t.Error("no \"best overall\" team in result, expected one regardless of scenario wins")
	}
}

// TestTeamBuilderTool_ShieldsDoNotAffectScoring pins the Phase-D
// semantic: the per-scenario rating matrix is always computed over
// [0, 1, 2] shield scenarios regardless of the caller's Shields
// slice, so scoring results must be identical across single-
// scenario Shields overrides. Only OptimizeFor picks the reporting
// axis. If someone later re-wires Shields into the scoring
// pipeline this test starts failing.
func TestTeamBuilderTool_ShieldsDoNotAffectScoring(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	pool := []tools.Combatant{
		baseCombatant("a"),
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, withZero, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool: pool, League: leagueGreat, Shields: []int{0},
	})
	if err != nil {
		t.Fatalf("[0]: %v", err)
	}

	_, withTwo, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool: pool, League: leagueGreat, Shields: []int{2},
	})
	if err != nil {
		t.Fatalf("[2]: %v", err)
	}

	if len(withZero.Teams) != 1 || len(withTwo.Teams) != 1 {
		t.Fatalf("expected one team each, got %d / %d",
			len(withZero.Teams), len(withTwo.Teams))
	}

	if withZero.Teams[0].TeamScore != withTwo.Teams[0].TeamScore {
		t.Errorf("TeamScore changed with Shields=[0] (%.2f) vs [2] (%.2f) — "+
			"Shields must not drive scoring post-Phase-D",
			withZero.Teams[0].TeamScore, withTwo.Teams[0].TeamScore)
	}
}

func TestTeamBuilderTool_NegativeMaxResults(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool: []tools.Combatant{
			baseCombatant("a"),
			baseCombatant("b"),
			baseCombatant("c"),
		},
		League:     leagueGreat,
		MaxResults: -1,
	})
	if !errors.Is(err, tools.ErrMaxResultsInvalid) {
		t.Errorf("error = %v, want wrapping ErrMaxResultsInvalid", err)
	}
}

// teamBuilderShadowFixtureGamemaster publishes both a base species "a"
// and the shadow variant "a_shadow" with a distinct charged-move list,
// so the round-trip test can prove the shadow moveset was selected.
const teamBuilderShadowFixtureGamemaster = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 1, "speciesId": "a", "speciesName": "A",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1","CH2"], "released": true},
    {"dex": 1, "speciesId": "a_shadow", "speciesName": "A (Shadow)",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1","CH2"], "released": true},
    {"dex": 2, "speciesId": "b", "speciesName": "B",
     "baseStats": {"atk": 152, "def": 143, "hp": 216},
     "types": ["water", "ground"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true},
    {"dex": 3, "speciesId": "c", "speciesName": "C",
     "baseStats": {"atk": 234, "def": 159, "hp": 207},
     "types": ["fighting", "none"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true}
  ],
  "moves": [
    {"moveId": "FAST1", "name": "Fast 1", "type": "normal",
     "power": 3, "energy": 0, "energyGain": 5, "cooldown": 1000, "turns": 2},
    {"moveId": "CH1", "name": "Charged 1", "type": "normal",
     "power": 50, "energy": 35, "energyGain": 0, "cooldown": 500},
    {"moveId": "CH2", "name": "Charged 2", "type": "psychic",
     "power": 70, "energy": 55, "energyGain": 0, "cooldown": 500}
  ]
}`

// teamBuilderShadowRankingsFixture ranks the shadow row with a
// DIFFERENT moveset (CH2) than the base row (CH1), so the resolved
// ChargedMoves is a ground-truth signal for which row was picked.
const teamBuilderShadowRankingsFixture = `[
  {"speciesId": "a", "speciesName": "A", "rating": 700, "score": 95,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2100, "atk": 107, "def": 139, "hp": 141}},
  {"speciesId": "a_shadow", "speciesName": "A (Shadow)", "rating": 720, "score": 96,
   "moveset": ["FAST1", "CH2"],
   "stats": {"product": 2150, "atk": 130, "def": 116, "hp": 141}},
  {"speciesId": "b", "speciesName": "B", "rating": 680, "score": 93,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 111, "def": 113, "hp": 161}},
  {"speciesId": "c", "speciesName": "C", "rating": 650, "score": 90,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 1900, "atk": 125, "def": 120, "hp": 130}}
]`

func newTeamBuilderToolShadowFixture(t *testing.T) *tools.TeamBuilderTool {
	t.Helper()

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(teamBuilderShadowFixtureGamemaster))
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
		_, _ = w.Write([]byte(teamBuilderShadowRankingsFixture))
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

// TestTeamBuilderTool_ShadowAutoResolvesShadowRankings pins Phase X-I
// round-1 review blocker #4: when a team member has Options.Shadow=true
// and FastMove is empty, applyMovesetDefaults must resolve against the
// "_shadow" gamemaster+rankings entry. The fixture ranks the shadow row
// with a distinct charged move (CH2 vs CH1) — the builder must surface
// CH2 on the resolved member and echo "a_shadow" as ResolvedSpeciesID.
func TestTeamBuilderTool_ShadowAutoResolvesShadowRankings(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderToolShadowFixture(t)
	handler := tool.Handler()

	shadowA := tools.Combatant{
		Species: "a",
		IV:      [3]int{15, 15, 15},
		Level:   40,
		// FastMove intentionally omitted — triggers applyMovesetDefaults
		// which must flip to the shadow row because Options.Shadow=true.
		Options: tools.CombatantOptions{Shadow: true},
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool: []tools.Combatant{
			shadowA,
			baseCombatant("b"),
			baseCombatant("c"),
		},
		League: leagueGreat,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatalf("Teams is empty; fixture pool is C(3,3) = 1 triple and must produce one team")
	}

	var shadowMember *tools.ResolvedCombatant

	for i := range result.Teams[0].Members {
		if result.Teams[0].Members[i].Species == "a" {
			shadowMember = &result.Teams[0].Members[i]

			break
		}
	}

	if shadowMember == nil {
		t.Fatalf("Members does not include species 'a' — fixture triple must contain it")
	}

	if shadowMember.ResolvedSpeciesID != "a_shadow" {
		t.Errorf("ResolvedSpeciesID = %q, want %q (shadow variant must be picked)",
			shadowMember.ResolvedSpeciesID, "a_shadow")
	}

	if shadowMember.ShadowVariantMissing {
		t.Errorf("ShadowVariantMissing = true; fixture publishes a_shadow entry — must not signal missing")
	}

	if len(shadowMember.ChargedMoves) != 1 || shadowMember.ChargedMoves[0] != "CH2" {
		t.Errorf("ChargedMoves = %v, want [CH2] (resolved from shadow rankings row)",
			shadowMember.ChargedMoves)
	}
}

// TestTeamBuilderTool_CostBreakdownPresent pins Phase 3A: every
// team in the response carries a CostBreakdowns slice aligned with
// Members. Stardust-only (candy is only present on
// SecondMoveCandy, which requires a BuddyDistance-ful species —
// the shared fixture does not set one, so availability=false for
// those fields).
func TestTeamBuilderTool_CostBreakdownPresent(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	pool := []tools.Combatant{
		baseCombatant("a"),
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:   pool,
		League: leagueGreat,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("no teams returned; cannot check cost breakdowns")
	}

	team := result.Teams[0]

	if len(team.CostBreakdowns) != len(team.Members) {
		t.Fatalf("CostBreakdowns len = %d, want %d (aligned with Members)",
			len(team.CostBreakdowns), len(team.Members))
	}

	for i, breakdown := range team.CostBreakdowns {
		if breakdown.TargetLevel <= 0 {
			t.Errorf("team[%d] TargetLevel = %v, want > 0 (per-species default)",
				i, breakdown.TargetLevel)
		}
	}
}

// TestTeamBuilderTool_AlreadyAtTargetClamp pins the zero-cost
// clamp: a pool member at the league's target level already
// should see PowerupStardustCost=0 and the
// already_at_or_above_target flag.
func TestTeamBuilderTool_AlreadyAtTargetClamp(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	// Level 1 members with a tiny explicit target at level 1
	// (so every member is already at target).
	member := tools.Combatant{
		IV: [3]int{15, 15, 15}, Level: 1.0,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}

	ma, mb, mc := member, member, member
	ma.Species = "a"
	mb.Species = "b"
	mc.Species = "c"

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:        []tools.Combatant{ma, mb, mc},
		League:      leagueGreat,
		TargetLevel: 1.0,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("no teams returned")
	}

	for i, breakdown := range result.Teams[0].CostBreakdowns {
		if breakdown.PowerupStardustCost != 0 {
			t.Errorf("team[%d] PowerupStardustCost = %d, want 0 (already at target)",
				i, breakdown.PowerupStardustCost)
		}

		if !breakdown.AlreadyAtOrAboveTarget {
			t.Errorf("team[%d] AlreadyAtOrAboveTarget = false, want true", i)
		}
	}
}

// TestTeamBuilderTool_PowerupStardustClimb pins the actual stardust
// number on a non-trivial climb: L1.0 → L2.0 is 4 half-steps of
// the L1-L2.5 bucket at 200 each = 800 stardust baseline. The
// breakdown must carry this value unscaled (no Options flags in
// this test).
func TestTeamBuilderTool_PowerupStardustClimb(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	member := tools.Combatant{
		IV: [3]int{15, 15, 15}, Level: 1.0,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}

	ma, mb, mc := member, member, member
	ma.Species = "a"
	mb.Species = "b"
	mc.Species = "c"

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:        []tools.Combatant{ma, mb, mc},
		League:      leagueGreat,
		TargetLevel: 2.0,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("no teams returned")
	}

	// L1.0→L1.5 (200) + L1.5→L2.0 (200) = 400 stardust baseline.
	const wantStardust = 400

	for i, breakdown := range result.Teams[0].CostBreakdowns {
		if breakdown.PowerupStardustCost != wantStardust {
			t.Errorf("team[%d] PowerupStardustCost = %d, want %d (L1→L2 = 2 steps × 200)",
				i, breakdown.PowerupStardustCost, wantStardust)
		}

		if breakdown.TargetLevel != 2.0 {
			t.Errorf("team[%d] TargetLevel = %v, want 2.0 (explicit target)",
				i, breakdown.TargetLevel)
		}

		if breakdown.AlreadyAtOrAboveTarget {
			t.Errorf("team[%d] AlreadyAtOrAboveTarget = true, want false", i)
		}
	}
}

// TestTeamBuilderTool_PowerupShadowPremium pins the ×1.2 shadow
// multiplier on the climb stardust: Options.Shadow=true must
// scale the baseline climb 200→240 for a single-step test.
func TestTeamBuilderTool_PowerupShadowPremium(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderToolShadowFixture(t)
	handler := tool.Handler()

	shadowA := tools.Combatant{
		Species: "a",
		IV:      [3]int{15, 15, 15},
		Level:   1.0,
		Options: tools.CombatantOptions{Shadow: true},
	}

	pool := []tools.Combatant{
		shadowA,
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:        pool,
		League:      leagueGreat,
		TargetLevel: 1.5,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("no teams returned")
	}

	var shadowBreakdown *tools.MemberCostBreakdown

	for i, m := range result.Teams[0].Members {
		if m.Species == "a" {
			shadowBreakdown = &result.Teams[0].CostBreakdowns[i]

			break
		}
	}

	if shadowBreakdown == nil {
		t.Fatal("shadow member not present in team")
	}

	if shadowBreakdown.PowerupStardustBaseline != 200 {
		t.Errorf("PowerupStardustBaseline = %d, want 200 (L1.0→L1.5 baseline)",
			shadowBreakdown.PowerupStardustBaseline)
	}

	if shadowBreakdown.PowerupStardustCost != 240 {
		t.Errorf("PowerupStardustCost = %d, want 240 (200 × 1.2 shadow)",
			shadowBreakdown.PowerupStardustCost)
	}

	if shadowBreakdown.StardustMultiplier != 1.2 {
		t.Errorf("StardustMultiplier = %v, want 1.2",
			shadowBreakdown.StardustMultiplier)
	}
}

// TestTeamBuilderTool_InvalidMemberForLeague pins the hard-error
// path: a pool member whose level-1 CP already exceeds the league
// cap surfaces ErrMemberInvalidForLeague before any simulation.
// Uses a custom fixture with an inflated base species so level-1
// CP already blows past the Great League 1500 cap.
func TestTeamBuilderTool_InvalidMemberForLeague(t *testing.T) {
	t.Parallel()

	const oversizedFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 1, "speciesId": "colossus", "speciesName": "Colossus",
     "baseStats": {"atk": 9000, "def": 9000, "hp": 9000},
     "types": ["normal"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true},
    {"dex": 2, "speciesId": "b", "speciesName": "B",
     "baseStats": {"atk": 152, "def": 143, "hp": 216},
     "types": ["water"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true},
    {"dex": 3, "speciesId": "c", "speciesName": "C",
     "baseStats": {"atk": 234, "def": 159, "hp": 207},
     "types": ["fighting"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true}
  ],
  "moves": [
    {"moveId": "FAST1", "name": "Fast 1", "type": "normal",
     "power": 3, "energy": 0, "energyGain": 5, "cooldown": 1000, "turns": 2},
    {"moveId": "CH1", "name": "Charged 1", "type": "normal",
     "power": 50, "energy": 35, "cooldown": 500}
  ]
}`

	tool := newTeamBuilderToolFromFixture(t, oversizedFixture, `[]`)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{Species: "colossus", IV: [3]int{15, 15, 15}, Level: 1.0, FastMove: "FAST1", ChargedMoves: []string{"CH1"}},
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, _, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:   pool,
		League: leagueGreat,
	})
	if !errors.Is(err, tools.ErrMemberInvalidForLeague) {
		t.Errorf("error = %v, want wrapping ErrMemberInvalidForLeague", err)
	}
}
