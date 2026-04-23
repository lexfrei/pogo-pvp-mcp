package tools_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
// clamp: a pool member at or above the target level should see
// PowerupStardustCost=0 and the already_at_or_above_target flag.
// Uses Level: 20.0 with TargetLevel: 10.0 (non-minimum target) so
// the MinLevel short-circuit cannot accidentally satisfy the
// assertion — round-1 review caught the earlier Level=1.0 +
// target=1.0 test being vacuously true via the targetLevel <= 1
// branch.
func TestTeamBuilderTool_AlreadyAtTargetClamp(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	member := tools.Combatant{
		IV: [3]int{15, 15, 15}, Level: 20.0,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}

	ma, mb, mc := member, member, member
	ma.Species = "a"
	mb.Species = "b"
	mc.Species = "c"

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:        []tools.Combatant{ma, mb, mc},
		League:      leagueGreat,
		TargetLevel: 10.0,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("no teams returned")
	}

	for i, breakdown := range result.Teams[0].CostBreakdowns {
		if breakdown.PowerupStardustCost != 0 {
			t.Errorf("team[%d] PowerupStardustCost = %d, want 0 (already above target)",
				i, breakdown.PowerupStardustCost)
		}

		if !breakdown.AlreadyAtOrAboveTarget {
			t.Errorf("team[%d] AlreadyAtOrAboveTarget = false, want true", i)
		}
	}
}

// TestTeamBuilderTool_PowerupStardustClimb pins the actual stardust
// number on a non-trivial climb: L1.0 → L2.0 is 2 half-steps
// (L1.0→L1.5 and L1.5→L2.0) inside the 200-stardust bucket for a
// total of 400 stardust baseline. The breakdown must carry this
// value unscaled (no Options flags in this test).
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

// TestTeamBuilderTool_MasterLeagueCostBreakdown pins the round-1
// fix for the master-league silent-zero bug: resolveTargetLevelFor
// Species used to return pogopvp.MaxLevel (L51) for master, which
// exceeds maxPowerupLevel (L50) and tripped populatePowerupPortion's
// pricing-skipped fallback. Now clamped to L50. A full L1→L50
// climb at baseline sums to 520,000 stardust; the team_builder
// master-league default must reproduce that number for a member
// at Level 1.
func TestTeamBuilderTool_MasterLeagueCostBreakdown(t *testing.T) {
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
		Pool:   []tools.Combatant{ma, mb, mc},
		League: "master",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("no teams returned")
	}

	const (
		wantTargetLevel = 50.0
		wantStardust    = 520000
	)

	for i, breakdown := range result.Teams[0].CostBreakdowns {
		if breakdown.TargetLevel != wantTargetLevel {
			t.Errorf("team[%d] TargetLevel = %v, want %v (master league default)",
				i, breakdown.TargetLevel, wantTargetLevel)
		}

		if breakdown.PowerupStardustCost != wantStardust {
			t.Errorf("team[%d] PowerupStardustCost = %d, want %d (L1→L50 full climb)",
				i, breakdown.PowerupStardustCost, wantStardust)
		}

		for _, flag := range breakdown.Flags {
			if flag == "powerup_pricing_skipped" {
				t.Errorf("team[%d] has powerup_pricing_skipped flag — master league regressed", i)
			}
		}
	}
}

// TestTeamBuilderTool_InvalidTargetLevel pins the round-1 fix for
// unvalidated target_level input: off-grid, out-of-range, and
// negative values must surface ErrInvalidTargetLevel rather than
// silently producing zero-cost breakdowns.
func TestTeamBuilderTool_InvalidTargetLevel(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	pool := []tools.Combatant{
		baseCombatant("a"),
		baseCombatant("b"),
		baseCombatant("c"),
	}

	cases := []struct {
		name   string
		target float64
	}{
		{"off-grid target", 10.3},
		{"above L50", 75.0},
		{"negative", -1.0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := handler(t.Context(), nil, tools.TeamBuilderParams{
				Pool:        pool,
				League:      leagueGreat,
				TargetLevel: tc.target,
			})
			if !errors.Is(err, tools.ErrInvalidTargetLevel) {
				t.Errorf("target %.2f: error = %v, want wrapping ErrInvalidTargetLevel",
					tc.target, err)
			}
		})
	}
}

