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
