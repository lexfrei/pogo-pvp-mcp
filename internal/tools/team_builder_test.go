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
		if !slices.Contains(team.Members, "a") {
			t.Errorf("team %v missing required anchor 'a'", team.Members)
		}
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