// autoEvolveLinearFixture publishes a three-stage linear chain
// (squirtle → wartortle → blastoise), plus the team-analysis a/b/c
// stand-ins so the test has a valid 3-member pool. Squirtle and
// blastoise are deliberately given disjoint charged movesets
// (CH_SQUIRT vs CH_BLAST) so the linear-chain test can prove the
// moveset was actually cleared and re-resolved from the evolved
// species' rankings entry — if the clearing step regresses, the
// resolved member still carries CH_SQUIRT and the assertion fails.
// Additionally publishes squirtle_shadow so the Shadow+AutoEvolve
// interaction test can verify Options.Shadow survives promotion.
const autoEvolveLinearFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 7, "speciesId": "squirtle", "speciesName": "Squirtle",
     "baseStats": {"atk": 94, "def": 121, "hp": 127},
     "types": ["water"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH_SQUIRT"],
     "family": {"id": "FAMILY_SQUIRTLE", "evolutions": ["wartortle"]},
     "released": true},
    {"dex": 7, "speciesId": "squirtle_shadow", "speciesName": "Squirtle (Shadow)",
     "baseStats": {"atk": 94, "def": 121, "hp": 127},
     "types": ["water"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH_SQUIRT"],
     "family": {"id": "FAMILY_SQUIRTLE", "evolutions": ["wartortle"]},
     "released": true},
    {"dex": 8, "speciesId": "wartortle", "speciesName": "Wartortle",
     "baseStats": {"atk": 126, "def": 155, "hp": 153},
     "types": ["water"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH_WART"],
     "family": {"id": "FAMILY_SQUIRTLE", "parent": "squirtle", "evolutions": ["blastoise"]},
     "released": true},
    {"dex": 9, "speciesId": "blastoise", "speciesName": "Blastoise",
     "baseStats": {"atk": 171, "def": 207, "hp": 188},
     "types": ["water"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH_BLAST"],
     "family": {"id": "FAMILY_SQUIRTLE", "parent": "wartortle"},
     "released": true},
    {"dex": 9, "speciesId": "blastoise_shadow", "speciesName": "Blastoise (Shadow)",
     "baseStats": {"atk": 171, "def": 207, "hp": 188},
     "types": ["water"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH_BLAST"],
     "family": {"id": "FAMILY_SQUIRTLE", "parent": "wartortle"},
     "released": true},
    {"dex": 2, "speciesId": "b", "speciesName": "B",
     "baseStats": {"atk": 152, "def": 143, "hp": 216}, "types": ["water"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true},
    {"dex": 3, "speciesId": "c", "speciesName": "C",
     "baseStats": {"atk": 234, "def": 159, "hp": 207}, "types": ["fighting"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true}
  ],
  "moves": [
    {"moveId": "FAST1", "name": "Fast 1", "type": "normal",
     "power": 3, "energy": 0, "energyGain": 5, "cooldown": 1000, "turns": 2},
    {"moveId": "CH1", "name": "Charged 1", "type": "normal",
     "power": 50, "energy": 35, "cooldown": 500},
    {"moveId": "CH_SQUIRT", "name": "Squirt Blast", "type": "water",
     "power": 50, "energy": 35, "cooldown": 500},
    {"moveId": "CH_WART", "name": "Wart Pound", "type": "water",
     "power": 50, "energy": 35, "cooldown": 500},
    {"moveId": "CH_BLAST", "name": "Blast Cannon", "type": "water",
     "power": 90, "energy": 55, "cooldown": 500}
  ]
}`

// TestTeamBuilderTool_AutoEvolveLinearChain pins the happy path:
// AutoEvolve=true walks a linear chain (squirtle → wartortle →
// blastoise) to the terminal form. The returned team's squirtle
// member becomes blastoise in the resolved Species field, and the
// cost_breakdown flag surfaces the original species via
// auto_evolved_from:squirtle.
func TestTeamBuilderTool_AutoEvolveLinearChain(t *testing.T) {
	t.Parallel()

	// Blastoise rankings entry declares CH_BLAST. Squirtle's
	// explicit charged move (CH_SQUIRT) must be cleared by the
	// promotion; the evolved member's ChargedMoves must come from
	// this rankings row — if the clearing step regresses the
	// resolved member still carries CH_SQUIRT and the assertion
	// fails.
	const rankingsPayload = `[
  {"speciesId": "blastoise", "speciesName": "Blastoise", "rating": 700,
   "moveset": ["FAST1", "CH_BLAST"],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}},
  {"speciesId": "b", "speciesName": "B", "rating": 600,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 100, "def": 120, "hp": 150}},
  {"speciesId": "c", "speciesName": "C", "rating": 650,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2050, "atk": 105, "def": 125, "hp": 145}}
]`

	tool := newTeamBuilderToolFromFixture(t, autoEvolveLinearFixture, rankingsPayload)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{Species: "squirtle", IV: [3]int{15, 15, 15}, Level: 1, FastMove: "FAST1", ChargedMoves: []string{"CH_SQUIRT"}},
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:       pool,
		League:     leagueGreat,
		AutoEvolve: true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("no teams returned")
	}

	var evolvedMember *tools.ResolvedCombatant

	var evolvedBreakdown *tools.MemberCostBreakdown

	for i := range result.Teams[0].Members {
		if result.Teams[0].Members[i].Species == speciesBlastoise {
			evolvedMember = &result.Teams[0].Members[i]
			evolvedBreakdown = &result.Teams[0].CostBreakdowns[i]

			break
		}
	}

	if evolvedMember == nil {
		t.Fatalf("no blastoise in team; members=%+v", result.Teams[0].Members)
	}

	if evolvedBreakdown == nil {
		t.Fatal("cost breakdown missing for evolved member")
	}

	if !slices.Contains(evolvedBreakdown.Flags, "auto_evolved_from:squirtle") {
		t.Errorf("auto_evolved_from:squirtle flag missing in Flags=%v", evolvedBreakdown.Flags)
	}

	// Moveset-cleared signal: the resolved member's ChargedMoves
	// must come from the BLASTOISE rankings row, not the original
	// squirtle-only CH_SQUIRT. If a future refactor drops the
	// clearing step, this assertion fails.
	if !slices.Contains(evolvedMember.ChargedMoves, "CH_BLAST") {
		t.Errorf("ChargedMoves = %v, want [CH_BLAST] (moveset must be cleared and re-resolved from evolved species rankings)",
			evolvedMember.ChargedMoves)
	}

	if slices.Contains(evolvedMember.ChargedMoves, "CH_SQUIRT") {
		t.Errorf("ChargedMoves = %v; the original squirtle CH_SQUIRT must not survive promotion",
			evolvedMember.ChargedMoves)
	}
}

// TestTeamBuilderTool_AutoEvolveRequiredMatchesPostEvolve pins the
// documented Required semantics: Required matches the POST-evolve
// species id. Listing the pre-evolution id alongside auto_evolve
// therefore surfaces ErrRequiredNotInPool once squirtle becomes
// blastoise. This is the counter-intuitive edge the README /
// CLAUDE.md call out explicitly.
func TestTeamBuilderTool_AutoEvolveRequiredMatchesPostEvolve(t *testing.T) {
	t.Parallel()

	const rankingsPayload = `[
  {"speciesId": "blastoise", "speciesName": "Blastoise", "rating": 700,
   "moveset": ["FAST1", "CH_BLAST"],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}},
  {"speciesId": "b", "speciesName": "B", "rating": 600,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 100, "def": 120, "hp": 150}},
  {"speciesId": "c", "speciesName": "C", "rating": 650,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2050, "atk": 105, "def": 125, "hp": 145}}
]`

	tool := newTeamBuilderToolFromFixture(t, autoEvolveLinearFixture, rankingsPayload)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{Species: "squirtle", IV: [3]int{15, 15, 15}, Level: 1, FastMove: "FAST1", ChargedMoves: []string{"CH_SQUIRT"}},
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, _, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:       pool,
		League:     leagueGreat,
		AutoEvolve: true,
		Required:   []string{"squirtle"},
	})
	if !errors.Is(err, tools.ErrRequiredNotInPool) {
		t.Errorf("error = %v, want wrapping ErrRequiredNotInPool (squirtle is no longer in the pool after auto-evolve)",
			err)
	}
}

// TestTeamBuilderTool_AutoEvolveBanMatchesPreEvolveID pins the
// documented Banned semantics: a ban on the pre-evolution species
// id still filters the pool entry after auto-evolve promotes it.
// Without the autoEvolvedFrom check in filterPool, banning the
// base form would silently bypass and the evolved species would
// make the team.
func TestTeamBuilderTool_AutoEvolveBanMatchesPreEvolveID(t *testing.T) {
	t.Parallel()

	const rankingsPayload = `[
  {"speciesId": "blastoise", "speciesName": "Blastoise", "rating": 700,
   "moveset": ["FAST1", "CH_BLAST"],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}},
  {"speciesId": "b", "speciesName": "B", "rating": 600,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 100, "def": 120, "hp": 150}},
  {"speciesId": "c", "speciesName": "C", "rating": 650,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2050, "atk": 105, "def": 125, "hp": 145}}
]`

	tool := newTeamBuilderToolFromFixture(t, autoEvolveLinearFixture, rankingsPayload)
	handler := tool.Handler()

	// Four-member pool with squirtle banned by pre-evolve id.
	// Without the autoEvolvedFrom ban check, blastoise would
	// survive the filter (Species no longer == "squirtle") and
	// land in teams. Since C(3, 3) = 1 triple, the ban working
	// means squirtle/blastoise is dropped → pool of 3 = one
	// non-banned triple, all three others compose the team.
	pool := []tools.Combatant{
		{Species: "squirtle", IV: [3]int{15, 15, 15}, Level: 1, FastMove: "FAST1", ChargedMoves: []string{"CH_SQUIRT"}},
		baseCombatant("b"),
		baseCombatant("c"),
		{Species: "b", IV: [3]int{14, 14, 14}, Level: 39, FastMove: "FAST1", ChargedMoves: []string{"CH1"}},
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:       pool,
		League:     leagueGreat,
		AutoEvolve: true,
		Banned:     []string{"squirtle"},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	for _, team := range result.Teams {
		for _, member := range team.Members {
			if member.Species == speciesBlastoise {
				t.Errorf("blastoise leaked into team despite banned=[squirtle] (pre-evolve id must match post-promotion pool entries)")
			}
		}
	}
}

// TestTeamBuilderTool_AutoEvolveShadowCarriesOver pins that
// Options.Shadow survives auto-evolve: a shadow squirtle pool
// entry becomes shadow blastoise after promotion. resolvedFromSpec
// exposes ResolvedSpeciesID=blastoise_shadow via the shadow-aware
// lookup in buildEngineCombatant. The fixture publishes both
// squirtle_shadow and blastoise_shadow so pvpoke has a dedicated
// entry for each form; if blastoise_shadow were missing, we'd
// expect shadow_variant_missing=true in the resolved combatant.
func TestTeamBuilderTool_AutoEvolveShadowCarriesOver(t *testing.T) {
	t.Parallel()

	const rankingsPayload = `[
  {"speciesId": "blastoise", "speciesName": "Blastoise", "rating": 700,
   "moveset": ["FAST1", "CH_BLAST"],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}},
  {"speciesId": "blastoise_shadow", "speciesName": "Blastoise (Shadow)", "rating": 720,
   "moveset": ["FAST1", "CH_BLAST"],
   "stats": {"product": 2150, "atk": 130, "def": 116, "hp": 141}},
  {"speciesId": "b", "speciesName": "B", "rating": 600,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 100, "def": 120, "hp": 150}},
  {"speciesId": "c", "speciesName": "C", "rating": 650,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2050, "atk": 105, "def": 125, "hp": 145}}
]`

	tool := newTeamBuilderToolFromFixture(t, autoEvolveLinearFixture, rankingsPayload)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{
			Species: "squirtle", IV: [3]int{15, 15, 15}, Level: 1,
			Options: tools.CombatantOptions{Shadow: true},
		},
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:       pool,
		League:     leagueGreat,
		AutoEvolve: true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("no teams returned")
	}

	var evolved *tools.ResolvedCombatant

	for i := range result.Teams[0].Members {
		if result.Teams[0].Members[i].Species == speciesBlastoise {
			evolved = &result.Teams[0].Members[i]

			break
		}
	}

	if evolved == nil {
		t.Fatalf("blastoise not in team; members=%+v", result.Teams[0].Members)
	}

	if !evolved.Options.Shadow {
		t.Errorf("Options.Shadow = false after promotion, want true (shadow flag must carry over)")
	}

	if evolved.ResolvedSpeciesID != "blastoise_shadow" {
		t.Errorf("ResolvedSpeciesID = %q, want \"blastoise_shadow\" (shadow lookup must resolve the evolved shadow entry)",
			evolved.ResolvedSpeciesID)
	}

	if evolved.ShadowVariantMissing {
		t.Errorf("ShadowVariantMissing = true; fixture publishes blastoise_shadow — must not signal missing")
	}
}

// TestTeamBuilderTool_BudgetDropsOverBudgetTeam pins the hard-drop
// path: a StardustLimit below the team's aggregate cost with no
// tolerance filters the team out entirely. Uses a high target
// level so the L1-member climb produces meaningful stardust.
func TestTeamBuilderTool_BudgetDropsOverBudgetTeam(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	member := tools.Combatant{
		IV: [3]int{15, 15, 15}, Level: 1.0,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}
	memberA := member
	memberA.Species = "a"
	memberB := member
	memberB.Species = "b"
	memberC := member
	memberC.Species = "c"

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:        []tools.Combatant{memberA, memberB, memberC},
		League:      leagueGreat,
		TargetLevel: 40.0,
		Budget:      &tools.BudgetSpec{StardustLimit: 1000},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) != 0 {
		t.Errorf("Teams len = %d, want 0 (budget 1000 stardust << 3 × L1→L40 climbs)",
			len(result.Teams))
	}
}

// TestTeamBuilderTool_BudgetInToleranceKeptAndFlagged pins the
// tolerance branch: a team whose AggregateCost sits between
// StardustLimit and StardustLimit × (1+Tolerance) is kept in the
// response with BudgetExceeded=true + BudgetExcess > 0.
// Synthesises the exact budget from the actual aggregate cost so
// the fixture stays deterministic across powerup table edits.
func TestTeamBuilderTool_BudgetInToleranceKeptAndFlagged(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	member := tools.Combatant{
		IV: [3]int{15, 15, 15}, Level: 1.0,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}
	memberA := member
	memberA.Species = "a"
	memberB := member
	memberB.Species = "b"
	memberC := member
	memberC.Species = "c"

	// Warm-up run to discover the actual aggregate cost for this
	// team at target level 40.
	_, warm, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:        []tools.Combatant{memberA, memberB, memberC},
		League:      leagueGreat,
		TargetLevel: 40.0,
	})
	if err != nil {
		t.Fatalf("warm: %v", err)
	}

	if len(warm.Teams) == 0 {
		t.Fatal("warm: no teams to measure aggregate")
	}

	var actualCost int

	for _, breakdown := range warm.Teams[0].CostBreakdowns {
		actualCost += breakdown.PowerupStardustCost + breakdown.SecondMoveStardustCost
	}

	// Set the budget below actualCost but within 50% tolerance.
	limit := actualCost * 9 / 10 // 10% below actual
	tolerance := 0.5             // 50% tolerance → hardCap = limit × 1.5 >> actualCost

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:        []tools.Combatant{memberA, memberB, memberC},
		League:      leagueGreat,
		TargetLevel: 40.0,
		Budget: &tools.BudgetSpec{
			StardustLimit:     limit,
			StardustTolerance: tolerance,
		},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("Teams empty; team within tolerance must be kept")
	}

	team := result.Teams[0]

	if !team.BudgetExceeded {
		t.Errorf("BudgetExceeded = false; team over limit but within tolerance must flag")
	}

	if team.BudgetExcess <= 0 {
		t.Errorf("BudgetExcess = %d, want > 0", team.BudgetExcess)
	}

	if team.AggregateCost != actualCost {
		t.Errorf("AggregateCost = %d, want %d (matches warm-run sum)",
			team.AggregateCost, actualCost)
	}
}

// TestTeamBuilderTool_BudgetKeepsFittingTeam pins the negative
// path of the budget filter: a limit comfortably above the team's
// actual cost keeps the team in the response with
// BudgetExceeded=false and BudgetExcess=0. AggregateCost is still
// populated so callers can see the total even when they're under
// budget.
func TestTeamBuilderTool_BudgetKeepsFittingTeam(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	member := tools.Combatant{
		IV: [3]int{15, 15, 15}, Level: 1.0,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}
	memberA := member
	memberA.Species = "a"
	memberB := member
	memberB.Species = "b"
	memberC := member
	memberC.Species = "c"

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:        []tools.Combatant{memberA, memberB, memberC},
		League:      leagueGreat,
		TargetLevel: 40.0,
		Budget:      &tools.BudgetSpec{StardustLimit: 100_000_000},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("Teams empty; team well under 100M budget must be kept")
	}

	team := result.Teams[0]

	if team.BudgetExceeded {
		t.Errorf("BudgetExceeded = true; 100M budget comfortably covers the climb")
	}

	if team.BudgetExcess != 0 {
		t.Errorf("BudgetExcess = %d, want 0", team.BudgetExcess)
	}

	if team.AggregateCost == 0 {
		t.Errorf("AggregateCost = 0; must be populated even when under budget")
	}
}

// budgetFilterOrderFixture stacks c with the highest base-ATK
// AND a massive thirdMoveCost (500k stardust) so c-containing
// triples are BOTH top-scored in simulation AND over any sane
// budget. The rankings list c first (rating 750, above a/b) to
// match the pool-side scoring bias; this gives the order-sensitive
// assertion in TestTeamBuilderTool_BudgetFilterRunsBeforeTrim a
// deterministic top-team expectation.
const budgetFilterOrderFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 1, "speciesId": "a", "speciesName": "A",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true,
     "thirdMoveCost": 0, "buddyDistance": 1},
    {"dex": 2, "speciesId": "b", "speciesName": "B",
     "baseStats": {"atk": 152, "def": 143, "hp": 216},
     "types": ["water", "ground"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true,
     "thirdMoveCost": 0, "buddyDistance": 1},
    {"dex": 3, "speciesId": "c", "speciesName": "C",
     "baseStats": {"atk": 300, "def": 200, "hp": 250},
     "types": ["fighting", "none"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true,
     "thirdMoveCost": 500000, "buddyDistance": 1}
  ],
  "moves": [
    {"moveId": "FAST1", "name": "Fast 1", "type": "normal",
     "power": 3, "energy": 0, "energyGain": 5, "cooldown": 1000, "turns": 2},
    {"moveId": "CH1", "name": "Charged 1", "type": "normal",
     "power": 50, "energy": 35, "energyGain": 0, "cooldown": 500}
  ]
}`

const budgetFilterOrderRankings = `[
  {"speciesId": "c", "speciesName": "C", "rating": 750, "score": 100,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2400, "atk": 160, "def": 160, "hp": 180}},
  {"speciesId": "a", "speciesName": "A", "rating": 700, "score": 95,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2100, "atk": 107, "def": 139, "hp": 141}},
  {"speciesId": "b", "speciesName": "B", "rating": 680, "score": 93,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 111, "def": 113, "hp": 161}}
]`

