package tools_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
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
			if member.Species == "blastoise" {
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
		{Species: "eevee", IV: [3]int{15, 15, 15}, Level: 20, FastMove: "FAST1", ChargedMoves: []string{"CH1"}},
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
		if result.Teams[0].Members[i].Species == "eevee" {
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