// TestTeamBuilderTool_BudgetFilterRunsBeforeTrim pins the CLAUDE.md
// invariant that applyBudgetFilter executes BEFORE the MaxResults
// trim. Setup: c is simultaneously the strongest scorer (base ATK
// 300 + rating 750) AND over budget (thirdMoveCost=500,000). A
// four-member pool {a, b15, b14, c} yields four triples; three
// contain c. With MaxResults=1:
//
//   - Filter-first (correct): drop the three c-triples on budget,
//     then trim picks the single survivor {a, b15, b14} → len=1.
//   - Trim-first (regression): sort by score → top-1 is a c-triple
//     → trim keeps it → filter drops it → len=0.
//
// The len assertion alone discriminates because the c-triples are
// the top-scored — without the budget, MaxResults=1 would return
// a c-triple. The explicit "no c in member list" check is a
// belt-and-braces guard against a future regression that leaves
// a c-triple in by some other mechanism.
func TestTeamBuilderTool_BudgetFilterRunsBeforeTrim(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderToolFromFixture(t, budgetFilterOrderFixture, budgetFilterOrderRankings)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{Species: "a", IV: [3]int{15, 15, 15}, Level: 40, FastMove: "FAST1", ChargedMoves: []string{"CH1"}},
		{Species: "b", IV: [3]int{15, 15, 15}, Level: 40, FastMove: "FAST1", ChargedMoves: []string{"CH1"}},
		{Species: "b", IV: [3]int{14, 15, 15}, Level: 40, FastMove: "FAST1", ChargedMoves: []string{"CH1"}},
		{Species: "c", IV: [3]int{15, 15, 15}, Level: 40, FastMove: "FAST1", ChargedMoves: []string{"CH1"}},
	}

	// Warm-up with no budget confirms a c-triple tops the
	// unconstrained ranking. If this invariant ever breaks, the
	// order-sensitive assertion below loses meaning — catch it
	// here before the ambiguous case.
	_, warm, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool: pool, League: leagueGreat, TargetLevel: 40.0, MaxResults: 1,
	})
	if err != nil {
		t.Fatalf("warm-up handler: %v", err)
	}

	if len(warm.Teams) == 0 {
		t.Fatal("warm-up returned no teams; fixture broken")
	}

	topHasC := false
	for _, member := range warm.Teams[0].Members {
		if member.Species == "c" {
			topHasC = true

			break
		}
	}

	if !topHasC {
		t.Fatalf("warm-up top team %v does not contain c; order-sensitive test loses meaning",
			warm.Teams[0].Members)
	}

	// Real run: budget 1000 stardust << 500,000 thirdMoveCost for
	// c; tolerance zero. c-triples drop; the non-c triple (cost 0)
	// survives. If trim ran first, only the top-scored c-triple
	// would survive to the filter and then get dropped → len=0.
	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:        pool,
		League:      leagueGreat,
		TargetLevel: 40.0,
		MaxResults:  1,
		Budget:      &tools.BudgetSpec{StardustLimit: 1000},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) != 1 {
		t.Fatalf("Teams len = %d, want 1 (filter must drop c-teams BEFORE trim keeps survivors); "+
			"len=0 would indicate trim ran first and filter dropped the top-scored c-team",
			len(result.Teams))
	}

	for _, member := range result.Teams[0].Members {
		if member.Species == "c" {
			t.Errorf("c leaked into the returned team despite budget < c thirdMoveCost")
		}
	}
}

// TestTeamBuilderTool_BudgetAggregateCostAlwaysPresent pins the
// JSON contract: `aggregate_stardust_cost` appears on every team
// regardless of value. An `omitempty` tag would drop the field on
// zero-cost teams (all members at target, no second-move cost),
// breaking the README promise that under-budget teams still carry
// the total so callers can compare to their inventory.
func TestTeamBuilderTool_BudgetAggregateCostAlwaysPresent(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	pool := []tools.Combatant{
		baseCombatant("a"),
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:        pool,
		League:      leagueGreat,
		TargetLevel: 40.0,
		Budget:      &tools.BudgetSpec{StardustLimit: 1_000_000},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("no teams returned")
	}

	encoded, err := json.Marshal(result.Teams[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if !strings.Contains(string(encoded), `"aggregate_stardust_cost"`) {
		t.Errorf("aggregate_stardust_cost missing from JSON when value is zero (omitempty regressed): %s",
			encoded)
	}
}

// TestTeamBuilderTool_BudgetStubFieldsIgnored pins the JSON-
// contract stability claim: EliteChargedTM / EliteFastTM /
// XLCandy / RareCandyXL are accepted on the input but have no
// enforcement yet. A request with all four populated returns
// identically to the same request with them zero — if a later
// branch adds partial enforcement, this test breaks first and
// flags the silent contract change.
func TestTeamBuilderTool_BudgetStubFieldsIgnored(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	member := tools.Combatant{
		IV: [3]int{15, 15, 15}, Level: 40,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}
	memberA := member
	memberA.Species = "a"
	memberB := member
	memberB.Species = "b"
	memberC := member
	memberC.Species = "c"

	baseParams := tools.TeamBuilderParams{
		Pool:        []tools.Combatant{memberA, memberB, memberC},
		League:      leagueGreat,
		TargetLevel: 40.0,
	}

	// First run: plain, no budget at all.
	_, plain, err := handler(t.Context(), nil, baseParams)
	if err != nil {
		t.Fatalf("plain: %v", err)
	}

	// Second run: Budget with only the stub fields populated —
	// StardustLimit zero so the filter short-circuits to "no
	// budget enforced". Result should match the plain run.
	stubParams := baseParams
	stubParams.Budget = &tools.BudgetSpec{
		EliteChargedTM: 5,
		EliteFastTM:    3,
		XLCandy:        296,
		RareCandyXL:    true,
	}

	_, stub, err := handler(t.Context(), nil, stubParams)
	if err != nil {
		t.Fatalf("stub: %v", err)
	}

	if len(plain.Teams) != len(stub.Teams) {
		t.Errorf("Teams len differs: plain=%d stub=%d — stub fields must be ignored",
			len(plain.Teams), len(stub.Teams))
	}

	for i := range plain.Teams {
		if plain.Teams[i].TeamScore != stub.Teams[i].TeamScore {
			t.Errorf("TeamScore differs at i=%d: plain=%v stub=%v",
				i, plain.Teams[i].TeamScore, stub.Teams[i].TeamScore)
		}
	}
}

// TestTeamBuilderTool_BudgetGuardsBypass pins the no-op branches
// of applyBudgetFilter: nil Budget and StardustLimit <= 0 both
// skip the filter entirely. Without these guards a regression
// could make an empty budget spec silently reject every team.
func TestTeamBuilderTool_BudgetGuardsBypass(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	member := tools.Combatant{
		IV: [3]int{15, 15, 15}, Level: 1.0,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}
	memberA := member
	memberA.Species = "a"
	memberB := member
	memberB.Species = "b"
	memberC := member
	memberC.Species = "c"

	baseParams := tools.TeamBuilderParams{
		Pool:        []tools.Combatant{memberA, memberB, memberC},
		League:      leagueGreat,
		TargetLevel: 40.0,
	}

	cases := []struct {
		name   string
		budget *tools.BudgetSpec
	}{
		{"nil budget", nil},
		{"zero StardustLimit", &tools.BudgetSpec{StardustLimit: 0}},
		{"negative StardustLimit", &tools.BudgetSpec{StardustLimit: -1}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			params := baseParams
			params.Pool = append([]tools.Combatant(nil), baseParams.Pool...)
			params.Budget = tc.budget

			_, result, err := handler(t.Context(), nil, params)
			if err != nil {
				t.Fatalf("handler: %v", err)
			}

			if len(result.Teams) == 0 {
				t.Errorf("Teams empty; guard must skip filter entirely, not reject everything")
			}
		})
	}
}

// TestTeamBuilderTool_BudgetNegativeToleranceClamped pins the
// clamp at applyBudgetFilter: a negative StardustTolerance is
// treated as zero (hard filter). Without the clamp, a negative
// value would produce hardCap < limit and reject more teams than
// intended.
func TestTeamBuilderTool_BudgetNegativeToleranceClamped(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	member := tools.Combatant{
		IV: [3]int{15, 15, 15}, Level: 1.0,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}
	memberA := member
	memberA.Species = "a"
	memberB := member
	memberB.Species = "b"
	memberC := member
	memberC.Species = "c"

	// Warm-up: discover actual aggregate cost so we can set the
	// limit exactly AT the cost. With tolerance=0 the team should
	// just barely fit. With negative-tolerance (clamped to 0), it
	// should behave the same — not stricter.
	_, warm, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:        []tools.Combatant{memberA, memberB, memberC},
		League:      leagueGreat,
		TargetLevel: 40.0,
	})
	if err != nil {
		t.Fatalf("warm: %v", err)
	}

	if len(warm.Teams) == 0 {
		t.Fatal("warm: no teams")
	}

	var actualCost int
	for _, breakdown := range warm.Teams[0].CostBreakdowns {
		actualCost += breakdown.PowerupStardustCost + breakdown.SecondMoveStardustCost
	}

	// Limit exactly equal to cost; negative tolerance must clamp
	// to zero so the team fits (cost <= limit, not dropped).
	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:        []tools.Combatant{memberA, memberB, memberC},
		League:      leagueGreat,
		TargetLevel: 40.0,
		Budget: &tools.BudgetSpec{
			StardustLimit:     actualCost,
			StardustTolerance: -0.5,
		},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Errorf("Teams empty; negative tolerance must clamp to 0, not to a stricter filter")
	}
}

// autoEvolveItemGatedLinearFixture publishes a single-branch chain
// whose terminal is in the curated evolution-item table
// (scyther → scizor, Metal Coat in real GO mechanics). Lets the
// R7.P2 test prove walkEvolutionChain accumulates the Metal Coat
// requirement on a linear promotion, distinct from the
// branching-path Requirement which was already covered in R6.7.
const autoEvolveItemGatedLinearFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-23 00:00:00",
  "pokemon": [
    {"dex": 123, "speciesId": "scyther", "speciesName": "Scyther",
     "baseStats": {"atk": 218, "def": 170, "hp": 172},
     "types": ["bug", "flying"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_SCYTHER", "evolutions": ["scizor"]},
     "released": true},
    {"dex": 212, "speciesId": "scizor", "speciesName": "Scizor",
     "baseStats": {"atk": 236, "def": 191, "hp": 172},
     "types": ["bug", "steel"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_SCYTHER", "parent": "scyther"},
     "released": true},
    {"dex": 2, "speciesId": "b", "speciesName": "B",
     "baseStats": {"atk": 152, "def": 143, "hp": 216}, "types": ["water"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true},
    {"dex": 3, "speciesId": "c", "speciesName": "C",
     "baseStats": {"atk": 234, "def": 159, "hp": 207}, "types": ["fighting"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true}
  ],
  "moves": [
    {"moveId": "FAST1", "name": "Fast 1", "type": "normal",
     "power": 3, "energy": 0, "energyGain": 5, "cooldown": 1000, "turns": 2},
    {"moveId": "CH1", "name": "Charged 1", "type": "normal",
     "power": 50, "energy": 35, "cooldown": 500}
  ]
}`

// TestTeamBuilderTool_AutoEvolveLinearChainRequirement pins R7.P2:
// walking a single-branch chain whose terminal is in the curated
// evolution-item table (scyther → scizor via Metal Coat) must
// populate MemberCostBreakdown.AutoEvolveRequirements with the
// item + candy cost. Previously only branching chains surfaced
// the requirement (R6.7 scope trim); r7 closes the linear-path
// gap.
func TestTeamBuilderTool_AutoEvolveLinearChainRequirement(t *testing.T) {
	t.Parallel()

	const rankingsPayload = `[
  {"speciesId": "scizor", "speciesName": "Scizor", "rating": 700,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}},
  {"speciesId": "b", "speciesName": "B", "rating": 600,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 100, "def": 120, "hp": 150}},
  {"speciesId": "c", "speciesName": "C", "rating": 650,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2050, "atk": 105, "def": 125, "hp": 145}}
]`

	tool := newTeamBuilderToolFromFixture(t, autoEvolveItemGatedLinearFixture, rankingsPayload)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{Species: "scyther", IV: [3]int{15, 15, 15}, Level: 20, FastMove: "FAST1", ChargedMoves: []string{"CH1"}},
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:       pool,
		League:     leagueGreat,
		AutoEvolve: true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("no teams returned")
	}

	var breakdown *tools.MemberCostBreakdown
	for i := range result.Teams[0].Members {
		if result.Teams[0].Members[i].Species == speciesScizor {
			breakdown = &result.Teams[0].CostBreakdowns[i]

			break
		}
	}

	if breakdown == nil {
		t.Fatalf("scizor not in returned team; members=%+v", result.Teams[0].Members)
	}

	if len(breakdown.AutoEvolveRequirements) != 1 {
		t.Fatalf("AutoEvolveRequirements len = %d, want 1 (single Metal Coat hop); got %+v",
			len(breakdown.AutoEvolveRequirements), breakdown.AutoEvolveRequirements)
	}

	req := breakdown.AutoEvolveRequirements[0]
	if req.Item != itemMetalCoat {
		t.Errorf("Requirement.Item = %q, want metal_coat", req.Item)
	}

	const wantScizorCandy = 50
	if req.Candy != wantScizorCandy {
		t.Errorf("Requirement.Candy = %d, want %d", req.Candy, wantScizorCandy)
	}
}

// itemUpGrade / itemMetalCoat are snake_case ids from the R7.P2
// evolution-item table; hoisted to consts so the multi-hop +
// over-cap + branching-preserve tests share one literal each.
const (
	itemUpGrade   = "up_grade"
	itemMetalCoat = "metal_coat"
)

// speciesScizorShadow is the legacy-suffix id used by the two
// shadow-promotion tests in this file (non-suffix convention
// via Options.Shadow=true + suffix convention directly). Shared
// as a const so the assertions don't trip goconst.
const speciesScizorShadow = "scizor_shadow"

// autoEvolveTwoHopItemFixture publishes the porygon → porygon2 →
// porygon_z chain. Both terminals are in the curated table
// (up_grade, sinnoh_stone) so the R7.P2 multi-hop accumulator gets
// tested end-to-end. Base-stat values are the real Pokémon GO
// numbers so the level-1 fit check passes in Ultra League
// (porygon_z fits at level 1 under 2500 CP).
const autoEvolveTwoHopItemFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-23 00:00:00",
  "pokemon": [
    {"dex": 137, "speciesId": "porygon", "speciesName": "Porygon",
     "baseStats": {"atk": 153, "def": 136, "hp": 163},
     "types": ["normal"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_PORYGON", "evolutions": ["porygon2"]},
     "released": true},
    {"dex": 233, "speciesId": "porygon2", "speciesName": "Porygon2",
     "baseStats": {"atk": 198, "def": 183, "hp": 197},
     "types": ["normal"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_PORYGON", "parent": "porygon", "evolutions": ["porygon_z"]},
     "released": true},
    {"dex": 474, "speciesId": "porygon_z", "speciesName": "Porygon-Z",
     "baseStats": {"atk": 264, "def": 150, "hp": 198},
     "types": ["normal"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_PORYGON", "parent": "porygon2"},
     "released": true},
    {"dex": 2, "speciesId": "b", "speciesName": "B",
     "baseStats": {"atk": 152, "def": 143, "hp": 216}, "types": ["water"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true},
    {"dex": 3, "speciesId": "c", "speciesName": "C",
     "baseStats": {"atk": 234, "def": 159, "hp": 207}, "types": ["fighting"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true}
  ],
  "moves": [
    {"moveId": "FAST1", "name": "Fast 1", "type": "normal",
     "power": 3, "energy": 0, "energyGain": 5, "cooldown": 1000, "turns": 2},
    {"moveId": "CH1", "name": "Charged 1", "type": "normal",
     "power": 50, "energy": 35, "cooldown": 500}
  ]
}`

// TestTeamBuilderTool_AutoEvolveLinearChainRequirementMultiHop
// pins the R7.P2 multi-hop accumulator: porygon walked to
// porygon_z via porygon2 must surface two ordered requirement
// entries — Up-Grade (50 candy) then Sinnoh Stone (100 candy).
// A bug that reset the accumulator on advance would pass the
// single-hop scyther test but silently drop all but the last
// entry here.
func TestTeamBuilderTool_AutoEvolveLinearChainRequirementMultiHop(t *testing.T) {
	t.Parallel()

	const rankingsPayload = `[
  {"speciesId": "porygon_z", "speciesName": "Porygon-Z", "rating": 700,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2300, "atk": 140, "def": 120, "hp": 160}},
  {"speciesId": "b", "speciesName": "B", "rating": 600,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 100, "def": 120, "hp": 150}},
  {"speciesId": "c", "speciesName": "C", "rating": 650,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2050, "atk": 105, "def": 125, "hp": 145}}
]`

	tool := newTeamBuilderToolFromFixture(t, autoEvolveTwoHopItemFixture, rankingsPayload)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{Species: "porygon", IV: [3]int{15, 15, 15}, Level: 20, FastMove: "FAST1", ChargedMoves: []string{"CH1"}},
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:       pool,
		League:     "ultra",
		AutoEvolve: true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	var breakdown *tools.MemberCostBreakdown
	for i := range result.Teams[0].Members {
		if result.Teams[0].Members[i].Species == "porygon_z" {
			breakdown = &result.Teams[0].CostBreakdowns[i]

			break
		}
	}

	if breakdown == nil {
		t.Fatalf("porygon_z not in returned team; members=%+v", result.Teams[0].Members)
	}

	if len(breakdown.AutoEvolveRequirements) != 2 {
		t.Fatalf("AutoEvolveRequirements len = %d, want 2 (up_grade then sinnoh_stone); got %+v",
			len(breakdown.AutoEvolveRequirements), breakdown.AutoEvolveRequirements)
	}

	// Order must match the walker's hop order — up_grade first
	// (porygon → porygon2), sinnoh_stone second (porygon2 →
	// porygon_z). Swapping signals a regression in how the
	// slice is accumulated.
	const wantPorygon2Candy = 50
	if got := breakdown.AutoEvolveRequirements[0]; got.Item != itemUpGrade || got.Candy != wantPorygon2Candy {
		t.Errorf("AutoEvolveRequirements[0] = %+v, want {up_grade, %d}", got, wantPorygon2Candy)
	}

	const wantPorygonZCandy = 100
	if got := breakdown.AutoEvolveRequirements[1]; got.Item != "sinnoh_stone" || got.Candy != wantPorygonZCandy {
		t.Errorf("AutoEvolveRequirements[1] = %+v, want {sinnoh_stone, %d}", got, wantPorygonZCandy)
	}
}

// autoEvolveOverCapFixture mirrors autoEvolveTwoHopItemFixture
// but inflates porygon_z's base stats so its level-1 floor CP
// exceeds the Great-League cap (1500). Keeps porygon + porygon2
// fitting normally so the walker promotes to porygon2 and stops
// at over-cap on hop 2. Synthetic stats — Pokémon GO's real
// porygon_z floor fits under any league, so the over-cap path
// needs an inflated fixture to exercise.
const autoEvolveOverCapFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-23 00:00:00",
  "pokemon": [
    {"dex": 137, "speciesId": "porygon", "speciesName": "Porygon",
     "baseStats": {"atk": 153, "def": 136, "hp": 163},
     "types": ["normal"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_PORYGON", "evolutions": ["porygon2"]},
     "released": true},
    {"dex": 233, "speciesId": "porygon2", "speciesName": "Porygon2",
     "baseStats": {"atk": 198, "def": 183, "hp": 197},
     "types": ["normal"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_PORYGON", "parent": "porygon", "evolutions": ["porygon_z"]},
     "released": true},
    {"dex": 474, "speciesId": "porygon_z", "speciesName": "Porygon-Z",
     "baseStats": {"atk": 5000, "def": 5000, "hp": 5000},
     "types": ["normal"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_PORYGON", "parent": "porygon2"},
     "released": true},
    {"dex": 2, "speciesId": "b", "speciesName": "B",
     "baseStats": {"atk": 152, "def": 143, "hp": 216}, "types": ["water"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true},
    {"dex": 3, "speciesId": "c", "speciesName": "C",
     "baseStats": {"atk": 234, "def": 159, "hp": 207}, "types": ["fighting"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true}
  ],
  "moves": [
    {"moveId": "FAST1", "name": "Fast 1", "type": "normal",
     "power": 3, "energy": 0, "energyGain": 5, "cooldown": 1000, "turns": 2},
    {"moveId": "CH1", "name": "Charged 1", "type": "normal",
     "power": 50, "energy": 35, "cooldown": 500}
  ]
}`

// TestTeamBuilderTool_AutoEvolveLinearChainRequirementOverCapPreserves
// pins the processEvolveStep edge case: on a two-hop linear walk
// where hop 1 fits the league cap (porygon → porygon2) and hop 2
// busts it (porygon2 → porygon_z at tight Great-League 1500),
// the partial promotion must surface the hop-1 requirement
// (Up-Grade) on AutoEvolveRequirements. A regression that
// discarded the accumulator on overCap would silently drop the
// requirement and leave the caller without the upgrade cost for
// the promotion they just got.
func TestTeamBuilderTool_AutoEvolveLinearChainRequirementOverCapPreserves(t *testing.T) {
	t.Parallel()

	const rankingsPayload = `[
  {"speciesId": "porygon2", "speciesName": "Porygon2", "rating": 700,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2100, "atk": 100, "def": 120, "hp": 150}},
  {"speciesId": "b", "speciesName": "B", "rating": 600,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 100, "def": 120, "hp": 150}},
  {"speciesId": "c", "speciesName": "C", "rating": 650,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2050, "atk": 105, "def": 125, "hp": 145}}
]`

	tool := newTeamBuilderToolFromFixture(t, autoEvolveOverCapFixture, rankingsPayload)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{Species: "porygon", IV: [3]int{15, 15, 15}, Level: 15, FastMove: "FAST1", ChargedMoves: []string{"CH1"}},
		baseCombatant("b"),
		baseCombatant("c"),
	}

	// Great League (cpCap=1500): the inflated porygon_z busts the
	// cap at level 1, so the walker promotes to porygon2 (hop 1,
	// fits) and stops at the overCap branch before advancing to
	// porygon_z. AutoEvolveRequirements must still carry up_grade
	// from hop 1.
	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:       pool,
		League:     leagueGreat,
		AutoEvolve: true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	var breakdown *tools.MemberCostBreakdown
	for i := range result.Teams[0].Members {
		if result.Teams[0].Members[i].Species == "porygon2" {
			breakdown = &result.Teams[0].CostBreakdowns[i]

			break
		}
	}

	if breakdown == nil {
		t.Fatalf("porygon2 not in returned team; members=%+v", result.Teams[0].Members)
	}

	if len(breakdown.AutoEvolveRequirements) != 1 {
		t.Fatalf("AutoEvolveRequirements len = %d, want 1 (hop-1 up_grade survives the hop-2 over-cap); got %+v",
			len(breakdown.AutoEvolveRequirements), breakdown.AutoEvolveRequirements)
	}

	if got := breakdown.AutoEvolveRequirements[0]; got.Item != itemUpGrade {
		t.Errorf("AutoEvolveRequirements[0].Item = %q, want up_grade", got.Item)
	}
}

// autoEvolveShadowLinearFixture mirrors autoEvolveItemGatedLinearFixture
// but adds scyther_shadow + scizor_shadow so the R7.P2 promotion
// path can be exercised with Options.Shadow=true. Shadow metal-
// coat emission is non-obvious because autoEvolveMember uses
// snapshot.Pokemon[spec.Species] (the shadow id) and the table
// lookup runs on the evolved non-shadow child id — this test
// pins that the requirement still surfaces on the shadow
// breakdown.
const autoEvolveShadowLinearFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-23 00:00:00",
  "pokemon": [
    {"dex": 123, "speciesId": "scyther", "speciesName": "Scyther",
     "baseStats": {"atk": 218, "def": 170, "hp": 172},
     "types": ["bug", "flying"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_SCYTHER", "evolutions": ["scizor"]},
     "released": true},
    {"dex": 123, "speciesId": "scyther_shadow", "speciesName": "Scyther (Shadow)",
     "baseStats": {"atk": 218, "def": 170, "hp": 172},
     "types": ["bug", "flying"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_SCYTHER", "evolutions": ["scizor_shadow"]},
     "released": true},
    {"dex": 212, "speciesId": "scizor", "speciesName": "Scizor",
     "baseStats": {"atk": 236, "def": 191, "hp": 172},
     "types": ["bug", "steel"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_SCYTHER", "parent": "scyther"},
     "released": true},
    {"dex": 212, "speciesId": "scizor_shadow", "speciesName": "Scizor (Shadow)",
     "baseStats": {"atk": 236, "def": 191, "hp": 172},
     "types": ["bug", "steel"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_SCYTHER", "parent": "scyther_shadow"},
     "released": true},
    {"dex": 2, "speciesId": "b", "speciesName": "B",
     "baseStats": {"atk": 152, "def": 143, "hp": 216}, "types": ["water"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true},
    {"dex": 3, "speciesId": "c", "speciesName": "C",
     "baseStats": {"atk": 234, "def": 159, "hp": 207}, "types": ["fighting"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true}
  ],
  "moves": [
    {"moveId": "FAST1", "name": "Fast 1", "type": "normal",
     "power": 3, "energy": 0, "energyGain": 5, "cooldown": 1000, "turns": 2},
    {"moveId": "CH1", "name": "Charged 1", "type": "normal",
     "power": 50, "energy": 35, "cooldown": 500}
  ]
}`

// TestTeamBuilderTool_AutoEvolveShadowLinearChainRequirement pins
// the R7.P2 behaviour for shadow pool members. Subtle: autoEvolve
// walks the BASE-species evolutions (reads snapshot.Pokemon[
// spec.Species] where spec.Species is the non-shadow id stored on
// the caller's Combatant even when Options.Shadow=true), so the
// walker traverses the non-shadow chain and the table lookup hits
// the non-shadow "scizor" entry. That's how shadow promotions pick
// up the Metal Coat requirement naturally — the shadow/non-shadow
// redirect lives at a later stage (moveset resolution), not in
// autoEvolveMember. A regression that broke the base-chain walk
// for shadow members would drop the requirement silently.
func TestTeamBuilderTool_AutoEvolveShadowLinearChainRequirement(t *testing.T) {
	t.Parallel()

	const rankingsPayload = `[
  {"speciesId": "scizor_shadow", "speciesName": "Scizor (Shadow)", "rating": 720,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}},
  {"speciesId": "scizor", "speciesName": "Scizor", "rating": 700,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}},
  {"speciesId": "b", "speciesName": "B", "rating": 600,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 100, "def": 120, "hp": 150}},
  {"speciesId": "c", "speciesName": "C", "rating": 650,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2050, "atk": 105, "def": 125, "hp": 145}}
]`

	tool := newTeamBuilderToolFromFixture(t, autoEvolveShadowLinearFixture, rankingsPayload)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{
			Species: speciesScyther, IV: [3]int{15, 15, 15}, Level: 20,
			FastMove: "FAST1", ChargedMoves: []string{"CH1"},
			Options: tools.CombatantOptions{Shadow: true},
		},
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:       pool,
		League:     leagueGreat,
		AutoEvolve: true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	var breakdown *tools.MemberCostBreakdown
	for i := range result.Teams[0].Members {
		sp := result.Teams[0].Members[i].Species
		if sp == speciesScizorShadow || sp == speciesScizor {
			breakdown = &result.Teams[0].CostBreakdowns[i]

			break
		}
	}

	if breakdown == nil {
		t.Fatalf("shadow scizor not in returned team; members=%+v", result.Teams[0].Members)
	}

	if len(breakdown.AutoEvolveRequirements) != 1 {
		t.Fatalf("AutoEvolveRequirements len = %d, want 1 "+
			"(shadow walk goes through the base-species chain, so scizor's Metal Coat surfaces); got %+v",
			len(breakdown.AutoEvolveRequirements), breakdown.AutoEvolveRequirements)
	}

	if got := breakdown.AutoEvolveRequirements[0]; got.Item != itemMetalCoat {
		t.Errorf("AutoEvolveRequirements[0].Item = %q, want metal_coat", got.Item)
	}
}

// TestTeamBuilderTool_AutoEvolveShadowSuffixChainRequirement
// pins the legacy "_shadow"-suffix caller convention: a pool
// entry with Species="scyther_shadow" (and no Options.Shadow —
// id already disambiguates) walks the shadow chain
// (scyther_shadow → scizor_shadow). The curated table keys on
// non-shadow ids only, so the lookup strips "_shadow" before
// reading — without that strip, every shadow-suffix caller
// silently loses their Metal Coat (and every other item-gated
// R7.P2 requirement). Regression without the strip: len=0.
func TestTeamBuilderTool_AutoEvolveShadowSuffixChainRequirement(t *testing.T) {
	t.Parallel()

	const rankingsPayload = `[
  {"speciesId": "scizor_shadow", "speciesName": "Scizor (Shadow)", "rating": 720,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}},
  {"speciesId": "scizor", "speciesName": "Scizor", "rating": 700,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}},
  {"speciesId": "b", "speciesName": "B", "rating": 600,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 100, "def": 120, "hp": 150}},
  {"speciesId": "c", "speciesName": "C", "rating": 650,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2050, "atk": 105, "def": 125, "hp": 145}}
]`

	tool := newTeamBuilderToolFromFixture(t, autoEvolveShadowLinearFixture, rankingsPayload)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{
			Species: "scyther_shadow", IV: [3]int{15, 15, 15}, Level: 20,
			FastMove: "FAST1", ChargedMoves: []string{"CH1"},
		},
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:       pool,
		League:     leagueGreat,
		AutoEvolve: true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	var breakdown *tools.MemberCostBreakdown
	for i := range result.Teams[0].Members {
		sp := result.Teams[0].Members[i].Species
		if sp == speciesScizorShadow || sp == speciesScizor {
			breakdown = &result.Teams[0].CostBreakdowns[i]

			break
		}
	}

	if breakdown == nil {
		t.Fatalf("shadow scizor not in returned team; members=%+v", result.Teams[0].Members)
	}

	if len(breakdown.AutoEvolveRequirements) != 1 {
		t.Fatalf("AutoEvolveRequirements len = %d, want 1 "+
			"(shadow-suffix caller must still get Metal Coat via _shadow strip); got %+v",
			len(breakdown.AutoEvolveRequirements), breakdown.AutoEvolveRequirements)
	}

	if got := breakdown.AutoEvolveRequirements[0]; got.Item != itemMetalCoat {
		t.Errorf("AutoEvolveRequirements[0].Item = %q, want metal_coat", got.Item)
	}
}

// autoEvolveLinearThenBranchFixture publishes a synthetic chain
// where hop 1 promotes to a species in the curated R7.P2 table
// (chainbase → scizor, scizor has the metal_coat entry) and hop 2
// branches off scizor (scizor → scizorA / scizorB). Lets the R7.P2
// round-2 fix prove that branching-after-linear preserves the
// hop-1 promotion AND the hop-1 requirement, instead of dropping
// both to nil. Real pvpoke gamemaster doesn't have scizor as a
// branching parent; the fixture is synthetic to exercise the
// code path only.
const autoEvolveLinearThenBranchFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-23 00:00:00",
  "pokemon": [
    {"dex": 900, "speciesId": "chainbase", "speciesName": "ChainBase",
     "baseStats": {"atk": 100, "def": 100, "hp": 100},
     "types": ["normal"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_CHAIN", "evolutions": ["scizor"]},
     "released": true},
    {"dex": 212, "speciesId": "scizor", "speciesName": "Scizor",
     "baseStats": {"atk": 140, "def": 140, "hp": 140},
     "types": ["bug", "steel"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_CHAIN", "parent": "chainbase", "evolutions": ["scizorA", "scizorB"]},
     "released": true},
    {"dex": 9001, "speciesId": "scizorA", "speciesName": "ScizorA",
     "baseStats": {"atk": 145, "def": 135, "hp": 145},
     "types": ["bug", "steel"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_CHAIN", "parent": "scizor"},
     "released": true},
    {"dex": 9002, "speciesId": "scizorB", "speciesName": "ScizorB",
     "baseStats": {"atk": 145, "def": 135, "hp": 145},
     "types": ["bug", "steel"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_CHAIN", "parent": "scizor"},
     "released": true},
    {"dex": 2, "speciesId": "b", "speciesName": "B",
     "baseStats": {"atk": 152, "def": 143, "hp": 216}, "types": ["water"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true},
    {"dex": 3, "speciesId": "c", "speciesName": "C",
     "baseStats": {"atk": 234, "def": 159, "hp": 207}, "types": ["fighting"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true}
  ],
  "moves": [
    {"moveId": "FAST1", "name": "Fast 1", "type": "normal",
     "power": 3, "energy": 0, "energyGain": 5, "cooldown": 1000, "turns": 2},
    {"moveId": "CH1", "name": "Charged 1", "type": "normal",
     "power": 50, "energy": 35, "cooldown": 500}
  ]
}`

// TestTeamBuilderTool_AutoEvolveLinearThenBranchingPreservesRequirements
// pins the R7.P2 round-2 fix: when walkEvolutionChain advances
// through at least one hop (chainbase → scizor, item-gated via
// Metal Coat per the curated table) and then hits a branching
// step (scizor → {scizorA, scizorB}), the resulting breakdown
// must carry scizor as the promoted species AND the Metal Coat
// requirement from hop 1. A regression to the pre-round-2
// behavior would drop the promotion back to chainbase and lose
// the requirement entirely.
func TestTeamBuilderTool_AutoEvolveLinearThenBranchingPreservesRequirements(t *testing.T) {
	t.Parallel()

	const rankingsPayload = `[
  {"speciesId": "scizor", "speciesName": "Scizor", "rating": 700,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}},
  {"speciesId": "b", "speciesName": "B", "rating": 600,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 100, "def": 120, "hp": 150}},
  {"speciesId": "c", "speciesName": "C", "rating": 650,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2050, "atk": 105, "def": 125, "hp": 145}}
]`

	tool := newTeamBuilderToolFromFixture(t, autoEvolveLinearThenBranchFixture, rankingsPayload)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{Species: "chainbase", IV: [3]int{15, 15, 15}, Level: 20, FastMove: "FAST1", ChargedMoves: []string{"CH1"}},
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:       pool,
		League:     leagueGreat,
		AutoEvolve: true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	var breakdown *tools.MemberCostBreakdown
	for i := range result.Teams[0].Members {
		if result.Teams[0].Members[i].Species == speciesScizor {
			breakdown = &result.Teams[0].CostBreakdowns[i]

			break
		}
	}

	if breakdown == nil {
		t.Fatalf("scizor not in returned team — promotion must survive the downstream branching; members=%+v",
			result.Teams[0].Members)
	}

	if len(breakdown.AutoEvolveRequirements) != 1 {
		t.Fatalf("AutoEvolveRequirements len = %d, want 1 (hop-1 metal_coat must survive downstream branching); got %+v",
			len(breakdown.AutoEvolveRequirements), breakdown.AutoEvolveRequirements)
	}

	if got := breakdown.AutoEvolveRequirements[0]; got.Item != itemMetalCoat {
		t.Errorf("AutoEvolveRequirements[0].Item = %q, want metal_coat", got.Item)
	}
}

// TestTeamBuilderTool_AutoEvolveLinearChainNoItemRequirements pins
// the complement: a linear chain whose intermediate species are
// outside the curated table (squirtle → wartortle → blastoise
// — both linear, no items) must leave AutoEvolveRequirements
// empty. Differentiates "linear, no item" from "linear, item-
// gated" so a regression that started flagging every promotion
// fails loudly.
func TestTeamBuilderTool_AutoEvolveLinearChainNoItemRequirements(t *testing.T) {
	t.Parallel()

	const rankingsPayload = `[
  {"speciesId": "blastoise", "speciesName": "Blastoise", "rating": 700,
   "moveset": ["FAST1", "CH_BLAST"],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}},
  {"speciesId": "b", "speciesName": "B", "rating": 600,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 100, "def": 120, "hp": 150}},
  {"speciesId": "c", "speciesName": "C", "rating": 650,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2050, "atk": 105, "def": 125, "hp": 145}}
]`

	tool := newTeamBuilderToolFromFixture(t, autoEvolveLinearFixture, rankingsPayload)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{Species: "squirtle", IV: [3]int{15, 15, 15}, Level: 20, FastMove: "FAST1", ChargedMoves: []string{"CH_SQUIRT"}},
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:       pool,
		League:     leagueGreat,
		AutoEvolve: true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("no teams returned")
	}

	for i := range result.Teams[0].Members {
		if result.Teams[0].Members[i].Species == speciesBlastoise {
			if n := len(result.Teams[0].CostBreakdowns[i].AutoEvolveRequirements); n > 0 {
				t.Errorf("AutoEvolveRequirements len = %d, want 0 (squirtle→wartortle→blastoise is linear no-item)", n)
			}

			return
		}
	}

	t.Fatalf("blastoise not in returned team; members=%+v", result.Teams[0].Members)
}

// autoEvolveBranchingFixture publishes eevee with three evolutions;
// autoEvolve must refuse to pick one and flag the skip.
const autoEvolveBranchingFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 133, "speciesId": "eevee", "speciesName": "Eevee",
     "baseStats": {"atk": 104, "def": 114, "hp": 146},
     "types": ["normal"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_EEVEE", "evolutions": ["vaporeon", "jolteon", "flareon"]},
     "released": true},
    {"dex": 134, "speciesId": "vaporeon", "speciesName": "Vaporeon",
     "baseStats": {"atk": 186, "def": 168, "hp": 277},
     "types": ["water"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_EEVEE", "parent": "eevee"},
     "released": true},
    {"dex": 135, "speciesId": "jolteon", "speciesName": "Jolteon",
     "baseStats": {"atk": 232, "def": 182, "hp": 163},
     "types": ["electric"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_EEVEE", "parent": "eevee"},
     "released": true},
    {"dex": 136, "speciesId": "flareon", "speciesName": "Flareon",
     "baseStats": {"atk": 246, "def": 179, "hp": 163},
     "types": ["fire"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_EEVEE", "parent": "eevee"},
     "released": true},
    {"dex": 2, "speciesId": "b", "speciesName": "B",
     "baseStats": {"atk": 152, "def": 143, "hp": 216}, "types": ["water"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true},
    {"dex": 3, "speciesId": "c", "speciesName": "C",
     "baseStats": {"atk": 234, "def": 159, "hp": 207}, "types": ["fighting"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true}
  ],
  "moves": [
    {"moveId": "FAST1", "name": "Fast 1", "type": "normal",
     "power": 3, "energy": 0, "energyGain": 5, "cooldown": 1000, "turns": 2},
    {"moveId": "CH1", "name": "Charged 1", "type": "normal",
     "power": 50, "energy": 35, "cooldown": 500}
  ]
}`

// TestTeamBuilderTool_AutoEvolveBranchingSkipped pins the branching
// path: len(Evolutions) > 1 with no caller guidance → leave the
// base form intact and flag the skip reason. Eevee stays eevee;
// the cost breakdown carries auto_evolve_skipped_branching:eevee.
func TestTeamBuilderTool_AutoEvolveBranchingSkipped(t *testing.T) {
	t.Parallel()

	const rankingsPayload = `[
  {"speciesId": "eevee", "speciesName": "Eevee", "rating": 500,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2100, "atk": 100, "def": 100, "hp": 150}},
  {"speciesId": "b", "speciesName": "B", "rating": 600,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 100, "def": 120, "hp": 150}},
  {"speciesId": "c", "speciesName": "C", "rating": 650,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2050, "atk": 105, "def": 125, "hp": 145}}
]`

	tool := newTeamBuilderToolFromFixture(t, autoEvolveBranchingFixture, rankingsPayload)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{Species: speciesEevee, IV: [3]int{15, 15, 15}, Level: 20, FastMove: "FAST1", ChargedMoves: []string{"CH1"}},
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:       pool,
		League:     leagueGreat,
		AutoEvolve: true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("no teams returned")
	}

	var eeveeBreakdown *tools.MemberCostBreakdown

	var eeveePresent bool

	for i := range result.Teams[0].Members {
		if result.Teams[0].Members[i].Species == speciesEevee {
			eeveePresent = true
			eeveeBreakdown = &result.Teams[0].CostBreakdowns[i]

			break
		}
	}

	if !eeveePresent {
		t.Fatalf("eevee not in team (branching chain should leave it unchanged); members=%+v",
			result.Teams[0].Members)
	}

	if !slices.Contains(eeveeBreakdown.Flags, "auto_evolve_skipped_branching:eevee") {
		t.Errorf("auto_evolve_skipped_branching:eevee flag missing in Flags=%v",
			eeveeBreakdown.Flags)
	}

	// Phase R5 finding #5: branching skip must also surface per-
	// branch alternatives with CP predictions + league-fit flag
	// so the caller doesn't need a second gamemaster round-trip.
	if len(eeveeBreakdown.AutoEvolveAlternatives) != 3 {
		t.Fatalf("AutoEvolveAlternatives len = %d, want 3 (vaporeon / jolteon / flareon); got %+v",
			len(eeveeBreakdown.AutoEvolveAlternatives), eeveeBreakdown.AutoEvolveAlternatives)
	}

	alts := make(map[string]tools.EvolveAlternative, len(eeveeBreakdown.AutoEvolveAlternatives))
	for _, alt := range eeveeBreakdown.AutoEvolveAlternatives {
		alts[alt.To] = alt
	}

	for _, want := range []string{speciesVaporeon, speciesJolteon, "flareon"} {
		alt, ok := alts[want]
		if !ok {
			t.Errorf("alternative %q missing from AutoEvolveAlternatives", want)

			continue
		}

		if alt.PredictedCP <= 0 {
			t.Errorf("alternative %q PredictedCP = %d, want > 0", want, alt.PredictedCP)
		}
	}
}

// TestTeamBuilderTool_AutoEvolveBranchingAlternativesLeagueFit pins
// the league_fit flag semantics: the flag reflects whether the
// child species fits the league CP cap at level 1 (floor CP check,
// matching walkEvolutionChain). Vaporeon + jolteon + flareon all
// have base-stat lines that the fixture keeps under 1500 CP at the
// floor, so all three must report league_fit=true in Great League.
// A regression that miscomputed the floor CP would silently flip
// this to false.
func TestTeamBuilderTool_AutoEvolveBranchingAlternativesLeagueFit(t *testing.T) {
	t.Parallel()

	const rankingsPayload = `[
  {"speciesId": "eevee", "speciesName": "Eevee", "rating": 500,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2100, "atk": 100, "def": 100, "hp": 150}},
  {"speciesId": "b", "speciesName": "B", "rating": 600,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 100, "def": 120, "hp": 150}},
  {"speciesId": "c", "speciesName": "C", "rating": 650,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2050, "atk": 105, "def": 125, "hp": 145}}
]`

	tool := newTeamBuilderToolFromFixture(t, autoEvolveBranchingFixture, rankingsPayload)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{Species: speciesEevee, IV: [3]int{15, 15, 15}, Level: 20, FastMove: "FAST1", ChargedMoves: []string{"CH1"}},
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:       pool,
		League:     leagueGreat,
		AutoEvolve: true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("no teams returned")
	}

	for i := range result.Teams[0].Members {
		if result.Teams[0].Members[i].Species != speciesEevee {
			continue
		}

		for _, alt := range result.Teams[0].CostBreakdowns[i].AutoEvolveAlternatives {
			if !alt.LeagueFit {
				t.Errorf("alt %q LeagueFit = false, want true (fixture base stats fit GL at L1)",
					alt.To)
			}
		}

		return
	}

	t.Fatalf("eevee not in the returned team")
}

// gloomBranchingFixture publishes gloom with two evolutions to pin
// the item-gated branching path (R6.7): vileplume no item,
// bellossom sun_stone. Base stats chosen so both children fit GL
// at level 1 floor.
const gloomBranchingFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-23 00:00:00",
  "pokemon": [
    {"dex": 44, "speciesId": "gloom", "speciesName": "Gloom",
     "baseStats": {"atk": 131, "def": 112, "hp": 155},
     "types": ["grass", "poison"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_ODDISH", "parent": "oddish", "evolutions": ["vileplume", "bellossom"]},
     "released": true},
    {"dex": 45, "speciesId": "vileplume", "speciesName": "Vileplume",
     "baseStats": {"atk": 172, "def": 148, "hp": 181},
     "types": ["grass", "poison"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_ODDISH", "parent": "gloom"},
     "released": true},
    {"dex": 182, "speciesId": "bellossom", "speciesName": "Bellossom",
     "baseStats": {"atk": 169, "def": 189, "hp": 181},
     "types": ["grass"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_ODDISH", "parent": "gloom"},
     "released": true},
    {"dex": 2, "speciesId": "b", "speciesName": "B",
     "baseStats": {"atk": 152, "def": 143, "hp": 216}, "types": ["water"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true},
    {"dex": 3, "speciesId": "c", "speciesName": "C",
     "baseStats": {"atk": 234, "def": 159, "hp": 207}, "types": ["fighting"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true}
  ],
  "moves": [
    {"moveId": "FAST1", "name": "Fast 1", "type": "normal",
     "power": 3, "energy": 0, "energyGain": 5, "cooldown": 1000, "turns": 2},
    {"moveId": "CH1", "name": "Charged 1", "type": "normal",
     "power": 50, "energy": 35, "cooldown": 500}
  ]
}`

// TestTeamBuilderTool_AutoEvolveItemGatedAlternativesRequirement
// pins the R6.7 item-gated contract end-to-end: gloom branching
// surfaces vileplume + bellossom Requirement entries, bellossom
// carries the sun_stone item, vileplume is item-free, both at
// 100 candy. Regression guard for the pre-round-2 table.
func TestTeamBuilderTool_AutoEvolveItemGatedAlternativesRequirement(t *testing.T) {
	t.Parallel()

	const rankingsPayload = `[
  {"speciesId": "gloom", "speciesName": "Gloom", "rating": 500,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2100, "atk": 100, "def": 100, "hp": 150}},
  {"speciesId": "b", "speciesName": "B", "rating": 600,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 100, "def": 120, "hp": 150}},
  {"speciesId": "c", "speciesName": "C", "rating": 650,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2050, "atk": 105, "def": 125, "hp": 145}}
]`

	tool := newTeamBuilderToolFromFixture(t, gloomBranchingFixture, rankingsPayload)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{Species: "gloom", IV: [3]int{15, 15, 15}, Level: 20, FastMove: "FAST1", ChargedMoves: []string{"CH1"}},
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:       pool,
		League:     leagueGreat,
		AutoEvolve: true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("no teams returned")
	}

	var alts []tools.EvolveAlternative
	for i := range result.Teams[0].Members {
		if result.Teams[0].Members[i].Species == "gloom" {
			alts = result.Teams[0].CostBreakdowns[i].AutoEvolveAlternatives

			break
		}
	}

	if len(alts) != 2 {
		t.Fatalf("alts len = %d, want 2 (vileplume + bellossom); alts=%+v", len(alts), alts)
	}

	byID := make(map[string]tools.EvolveAlternative, len(alts))
	for _, alt := range alts {
		byID[alt.To] = alt
	}

	bellossom, ok := byID["bellossom"]
	if !ok {
		t.Fatal("bellossom missing from alts")
	}
	if bellossom.Requirement == nil {
		t.Fatal("bellossom Requirement = nil, want populated")
	}
	if bellossom.Requirement.Item != "sun_stone" {
		t.Errorf("bellossom Requirement.Item = %q, want sun_stone", bellossom.Requirement.Item)
	}

	const wantBellossomCandy = 100
	if bellossom.Requirement.Candy != wantBellossomCandy {
		t.Errorf("bellossom Requirement.Candy = %d, want %d",
			bellossom.Requirement.Candy, wantBellossomCandy)
	}

	vileplume, ok := byID["vileplume"]
	if !ok {
		t.Fatal("vileplume missing from alts")
	}
	if vileplume.Requirement == nil {
		t.Fatal("vileplume Requirement = nil, want populated")
	}
	if vileplume.Requirement.Item != "" {
		t.Errorf("vileplume Requirement.Item = %q, want empty (no item)", vileplume.Requirement.Item)
	}

	const wantVileplumeCandy = 100
	if vileplume.Requirement.Candy != wantVileplumeCandy {
		t.Errorf("vileplume Requirement.Candy = %d, want %d",
			vileplume.Requirement.Candy, wantVileplumeCandy)
	}
}

// unknownBranchingFixture publishes a synthetic base species whose
// children are NOT in the evolution_items table — models the
// Scyther→Kleavor class of chain that pvpoke data may list but
// Niantic hasn't shipped in GO. Test pins that Requirement=nil
// for such branches and callers can distinguish them from the
// in-table cases.
const unknownBranchingFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-23 00:00:00",
  "pokemon": [
    {"dex": 9001, "speciesId": "unknownbase", "speciesName": "UnknownBase",
     "baseStats": {"atk": 104, "def": 114, "hp": 146},
     "types": ["normal"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_UNK", "evolutions": ["unknownchildA", "unknownchildB"]},
     "released": true},
    {"dex": 9002, "speciesId": "unknownchildA", "speciesName": "UnknownChildA",
     "baseStats": {"atk": 152, "def": 143, "hp": 155},
     "types": ["normal"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_UNK", "parent": "unknownbase"},
     "released": true},
    {"dex": 9003, "speciesId": "unknownchildB", "speciesName": "UnknownChildB",
     "baseStats": {"atk": 148, "def": 139, "hp": 160},
     "types": ["normal"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_UNK", "parent": "unknownbase"},
     "released": true},
    {"dex": 2, "speciesId": "b", "speciesName": "B",
     "baseStats": {"atk": 152, "def": 143, "hp": 216}, "types": ["water"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true},
    {"dex": 3, "speciesId": "c", "speciesName": "C",
     "baseStats": {"atk": 234, "def": 159, "hp": 207}, "types": ["fighting"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true}
  ],
  "moves": [
    {"moveId": "FAST1", "name": "Fast 1", "type": "normal",
     "power": 3, "energy": 0, "energyGain": 5, "cooldown": 1000, "turns": 2},
    {"moveId": "CH1", "name": "Charged 1", "type": "normal",
     "power": 50, "energy": 35, "cooldown": 500}
  ]
}`

// TestTeamBuilderTool_AutoEvolveUnknownBranchRequirementNil pins
// the graceful fall-through: a branch whose child is absent from
// evolutionItemRequirements table leaves Requirement=nil on that
// alternative, not a zero-valued struct. Matches the Scyther →
// Kleavor and similar "pvpoke lists it but Niantic doesn't ship"
// contract.
func TestTeamBuilderTool_AutoEvolveUnknownBranchRequirementNil(t *testing.T) {
	t.Parallel()

	const rankingsPayload = `[
  {"speciesId": "unknownbase", "speciesName": "UnknownBase", "rating": 500,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2100, "atk": 100, "def": 100, "hp": 150}},
  {"speciesId": "b", "speciesName": "B", "rating": 600,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 100, "def": 120, "hp": 150}},
  {"speciesId": "c", "speciesName": "C", "rating": 650,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2050, "atk": 105, "def": 125, "hp": 145}}
]`

	tool := newTeamBuilderToolFromFixture(t, unknownBranchingFixture, rankingsPayload)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{Species: "unknownbase", IV: [3]int{15, 15, 15}, Level: 20, FastMove: "FAST1", ChargedMoves: []string{"CH1"}},
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:       pool,
		League:     leagueGreat,
		AutoEvolve: true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("no teams returned")
	}

	var alts []tools.EvolveAlternative
	for i := range result.Teams[0].Members {
		if result.Teams[0].Members[i].Species == "unknownbase" {
			alts = result.Teams[0].CostBreakdowns[i].AutoEvolveAlternatives

			break
		}
	}

	if len(alts) != 2 {
		t.Fatalf("alts len = %d, want 2; got %+v", len(alts), alts)
	}

	for _, alt := range alts {
		if alt.Requirement != nil {
			t.Errorf("alt %q Requirement = %+v, want nil (not in curated table)",
				alt.To, alt.Requirement)
		}
	}
}

// TestTeamBuilderTool_AutoEvolveBranchingAlternativesRequirement
// pins R6.7: each EvolveAlternative on a branching eevee skip
// carries a Requirement pulled from the curated evolution-item
// table. All three vanilla eeveelutions (vaporeon / jolteon /
// flareon) use 25 candy, no item. A regression that missed the
// evolutionRequirementFor lookup would leave Requirement=nil.
func TestTeamBuilderTool_AutoEvolveBranchingAlternativesRequirement(t *testing.T) {
	t.Parallel()

	const rankingsPayload = `[
  {"speciesId": "eevee", "speciesName": "Eevee", "rating": 500,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2100, "atk": 100, "def": 100, "hp": 150}},
  {"speciesId": "b", "speciesName": "B", "rating": 600,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 100, "def": 120, "hp": 150}},
  {"speciesId": "c", "speciesName": "C", "rating": 650,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2050, "atk": 105, "def": 125, "hp": 145}}
]`

	tool := newTeamBuilderToolFromFixture(t, autoEvolveBranchingFixture, rankingsPayload)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{Species: speciesEevee, IV: [3]int{15, 15, 15}, Level: 20, FastMove: "FAST1", ChargedMoves: []string{"CH1"}},
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:       pool,
		League:     leagueGreat,
		AutoEvolve: true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("no teams returned")
	}

	var alts []tools.EvolveAlternative
	for i := range result.Teams[0].Members {
		if result.Teams[0].Members[i].Species == speciesEevee {
			alts = result.Teams[0].CostBreakdowns[i].AutoEvolveAlternatives

			break
		}
	}

	if len(alts) == 0 {
		t.Fatal("eevee branch alternatives not present")
	}

	// Expected contract: all three vanilla eeveelutions carry
	// Requirement with 25 candy and no item (random-pick branches).
	const wantCandy = 25

	for _, alt := range alts {
		if alt.Requirement == nil {
			t.Errorf("alt %q Requirement = nil, want populated (r6.7 table)", alt.To)

			continue
		}
		if alt.Requirement.Item != "" {
			t.Errorf("alt %q Requirement.Item = %q, want empty (random pick)", alt.To, alt.Requirement.Item)
		}
		if alt.Requirement.Candy != wantCandy {
			t.Errorf("alt %q Requirement.Candy = %d, want %d", alt.To, alt.Requirement.Candy, wantCandy)
		}
	}
}

// TestTeamBuilderTool_ParallelSharedPoolNoRace pins the R5
// defensive-clone claim on Combatant.originalIndex's godoc: two
// parallel handler invocations sharing one []Combatant pool must
// not trip the race detector, because handle() clones params.Pool
// before stamping originalIndex / running autoEvolvePool. A
// regression that reverted to in-place caller mutation would
// surface as a write/write race on pool[i].originalIndex here
// under -race.
func TestTeamBuilderTool_ParallelSharedPoolNoRace(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	pool := []tools.Combatant{
		baseCombatant("a"),
		baseCombatant("b"),
		baseCombatant("c"),
	}

	var wg sync.WaitGroup

	wg.Add(2)

	for range 2 {
		go func() {
			defer wg.Done()

			_, _, handlerErr := handler(t.Context(), nil, tools.TeamBuilderParams{
				Pool:   pool,
				League: leagueGreat,
			})
			if handlerErr != nil {
				t.Errorf("handler: %v", handlerErr)
			}
		}()
	}

	wg.Wait()
}

// TestTeamBuilderTool_PoolIndicesAlignWithPoolMembers pins the R5
// round-1 review blocker: TeamBuilderTeam.PoolIndices and
// TeamBuilderResult.PoolMembers must share one coordinate system
// (original-pool). Without the remapPoolIndicesToOriginal step,
// PoolIndices would point into the ban-filtered pool while
// PoolMembers indexes the original — a cross-reference like
// PoolMembers[team.PoolIndices[k]].Species would silently return
// the wrong entry. Banned leading species in the fixture exposes
// the mismatch cleanly.
func TestTeamBuilderTool_PoolIndicesAlignWithPoolMembers(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	// 4-member pool with "a" banned so the filtered pool is
	// [b, c, c14] with indices shifting: filtered[0]=original[1]=b,
	// filtered[1]=original[2]=c, filtered[2]=original[3]=c14. A
	// regression that skipped remapPoolIndicesToOriginal would
	// hand back filtered indices 0..2, but PoolMembers slot 0 is
	// the banned "a" — cross-reference mismatch.
	pool := []tools.Combatant{
		baseCombatant("a"), // banned; filtered pool starts at b.
		baseCombatant("b"),
		baseCombatant("c"),
		{
			Species: "c", IV: [3]int{14, 14, 15}, Level: 40,
			FastMove: "FAST1", ChargedMoves: []string{"CH1"},
		},
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:   pool,
		League: leagueGreat,
		Banned: []string{"a"},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("no teams returned")
	}

	team := result.Teams[0]
	if len(team.Members) != 3 {
		t.Fatalf("members = %d, want 3", len(team.Members))
	}

	for k, poolIdx := range team.PoolIndices {
		if poolIdx < 0 || poolIdx >= len(result.PoolMembers) {
			t.Errorf("team.PoolIndices[%d]=%d out of PoolMembers range len=%d",
				k, poolIdx, len(result.PoolMembers))

			continue
		}

		want := team.Members[k].Species
		got := result.PoolMembers[poolIdx].ResolvedSpecies

		if got != want {
			t.Errorf("PoolMembers[team.PoolIndices[%d]=%d].ResolvedSpecies = %q, want %q "+
				"(one coordinate system)", k, poolIdx, got, want)
		}
	}
}

// TestTeamBuilderTool_PoolMembersDebugInfo pins Finding #6 — the
// per-pool-entry status snapshot attached to the TeamBuilderResult.
// One kept entry, one auto_evolve branching skip, one banned entry.
// Every slot in PoolMembers must point back to its original index,
// report the right AutoEvolveAction, and flip InReturnedTeam
// correctly based on team selection.
func TestTeamBuilderTool_PoolMembersDebugInfo(t *testing.T) {
	t.Parallel()

	const rankingsPayload = `[
  {"speciesId": "eevee", "speciesName": "Eevee", "rating": 500,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2100, "atk": 100, "def": 100, "hp": 150}},
  {"speciesId": "b", "speciesName": "B", "rating": 600,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 100, "def": 120, "hp": 150}},
  {"speciesId": "c", "speciesName": "C", "rating": 650,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2050, "atk": 105, "def": 125, "hp": 145}}
]`

	tool := newTeamBuilderToolFromFixture(t, autoEvolveBranchingFixture, rankingsPayload)
	handler := tool.Handler()

	pool := []tools.Combatant{
		baseCombatant("b"), // kept, should appear in team
		{Species: speciesEevee, IV: [3]int{15, 15, 15}, Level: 20, FastMove: "FAST1", ChargedMoves: []string{"CH1"}}, // branching skip
		baseCombatant("c"),        // kept, appears in team
		baseCombatant("vaporeon"), // banned
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:       pool,
		League:     leagueGreat,
		AutoEvolve: true,
		Banned:     []string{"vaporeon"},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.PoolMembers) != len(pool) {
		t.Fatalf("PoolMembers len = %d, want %d (one row per input entry)",
			len(result.PoolMembers), len(pool))
	}

	// Index must match input order — PoolMembers[i].Index == i.
	for i, pm := range result.PoolMembers {
		if pm.Index != i {
			t.Errorf("PoolMembers[%d].Index = %d, want %d", i, pm.Index, i)
		}
	}

	// Pool[0] = b: kept, appears in team (no banned, no auto_evolve chain).
	if got := result.PoolMembers[0].AutoEvolveAction; got != tools.AutoEvolveActionKept {
		t.Errorf("PoolMembers[0] action = %q, want %q", got, tools.AutoEvolveActionKept)
	}

	if !result.PoolMembers[0].InReturnedTeam {
		t.Errorf("PoolMembers[0] (b) InReturnedTeam = false, want true")
	}

	// Pool[1] = eevee: branching skip.
	if got := result.PoolMembers[1].AutoEvolveAction; got != tools.AutoEvolveActionSkippedBranching {
		t.Errorf("PoolMembers[1] (eevee) action = %q, want %q",
			got, tools.AutoEvolveActionSkippedBranching)
	}

	if result.PoolMembers[1].OriginalSpecies != speciesEevee {
		t.Errorf("PoolMembers[1] OriginalSpecies = %q, want %q",
			result.PoolMembers[1].OriginalSpecies, speciesEevee)
	}

	if result.PoolMembers[1].ResolvedSpecies != speciesEevee {
		t.Errorf("PoolMembers[1] ResolvedSpecies = %q, want %q (branching skip keeps base form)",
			result.PoolMembers[1].ResolvedSpecies, speciesEevee)
	}

	// Pool[3] = vaporeon: banned.
	if !result.PoolMembers[3].Banned {
		t.Errorf("PoolMembers[3] (vaporeon) Banned = false, want true")
	}

	if result.PoolMembers[3].InReturnedTeam {
		t.Errorf("PoolMembers[3] (vaporeon) InReturnedTeam = true, want false (banned → filtered out)")
	}
}

// autoEvolveFirstHopOverCapFixture publishes a base species whose
// immediate evolution has inflated stats that bust Great League at
// level 1. The base form itself fits (L1 CP comfortably < 1500),
// so validatePoolForLeague allows it into the pool; autoEvolve
// then refuses to promote because the first hop blows the cap
// with no intermediate fit.
const autoEvolveFirstHopOverCapFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 500, "speciesId": "tinybase", "speciesName": "Tinybase",
     "baseStats": {"atk": 80, "def": 80, "hp": 80}, "types": ["normal"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_TINYBASE", "evolutions": ["hugeevo"]},
     "released": true},
    {"dex": 501, "speciesId": "hugeevo", "speciesName": "Hugeevo",
     "baseStats": {"atk": 9000, "def": 9000, "hp": 9000}, "types": ["normal"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_TINYBASE", "parent": "tinybase"},
     "released": true},
    {"dex": 2, "speciesId": "b", "speciesName": "B",
     "baseStats": {"atk": 152, "def": 143, "hp": 216}, "types": ["water"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true},
    {"dex": 3, "speciesId": "c", "speciesName": "C",
     "baseStats": {"atk": 234, "def": 159, "hp": 207}, "types": ["fighting"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true}
  ],
  "moves": [
    {"moveId": "FAST1", "name": "Fast 1", "type": "normal",
     "power": 3, "energy": 0, "energyGain": 5, "cooldown": 1000, "turns": 2},
    {"moveId": "CH1", "name": "Charged 1", "type": "normal",
     "power": 50, "energy": 35, "cooldown": 500}
  ]
}`

// TestTeamBuilderTool_AutoEvolveFirstHopOverCap pins the
// "first-hop busts" path: the base species' single next evolution
// has stats so inflated that level-1 CP already blows the cap.
// walkEvolutionChain returns (nil, "auto_evolve_over_cap"); the
// base form stays in the team with the flag appended.
func TestTeamBuilderTool_AutoEvolveFirstHopOverCap(t *testing.T) {
	t.Parallel()

	const rankingsPayload = `[
  {"speciesId": "tinybase", "speciesName": "Tinybase", "rating": 500,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 80, "def": 80, "hp": 150}},
  {"speciesId": "b", "speciesName": "B", "rating": 600,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 100, "def": 120, "hp": 150}},
  {"speciesId": "c", "speciesName": "C", "rating": 650,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2050, "atk": 105, "def": 125, "hp": 145}}
]`

	tool := newTeamBuilderToolFromFixture(t, autoEvolveFirstHopOverCapFixture, rankingsPayload)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{Species: "tinybase", IV: [3]int{15, 15, 15}, Level: 20, FastMove: "FAST1", ChargedMoves: []string{"CH1"}},
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:       pool,
		League:     leagueGreat,
		AutoEvolve: true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("no teams returned")
	}

	var basePresent bool

	var baseBreakdown *tools.MemberCostBreakdown

	for i := range result.Teams[0].Members {
		if result.Teams[0].Members[i].Species == "tinybase" {
			basePresent = true
			baseBreakdown = &result.Teams[0].CostBreakdowns[i]

			break
		}
	}

	if !basePresent {
		t.Fatalf("tinybase not in team; first-hop-busts must leave base intact. members=%+v",
			result.Teams[0].Members)
	}

	if !slices.Contains(baseBreakdown.Flags, "auto_evolve_over_cap:tinybase") {
		t.Errorf("auto_evolve_over_cap:tinybase flag missing in Flags=%v",
			baseBreakdown.Flags)
	}
}

// autoEvolvePartialWalkFixture publishes a three-stage linear
// chain where the mid-stage fits the cap but the terminal busts
// it. walkEvolutionChain must stop at the mid-stage and return it
// as a successful promotion (not a skip). The flag on the
// resolved member is auto_evolved_from:<orig> — same as a full
// promotion — because partial walks are recorded as normal
// promotions per the godoc.
const autoEvolvePartialWalkFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 600, "speciesId": "stepbase", "speciesName": "Stepbase",
     "baseStats": {"atk": 80, "def": 80, "hp": 80}, "types": ["normal"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_STEP", "evolutions": ["stepmid"]},
     "released": true},
    {"dex": 601, "speciesId": "stepmid", "speciesName": "Stepmid",
     "baseStats": {"atk": 120, "def": 120, "hp": 150}, "types": ["normal"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_STEP", "parent": "stepbase", "evolutions": ["stephuge"]},
     "released": true},
    {"dex": 602, "speciesId": "stephuge", "speciesName": "Stephuge",
     "baseStats": {"atk": 9000, "def": 9000, "hp": 9000}, "types": ["normal"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"],
     "family": {"id": "FAMILY_STEP", "parent": "stepmid"},
     "released": true},
    {"dex": 2, "speciesId": "b", "speciesName": "B",
     "baseStats": {"atk": 152, "def": 143, "hp": 216}, "types": ["water"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true},
    {"dex": 3, "speciesId": "c", "speciesName": "C",
     "baseStats": {"atk": 234, "def": 159, "hp": 207}, "types": ["fighting"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true}
  ],
  "moves": [
    {"moveId": "FAST1", "name": "Fast 1", "type": "normal",
     "power": 3, "energy": 0, "energyGain": 5, "cooldown": 1000, "turns": 2},
    {"moveId": "CH1", "name": "Charged 1", "type": "normal",
     "power": 50, "energy": 35, "cooldown": 500}
  ]
}`

// TestTeamBuilderTool_AutoEvolvePartialWalkTerminalOverCap pins
// the partial-walk path: stepbase → stepmid fits, stepmid →
// stephuge busts. Result: member becomes stepmid (not stephuge,
// not stepbase). Flag is auto_evolved_from:stepbase — the same
// flag a full terminal promotion would emit — because the godoc
// records partial walks as normal promotions rather than a
// separate "terminal_over_cap" signal.
func TestTeamBuilderTool_AutoEvolvePartialWalkTerminalOverCap(t *testing.T) {
	t.Parallel()

	const rankingsPayload = `[
  {"speciesId": "stepmid", "speciesName": "Stepmid", "rating": 600,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2100, "atk": 120, "def": 120, "hp": 150}},
  {"speciesId": "b", "speciesName": "B", "rating": 600,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 100, "def": 120, "hp": 150}},
  {"speciesId": "c", "speciesName": "C", "rating": 650,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2050, "atk": 105, "def": 125, "hp": 145}}
]`

	tool := newTeamBuilderToolFromFixture(t, autoEvolvePartialWalkFixture, rankingsPayload)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{Species: "stepbase", IV: [3]int{15, 15, 15}, Level: 1, FastMove: "FAST1", ChargedMoves: []string{"CH1"}},
		baseCombatant("b"),
		baseCombatant("c"),
	}

	_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:       pool,
		League:     leagueGreat,
		AutoEvolve: true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Teams) == 0 {
		t.Fatal("no teams returned")
	}

	var promoted *tools.ResolvedCombatant

	var promotedBreakdown *tools.MemberCostBreakdown

	for i := range result.Teams[0].Members {
		if result.Teams[0].Members[i].Species == "stepmid" {
			promoted = &result.Teams[0].Members[i]
			promotedBreakdown = &result.Teams[0].CostBreakdowns[i]

			break
		}
	}

	if promoted == nil {
		t.Fatalf("stepmid not in team (partial walk should stop at mid-stage); members=%+v",
			result.Teams[0].Members)
	}

	if !slices.Contains(promotedBreakdown.Flags, "auto_evolved_from:stepbase") {
		t.Errorf("auto_evolved_from:stepbase flag missing in Flags=%v",
			promotedBreakdown.Flags)
	}
}

// TestTeamBuilderTool_ParallelMatrixDeterministicAcrossRuns pins
// the Phase 4 invariant: rating-matrix precompute is parallel
// (runtime.NumCPU() workers) but the resulting Teams list is
// bit-identical across repeated runs. Workers complete in
// non-deterministic order; if write-back slots weren't row-
// indexed or sort keys weren't stable, the output could reorder
// between runs. This test catches such a regression by running
// the same request 5 times and comparing every field.
func TestTeamBuilderTool_ParallelMatrixDeterministicAcrossRuns(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderTool(t)
	handler := tool.Handler()

	// 5 combatants → C(5,3) = 10 triples. Large enough that the
	// downstream sort actually has work to do, so a completion-
	// order regression in the parallel precompute would reorder
	// the Teams list visibly across runs (vs. a 3-combatant pool
	// where C(3,3)=1 triple means "no reorder possible" is not a
	// reorder test at all).
	pool := []tools.Combatant{
		baseCombatant("a"),
		baseCombatant("b"),
		baseCombatant("c"),
		{
			Species: "b", IV: [3]int{14, 15, 15}, Level: 40,
			FastMove: "FAST1", ChargedMoves: []string{"CH1"},
		},
		{
			Species: "c", IV: [3]int{14, 14, 15}, Level: 40,
			FastMove: "FAST1", ChargedMoves: []string{"CH1"},
		},
	}

	var canonical *tools.TeamBuilderResult

	const iterations = 5

	for run := range iterations {
		_, result, err := handler(t.Context(), nil, tools.TeamBuilderParams{
			Pool:       pool,
			League:     leagueGreat,
			MaxResults: 10, // return all triples so reorder is observable
		})
		if err != nil {
			t.Fatalf("run %d handler: %v", run, err)
		}

		if run == 0 {
			canonical = &result

			continue
		}

		if len(result.Teams) != len(canonical.Teams) {
			t.Fatalf("run %d len(Teams) = %d, canonical = %d",
				run, len(result.Teams), len(canonical.Teams))
		}

		for i := range result.Teams {
			if result.Teams[i].TeamScore != canonical.Teams[i].TeamScore {
				t.Errorf("run %d team[%d] TeamScore = %v, canonical = %v",
					run, i, result.Teams[i].TeamScore, canonical.Teams[i].TeamScore)
			}

			if !slices.Equal(result.Teams[i].PoolIndices, canonical.Teams[i].PoolIndices) {
				t.Errorf("run %d team[%d] PoolIndices = %v, canonical = %v",
					run, i, result.Teams[i].PoolIndices, canonical.Teams[i].PoolIndices)
			}
		}
	}
}
